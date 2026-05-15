package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
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

	buckets := make(map[string][]archive.ArchiveEntry)
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
			return 0, fmt.Errorf("listing events: %w", err)
		}

		for _, event := range resp.Items {
			year := eventYear(event)
			entryName := fmt.Sprintf("%s.json", event.Id)

			for _, att := range event.Attachments {
				if att.FileUrl != "" {
					event.Description = fmt.Sprintf("%s\n\n[Attachment: %s (%s)]", event.Description, att.Title, att.MimeType)
				}
			}
			event.Attachments = nil

			data, err := json.Marshal(event)
			if err != nil {
				return 0, fmt.Errorf("marshaling event %s: %w", event.Id, err)
			}

			buckets[year] = append(buckets[year], archive.ArchiveEntry{
				Name: entryName,
				Data: data,
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

	var totalCount int
	for year, entries := range buckets {
		objKey := storage.ObjectKey("calendar", user, fmt.Sprintf("%s.tar.gz", year))

		var archiveReader io.Reader
		if !full {
			existing, err := c.store.Download(ctx, objKey)
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
		} else {
			archiveReader, err = archive.Create(entries)
			if err != nil {
				return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
			}
		}

		if err := c.store.Upload(ctx, objKey, archiveReader); err != nil {
			return totalCount, fmt.Errorf("uploading %s: %w", objKey, err)
		}

		for _, entry := range entries {
			if c.metaDB != nil {
				c.metaDB.TrackItem(&metadata.Item{
					Service:   "calendar",
					User:      user,
					ObjectKey: objKey,
					ItemPath:  entry.Name,
					ItemID:    strings.TrimSuffix(entry.Name, ".json"),
					Size:      int64(len(entry.Data)),
					Checksum:  entry.Name,
				})
			}
			totalCount++
		}
	}

	if c.metaDB != nil {
		c.metaDB.RecordBackup("calendar", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return totalCount, nil
}

func eventYear(event *calendar.Event) string {
	if event.Start == nil {
		return time.Now().Format("2006")
	}
	if event.Start.Date != "" {
		t, err := time.Parse("2006-01-02", event.Start.Date)
		if err == nil {
			return t.Format("2006")
		}
	}
	if event.Start.DateTime != "" {
		t, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err == nil {
			return t.Format("2006")
		}
	}
	return time.Now().Format("2006")
}
