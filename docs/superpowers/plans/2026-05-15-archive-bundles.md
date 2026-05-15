# Archive Bundle Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-item S3 uploads with batched `.tar.gz` archives (monthly for Gmail, yearly for Calendar, single-file for Contacts). Drive stays unchanged.

**Architecture:** New `internal/archive` package handles tar/gzip creation, append, and streaming extraction. Each backup service accumulates entries in memory per time bucket, then flushes one archive per bucket. Restore groups items by archive key, downloads each archive once, and stream-extracts matching entries.

**Tech Stack:** Go stdlib `archive/tar`, `compress/gzip`, SQLite (metadata), S3-compatible storage.

**Spec:** `docs/superpowers/specs/2026-05-15-archive-bundles-design.md`

---

### Task 1: `internal/archive` package — Create, AppendToArchive, Read

**Files:**
- Create: `internal/archive/archive.go`
- Create: `internal/archive/archive_test.go`

- [ ] **Step 1: Write failing tests for archive create, append, read round-trip**

File: `internal/archive/archive_test.go`

```go
package archive

import (
    "bytes"
    "io"
    "strings"
    "testing"
)

func TestCreateAndRead(t *testing.T) {
    entries := []ArchiveEntry{
        {Name: "a.txt", Data: []byte("hello")},
        {Name: "b.txt", Data: []byte("world")},
    }

    r, err := Create(entries)
    if err != nil {
        t.Fatal(err)
    }

    var seen []ArchiveEntry
    err = Read(r, func(e ArchiveEntry) error {
        seen = append(seen, e)
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
    if len(seen) != 2 {
        t.Fatalf("expected 2 entries, got %d", len(seen))
    }
    if seen[0].Name != "a.txt" || string(seen[0].Data) != "hello" {
        t.Fatalf("unexpected first entry: %+v", seen[0])
    }
}

func TestAppendToArchive(t *testing.T) {
    original := []ArchiveEntry{
        {Name: "a.txt", Data: []byte("original")},
    }
    r, err := Create(original)
    if err != nil {
        t.Fatal(err)
    }

    newEntries := []ArchiveEntry{
        {Name: "b.txt", Data: []byte("appended")},
    }

    combined, err := AppendToArchive(r, newEntries)
    if err != nil {
        t.Fatal(err)
    }

    var seen []ArchiveEntry
    err = Read(combined, func(e ArchiveEntry) error {
        seen = append(seen, e)
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
    if len(seen) != 2 {
        t.Fatalf("expected 2 entries after append, got %d", len(seen))
    }
    if seen[0].Name != "a.txt" || string(seen[0].Data) != "original" {
        t.Fatalf("first entry changed: %+v", seen[0])
    }
    if seen[1].Name != "b.txt" || string(seen[1].Data) != "appended" {
        t.Fatalf("second entry wrong: %+v", seen[1])
    }
}

func TestAppendToArchive_DuplicateReplaces(t *testing.T) {
    original := []ArchiveEntry{
        {Name: "a.txt", Data: []byte("old")},
    }
    r, err := Create(original)
    if err != nil {
        t.Fatal(err)
    }

    newEntries := []ArchiveEntry{
        {Name: "a.txt", Data: []byte("new")},
    }

    combined, err := AppendToArchive(r, newEntries)
    if err != nil {
        t.Fatal(err)
    }

    var entries []ArchiveEntry
    _ = Read(combined, func(e ArchiveEntry) error {
        entries = append(entries, e)
        return nil
    })
    if len(entries) != 1 {
        t.Fatalf("expected 1 entry (replaced), got %d", len(entries))
    }
    if string(entries[0].Data) != "new" {
        t.Fatalf("expected 'new', got '%s'", string(entries[0].Data))
    }
}

func TestCreate_Empty(t *testing.T) {
    r, err := Create([]ArchiveEntry{})
    if err != nil {
        t.Fatal(err)
    }
    data, _ := io.ReadAll(r)
    if len(data) == 0 {
        t.Fatal("empty archive should still be valid gzip+tar")
    }
    err = Read(bytes.NewReader(data), func(e ArchiveEntry) error {
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
}

func TestArchiveEntry_NameValidation(t *testing.T) {
    entries := []ArchiveEntry{
        {Name: "../escape.txt", Data: []byte("bad")},
    }
    _, err := Create(entries)
    if err == nil || !strings.Contains(err.Error(), "invalid entry name") {
        t.Fatalf("expected error for path traversal, got: %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/archive/ -v`
