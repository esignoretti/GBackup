# GBackup: Archive Bundle Storage

## Overview

Replace the current per-item S3 upload pattern with batched, compressed archives (`.tar.gz`) organized by temporal bucket. Reduces object count from tens-of-thousands to dozens per user per service.

## Storage Layout

```
s3://bucket/
├── _meta/                              (unchanged)
├── drive/{user}/files/                 (unchanged — individual files)
│
├── gmail/{user}/
│   ├── 2026-01.tar.gz
│   ├── 2026-02.tar.gz
│   └── ...
│
├── calendar/{user}/
│   ├── 2025.tar.gz
│   ├── 2026.tar.gz
│   └── ...
│
└── contacts/{user}/
    └── all.tar.gz
```

Drive stays per-file (unchanged).

### Archive Contents

Each `.tar.gz` contains entries stored flat (no subdirectories):

```
gmail/2026-01.tar.gz:
  msg001.eml
  msg002.eml
  ...

calendar/2026.tar.gz:
  event_abc123.json
  event_def456.json
  ...

contacts/all.tar.gz:
  contact_people_abc123.json
  contact_people_def456.json
  ...
```

## New Package: `internal/archive`

Handles tar/gzip creation, appending, and streaming extraction.

### Interface

```go
// Create builds a new .tar.gz from entries.
func Create(entries []ArchiveEntry) (io.Reader, error)

// AppendToArchive reads an existing .tar.gz, appends new entries,
// and returns the re-compressed archive.
func AppendToArchive(existing io.Reader, entries []ArchiveEntry) (io.Reader, error)

// Read streams entries from a .tar.gz, calling fn for each.
func Read(r io.Reader, fn func(ArchiveEntry) error) error

type ArchiveEntry struct {
    Name string // filename inside archive (e.g. "msg001.eml")
    Data []byte // raw file content
}
```

## Backup Changes

### Gmail (`internal/backup/gmail.go`)

- During the message list loop, group fetched messages by `YYYY-MM` (derived from `internalDate`).
- Accumulate entries per month bucket in a `map[string][]ArchiveEntry`.
- After all pages are exhausted (or at service boundaries), flush each bucket:
  - **Full backup**: `archive.Create(entries)` → `store.Upload(key)`
  - **Incremental backup** (last 3 months only): `store.Download(key)` → `archive.AppendToArchive(existing, newEntries)` → `store.Upload(key)`
  - Incremental for months older than 3 months: skip entirely (they're frozen).
- Update `TrackItem` to store archive key (e.g. `gmail/user/2026-01.tar.gz`) as `ObjectKey`, and add the entry filename as a new `ItemPath` field.

### Calendar (`internal/backup/calendar.go`)

- Group events by `YYYY` (derived from `event.Start.DateTime` or `event.Start.Date`).
- Accumulate per year bucket.
- Flush each year bucket:
  - **Full**: `Create` + `Upload`
  - **Incremental**: `Download` → `AppendToArchive` → `Upload` (only current/last year affected typically).

### Contacts (`internal/backup/contacts.go`)

- Accumulate all contacts into a single buffer.
- Flush once at end:
  - **Full**: `Create(all)` + `Upload`
  - **Incremental**: `Download("all.tar.gz")` → `AppendToArchive(existing, new)` → `Upload`

### Drive — Unchanged

Drive remains per-file as-is.

### Incremental Scope

- After the first full backup, subsequent runs are always incremental.
- A `--full` flag triggers a full rebuild: archives are deleted and re-created from scratch.
- For Gmail, incremental archive updates are limited to the **last 3 months**. Older archives are frozen and never modified.

## Metadata Changes

Extend the `Item` struct to track the path inside an archive:

```go
type Item struct {
    Service    string
    User       string
    ObjectKey  string    // archive key (e.g. "gmail/user/2026-01.tar.gz")
    ItemPath   string    // entry path inside archive (e.g. "msg001.eml")
    ItemID     string
    Size       int64
    Checksum   string
    ModifiedAt time.Time
}
```

Add `item_path` column to the SQLite schema.

## Restore Changes

### Gmail & Calendar

- Group restore items by `ObjectKey` (archive key).
- For each unique archive, download once and stream-extract matching entries:
  - Gmail: `archive.Read(reader, fn)` → match entries where `name == item.ItemPath` → restore via `messages.import`
  - Calendar: same pattern, restore via `events.insert`
- Apply date filtering: if `--date` is specified, only process archives whose time bucket is <= the target date. Default: latest only (short restore window). Full restore requires an explicit `--all` flag.

### Contacts

- Download `all.tar.gz`, extract all entries, restore each contact.
- No date filtering (single archive).

### Drive — Unchanged

Drive restore stays per-file as-is.

## Migration

Existing installations have an object layout with individual `.eml.gz` / `.json.gz` / etc. per item. A `gbackup migrate-storage` command will:

1. List all per-item objects for a service/user from S3.
2. Group into archive buckets (by month/year).
3. Download, batch into `.tar.gz`, upload archives.
4. Optionally delete originals (with `--prune` flag).

Not required for v1 of this feature — existing backups can continue to use the old layout, and new backups start fresh with archives.

## Testing

- **Unit tests** for `internal/archive`: create, append, read round-trip with various entry sets.
- **Unit tests** for bucket key generation (`YYYY-MM`, `YYYY`, `all`).
- **Backup tests**: verify correct archive keys, entry names, and metadata tracking.
- **Restore tests**: verify archive download + streaming extraction.
- **Incremental tests**: verify append-to-archive produces correct combined archive.

## Spec Self-Review

### Placeholder scan
No TBDs or TODOs. All behaviors are specified.

### Internal consistency
- Archive key format matches between backup (write) and restore (read).
- Metadata tracks both archive key and internal path — restore groups by archive, filters by item path.
- Incremental constraints (last 3 months for Gmail) are consistent with the "frozen archives" principle.

### Scope check
Focused on a single concern: batching backup storage into archives. Does one thing well.

### Ambiguity check
- "Last 3 months" means current month + 2 prior months (e.g., if today is May 15, archives for May, April, March are updated; February and older are frozen).
- Restore date filter for Gmail/Calendar operates at archive-key granularity: a `--date 2024-06-15` will include archive `2024-06.tar.gz` (whole month).
- Full backup re-upload replaces entire archives, not appends.
