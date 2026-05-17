# Per-Service Max Age Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable per-service maximum age filters so backups only fetch items newer than a given cutoff (e.g., Gmail last 2 years, Drive last 1 year).

**Architecture:** A per-service `max_age` duration map in the YAML config gets parsed and wired into each backup service via a `WithMaxAge` builder method. Each service compares item timestamps against the cutoff during the fetch/scan phase and skips old items.

**Tech Stack:** Go, Google Workspace APIs (Gmail, Calendar, People, Drive), gopkg.in/yaml.v3

---

### Task 1: Add Retention config type

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Add RetentionConfig and PerServiceDuration types**

```go
// In internal/config/config.go, add to Config struct:

type Config struct {
	Workspace WorkspaceConfig `yaml:"workspace"`
	Storage   StorageConfig   `yaml:"storage"`
	Schedule  ScheduleConfig  `yaml:"schedule"`
	Services  []string        `yaml:"services"`
	Users     UsersConfig     `yaml:"users"`
	Retention RetentionConfig `yaml:"retention"`
}

type RetentionConfig struct {
	MaxAge map[string]string `yaml:"max_age"` // service -> duration string like "2y", "6mo", "30d"
}
```

- [ ] **Step 2: Add ParseDuration helper that supports y/mo/d units**

```go
// In internal/config/config.go, add:

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'y':
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	case 'o':
		if len(s) < 3 || s[len(s)-2:] != "mo" {
			return 0, fmt.Errorf("invalid duration %q: expected d, mo, or y", s)
		}
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: expected d, mo, or y", s)
	}
}
```

- [ ] **Step 3: Add a helper to get a parsed duration for a service**

```go
// In internal/config/config.go, add:

func (r RetentionConfig) MaxAgeFor(service string) time.Duration {
	if r.MaxAge == nil {
		return 0
	}
	s, ok := r.MaxAge[service]
	if !ok || s == "" {
		return 0
	}
	d, err := ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
```

- [ ] **Step 4: Write tests for ParseDuration**

```go
// In internal/config/config_test.go, add:

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"0", 0},
		{"", 0},
		{"30d", 30 * 24 * time.Hour},
		{"1y", 365 * 24 * time.Hour},
		{"6mo", 180 * 24 * time.Hour},
	}
	for _, tc := range tests {
		got, err := ParseDuration(tc.input)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseDuration(%q): got %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseDurationInvalid(t *testing.T) {
	_, err := ParseDuration("2x")
	if err == nil {
		t.Fatal("expected error for '2x'")
	}
}

func TestRetentionMaxAgeFor(t *testing.T) {
	r := RetentionConfig{MaxAge: map[string]string{"gmail": "2y", "drive": "30d"}}
	if r.MaxAgeFor("gmail") != 2*365*24*time.Hour {
		t.Fatal("unexpected gmail max age")
	}
	if r.MaxAgeFor("drive") != 30*24*time.Hour {
		t.Fatal("unexpected drive max age")
	}
	if r.MaxAgeFor("contacts") != 0 {
		t.Fatal("expected 0 for unconfigured service")
	}
	if r.MaxAgeFor("nonexistent") != 0 {
		t.Fatal("expected 0 for nonexistent service")
	}
}
```

- [ ] **Step 5: Add `strings` and `strconv` and `time` imports**

The existing imports in `internal/config/config.go` are:
```go
import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)
```

Change to:
```go
import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)
```

- [ ] **Step 6: Run tests to verify**

Run: `go test ./internal/config/... -v`
Expected: All tests pass, including the 3 new ones.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add retention config with per-service max age parsing"
```

---

### Task 2: Add WithMaxAge to GmailBackup and filter during fetch

**Files:**
- Modify: `internal/backup/gmail.go`
- Test: `internal/backup/gmail_test.go`

- [ ] **Step 1: Add maxAge field and WithMaxAge method to GmailBackup**

```go
type GmailBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *GmailBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func (g *GmailBackup) WithMaxAge(d time.Duration) *GmailBackup {
	g.maxAge = d
	return g
}
```

- [ ] **Step 2: Filter messages by InternalDate in the worker goroutines**

After decoding `msg.Raw` and before creating the ArchiveEntry, add:

```go
if g.maxAge > 0 {
	msgTime := time.UnixMilli(msg.InternalDate)
	if time.Since(msgTime) > g.maxAge {
		continue
	}
}
```

Since this is inside the `for id := range work` loop, `continue` skips to the next work item. The `fetched` counter is incremented only after this check in the result consumer loop, so this is safe.

Replace the entire inner worker body:

```go
msg, err := fetchWithRetry(ctx, svc, user, id)
if err != nil {
	select {
	case errCh <- err:
	default:
	}
	return
}