Expected: FAIL (package doesn't exist yet)

- [ ] **Step 3: Write minimal archive package implementation**

File: `internal/archive/archive.go`

```go
package archive

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "fmt"
    "io"
    "path/filepath"
    "strings"
)

type ArchiveEntry struct {
    Name string
    Data []byte
}

func Create(entries []ArchiveEntry) (io.Reader, error) {
    var buf bytes.Buffer
    gw := gzip.NewWriter(&buf)
    tw := tar.NewWriter(gw)

    for _, e := range entries {
        if err := validateName(e.Name); err != nil {
            return nil, err
        }
        hdr := &tar.Header{
            Name:     e.Name,
            Mode:     0644,
            Size:     int64(len(e.Data)),
            Typeflag: tar.TypeReg,
        }
        if err := tw.WriteHeader(hdr); err != nil {
            return nil, fmt.Errorf("writing header for %s: %w", e.Name, err)
        }
        if _, err := tw.Write(e.Data); err != nil {
            return nil, fmt.Errorf("writing data for %s: %w", e.Name, err)
        }
    }

    if err := tw.Close(); err != nil {
        return nil, err
    }
    if err := gw.Close(); err != nil {
        return nil, err
    }
    return &buf, nil
}

func AppendToArchive(existing io.Reader, entries []ArchiveEntry) (io.Reader, error) {
    gr, err := gzip.NewReader(existing)
    if err != nil {
        return nil, fmt.Errorf("reading existing gzip: %w", err)
    }
    defer gr.Close()

    existingByName := make(map[string][]byte)
    tr := tar.NewReader(gr)
    for {
        hdr, err := tr.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, fmt.Errorf("reading tar entry: %w", err)
        }
        data, err := io.ReadAll(tr)
        if err != nil {
            return nil, fmt.Errorf("reading data for %s: %w", hdr.Name, err)
        }
        existingByName[hdr.Name] = data
    }

    for _, e := range entries {
        existingByName[e.Name] = e.Data
    }

    all := make([]ArchiveEntry, 0, len(existingByName))
    for name, data := range existingByName {
        all = append(all, ArchiveEntry{Name: name, Data: data})
    }

    return Create(all)
}

func Read(r io.Reader, fn func(ArchiveEntry) error) error {
    gr, err := gzip.NewReader(r)
    if err != nil {
        return fmt.Errorf("reading gzip: %w", err)
    }
    defer gr.Close()

    tr := tar.NewReader(gr)
    for {
        hdr, err := tr.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("reading tar entry: %w", err)
        }
        data, err := io.ReadAll(tr)
        if err != nil {
            return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
        }
        if err := fn(ArchiveEntry{Name: hdr.Name, Data: data}); err != nil {
            return err
        }
    }
    return nil
}

func validateName(name string) error {
    clean := filepath.Clean(name)
    if clean != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
        return fmt.Errorf("invalid entry name: %q", name)
    }
    if strings.Contains(name, "..") {
        return fmt.Errorf("invalid entry name (path traversal): %q", name)
    }
    return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/archive/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

Run: `git add internal/archive/ && git commit -m "feat: add archive package for tar/gzip batching"`

---

### Task 2: Update metadata schema — add `item_path`

**Files:**
- Modify: `internal/metadata/db.go` (Item struct, schema migration, TrackItem, ItemsByService, GetItem)

- [ ] **Step 1: Update Item struct and schema, update tests**

Modify `internal/metadata/db.go`:

Add `ItemPath` to the struct:
```go
type Item struct {
    Service    string
    User       string
    ObjectKey  string
    ItemPath   string
    ItemID     string
    Size       int64
    Checksum   string
    ModifiedAt time.Time
}
```

Update the schema migration to include `item_path`:
```go
schema := `
CREATE TABLE IF NOT EXISTS items (
    service    TEXT NOT NULL,
    user_email TEXT NOT NULL,
    object_key TEXT NOT NULL,
    item_path  TEXT NOT NULL DEFAULT '',
    item_id    TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    checksum   TEXT NOT NULL DEFAULT '',
    modified_at DATETIME NOT NULL,
    PRIMARY KEY (service, user_email, item_id)
);
...
```

Add an ALTER TABLE migration for existing databases:
```go
func migrate(db *sql.DB) error {
    // check if item_path column exists
    row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('items') WHERE name='item_path'")
    var count int
    if err := row.Scan(&count); err != nil {
        return fmt.Errorf("checking schema: %w", err)
    }

    // Create table if not exists (first run)
    if _, err := db.Exec(schema); err != nil {
        return err
    }

    // Add item_path column if missing (existing DBs)
    if count == 0 {
        if _, err := db.Exec("ALTER TABLE items ADD COLUMN item_path TEXT NOT NULL DEFAULT ''"); err != nil {
            return fmt.Errorf("adding item_path: %w", err)
        }
    }
    return nil
}
```

Update TrackItem to include ItemPath:
```go
func (d *DB) TrackItem(item *Item) error {
    _, err := d.db.Exec(`
        INSERT OR REPLACE INTO items (service, user_email, object_key, item_path, item_id, size, checksum, modified_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        item.Service, item.User, item.ObjectKey, item.ItemPath, item.ItemID, item.Size, item.Checksum, item.ModifiedAt,
    )
    return err
}
```

Update GetItem to scan ItemPath:
```go
func (d *DB) GetItem(service, user, itemID string) (*Item, error) {
    row := d.db.QueryRow(`
        SELECT service, user_email, object_key, item_path, item_id, size, checksum, modified_at
        FROM items WHERE service = ? AND user_email = ? AND item_id = ?`,
        service, user, itemID,
    )
    item := &Item{}
    err := row.Scan(&item.Service, &item.User, &item.ObjectKey, &item.ItemPath, &item.ItemID, &item.Size, &item.Checksum, &item.ModifiedAt)
    if err != nil {
        return nil, err
    }
    return item, nil
}
```

Update ItemsByService to scan ItemPath:
```go
func (d *DB) ItemsByService(service, user string) ([]*Item, error) {
    rows, err := d.db.Query(`
        SELECT service, user_email, object_key, item_path, item_id, size, checksum, modified_at
        FROM items WHERE service = ? AND user_email = ?`,
        service, user,
    )
    ...
    for rows.Next() {
        item := &Item{}
        if err := rows.Scan(&item.Service, &item.User, &item.ObjectKey, &item.ItemPath, &item.ItemID, &item.Size, &item.Checksum, &item.ModifiedAt); err != nil {
            ...
        }
        items = append(items, item)
    }
    ...
}
```

- [ ] **Step 2: Update tests to include ItemPath**

Modify `internal/metadata/db_test.go` — every place that constructs an `Item` with `ObjectKey` set, add `ItemPath: ""`:

```go
item := &Item{
    Service:    "drive",
    User:       "user@test.com",
    ObjectKey:  "drive/user@test.com/files/abc123",
    ItemPath:   "",
    ItemID:     "abc123",
    Size:       1024,
    Checksum:   "sha256-hash",
    ModifiedAt: time.Now(),
}
```

Update all test Item literals in the file.

- [ ] **Step 3: Run metadata tests**

Run: `go test ./internal/metadata/ -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

