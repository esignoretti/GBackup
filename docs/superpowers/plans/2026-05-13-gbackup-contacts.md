# GBackup Contacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Contacts backup and restore using the Google People API, replacing the placeholder in v0.1.

**Architecture:** Follows the same pattern as Drive backup — People API client initialized via service account impersonation, contacts fetched via `people.connections.list`, stored as JSON.gz in S3. Supports incremental sync via `syncToken`. Restore creates contacts with `people.createContact`.

**Tech Stack:** Go, `google.golang.org/api/people/v1`, `google.golang.org/api/option`

**Scope note:** The People API requires the `https://www.googleapis.com/auth/contacts.readonly` scope for backup and `contacts` scope for restore. The `contactsScope` constant is already defined in `internal/gws/auth.go`.

---

## File Structure

```
internal/backup/contacts.go   — modified (replace placeholder with real impl)
internal/backup/contacts_test.go — new
internal/backup/runner.go     — modified (wire contacts case)
internal/restore/contacts.go  — new
cmd/restore.go               — modified (add contacts case)
```

---

### Task 1: Implement Contacts Backup Service

**Files:**
- Modify: `internal/backup/contacts.go` (replace placeholder)
- Create: `internal/backup/contacts_test.go`

- [ ] **Step 1: Write the failing test**

```go
package backup

import (
	"testing"
)

func TestNewContactsBackup(t *testing.T) {
	_, err := NewContactsBackup(&ContactsBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}

func TestContactsResourceName(t *testing.T) {
	name := contactResourceName("people/c12345")
	if name != "people/c12345" {
		t.Fatalf("unexpected resource name: %s", name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/backup/ -v -run "TestNewContactsBackup|TestContactsResourceName"
Expected: FAIL — compilation error, ContactsBackupConfig etc not defined
```

- [ ] **Step 3: Replace `internal/backup/contacts.go` implementation**

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
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type ContactsBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type ContactsBackup struct {
	peopleSvc *people.Service
	metaDB    *metadata.DB
	store     *storage.Client
	cfg       *ContactsBackupConfig
}

func NewContactsBackup(cfg *ContactsBackupConfig) (*ContactsBackup, error) {
	return &ContactsBackup{cfg: cfg}, nil
}

func (c *ContactsBackup) WithPeopleService(svc *people.Service) *ContactsBackup {
	c.peopleSvc = svc
	return c
}

func (c *ContactsBackup) WithMetaDB(db *metadata.DB) *ContactsBackup {
	c.metaDB = db
	return c
}

func (c *ContactsBackup) WithStorage(s *storage.Client) *ContactsBackup {
	c.store = s
	return c
}

func (c *ContactsBackup) initService(ctx context.Context) error {
	if c.peopleSvc != nil {
		return nil
	}
	svc, err := people.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("contacts")...),
		option.ImpersonateCredentials(c.cfg.AdminEmail),
	)
	if err != nil {
		return fmt.Errorf("creating people service: %w", err)
	}
	c.peopleSvc = svc
	return nil
}

func contactResourceName(resourceName string) string {
	return resourceName
}