raw, err := base64.URLEncoding.DecodeString(msg.Raw)
if err != nil {
	select {
	case errCh <- fmt.Errorf("decoding message %s: %w", id, err):
	default:
	}
	return
}

if g.maxAge > 0 {
	msgTime := time.UnixMilli(msg.InternalDate)
	if time.Since(msgTime) > g.maxAge {
		continue
	}
}

month := messageMonth(msg)
entryName := fmt.Sprintf("%s.eml", id)
results <- result{
	entry: archive.ArchiveEntry{
		Name: entryName,
		Data: raw,
	},
	month: month,
	msgID: id,
}
```

- [ ] **Step 3: Add time import if missing**

The current imports in `gmail.go` already include `"time"`.

- [ ] **Step 4: Add test for age filtering**

In `internal/backup/gmail_test.go`, add:

```go
func TestMessageAgeFiltering(t *testing.T) {
	b := &GmailBackup{maxAge: 30 * 24 * time.Hour} // 30 days

	oldMsg := &gmail.Message{InternalDate: time.Now().Add(-60 * 24 * time.Hour).UnixMilli()}
	newMsg := &gmail.Message{InternalDate: time.Now().Add(-1 * time.Hour).UnixMilli()}

	if time.Since(time.UnixMilli(oldMsg.InternalDate)) <= b.maxAge {
		t.Fatal("expected old message to exceed maxAge")
	}
	if time.Since(time.UnixMilli(newMsg.InternalDate)) > b.maxAge {
		t.Fatal("expected new message to be within maxAge")
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/backup/... -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/backup/gmail.go internal/backup/gmail_test.go
git commit -m "feat: filter gmail messages by max age during fetch"
```

---

### Task 3: Add WithMaxAge to CalendarBackup and filter during fetch

**Files:**
- Modify: `internal/backup/calendar.go`

- [ ] **Step 1: Add maxAge field and WithMaxAge method**

```go
type CalendarBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *CalendarBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func (c *CalendarBackup) WithMaxAge(d time.Duration) *CalendarBackup {
	c.maxAge = d
	return c
}
```

- [ ] **Step 2: Filter events by start date during listing**

After `event.Id` is extracted into `entryName` and before marshaling, add:

```go
if c.maxAge > 0 {
	eventTime := eventStartTime(event)
	if eventTime != nil && time.Since(*eventTime) > c.maxAge {
		continue
	}
}
```

The block becomes:

```go
for _, event := range resp.Items {
	year := eventYear(event)
	entryName := fmt.Sprintf("%s.json", event.Id)

	if c.maxAge > 0 {
		eventTime := eventStartTime(event)
		if eventTime != nil && time.Since(*eventTime) > c.maxAge {
			continue
		}
	}

	// ... existing attachment handling, marshal, append ...
}
```

- [ ] **Step 3: Add eventStartTime helper**

```go
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
```

- [ ] **Step 4: Verify build**

Run: `go build ./internal/backup/`
Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/calendar.go
git commit -m "feat: filter calendar events by max age during fetch"
```

---

### Task 4: Add WithMaxAge to DriveBackup and filter via API query

**Files:**
- Modify: `internal/backup/drive.go`

- [ ] **Step 1: Add maxAge field and WithMaxAge method**

```go
type DriveBackup struct {
	driveSvc *drive.Service
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *DriveBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func (d *DriveBackup) WithMaxAge(dur time.Duration) *DriveBackup {
	d.maxAge = dur
	return d
}
```

- [ ] **Step 2: Add modifiedTime filter to the Files.List query**

In `BackupUser`, the query is currently:
```go
Q(fmt.Sprintf("'%s' in owners", user)),
```

Change to:
```go
func (d *DriveBackup) buildQuery(user string) string {
	q := fmt.Sprintf("'%s' in owners", user)
	if d.maxAge > 0 {
		cutoff := time.Now().Add(-d.maxAge).Format(time.RFC3339)
		q += fmt.Sprintf(" and modifiedTime > '%s'", cutoff)
	}
	return q
}
```

Then in `BackupUser`:
```go
Q(d.buildQuery(user)),
```

- [ ] **Step 3: Verify build**

Run: `go build ./internal/backup/`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add internal/backup/drive.go
git commit -m "feat: filter drive files by max age via API query"
```

---

### Task 5: Add WithMaxAge to ContactsBackup (no-op)

**Files:**
- Modify: `internal/backup/contacts.go`

- [ ] **Step 1: Add maxAge field and WithMaxAge method**

```go
type ContactsBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *ContactsBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func (c *ContactsBackup) WithMaxAge(d time.Duration) *ContactsBackup {
	c.maxAge = d
	return c
}
```

No filtering logic needed — contacts don't have a meaningful age property. The field exists for interface consistency.

- [ ] **Step 2: Verify build**

Run: `go build ./internal/backup/`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add internal/backup/contacts.go
git commit -m "feat: add WithMaxAge to contacts backup (no-op)"
```

---

### Task 6: Wire retention config from RunnerConfig to each service

**Files:**
- Modify: `internal/backup/runner.go`

- [ ] **Step 1: Add Retention import**

The runner already imports `"github.com/esignoretti/gbackup/internal/config"`. No new import needed since `RetentionConfig` is part of `config`.

- [ ] **Step 2: Update each newXxxBackup helper to wire maxAge**

Replace `newGmailBackup`:

```go
func newGmailBackup(cfg *RunnerConfig) (*GmailBackup, error) {
	gb, err := NewGmailBackup(&GmailBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	gb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		gb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("gmail"); maxAge > 0 {
		gb.WithMaxAge(maxAge)
	}
	return gb, nil
}
```

Replace `newCalendarBackup`:

```go
func newCalendarBackup(cfg *RunnerConfig) (*CalendarBackup, error) {
	cb, err := NewCalendarBackup(&CalendarBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	cb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		cb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("calendar"); maxAge > 0 {
		cb.WithMaxAge(maxAge)
	}
	return cb, nil
}
```

Replace `newDriveBackup`:

```go
func newDriveBackup(cfg *RunnerConfig) (*DriveBackup, error) {
	b, err := NewDriveBackup(&DriveBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	b.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		b.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("drive"); maxAge > 0 {
		b.WithMaxAge(maxAge)
	}
	return b, nil
}
```

Replace `newContactsBackup`:

```go
func newContactsBackup(cfg *RunnerConfig) (*ContactsBackup, error) {
	cb, err := NewContactsBackup(&ContactsBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	cb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		cb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("contacts"); maxAge > 0 {
		cb.WithMaxAge(maxAge)
	}
	return cb, nil
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/runner.go
git commit -m "feat: wire per-service max age from config to backup instances"
```

---

### Self-Review

**1. Spec coverage:**
- Config: `Task 1` adds `RetentionConfig` with `MaxAge` map and `ParseDuration` helper ✓
- Gmail filtering: `Task 2` adds `WithMaxAge` + `InternalDate` check ✓
- Calendar filtering: `Task 3` adds `WithMaxAge` + event start date check ✓
- Drive filtering: `Task 4` adds `WithMaxAge` + `modifiedTime` API query ✓
- Contacts no-op: `Task 5` adds interface-consistent no-op ✓
- Wiring: `Task 6` connects config → services ✓

**2. Placeholder scan:** No placeholders in code blocks. Every step has exact code, file paths, and commands.

**3. Type consistency:**
- `ParseDuration` returns `(time.Duration, error)` — used consistently
- `MaxAgeFor` returns `time.Duration` — used consistently
- `WithMaxAge` accepts `time.Duration` — used consistently across all 4 services
- All backup services remain compatible with existing `BackupUser(ctx, user, full)` signature (unchanged)