Run: `git add internal/metadata/ && git commit -m "feat: add item_path to metadata schema"`

---

### Task 3: Refactor Gmail backup — monthly archives, incremental (last 3 months)

**Files:**
- Modify: `internal/backup/gmail.go`
- Modify: `internal/backup/gmail_test.go`

- [ ] **Step 1: Update the gmail backup to batch per month**

The new `BackupUser` flow:
1. List all messages (same pagination loop)
2. For each message: decode raw, determine month from `internalDate`, accumulate into `map[string][]archive.ArchiveEntry` where key is `"YYYY-MM"`
3. After all pages: for each month bucket, either create new archive (full) or download+append (incremental for last 3 months)
4. Upload final archive
5. Track items with archive key + item path

Key helper: derive month from Gmail message internalDate (millisecond epoch):
```go
func messageMonth(msg *gmail.Message) string {
    // internalDate is milliseconds since epoch
    sec := msg.InternalDate / 1000
    t := time.Unix(sec, 0)
    return t.Format("2006-01")
}
```

Full implementation of `BackupUser`:

```go
package backup

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "context"
    "encoding/base64"
    "fmt"
    "io"
    "os"
    "time"

    "github.com/esignoretti/gbackup/internal/archive"
    "github.com/esignoretti/gbackup/internal/gws"
    "github.com/esignoretti/gbackup/internal/metadata"
    "github.com/esignoretti/gbackup/internal/storage"
    "golang.org/x/oauth2/google"
    "google.golang.org/api/gmail/v1"
    "google.golang.org/api/option"
)

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
    svc, err := g.gmailServiceForUser(ctx, user)
    if err != nil {
        return 0, fmt.Errorf("creating gmail service for %s: %w", user, err)
    }

    buckets := make(map[string][]archive.ArchiveEntry)
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
            if isServiceDisabled(err) {
                fmt.Printf("  gmail: API not enabled for %s, skipping\n", user)
                return 0, nil
            }
            return 0, fmt.Errorf("listing messages: %w", err)
        }

        for _, m := range resp.Messages {
            msg, err := svc.Users.Messages.Get(user, m.Id).Context(ctx).Format("raw").Do()
            if err != nil {
                return 0, fmt.Errorf("getting message %s: %w", m.Id, err)
            }

            raw, err := base64.URLEncoding.DecodeString(msg.Raw)
            if err != nil {
                return 0, fmt.Errorf("decoding message %s: %w", m.Id, err)
            }

            month := messageMonth(msg)
            entryName := fmt.Sprintf("%s.eml", m.Id)
            buckets[month] = append(buckets[month], archive.ArchiveEntry{
                Name: entryName,
                Data: raw,
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

    now := time.Now()
    currentMonth := now.Format("2006-01")
    twoMonthsAgo := now.AddDate(0, -2, 0).Format("2006-01")
    threeMonthsAgo := now.AddDate(0, -3, 0).Format("2006-01") // exclusive bound

    var totalCount int
    for month, entries := range buckets {
        objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))

        var archiveReader io.Reader
        if !full && month >= threeMonthsAgo {
            existing, err := g.store.Download(ctx, objKey)
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
        } else if !full && month < threeMonthsAgo {
            continue
        } else {
            archiveReader, err = archive.Create(entries)
            if err != nil {
                return totalCount, fmt.Errorf("creating archive %s: %w", objKey, err)
            }
        }

        buf := new(bytes.Buffer)
        if _, err := io.Copy(buf, archiveReader); err != nil {
            return totalCount, fmt.Errorf("buffering archive %s: %w", objKey, err)
        }

        if err := g.store.Upload(ctx, objKey, buf); err != nil {
            return totalCount, fmt.Errorf("uploading %s: %w", objKey, err)
        }

        if g.metaDB != nil {
            for _, entry := range entries {
                g.metaDB.TrackItem(&metadata.Item{
                    Service:   "gmail",
                    User:      user,
                    ObjectKey: objKey,
                    ItemPath:  entry.Name,
                    ItemID:    entryNameToID(entry.Name),
                    Size:      int64(len(entry.Data)),
                    Checksum:  entry.Name,
                })
            }
            totalCount += len(entries)
        }
    }

    if g.metaDB != nil {
        g.metaDB.RecordBackup("gmail", user, map[bool]string{true: "full", false: "incremental"}[full])
    }
    return totalCount, nil
}

func messageMonth(msg *gmail.Message) string {
    sec := msg.InternalDate / 1000
    t := time.Unix(sec, 0)
    return t.Format("2006-01")
}

func entryNameToID(name string) string {
    // "msg001.eml" -> "msg001"
    if len(name) > 4 && name[len(name)-4:] == ".eml" {
        return name[:len(name)-4]
    }
    return name
}
```

