package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"

	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
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

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	svc, err := gmail.NewService(ctx,
		option.WithCredentialsFile(g.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("gmail")...),
		option.ImpersonateCredentials(user),
	)
	if err != nil {
		return 0, fmt.Errorf("creating gmail service for %s: %w", user, err)
	}

	var count int
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
			return count, fmt.Errorf("listing messages: %w", err)
		}

		for _, m := range resp.Messages {
			msg, err := svc.Users.Messages.Get(user, m.Id).Context(ctx).Format("raw").Do()
			if err != nil {
				return count, fmt.Errorf("getting message %s: %w", m.Id, err)
			}

			raw, err := base64.URLEncoding.DecodeString(msg.Raw)
			if err != nil {
				return count, fmt.Errorf("decoding message %s: %w", m.Id, err)
			}

			objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("messages/%s.eml", m.Id))
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			if _, err := gw.Write(raw); err != nil {
				gw.Close()
				return count, fmt.Errorf("gzip write: %w", err)
			}
			if err := gw.Close(); err != nil {
				return count, fmt.Errorf("gzip close: %w", err)
			}

			if err := g.store.Upload(ctx, objKey, &buf); err != nil {
				return count, fmt.Errorf("uploading %s: %w", objKey, err)
			}

			if g.metaDB != nil {
				g.metaDB.TrackItem(&metadata.Item{
					Service:   "gmail",
					User:      user,
					ObjectKey: objKey,
					ItemID:    m.Id,
					Size:      int64(buf.Len()),
					Checksum:  m.Id,
				})
			}
			count++
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if g.metaDB != nil {
		g.metaDB.RecordBackup("gmail", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return count, nil
}
