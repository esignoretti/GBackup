package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type CalendarBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *CalendarBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
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

func (c *CalendarBackup) WithProgress(p *progress.Reporter) *CalendarBackup {
	c.progress = p
	return c
}

func (c *CalendarBackup) WithMaxAge(d time.Duration) *CalendarBackup {
	c.maxAge = d
	return c
}

func (c *CalendarBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if c.progress != nil {
		c.progress.Service("calendar", user, runType)
	}

	svc, err := calendar.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("calendar")...),
		option.ImpersonateCredentials(user),
	)
	if err != nil {
		return 0, fmt.Errorf("creating calendar service for %s: %w", user, err)
	}

	buckets := make(map[string][]archive.ArchiveEntry)
	var fetched int
	pageToken := ""
	for {
		call := svc.Events.List("primary").
			Context(ctx).
			SingleEvents(true).
			MaxResults(2500).
			ShowDeleted(false).
			TimeMin(time.Now().Add(-366 * 24 * time.Hour).Format(time.RFC3339))
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

			if c.maxAge > 0 {
				eventTime := eventStartTime(event)
				if eventTime != nil && time.Since(*eventTime) > c.maxAge {
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
				return 0, fmt.Errorf("marshaling event %s: %w", event.Id, err)
			}

			buckets[year] = append(buckets[year], archive.ArchiveEntry{
				Name: entryName,
				Data: data,
			})
			fetched++
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if c.progress != nil {
		c.progress.FetchDone("events", fetched)
	}

	if len(buckets) == 0 {
		return 0, nil
	}

	var totalCount int
	for year, entries := range buckets {
		objKey := storage.ObjectKey("calendar", user, fmt.Sprintf("%s.tar.gz", year))

		var archiveData []byte
		if !full {
			existing, err := c.store.Download(ctx, objKey)
			if err == nil {
				archiveData, err = archive.AppendToArchive(existing, entries)
				if err != nil {
					return totalCount, fmt.Errorf("appending to archive %s: %w", objKey, err)
				}
			} else {
				archiveData, err = archive.Create(entries)
				if err != nil {
					return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
				}
			}
		} else {
			archiveData, err = archive.Create(entries)
			if err != nil {
				return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
			}
		}

		if err := c.store.Upload(ctx, objKey, bytes.NewReader(archiveData)); err != nil {
			return totalCount, fmt.Errorf("uploading %s: %w", objKey, err)
		}

		if c.progress != nil {
			c.progress.Upload(objKey)
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

func eventStartTime(event *calendar.Event) *time.Time {
	if event.Start == nil {
		return nil
	}
	if event.Start.Date != "" {
		t, err := time.Parse("2006-01-02", event.Start.Date)
		if err == nil {
			return &t
		}
	}
	if event.Start.DateTime != "" {
		t, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err == nil {
			return &t
		}
	}
	return nil
}