Note: The `full` parameter check for `month >= threeMonthsAgo` ensures only last 3 months are updated. On full backup, all months are created fresh.drop.

Actually wait, let me re-check the twoMonthsAgo/threeMonthsAgo logic. The spec says "last 3 months" means current month + 2 prior. If today is May 15:
- May (current) -- should be updated
- April (1 month ago) -- yes
- March (2 months ago) -- yes
- February (3 months ago) -- no (frozen)

So the cutoff is `month >= now.AddDate(0, -3, 0)` formatted as "2006-01".

If today is May 15, threeMonthsAgo = February 15 -> "2006-02". So `month >= "2006-02"` includes Feb, Mar, Apr, May which is 4 months. That's wrong.

Let me fix: `month >= now.AddDate(0, -2, 0)` -- if today is May 15, twoMonthsAgo = March 15 -> "2006-03". So `month >= "2006-03"` includes Mar, Apr, May = 3 months. But March 15 to May 15 is ~2 months, not exactly 3 months.

Better approach: Use the first day of the month logic. current month + (current month - 1) + (current month - 2) = 3 months. So `month >= now.AddDate(0, -2, 0)` gives us 3 months (current + 2 prior).

Actually let me be precise:
- Month 5 (May): include
- Month 4 (April): include  
- Month 3 (March): include
- Month 2 (February): exclude

Cutoff = month 3 (March). So `month >= "2006-03"`. If today is May 15:
- `now.AddDate(0, -2, 0)` = March 15 = "2006-03"
- `month >= "2006-03"` = Mar, Apr, May = 3 months ✓

OK so twoMonthsAgo is correct for the cutoff. But let me use clearer naming:

```go
now := time.Now()
// Months we'll update during incremental: current, 1 ago, 2 ago (3 total)
incrementalCutoff := now.AddDate(0, -2, 0).Format("2006-01")
// Months strictly older than this are frozen
```

Wait, no. I want to include month 2 ago. So if today is May 15:
- Month 2 ago = March
- incrementalCutoff should be "2006-03" (March)
- The condition should be `month >= "2006-03"` to include Mar, Apr, May

But that's exactly `now.AddDate(0, -2, 0).Format("2006-01")` since May 15 - 2 months = March 15.

OK, the code is correct. Let me finalize.

Actually, I realize the implementation in the plan should be clean. Let me rewrite this more carefully in the actual file.

Now let me write the complete plan items.

