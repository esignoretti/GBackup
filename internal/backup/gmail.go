package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
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
	gmailMonthChanBuf  = 100
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
	ts, err := gws.UserTokenSource(ctx, g.cfg.ServiceAccountFile, user, gws.ScopesForService("gmail"))
	if err != nil {
		return nil, err
	}
	return gmail.NewService(ctx, option.WithTokenSource(ts))
}

// monthState holds the per-month channel + uploader bookkeeping.
type monthState struct {
	ch      chan archive.ArchiveEntry
	done    chan error // nil = success; ErrNoChanges = no-op; else error
	entries []archive.ArchiveEntry
	mu      sync.Mutex
}

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if g.progress != nil {
		g.progress.Service("gmail", user, runType)
	}

	var runID int64
	if g.metaDB != nil {
		id, err := g.metaDB.StartBackup("gmail", user, runType)
		if err != nil {
			return 0, fmt.Errorf("recording backup start: %w", err)
		}
		runID = id
	}

	svc, err := g.gmailServiceForUser(ctx, user)
	if err != nil {
		return 0, fmt.Errorf("creating gmail service for %s: %w", user, err)
	}

	// Stage 1: list all message IDs.
	allMsgIDs, err := g.listAllIDs(ctx, svc, user)
	if err != nil {
		return 0, err
	}
	if allMsgIDs == nil {
		// Service disabled — listAllIDs already printed a notice.
		return 0, nil
	}
	if g.progress != nil {
		fmt.Printf("  found %d messages to fetch\n", len(allMsgIDs))
	}

	// Per-month state, lazily created when the first message for a month is fetched.
	var (
		months   = map[string]*monthState{}
		monthsMu sync.Mutex
	)

	getMonth := func(month string) *monthState {
		monthsMu.Lock()
		defer monthsMu.Unlock()
		if ms, ok := months[month]; ok {
			return ms
		}
		ms := &monthState{
			ch:   make(chan archive.ArchiveEntry, gmailMonthChanBuf),
			done: make(chan error, 1),
		}
		months[month] = ms
		go g.runMonthUploader(ctx, user, month, full, ms)
		return ms
	}

	// Stage 2: fetch in parallel, route by month into per-month uploader channels.
	limiter := rate.NewLimiter(rate.Limit(gmailRateLimit), gmailRateBurst)
	var fetched int
	var fetchedMu sync.Mutex

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
			entry := archive.ArchiveEntry{
				Name:    fmt.Sprintf("%s.eml", id),
				Data:    raw,
				ModTime: time.UnixMilli(msg.InternalDate),
			}

			ms := getMonth(month)
			select {
			case ms.ch <- entry:
			case <-egCtx.Done():
				return egCtx.Err()
			}

			fetchedMu.Lock()
			fetched++
			n := fetched
			fetchedMu.Unlock()
			if g.progress != nil && n%100 == 0 {
				fmt.Printf("  fetched %d messages so far...\n", n)
			}
			return nil
		})
	}

	fetchErr := eg.Wait()

	// Close all month channels so uploaders can finalize.
	monthsMu.Lock()
	mss := make([]*monthState, 0, len(months))
	for _, ms := range months {
		close(ms.ch)
		mss = append(mss, ms)
	}
	monthsMu.Unlock()

	// Wait for every uploader and aggregate.
	var totalCount int
	var firstUploadErr error
	for _, ms := range mss {
		err := <-ms.done
		if err != nil && !errors.Is(err, archive.ErrNoChanges) {
			if firstUploadErr == nil {
				firstUploadErr = err
			}
			continue
		}
		// TrackItem each entry only if the upload succeeded (or was a no-op
		// against bytes-identical existing archive).
		for _, entry := range ms.entries {
			if g.metaDB != nil {
				if trackErr := g.metaDB.TrackItem(&metadata.Item{
					Service:    "gmail",
					User:       user,
					ObjectKey:  storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", monthForEntry(entry))),
					ItemPath:   entry.Name,
					ItemID:     entryNameToID(entry.Name),
					Size:       int64(len(entry.Data)),
					Checksum:   archive.Checksum(entry.Data),
					ModifiedAt: entry.ModTime,
				}); trackErr != nil {
					return totalCount, fmt.Errorf("tracking %s: %w", entry.Name, trackErr)
				}
			}
			totalCount++
		}
	}

	if g.progress != nil {
		fmt.Printf("  fetched %d messages\n", fetched)
	}

	if fetchErr != nil {
		return totalCount, fetchErr
	}
	if firstUploadErr != nil {
		return totalCount, firstUploadErr
	}

	if g.metaDB != nil && runID != 0 {
		if err := g.metaDB.CompleteBackup(runID); err != nil {
			return totalCount, fmt.Errorf("completing backup record: %w", err)
		}
	}
	return totalCount, nil
}

func (g *GmailBackup) listAllIDs(ctx context.Context, svc *gmail.Service, user string) ([]string, error) {
	listLimiter := rate.NewLimiter(rate.Limit(gmailListRateLimit), gmailListRateBurst)

	var listQuery string
	if g.maxAge > 0 {
		cutoff := time.Now().Add(-g.maxAge).Format("2006/01/02")
		listQuery = fmt.Sprintf("after:%s", cutoff)
	}

	var ids []string
	pageToken := ""
	for {
		if err := listLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit waiting for list: %w", err)
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
				return nil, nil
			}
			return nil, fmt.Errorf("listing messages: %w", err)
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return ids, nil
}

// runMonthUploader pumps entries from ms.ch into the per-month archive,
// streamed straight to S3. On normal completion it sends nil on ms.done; on
// no-change merge it sends archive.ErrNoChanges; on failure it sends the error.
func (g *GmailBackup) runMonthUploader(ctx context.Context, user, month string, full bool, ms *monthState) {
	objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))

	// Drain the channel up-front into a slice so we can both pass them to
	// StreamMerge (which needs all newEntries) and TrackItem them after the
	// upload completes. One month of messages comfortably fits in memory.
	for entry := range ms.ch {
		ms.mu.Lock()
		ms.entries = append(ms.entries, entry)
		ms.mu.Unlock()
	}

	entries := ms.entries
	if len(entries) == 0 {
		ms.done <- nil
		return
	}

	uploadErr := streamUpload(ctx, g.store, objKey, func(w io.Writer) error {
		if !full {
			existing, downloadErr := g.store.Download(ctx, objKey)
			if downloadErr == nil {
				err := archive.StreamMerge(existing, w, entries)
				existing.Close()
				return err
			}
		}
		aw := archive.NewWriter(w)
		for _, e := range entries {
			if err := aw.Append(e); err != nil {
				return err
			}
		}
		return aw.Close()
	})

	if g.progress != nil && uploadErr == nil {
		fmt.Printf("  ↑ %s\n", objKey)
	}
	ms.done <- uploadErr
}

// monthForEntry recovers the YYYY-MM bucket for an entry from its ModTime,
// used at TrackItem time to compute its object key without re-parsing.
func monthForEntry(e archive.ArchiveEntry) string {
	return e.ModTime.Format("2006-01")
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
