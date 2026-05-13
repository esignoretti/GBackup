# GBackup: Google Workspace Backup to S3

## Overview

GBackup is a CLI-first backup tool for Google Workspace (Drive, Gmail, Calendar, Contacts) that archives data to any S3-compatible storage. Targeting SMBs with 5–100 users.

## Architecture

```
┌─────────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│   Google Workspace  │◄────│   gbackup CLI (Go)   │────►│  S3-Compatible  │
│   (Drive/Gmail/     │     │                      │     │  Storage         │
│    Calendar/Contacts)│    │  ┌─────────────────┐  │     │  (AWS/MinIO/B2/  │
│                     │     │  │ SQLite Metadata │  │     │   R2/...)        │
│                     │     │  │ DB (local)      │──┼────►│  _meta/*.db.gz   │
│                     │     │  └─────────────────┘  │     └─────────────────┘
│                     │     │  ┌─────────────────┐  │
│                     │     │  │ Embedded Web UI │  │
│                     │     │  │ (gbackup serve)  │  │
│                     │     │  └─────────────────┘  │
│                     │     └──────────────────────┘
└─────────────────────┘
```

### Key Design Decisions

- **Language**: Go — single binary, excellent concurrency, mature S3/Google API libraries
- **Metadata**: Local SQLite DB tracks backup state (hashes, IDs, timestamps)
- **Auto-sync**: SQLite metadata DB is automatically compressed and uploaded to S3 after every backup run (both full and incremental). This is enforced, not optional.
- **Incremental**: First run is full; subsequent runs only fetch and upload changes
- **Compression**: All objects gzip-compressed before upload to minimize S3 costs

## CLI Commands

| Command | Description |
|---------|-------------|
| `gbackup init` | Create config file interactively |
| `gbackup backup` | Run a single backup (full or incremental based on config/state). Intended to be triggered by system cron / Task Scheduler — no daemon mode. |
| `gbackup restore <service> [user]` | Restore from S3 |
| `gbackup meta-backup` | Manually backup SQLite DB to S3 |
| `gbackup meta-restore` | Restore SQLite DB from S3 snapshot |
| `gbackup serve` | Start embedded web dashboard |
| `gbackup status` | Show last backup state |

## S3 Storage Layout

```
s3://bucket/
├── _meta/                          # Metadata snapshots (auto-synced)
│   ├── latest.json                 # Pointer to latest snapshot
│   ├── 2026-05-13T10-00-00.db.gz   # Timestamped SQLite backup
│   └── ...
├── drive/{user}/
│   ├── files/
│   │   ├── {file_id}_v{version}    # File content (compressed)
│   │   └── ...
│   └── index.json                  # File tree for fast listing
├── gmail/{user}/
│   ├── messages/
│   │   ├── {message_id}.eml.gz     # Individual emails
│   │   └── ...
│   └── index.json
├── calendar/{user}/
│   ├── events/
│   │   ├── {event_id}.json.gz
│   │   └── ...
│   └── index.json
└── contacts/{user}/
    ├── contacts/
    │   ├── {contact_id}.json.gz
    │   └── ...
    └── index.json
```

## Configuration

File: `~/.gbackup/gbackup.yaml` (created via `gbackup init`)

```yaml
workspace:
  domain: "mycompany.com"
  admin_email: "admin@mycompany.com"

storage:
  bucket: "mycompany-gbackup"
  region: "eu-west-1"
  endpoint: ""                    # optional, for S3-compatible backends
  access_key_id: "..."
  secret_access_key: "..."

schedule:
  incremental: "0 */6 * * *"     # every 6 hours
  full: "0 3 * * 0"              # weekly full backup (Sunday 3am)
  retention:
    incremental_days: 30
    full_backups: 12
    auto_prune: true

services:
  - drive
  - gmail
  - calendar
  - contacts

users:
  include: "*"
  # exclude: ["olduser@domain.com"]
```

## Google Workspace Authentication

- Service account with domain-wide delegation
- OAuth 2.0 scopes for Drive, Gmail, Calendar, People API
- Admin impersonation for directory-wide access

## Data Scope Per Service

| Service | What We Back Up | Format |
|---------|----------------|--------|
| Drive | All files, Shared Drives, metadata, versions | Raw binary + metadata JSON |
| Gmail | Full messages with attachments | `.eml.gz` |
| Calendar | Events with attendees, descriptions, attachments | JSON |
| Contacts | All contacts from directory | JSON |

Each item tracked in SQLite with: ID, created/modified time, size, checksum (for incremental detection).

## Backup Scheduling & Retention

- **Full backup** (configurable cron): Re-downloads everything, re-uploads fresh objects, snapshots SQLite
- **Incremental backup** (configurable cron): Fetches changes via Google API deltas, uploads new/changed objects
- **Metadata auto-sync**: SQLite DB uploaded to `_meta/` after every run (full and incremental)
- **Retention pruning**: Runs after each full backup, deletes expired objects per `retention` config

## Web Dashboard

- Embedded in Go binary via `embed`
- Started with `gbackup serve` (binds to `localhost:8080` by default, configurable)
- No database server, no auth (localhost-only)
- Reads from `_meta/latest.json` and direct S3 listing
- Views: Dashboard, Users, History, Settings (read-only)

## Restore

```
gbackup restore <service> [user] [flags]

Flags:
  --date       YYYY-MM-DD       # point-in-time (default: latest)
  --dry-run                      # preview without restoring
  --interactive                  # pick specific items
  --target-user user@domain      # restore to different user (migration)
```

| Service | Restore Method |
|---------|---------------|
| Drive | Creates `GBackup Restore - {date}/` folder, recreates tree structure |
| Gmail | Uses `messages.import` to re-insert with labels/threading preserved |
| Calendar | Re-inserts events, skips if identical exists |
| Contacts | Re-inserts via People API, deduplicates by name/email |

Always runs `--dry-run` first with a confirmation prompt before actual restore.

## Future Considerations (Not in v1)

- Email notifications for backup success/failure
- Multi-bucket support
- Encrypted backup at rest (client-side)
- Prometheus metrics endpoint