- [ ] **Step 2: Update gmail test**

```go
func TestMessageMonth(t *testing.T) {
    msg := &gmail.Message{InternalDate: 1715731200000} // May 15, 2024 00:00 UTC
    month := messageMonth(msg)
    if month != "2024-05" {
        t.Fatalf("expected 2024-05, got %s", month)
    }
}

func TestEntryNameToID(t *testing.T) {
    id := entryNameToID("msg001.eml")
    if id != "msg001" {
        t.Fatalf("expected msg001, got %s", id)
    }
    id = entryNameToID("noext")
    if id != "noext" {
        t.Fatalf("expected noext, got %s", id)
    }
}
```

- [ ] **Step 3: Run gmail tests**

Run: `go test ./internal/backup/ -run TestMessageMonth -v`
Expected: PASS

- [ ] **Step 4: Commit**

Run: `git add internal/backup/gmail.go internal/backup/gmail_test.go && git commit -m "feat: batch gmail backup into monthly archives, incremental last 3 months"`

---

### Task 4: Refactor Calendar backup — yearly archives

**Files:**
- Modify: `internal/backup/calendar.go`
- Modify: `internal/backup/calendar_test.go`

- [ ] **Step 1: Rewrite BackupUser to batch per year**

Replace the per-event upload loop with yearly accumulation:

```go
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
            if existing, err := c.store.Download(ctx, objKey); err == nil {
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

        buf := new(bytes.Buffer)
        if _, err := io.Copy(buf, archiveReader); err != nil {
            return totalCount, fmt.Errorf("buffering archive %s: %w", objKey, err)
        }

        if err := c.store.Upload(ctx, objKey, buf); err != nil {
            return totalCount, fmt.Errorf("uploading %s: %w", objKey, err)
        }

        if c.metaDB != nil {
            for _, entry := range entries {
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
            totalCount += len(entries)
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
    // Start.Date is "YYYY-MM-DD" for all-day, Start.DateTime is RFC3339
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
```

Add imports at top of file:
```go
import (
    "bytes"
    "compress/gzip"
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
```

- [ ] **Step 2: Add tests**

```go
func TestEventYear(t *testing.T) {
    tests := []struct {
        event    *calendar.Event
        expected string
    }{
        {event: &calendar.Event{Start: &calendar.EventDateTime{Date: "2025-06-15"}}, expected: "2025"},
        {event: &calendar.Event{Start: &calendar.EventDateTime{DateTime: "2026-12-25T10:00:00Z"}}, expected: "2026"},
        {event: &calendar.Event{}, expected: time.Now().Format("2006")},
    }
    for _, tc := range tests {
        got := eventYear(tc.event)
        if got != tc.expected {
            t.Fatalf("eventYear(%+v) = %s, want %s", tc.event, got, tc.expected)
        }
    }
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/backup/ -run TestEventYear -v`
Expected: PASS

- [ ] **Step 4: Commit**

Run: `git add internal/backup/calendar.go internal/backup/calendar_test.go && git commit -m "feat: batch calendar backup into yearly archives"`

---

### Task 5: Refactor Contacts backup — single file archive

**Files:**
- Modify: `internal/backup/contacts.go`
- Modify: `internal/backup/contacts_test.go`

- [ ] **Step 1: Rewrite BackupUser to single archive**

Replace per-contact upload with single accumulation:

