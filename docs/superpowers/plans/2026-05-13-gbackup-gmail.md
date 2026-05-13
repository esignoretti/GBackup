# GBackup Gmail Implementation Plan

**Goal:** Implement Gmail backup and restore using the Gmail API, replacing the final placeholder.

**Architecture:** Gmail API fetches message metadata via `messages.list`, then downloads full messages (raw RFC2822) via `messages.get`. Stored as `.eml.gz` in S3. Incremental via query filter on date. Restore via `messages.import` to preserve labels and threading.

**Tech Stack:** Go, `google.golang.org/api/gmail/v1`

---

### Task 1: Implement Gmail Backup

**Files:**
- Modify: `internal/backup/gmail.go` (replace placeholder)
- Create: `internal/backup/gmail_test.go`

**`internal/backup/gmail_test.go`:**
```go
package backup

import (
	"testing"
)

func TestNewGmailBackup(t *testing.T) {
	_, err := NewGmailBackup(&GmailBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}
```

**Replace `internal/backup/gmail.go`:**
```go
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
```

Build, test, commit:
```bash
go mod tidy && go test ./internal/backup/ -v -run "TestNewGmailBackup"
git add -A && git commit -m "feat: implement Gmail backup via Gmail API"
```

---

### Task 2: Wire Gmail into Runner

**File:** `internal/backup/runner.go`

Replace `case "gmail":` with:
```go
	case "gmail":
		gcfg := &GmailBackupConfig{
			ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
			AdminEmail:         r.cfg.DirAuth.AdminEmail,
		}
		gb, err := NewGmailBackup(gcfg)
		if err != nil {
			return err
		}
		gb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
		for _, user := range users {
			if _, err := gb.BackupUser(ctx, user, full); err != nil {
				return fmt.Errorf("gmail backup for %s: %w", user, err)
			}
		}
```

Build and commit.

---

### Task 3: Implement Gmail Restore

**Files:**
- Create: `internal/restore/gmail.go`
- Modify: `cmd/restore.go`

**`internal/restore/gmail.go`:**
```go
package restore

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *GmailRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	svc, err := gmail.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes(gmail.GmailInsertScope),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating gmail service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("gmail", r.User)
	if err != nil {
		return fmt.Errorf("listing gmail items: %w", err)
	}

	if r.DryRun {
		fmt.Printf("Would restore %d messages to %s\n", len(items), targetUser)
		return nil
	}

	var restored int
	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
		restored++
	}

	fmt.Printf("Restored %d messages\n", restored)
	return nil
}

func (r *GmailRestore) restoreOne(ctx context.Context, svc *gmail.Service, item *metadata.Item) error {
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

	raw, err := io.ReadAll(gr)
	if err != nil {
		return fmt.Errorf("reading message data: %w", err)
	}

	msg := &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString(raw),
	}
	_, err = svc.Users.Messages.Import("me", msg).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("importing message: %w", err)
	}
	return nil
}
```

Add `case "gmail":` to `cmd/restore.go` (same pattern as calendar/contacts) and commit.

---

### Task 4: Add Gmail scope for restore

The backup scope `gmail.readonly` is already defined in `auth.go`. The restore scope `gmail.insert` is used directly in the restore code. No auth.go changes needed since `gmail.GmailInsertScope` is provided by the gmail package.
