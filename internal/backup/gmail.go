package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"strings"

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
	gmailSvc *gmail.Service
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *GmailBackupConfig
}

func NewGmailBackup(cfg *GmailBackupConfig) (*GmailBackup, error) {
	return &GmailBackup{cfg: cfg}, nil
}

func (g *GmailBackup) WithGmailService(svc *gmail.Service) *GmailBackup {
	g.gmailSvc = svc
	return g
}

func (g *GmailBackup) WithMetaDB(db *metadata.DB) *GmailBackup {
	g.metaDB = db
	return g
}

func (g *GmailBackup) WithStorage(s *storage.Client) *GmailBackup {
	g.store = s
	return g
}

func (g *GmailBackup) initService(ctx context.Context) error {
	if g.gmailSvc != nil {
		return nil
	}
	svc, err := gmail.NewService(ctx,
		option.WithCredentialsFile(g.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("gmail")...),
		option.ImpersonateCredentials(g.cfg.AdminEmail),
	)
	if err != nil {
		return fmt.Errorf("creating gmail service: %w", err)
	}
	g.gmailSvc = svc
	return nil
}

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	if err := g.initService(ctx); err != nil {
		return 0, err
	}

	userEmail := user
	var count int
	pageToken := ""
	for {
		call := g.gmailSvc.Users.Messages.List(userEmail).
			Context(ctx).
			MaxResults(500).
			IncludeSpamTrash(false)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			if isGmailPreconditionFailed(err) {
				fmt.Printf("  gmail: %s has no Gmail mailbox, skipping\n", user)
				return 0, nil
			}
			return count, fmt.Errorf("listing messages: %w", err)
		}

		for _, m := range resp.Messages {
			msg, err := g.gmailSvc.Users.Messages.Get(userEmail, m.Id).Context(ctx).Format("raw").Do()
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

func isGmailPreconditionFailed(err error) bool {
	return strings.Contains(err.Error(), "failedPrecondition") &&
		(strings.Contains(err.Error(), "mailbox") || strings.Contains(err.Error(), "Account"))
}