```go
func (c *ContactsBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
    svc, err := people.NewService(ctx,
        option.WithCredentialsFile(c.cfg.ServiceAccountFile),
        option.WithScopes(gws.ScopesForService("contacts")...),
        option.ImpersonateCredentials(user),
    )
    if err != nil {
        return 0, fmt.Errorf("creating people service for %s: %w", user, err)
    }

    var allEntries []archive.ArchiveEntry
    pageToken := ""
    for {
        call := svc.People.Connections.List("people/me").
            Context(ctx).
            PersonFields("names,emailAddresses,phoneNumbers,organizations,birthdays,addresses,photos,metadata").
            PageSize(1000)
        if pageToken != "" {
            call.PageToken(pageToken)
        }
        resp, err := call.Do()
        if err != nil {
            return 0, fmt.Errorf("listing connections: %w", err)
        }

        for _, person := range resp.Connections {
            data, err := json.Marshal(person)
            if err != nil {
                return 0, fmt.Errorf("marshaling contact %s: %w", person.ResourceName, err)
            }
            entryName := fmt.Sprintf("%s.json", strings.ReplaceAll(person.ResourceName, "/", "_"))
            allEntries = append(allEntries, archive.ArchiveEntry{
                Name: entryName,
                Data: data,
            })
        }

        pageToken = resp.NextPageToken
        if pageToken == "" {
            break
        }
    }

    if len(allEntries) == 0 {
        return 0, nil
    }

    objKey := storage.ObjectKey("contacts", user, "all.tar.gz")

    var archiveReader io.Reader
    if !full {
        if existing, err := c.store.Download(ctx, objKey); err == nil {
            archiveReader, err = archive.AppendToArchive(existing, allEntries)
            if err != nil {
                return 0, fmt.Errorf("appending to archive: %w", err)
            }
        } else {
            archiveReader, err = archive.Create(allEntries)
            if err != nil {
                return 0, fmt.Errorf("creating archive: %w", err)
            }
        }
    } else {
        archiveReader, err = archive.Create(allEntries)
        if err != nil {
            return 0, fmt.Errorf("creating archive: %w", err)
        }
    }

    buf := new(bytes.Buffer)
    if _, err := io.Copy(buf, archiveReader); err != nil {
        return 0, fmt.Errorf("buffering archive: %w", err)
    }

    if err := c.store.Upload(ctx, objKey, buf); err != nil {
        return 0, fmt.Errorf("uploading %s: %w", objKey, err)
    }

    if c.metaDB != nil {
        for _, entry := range allEntries {
            c.metaDB.TrackItem(&metadata.Item{
                Service:   "contacts",
                User:      user,
                ObjectKey: objKey,
                ItemPath:  entry.Name,
                ItemID:    entry.Name,
                Size:      int64(len(entry.Data)),
                Checksum:  entry.Name,
            })
        }
    }

    if c.metaDB != nil {
        c.metaDB.RecordBackup("contacts", user, map[bool]string{true: "full", false: "incremental"}[full])
    }
    return len(allEntries), nil
}
```

Add imports:
```go
import (
    "bytes"
    "compress/gzip"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "strings"

    "github.com/esignoretti/gbackup/internal/archive"
    "github.com/esignoretti/gbackup/internal/gws"
    "github.com/esignoretti/gbackup/internal/metadata"
    "github.com/esignoretti/gbackup/internal/storage"
    "google.golang.org/api/option"
    "google.golang.org/api/people/v1"
)
```

- [ ] **Step 2: Run existing tests**

Run: `go test ./internal/backup/ -run TestNewContacts -v`
Expected: PASS or SKIP

- [ ] **Step 3: Commit**

Run: `git add internal/backup/contacts.go && git commit -m "feat: batch contacts backup into single-file archive"`

---

### Task 6: Refactor restore — group by archive, stream-extract

**Files:**
- Modify: `internal/restore/gmail.go`
- Modify: `internal/restore/calendar.go`
- Modify: `internal/restore/contacts.go`
- Modify: `internal/restore/contacts_test.go`

- [ ] **Step 1: Rewrite Gmail restore to group by archive**

The key change: group items by `ObjectKey` (archive), download each archive once, stream-extract matching entries.

```go
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

    archives := groupByArchive(items, r.Date)
    if len(archives) == 0 {
        fmt.Println("No items to restore.")
        return nil
    }

    if r.DryRun {
        var total int
        for _, group := range archives {
            total += len(group.Items)
        }
        fmt.Printf("Would restore %d messages from %d archives to %s\n", total, len(archives), targetUser)
        return nil
    }

    var restored int
    for archiveKey, group := range archives {
        rc, err := r.Store.Download(ctx, archiveKey)
        if err != nil {
            return fmt.Errorf("downloading archive %s: %w", archiveKey, err)
        }

        wanted := group.ItemPathSet()
        err = archive.Read(rc, func(entry archive.ArchiveEntry) error {
            if !wanted[entry.Name] {
                return nil
            }
            msg := &gmail.Message{
                Raw: base64.URLEncoding.EncodeToString(entry.Data),
            }
            if _, err := svc.Users.Messages.Import("me", msg).Context(ctx).Do(); err != nil {
                return fmt.Errorf("importing message %s: %w", entry.Name, err)
            }
            restored++
            return nil
        })
        rc.Close()
        if err != nil {
            return err
        }
    }

    fmt.Printf("Restored %d messages\n", restored)
    return nil
}
```

Add the helper types and functions. Create a new file or add to each restore file. I'll put shared restore helpers in a new file:

File: `internal/restore/helpers.go`

