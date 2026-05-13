# GBackup Calendar Implementation Plan

**Goal:** Implement Calendar backup and restore using the Google Calendar API, replacing the placeholder.

**Architecture:** Follows same pattern as contacts/Drive. Calendar API lists events via `Events.List()`, stores as JSON.gz in S3. Incremental via `UpdatedMin`.

**Tech Stack:** Go, `google.golang.org/api/calendar/v3`

---

### Task 1: Implement Calendar Backup

**Files:**
- Modify: `internal/backup/calendar.go` (replace placeholder)
- Create: `internal/backup/calendar_test.go`

**Step 1: test file `internal/backup/calendar_test.go`:**
```go
package backup

import (
	"testing"
)

func TestNewCalendarBackup(t *testing.T) {
	_, err := NewCalendarBackup(&CalendarBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}
```

**Step 2: Replace `internal/backup/calendar.go`:**
```go
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
	calSvc *calendar.Service
	metaDB *metadata.DB
	store  *storage.Client
	cfg    *CalendarBackupConfig
}

func NewCalendarBackup(cfg *CalendarBackupConfig) (*CalendarBackup, error) {
	return &CalendarBackup{cfg: cfg}, nil
}

func (c *CalendarBackup) WithCalendarService(svc *calendar.Service) *CalendarBackup {
	c.calSvc = svc
	return c
}

func (c *CalendarBackup) WithMetaDB(db *metadata.DB) *CalendarBackup {
	c.metaDB = db
	return c
}

func (c *CalendarBackup) WithStorage(s *storage.Client) *CalendarBackup {
	c.store = s
	return c
}

func (c *CalendarBackup) initService(ctx context.Context) error {
	if c.calSvc != nil {
		return nil
	}
	svc, err := calendar.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("calendar")...),
		option.ImpersonateCredentials(c.cfg.AdminEmail),
	)
	if err != nil {
		return fmt.Errorf("creating calendar service: %w", err)
	}
	c.calSvc = svc
	return nil
}

func (c *CalendarBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	if err := c.initService(ctx); err != nil {
		return 0, err
	}

	var count int
	pageToken := ""
	for {
		call := c.calSvc.Events.List("primary").
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
			etag := event.ETag
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
					Service:  "calendar",
					User:     user,
					ObjectKey: objKey,
					ItemID:   event.Id,
					Size:     int64(buf.Len()),
					Checksum: etag,
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
```

**Step 3: Verify and commit:**
```bash
go mod tidy && go test ./internal/backup/ -v -run "TestNewCalendarBackup"
git add -A && git commit -m "feat: implement Calendar backup via Calendar API"
```

---

### Task 2: Wire Calendar into Runner

**File:** `internal/backup/runner.go`

Replace `case "gmail", "calendar":` with:
```go
	case "calendar":
		ccfg := &CalendarBackupConfig{
			ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
			AdminEmail:         r.cfg.DirAuth.AdminEmail,
		}
		cb, err := NewCalendarBackup(ccfg)
		if err != nil {
			return err
		}
		cb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
		for _, user := range users {
			if _, err := cb.BackupUser(ctx, user, full); err != nil {
				return fmt.Errorf("calendar backup for %s: %w", user, err)
			}
		}
	case "gmail":
		return fmt.Errorf("%s backup not yet implemented", service)
```

Verify and commit:
```bash
go build ./...
git add -A && git commit -m "feat: wire calendar backup into runner"
```

---

### Task 3: Implement Calendar Restore

**Files:**
- Create: `internal/restore/calendar.go`
- Modify: `cmd/restore.go`

**`internal/restore/calendar.go`:**
```go
package restore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *CalendarRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	svc, err := calendar.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes(calendar.CalendarEventsScope),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating calendar service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("calendar", r.User)
	if err != nil {
		return fmt.Errorf("listing calendar items: %w", err)
	}

	if r.DryRun {
		fmt.Printf("Would restore %d events to %s\n", len(items), targetUser)
		return nil
	}

	var restored int
	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
		restored++
	}

	fmt.Printf("Restored %d events\n", restored)
	return nil
}

func (r *CalendarRestore) restoreOne(ctx context.Context, svc *calendar.Service, item *metadata.Item) error {
	rc, err := r.Store.Download(ctx, item.ObjectKey)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", item.ObjectKey, err)
	}
	defer rc.Close()

	gr, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer gr.Close()

	data, err := io.ReadAll(gr)
	if err != nil {
		return fmt.Errorf("reading event data: %w", err)
	}

	var event calendar.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshaling event: %w", err)
	}

	event.Id = ""
	_, err = svc.Events.Insert("primary", &event).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating event: %w", err)
	}
	return nil
}
```

**`cmd/restore.go`** — add calendar case (same pattern as contacts):
```go
case "calendar":
    r := &restore.CalendarRestore{
        Store:              store,
        MetaDB:             db,
        ServiceAccountFile: cfg.Workspace.AdminEmail + ".json",
        AdminEmail:         cfg.Workspace.AdminEmail,
        User:               user,
        Date:               restoreDate,
        DryRun:             true,
        TargetUser:         restoreTarget,
    }
    if err := r.Run(context.Background()); err != nil {
        return err
    }
    if !restoreDryRun {
        fmt.Print("Proceed with restore? [y/N]: ")
        var confirm string
        fmt.Scanln(&confirm)
        if confirm != "y" && confirm != "Y" {
            return fmt.Errorf("restore cancelled")
        }
        r.DryRun = false
        if err := r.Run(context.Background()); err != nil {
            return err
        }
    }
```

Verify and commit:
```bash
go mod tidy && go build ./... && go test ./... -v -race 2>&1 | tail -5
git add -A && git commit -m "feat: add calendar restore with Calendar API"
```
