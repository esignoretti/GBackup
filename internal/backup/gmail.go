package backup

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	gmailConcurrency   = 25
	gmailRateLimit     = 220
	gmailRateBurst     = 25
	gmailMaxRetries    = 3
	gmailListRateLimit = 20
	gmailListRateBurst = 10
)

type GmailBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type GmailBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *GmailBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func NewGmailBackup(cfg *GmailBackupConfig) (*GmailBackup, error) {
	return &GmailBackup{cfg: cfg}, nil
}

func (g *GmailBackup) WithMetaDB(db *metadata.DB) *GmailBackup {
	g.metaDB = db
	return g
}

func (g *GmailBackup) WithStorage(s *storage.Client) *GmailBackup {
	g.store = s
	return g
}

func (g *GmailBackup) WithProgress(p *progress.Reporter) *GmailBackup {
	g.progress = p
	return g
}

func (g *GmailBackup) WithMaxAge(d time.Duration) *GmailBackup {
	g.maxAge = d
	return g
}

func (g *GmailBackup) gmailServiceForUser(ctx context.Context, user string) (*gmail.Service, error) {
	keyData, err := os.ReadFile(g.cfg.ServiceAccountFile)
	if err != nil {
		return nil, fmt.Errorf("reading service account key: %w", err)
	}
	jwtCfg, err := google.JWTConfigFromJSON(keyData, gws.ScopesForService("gmail")...)
	if err != nil {
		return nil, fmt.Errorf("creating JWT config: %w", err)
	}
	jwtCfg.Subject = user
	return gmail.NewService(ctx, option.WithTokenSource(jwtCfg.TokenSource(ctx)))
}

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if g.progress != nil {
		g.progress.Service("gmail", user, runType)
	}

	svc, err := g.gmailServiceForUser(ctx, user)
	if err != nil {
		return 0, fmt.Errorf("creating gmail service for %s: %w", user, err)
	}

	listLimiter := rate.NewLimiter(rate.Limit(gmailListRateLimit), gmailListRateBurst)

	var listQuery string
	if g.maxAge > 0 {
		cutoff := time.Now().Add(-g.maxAge).Format("2006/01/02")
		listQuery = fmt.Sprintf("after:%s", cutoff)
	}

	var allMsgIDs []string
	pageToken := ""
	for {
		if err := listLimiter.Wait(ctx); err != nil {
			return 0, fmt.Errorf("rate limit waiting for list: %w", err)
		}

		listCtx, listCancel := context.WithTimeout(ctx, 30*time.Second)
		call := svc.Users.Messages.List(user).
			Context(listCtx).
			MaxResults(500).
			IncludeSpamTrash(false)
		if listQuery != "" {
			call.Q(listQuery)
		}
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		listCancel()
		if err != nil {
			if gws.IsServiceDisabled(err) {
				fmt.Printf("  gmail: API not enabled for %s, skipping\n", user)
				return 0, nil
			}
			return 0, fmt.Errorf("listing messages: %w", err)
		}

		for _, m := range resp.Messages {
			allMsgIDs = append(allMsgIDs, m.Id)
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if g.progress != nil {
		fmt.Printf("  found %d messages to fetch\n", len(allMsgIDs))
	}

	limiter := rate.NewLimiter(rate.Limit(gmailRateLimit), gmailRateBurst)

	buckets := make(map[string][]archive.ArchiveEntry)
	var mu sync.Mutex
	var fetched int

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(gmailConcurrency)

	for _, id := range allMsgIDs {
		id := id
		eg.Go(func() error {
			if err := limiter.Wait(egCtx); err != nil {
				return fmt.Errorf("rate limiter: %w", err)
			}
			msg, err := fetchWithRetry(egCtx, svc, user, id)
			if err != nil {
				return err
			}
			if g.maxAge > 0 {
				msgTime := time.UnixMilli(msg.InternalDate)
				if time.Since(msgTime) > g.maxAge {
					return nil
				}
			}
			raw, err := base64.URLEncoding.DecodeString(msg.Raw)
			if err != nil {
				return fmt.Errorf("decoding message %s: %w", id, err)
			}
			month := messageMonth(msg)
			entryName := fmt.Sprintf("%s.eml", id)

			mu.Lock()
			buckets[month] = append(buckets[month], archive.ArchiveEntry{
				Name:    entryName,
				Data:    raw,
				ModTime: time.UnixMilli(msg.InternalDate),
			})
			fetched++
			if g.progress != nil && fetched%100 == 0 {
				fmt.Printf("  fetched %d messages so far...\n", fetched)
			}
			mu.Unlock()
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return fetched, err
	}

	if g.progress != nil {
		fmt.Printf("  fetched %d messages\n", fetched)
	}

	if len(buckets) == 0 {
		return 0, nil
	}

	var totalCount int
	var uploadErr error
	var uploadMu sync.Mutex
	var uploadWg sync.WaitGroup

	for month, entries := range buckets {
		month, entries := month, entries
		uploadWg.Add(1)
		go func() {
			defer uploadWg.Done()

			n, err := g.uploadMonth(ctx, user, month, entries, full)
			if err != nil {
				uploadMu.Lock()
				if uploadErr == nil {
					uploadErr = err
				}
				uploadMu.Unlock()
				return
			}
			uploadMu.Lock()
			totalCount += n
			uploadMu.Unlock()
		}()
	}

	uploadWg.Wait()

	if uploadErr != nil {
		return totalCount, uploadErr
	}

	if g.metaDB != nil {
		if err := g.metaDB.RecordBackup("gmail", user, runType); err != nil {
			return totalCount, fmt.Errorf("recording backup: %w", err)
		}
	}
	return totalCount, nil
}

func (g *GmailBackup) uploadMonth(ctx context.Context, user, month string, entries []archive.ArchiveEntry, full bool) (int, error) {
	objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))

	var archiveData []byte
	if !full {
		existing, downloadErr := g.store.Download(ctx, objKey)
		if downloadErr == nil {
			data, appendErr := archive.AppendToArchive(existing, entries)
			existing.Close()
			if appendErr != nil {
				return 0, fmt.Errorf("appending to archive %s: %w", objKey, appendErr)
			}
			archiveData = data
		} else {
			data, createErr := archive.Create(entries)
			if createErr != nil {
				return 0, fmt.Errorf("creating archive %s: %w", objKey, createErr)
			}
			archiveData = data
		}
	} else {
		data, err := archive.Create(entries)
		if err != nil {
			return 0, fmt.Errorf("creating archive %s: %w", objKey, err)
		}
		archiveData = data
	}

	if err := g.store.Upload(ctx, objKey, bytes.NewReader(archiveData)); err != nil {
		return 0, fmt.Errorf("uploading %s: %w", objKey, err)
	}

	if g.progress != nil {
		fmt.Printf("  \u2191 %s\n", objKey)
	}

	for _, entry := range entries {
		if g.metaDB != nil {
			if err := g.metaDB.TrackItem(&metadata.Item{
				Service:    "gmail",
				User:       user,
				ObjectKey:  objKey,
				ItemPath:   entry.Name,
				ItemID:     entryNameToID(entry.Name),
				Size:       int64(len(entry.Data)),
				Checksum:   entry.Name,
				ModifiedAt: entry.ModTime,
			}); err != nil {
				return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
			}
		}
	}
	return len(entries), nil
}

func fetchWithRetry(ctx context.Context, svc *gmail.Service, user, id string) (*gmail.Message, error) {
	const baseBackoff = 200 * time.Millisecond
	const maxBackoff = 5 * time.Second

	var msg *gmail.Message
	var err error
	for attempt := 0; attempt < gmailMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := baseBackoff << (attempt - 1)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			backoff += time.Duration(rand.Int63n(int64(backoff)))
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		msg, err = svc.Users.Messages.Get(user, id).Context(fetchCtx).Format("raw").Do()
		cancel()
		if err == nil {
			return msg, nil
		}
		if gws.IsRetryable(err) {
			continue
		}
		return nil, fmt.Errorf("getting message %s: %w", id, err)
	}
	return nil, fmt.Errorf("getting message %s after %d retries: %w", id, gmailMaxRetries, err)
}

func messageMonth(msg *gmail.Message) string {
	sec := msg.InternalDate / 1000
	t := time.Unix(sec, 0)
	return t.Format("2006-01")
}

func entryNameToID(name string) string {
	if len(name) > 4 && name[len(name)-4:] == ".eml" {
		return name[:len(name)-4]
	}
	return name
}