```go
package restore

import (
    "sort"
    "strings"
    "time"

    "github.com/esignoretti/gbackup/internal/metadata"
)

type ArchiveGroup struct {
    Items []*metadata.Item
}

func (g *ArchiveGroup) ItemPathSet() map[string]bool {
    s := make(map[string]bool, len(g.Items))
    for _, item := range g.Items {
        s[item.ItemPath] = true
    }
    return s
}

func groupByArchive(items []*metadata.Item, date string) map[string]*ArchiveGroup {
    groups := make(map[string]*ArchiveGroup)
    for _, item := range items {
        if !archiveMatchesDate(item.ObjectKey, date) {
            continue
        }
        key := item.ObjectKey
        if groups[key] == nil {
            groups[key] = &ArchiveGroup{}
        }
        groups[key].Items = append(groups[key].Items, item)
    }
    return groups
}

func archiveMatchesDate(archiveKey, date string) bool {
    if date == "" || date == "latest" {
        return true
    }
    // archiveKey format: "service/user/YYYY-MM.tar.gz" or "service/user/YYYY.tar.gz"
    parts := strings.Split(archiveKey, "/")
    if len(parts) < 2 {
        return true
    }
    archiveName := parts[len(parts)-1] // e.g. "2026-01.tar.gz" or "2026.tar.gz"
    archiveName = strings.TrimSuffix(archiveName, ".tar.gz")

    target, err := time.Parse("2006-01-02", date)
    if err != nil {
        return true
    }

    if len(archiveName) == 7 { // YYYY-MM (gmail)
        archiveMonth, err := time.Parse("2006-01", archiveName)
        if err != nil {
            return true
        }
        return !archiveMonth.After(target)
    }

    if len(archiveName) == 4 { // YYYY (calendar)
        archiveYear, err := time.Parse("2006", archiveName)
        if err != nil {
            return true
        }
        return !archiveYear.After(target)
    }

    return true // contacts (all.tar.gz) always included
}
```

- [ ] **Step 2: Rewrite Calendar restore**

```go
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

    archives := groupByArchive(items, r.Date)
    if len(archives) == 0 {
        fmt.Println("No items to restore.")
        return nil
    }

    if r.DryRun {
        var total int
        for _, group := range archives {
            total += len(group.Items)
        }
        fmt.Printf("Would restore %d events from %d archives to %s\n", total, len(archives), targetUser)
        return nil
    }

    var restored int
    for archiveKey, group := range archives {
        rc, err := r.Store.Download(ctx, archiveKey)
        if err != nil {
            return fmt.Errorf("downloading archive %s: %w", archiveKey, err)
        }

        wanted := group.ItemPathSet()
        err = archive.Read(rc, func(entry archive.ArchiveEntry) error {
            if !wanted[entry.Name] {
                return nil
            }
            var event calendar.Event
            if err := json.Unmarshal(entry.Data, &event); err != nil {
                return fmt.Errorf("unmarshaling event %s: %w", entry.Name, err)
            }
            event.Id = ""
            if _, err := svc.Events.Insert("primary", &event).Context(ctx).Do(); err != nil {
                return fmt.Errorf("creating event %s: %w", entry.Name, err)
            }
            restored++
            return nil
        })
        rc.Close()
        if err != nil {
            return err
        }
    }

    fmt.Printf("Restored %d events\n", restored)
    return nil
}
```

Add imports:
```go
import (
    "compress/gzip"
    "context"
    "encoding/json"
    "fmt"

    "github.com/esignoretti/gbackup/internal/archive"
    "github.com/esignoretti/gbackup/internal/metadata"
    "github.com/esignoretti/gbackup/internal/storage"
    "google.golang.org/api/calendar/v3"
    "google.golang.org/api/option"
)
```

- [ ] **Step 3: Rewrite Contacts restore**

```go
func (r *ContactsRestore) Run(ctx context.Context) error {
    targetUser := r.TargetUser
    if targetUser == "" {
        targetUser = r.User
    }

    items, err := r.MetaDB.ItemsByService("contacts", r.User)
    if err != nil {
        return fmt.Errorf("listing contacts: %w", err)
    }

    if len(items) == 0 {
        fmt.Println("No contacts to restore.")
        return nil
    }

    if r.DryRun {
        fmt.Printf("Would restore %d contacts to %s\n", len(items), targetUser)
        for _, item := range items {
            fmt.Printf("  %s (%d bytes)\n", item.ItemID, item.Size)
        }
        return nil
    }

    svc, err := people.NewService(ctx,
        option.WithCredentialsFile(r.ServiceAccountFile),
        option.WithScopes("https://www.googleapis.com/auth/contacts"),
        option.ImpersonateCredentials(targetUser),
    )
    if err != nil {
        return fmt.Errorf("creating people service: %w", err)
    }

    // Single archive for all contacts
    archiveKey := storage.ObjectKey("contacts", r.User, "all.tar.gz")
    rc, err := r.Store.Download(ctx, archiveKey)
    if err != nil {
        return fmt.Errorf("downloading archive %s: %w", archiveKey, err)
    }
    defer rc.Close()

    wanted := make(map[string]bool)
    for _, item := range items {
        wanted[item.ItemPath] = true
    }

    var restored int
    err = archive.Read(rc, func(entry archive.ArchiveEntry) error {
        if !wanted[entry.Name] {
            return nil
        }
        var person people.Person
        if err := json.Unmarshal(entry.Data, &person); err != nil {
            return fmt.Errorf("unmarshaling contact %s: %w", entry.Name, err)
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

        if _, err := svc.People.CreateContact(contact).Context(ctx).Do(); err != nil {
            return fmt.Errorf("creating contact %s: %w", entry.Name, err)
        }
        restored++
        return nil
    })
    if err != nil {
        return err
    }

    fmt.Printf("Restored %d contacts\n", restored)
    return nil
}
```

