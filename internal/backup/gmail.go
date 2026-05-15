package backup

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type GmailBackup struct {
	metaDB *metadata.DB
	store  *storage.Client
	cfg    *GmailBackupConfig
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
	svc, err := g.gmailServiceForUser(ctx, user)
	if err != nil {
		return 0, fmt.Errorf("creating gmail service for %s: %w", user, err)
	}

	buckets := make(map[string][]archive.ArchiveEntry)
	pageToken := ""
	for {
		call := svc.Users.Messages.List(user).
			Context(ctx).
			MaxResults(500).
			IncludeSpamTrash(false)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			if isServiceDisabled(err) {
				fmt.Printf("  gmail: API not enabled for %s, skipping\n", user)
				return 0, nil
			}
			return 0, fmt.Errorf("listing messages: %w", err)
		}

		for _, m := range resp.Messages {
			msg, err := svc.Users.Messages.Get(user, m.Id).Context(ctx).Format("raw").Do()
			if err != nil {
				return 0, fmt.Errorf("getting message %s: %w", m.Id, err)
			}

			raw, err := base64.URLEncoding.DecodeString(msg.Raw)
			if err != nil {
				return 0, fmt.Errorf("decoding message %s: %w", m.Id, err)
			}

			month := messageMonth(msg)
			entryName := fmt.Sprintf("%s.eml", m.Id)
			buckets[month] = append(buckets[month], archive.ArchiveEntry{
				Name: entryName,
				Data: raw,
			})
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if len(buckets) == 0 {
		return 0, nil
	}

	now := time.Now()
	threeMonthsAgo := now.AddDate(0, -2, 0).Format("2006-01")

	var totalCount int
	for month, entries := range buckets {
		objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))

		var archiveReader io.Reader
		if !full && month >= threeMonthsAgo {
			existing, err := g.store.Download(ctx, objKey)
			if err == nil {
				archiveReader, err = archive.AppendToArchive(existing, entries)
				if err != nil {
					return totalCount, fmt.Errorf("appending to archive %s: %w", objKey, err)
				}
			} else {
				archiveReader, err = archive.Create(entries)
				if err != nil {
					return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
				}
			}
		} else if !full {
			continue
		} else {
			archiveReader, err = archive.Create(entries)
			if err != nil {
				return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
			}
		}

		if err := g.store.Upload(ctx, objKey, archiveReader); err != nil {
			return totalCount, fmt.Errorf("uploading %s: %w", objKey, err)
		}

		for _, entry := range entries {
			if g.metaDB != nil {
				g.metaDB.TrackItem(&metadata.Item{
					Service:   "gmail",
					User:      user,
					ObjectKey: objKey,
					ItemPath:  entry.Name,
					ItemID:    entryNameToID(entry.Name),
					Size:      int64(len(entry.Data)),
					Checksum:  entry.Name,
				})
			}
			totalCount++
		}
	}

	if g.metaDB != nil {
		g.metaDB.RecordBackup("gmail", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return totalCount, nil
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