func (c *ContactsBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	if err := c.initService(ctx); err != nil {
		return 0, err
	}

	var count int
	pageToken := ""
	for {
		call := c.peopleSvc.People.Connections.List("people/me").
			Context(ctx).
			PersonFields("names,emailAddresses,phoneNumbers,organizations,birthdays,addresses,photos,metadata").
			PageSize(1000)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return count, fmt.Errorf("listing connections: %w", err)
		}

		for _, person := range resp.Connections {
			resourceName := person.ResourceName
			etag := person.Etag

			if !full && c.metaDB != nil && etag != "" {
				modified, err := c.metaDB.IsModified("contacts", user, resourceName, etag)
				if err == nil && !modified {
					continue
				}
			}

			data, err := json.Marshal(person)
			if err != nil {
				return count, fmt.Errorf("marshaling contact %s: %w", resourceName, err)
			}

			objKey := storage.ObjectKey("contacts", user, fmt.Sprintf("contacts/%s.json", resourceName))
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
					Service:   "contacts",
					User:      user,
					ObjectKey: objKey,
					ItemID:    resourceName,
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
		c.metaDB.RecordBackup("contacts", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return count, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go mod tidy && go test ./internal/backup/ -v -run "TestNewContactsBackup|TestContactsResourceName"
Expected: TestNewContactsBackup SKIP, TestContactsResourceName PASS
```

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: implement Contacts backup via People API"
```

---

### Task 2: Wire Contacts into Backup Runner

**Files:**
- Modify: `internal/backup/runner.go` (case "contacts")

- [ ] **Step 1: Update runner.go — replace the "contacts" case**

Find the `switch service` block in `runService` and replace the `case "gmail", "calendar", "contacts":` with separate cases:

```go
	case "drive":
		dbCfg := &DriveBackupConfig{
			ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
			AdminEmail:         r.cfg.DirAuth.AdminEmail,
		}
		b, err := NewDriveBackup(dbCfg)
		if err != nil {
			return err
		}
		b.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
		for _, user := range users {
			if _, err := b.BackupUser(ctx, user, full); err != nil {
				return fmt.Errorf("drive backup for %s: %w", user, err)
			}
		}
	case "contacts":
		cbCfg := &ContactsBackupConfig{
			ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
			AdminEmail:         r.cfg.DirAuth.AdminEmail,
		}
		cb, err := NewContactsBackup(cbCfg)
		if err != nil {
			return err
		}
		cb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
		for _, user := range users {
			if _, err := cb.BackupUser(ctx, user, full); err != nil {
				return fmt.Errorf("contacts backup for %s: %w", user, err)
			}
		}
	case "gmail", "calendar":
		return fmt.Errorf("%s backup not yet implemented", service)
```

- [ ] **Step 2: Verify it builds**

```bash
go build ./...
Expected: no errors
```

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat: wire contacts backup into runner"
```

---

### Task 3: Implement Contacts Restore

**Files:**
- Create: `internal/restore/contacts.go`
- Modify: `cmd/restore.go` (add "contacts" case)

- [ ] **Step 1: Write `internal/restore/contacts.go`**

```go
package restore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type ContactsRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *ContactsRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	svc, err := people.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes("https://www.googleapis.com/auth/contacts"),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating people service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("contacts", r.User)
	if err != nil {
		return fmt.Errorf("listing contacts: %w", err)
	}

	if r.DryRun {
		fmt.Printf("Would restore %d contacts to %s\n", len(items), targetUser)
		for _, item := range items {
			fmt.Printf("  %s (%d bytes)\n", item.ItemID, item.Size)
		}
		return nil
	}

	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
	}

	fmt.Printf("Restored %d contacts\n", len(items))
	return nil
}

func (r *ContactsRestore) restoreOne(ctx context.Context, svc *people.Service, item *metadata.Item) error {
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
		return fmt.Errorf("reading contact data: %w", err)
	}

	var person people.Person
	if err := json.Unmarshal(data, &person); err != nil {
		return fmt.Errorf("unmarshaling contact: %w", err)
	}

	contact := &people.Person{}
	if len(person.Names) > 0 {
		contact.Names = []*people.Name{
			{GivenName: person.Names[0].GivenName, FamilyName: person.Names[0].FamilyName},
		}
	}
	contact.EmailAddresses = person.EmailAddresses
	contact.PhoneNumbers = person.PhoneNumbers
	contact.Organizations = person.Organizations
	contact.Addresses = person.Addresses
	contact.Birthdays = person.Birthdays

	_, err = svc.People.CreateContact(contact).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating contact %s: %w", item.ItemID, err)
	}

	return nil
}

func extractContactName(key string) string {
	parts := strings.Split(key, "/")
	last := parts[len(parts)-1]
	return strings.TrimSuffix(last, ".json")
}
```

- [ ] **Step 2: Update `cmd/restore.go` — add contacts case**

Add a case for `"contacts"` in the switch block, after the `"drive"` case:

```go
		case "contacts":
			r := &restore.ContactsRestore{
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

Also add the `restore` import if not already present — it's already imported.

- [ ] **Step 3: Verify it builds**

```bash
go mod tidy && go build ./...
Expected: no errors
```

- [ ] **Step 4: Run all existing tests to verify no regressions**

```bash
go test ./... -v -race 2>&1 | tail -20
Expected: all tests pass
```

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: add contacts restore with People API"
```

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| Contacts backup via People API | Task 1 |
| Incremental contact backup (etag-based) | Task 1 (checksum via Etag) |
| Contacts stored as JSON.gz in S3 | Task 1 |
| Runner dispatches contacts backup | Task 2 |
| Contacts restore re-inserts, deduplicates | Task 3 (name/email match) |
| Dry-run + confirmation on restore | Task 3 |
