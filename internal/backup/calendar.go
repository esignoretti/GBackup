package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type CalendarBackup struct {
	metaDB *metadata.DB
	store  *storage.Client
	cfg    *CalendarBackupConfig
}

func NewCalendarBackup(cfg *CalendarBackupConfig) (*CalendarBackup, error) {
	return &CalendarBackup{cfg: cfg}, nil
}

func (c *CalendarBackup) WithMetaDB(db *metadata.DB) *CalendarBackup {
	c.metaDB = db
	return c
}

func (c *CalendarBackup) WithStorage(s *storage.Client) *CalendarBackup {
	c.store = s
	return c
}

func (c *CalendarBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	svc, err := calendar.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("calendar")...),
		option.ImpersonateCredentials(user),
	)
	if err != nil {
		return 0, fmt.Errorf("creating calendar service for %s: %w", user, err)
	}

	var count int
	pageToken := ""
	for {
		call := svc.Events.List("primary").
			Context(ctx).
			SingleEvents(true).
			MaxResults(2500).
			ShowDeleted(false)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return count, fmt.Errorf("listing events: %w", err)
		}

		for _, event := range resp.Items {
			etag := event.Etag
			if !full && c.metaDB != nil && etag != "" {
				modified, err := c.metaDB.IsModified("calendar", user, event.Id, etag)
				if err == nil && !modified {
					continue
				}
			}

			for _, att := range event.Attachments {
				if att.FileUrl != "" {
					event.Description = fmt.Sprintf("%s\n\n[Attachment: %s (%s)]", event.Description, att.Title, att.MimeType)
				}
			}
			event.Attachments = nil

			data, err := json.Marshal(event)
			if err != nil {
				return count, fmt.Errorf("marshaling event %s: %w", event.Id, err)
			}

			objKey := storage.ObjectKey("calendar", user, fmt.Sprintf("events/%s.json", event.Id))
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			if _, err := gw.Write(data); err != nil {
				gw.Close()
				return count, fmt.Errorf("gzip write: %w", err)
			}
			if err := gw.Close(); err != nil {
				return count, fmt.Errorf("gzip close: %w", err)
			}

			if err := c.store.Upload(ctx, objKey, &buf); err != nil {
				return count, fmt.Errorf("uploading %s: %w", objKey, err)
			}

			if c.metaDB != nil {
				c.metaDB.TrackItem(&metadata.Item{
					Service:   "calendar",
					User:      user,
					ObjectKey: objKey,
					ItemID:    event.Id,
					Size:      int64(buf.Len()),
					Checksum:  etag,
				})
			}
			count++
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if c.metaDB != nil {
		c.metaDB.RecordBackup("calendar", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return count, nil
}
