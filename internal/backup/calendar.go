package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	var runID int64
	if c.metaDB != nil {
		id, err := c.metaDB.StartBackup("calendar", user, runType)
		if err != nil {
			return 0, fmt.Errorf("recording backup start: %w", err)
		}
		runID = id
	}

	ts, err := gws.UserTokenSource(ctx, c.cfg.ServiceAccountFile, user, gws.ScopesForService("calendar"))
	if err != nil {
		return 0, fmt.Errorf("creating calendar token for %s: %w", user, err)
	}
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return 0, fmt.Errorf("creating calendar service for %s: %w", user, err)
	}

	buckets := make(map[string][]archive.ArchiveEntry)
	var fetched int
	pageToken := ""
	for {
		listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
		now := time.Now()
		call := svc.Events.List("primary").
			Context(listCtx).
			SingleEvents(true).
			MaxResults(2500).
			ShowDeleted(false).
			TimeMin(now.Add(-366 * 24 * time.Hour).Format(time.RFC3339)).
			TimeMax(now.Add(366 * 24 * time.Hour).Format(time.RFC3339))
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		listCancel()
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

			modTime := time.Now()
			if event.Updated != "" {
				if t, parseErr := time.Parse(time.RFC3339, event.Updated); parseErr == nil {
					modTime = t
				}
			}

			buckets[year] = append(buckets[year], archive.ArchiveEntry{
				Name:    entryName,
				Data:    data,
				ModTime: modTime,
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

		uploadErr := streamUpload(ctx, c.store, objKey, func(w io.Writer) error {
			if !full {
				existing, downloadErr := c.store.Download(ctx, objKey)
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
		noChanges := errors.Is(uploadErr, archive.ErrNoChanges)
		if uploadErr != nil && !noChanges {
			return totalCount, fmt.Errorf("writing archive %s: %w", objKey, uploadErr)
		}

		if c.progress != nil && !noChanges {
			c.progress.Upload(objKey)
		}

		for _, entry := range entries {
			if c.metaDB != nil {
				if err := c.metaDB.TrackItem(&metadata.Item{
					Service:    "calendar",
					User:       user,
					ObjectKey:  objKey,
					ItemPath:   entry.Name,
					ItemID:     strings.TrimSuffix(entry.Name, ".json"),
					Size:       int64(len(entry.Data)),
					Checksum:   archive.Checksum(entry.Data),
					ModifiedAt: entry.ModTime,
				}); err != nil {
					return totalCount, fmt.Errorf("tracking %s: %w", entry.Name, err)
				}
			}
			totalCount++
		}
	}

	if c.metaDB != nil && runID != 0 {
		if err := c.metaDB.CompleteBackup(runID); err != nil {
			return totalCount, fmt.Errorf("completing backup record: %w", err)
		}
	}
	return totalCount, nil
}

// parseEventStart parses event.Start.Date or event.Start.DateTime, whichever
// is set, returning (time, true). If neither is set or both fail to parse it
// returns the zero value and false.
func parseEventStart(event *calendar.Event) (time.Time, bool) {
	if event.Start == nil {
		return time.Time{}, false
	}
	if event.Start.Date != "" {
		if t, err := time.Parse("2006-01-02", event.Start.Date); err == nil {
			return t, true
		}
	}
	if event.Start.DateTime != "" {
		if t, err := time.Parse(time.RFC3339, event.Start.DateTime); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func eventYear(event *calendar.Event) string {
	if t, ok := parseEventStart(event); ok {
		return t.Format("2006")
	}
	return time.Now().Format("2006")
}

func eventStartTime(event *calendar.Event) *time.Time {
	if t, ok := parseEventStart(event); ok {
		return &t
	}
	return nil
}