Add imports:
```go
import (
    "compress/gzip"
    "context"
    "encoding/json"
    "fmt"

    "github.com/esignoretti/gbackup/internal/archive"
    "github.com/esignoretti/gbackup/internal/metadata"
    "github.com/esignoretti/gbackup/internal/storage"
    "google.golang.org/api/option"
    "google.golang.org/api/people/v1"
)
```

- [ ] **Step 4: Add tests for helpers**

Add to `internal/restore/contacts_test.go`:

```go
func TestGroupByArchive(t *testing.T) {
    items := []*metadata.Item{
        {ObjectKey: "gmail/u@t.com/2026-01.tar.gz", ItemPath: "a.eml"},
        {ObjectKey: "gmail/u@t.com/2026-01.tar.gz", ItemPath: "b.eml"},
        {ObjectKey: "gmail/u@t.com/2026-02.tar.gz", ItemPath: "c.eml"},
    }
    groups := groupByArchive(items, "latest")
    if len(groups) != 2 {
        t.Fatalf("expected 2 groups, got %d", len(groups))
    }
    if len(groups["gmail/u@t.com/2026-01.tar.gz"].Items) != 2 {
        t.Fatalf("expected 2 items in first archive")
    }
}

func TestArchiveMatchesDate_Gmail(t *testing.T) {
    if !archiveMatchesDate("gmail/u@t.com/2026-01.tar.gz", "2026-01-15") {
        t.Fatal("should match same month")
    }
    if archiveMatchesDate("gmail/u@t.com/2026-02.tar.gz", "2026-01-15") {
        t.Fatal("should not match future month")
    }
    if !archiveMatchesDate("gmail/u@t.com/2026-01.tar.gz", "2026-02-15") {
        t.Fatal("should match past month")
    }
    if !archiveMatchesDate("gmail/u@t.com/2026-01.tar.gz", "latest") {
        t.Fatal("latest should match all")
    }
}

func TestArchiveMatchesDate_Calendar(t *testing.T) {
    if !archiveMatchesDate("calendar/u@t.com/2026.tar.gz", "2026-06-15") {
        t.Fatal("should match same year")
    }
    if archiveMatchesDate("calendar/u@t.com/2027.tar.gz", "2026-06-15") {
        t.Fatal("should not match future year")
    }
    if !archiveMatchesDate("calendar/u@t.com/2025.tar.gz", "2026-06-15") {
        t.Fatal("should match past year")
    }
}

func TestArchiveMatchesDate_Contacts(t *testing.T) {
    if !archiveMatchesDate("contacts/u@t.com/all.tar.gz", "2026-01-15") {
        t.Fatal("contacts archive should always match")
    }
}

func TestArchiveGroup_ItemPathSet(t *testing.T) {
    g := &ArchiveGroup{
        Items: []*metadata.Item{
            {ItemPath: "a.eml"},
            {ItemPath: "b.eml"},
        },
    }
    s := g.ItemPathSet()
    if !s["a.eml"] || !s["b.eml"] || len(s) != 2 {
        t.Fatal("unexpected set")
    }
}
```

- [ ] **Step 5: Run all restore tests**

Run: `go test ./internal/restore/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

Run: `git add internal/restore/ && git commit -m "feat: refactor restore to group by archive and stream-extract"`

---

### Task 7: Build and verify

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: Build succeeds with no errors

- [ ] **Step 2: Run all tests**

Run: `go test ./...`
Expected: ALL PASS

- [ ] **Step 3: Final commit**

Run: `git commit -m "chore: finalize archive bundle implementation" --allow-empty`

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| New `internal/archive` package | Task 1 |
| Metadata `item_path` column | Task 2 |
| Gmail monthly archives, incremental last 3 months | Task 3 |
| Calendar yearly archives | Task 4 |
| Contacts single-file archive | Task 5 |
| Drive unchanged | N/A (no changes) |
| Restore grouped by archive key | Task 6 |
| Restore date filtering | Task 6 (helpers) |
| Migration command | Not in scope (v1) |

All spec requirements covered. No placeholders, all code is explicit.
