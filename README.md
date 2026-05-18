# GBackup

Google Workspace backup to any S3-compatible storage. Single-binary Go CLI; no daemon, no SaaS, no agents on the workstations. Designed for SMB tenants (~5–100 users) that need durable off-Google copies of Drive, Gmail, Calendar, and Contacts on storage they control.

## What it does

- Full and incremental backups of **Drive**, **Gmail**, **Calendar**, and **Contacts** for every user in a Google Workspace domain (or an explicit allowlist).
- Writes to any **S3-compatible** backend: AWS S3, Cubbit, MinIO, Backblaze B2, Cloudflare R2, etc. Auth via static keys *or* the AWS default credential chain (env, profile, IAM instance role).
- **Per-service retention windows** (`30d`, `6mo`, `2y`) so you don't archive a decade of mail you don't need.
- Bundles each user's data into per-month / per-year **tar.gz archives** rather than one S3 object per item — far fewer requests, smaller bills.
- Local **SQLite metadata DB** tracks every item's checksum so incremental runs only re-upload what changed. The DB is auto-snapshotted to S3 after every run so a new machine can resume from scratch.
- **Restore** for every service into the same or a different user (`--target-user`), with `--dry-run` always shown before the live restore.
- **Web dashboard** (`gbackup serve`) on localhost shows backup status. Read-only, no auth, bind-locked to loopback.
- **No-daemon** model — invoke via cron, systemd timer, Task Scheduler, etc.

Targets correctness and operability over feature breadth: every Critical/High finding from the 2026-05-17 internal code review is closed; see [REVIEW.md](REVIEW.md) and the plans in [`docs/superpowers/`](docs/superpowers/).

## Install

```bash
git clone https://github.com/esignoretti/GBackup.git
cd GBackup
go build -o gbackup .
# or, with the Makefile:
make build   # -> bin/gbackup
```

Requires Go 1.22+. No CGO required at runtime if you build with `CGO_ENABLED=0` (pure-Go SQLite via mattn/go-sqlite3 is currently used, which *does* need CGO; if you need a static binary, swap to a pure-Go driver or build with cross-compile + a libsqlite3-providing base image).

## Setup

GBackup authenticates to Google APIs via a **service account with domain-wide delegation**, and to S3 via either static credentials or the AWS default chain.

### 1. Create the Google service account

In a Google Cloud project that you control:

1. Enable the APIs you intend to back up: **Gmail API**, **Google Drive API**, **Google Calendar API**, **People API**, and **Admin SDK API** (for `users.include: "*"` to enumerate the domain).
2. Create a service account and download its JSON key (don't lose it — there's no recovery).
3. In Google Workspace **Admin Console → Security → Access and data control → API controls → Domain-wide delegation**, add the service account's client ID with these OAuth scopes (read-only by default; write scopes are only used by `restore`):

   ```
   https://www.googleapis.com/auth/admin.directory.user.readonly
   https://www.googleapis.com/auth/drive.readonly
   https://www.googleapis.com/auth/gmail.readonly
   https://www.googleapis.com/auth/calendar.readonly
   https://www.googleapis.com/auth/contacts.readonly
   ```

   For restore, also add: `https://mail.google.com/`, `https://www.googleapis.com/auth/drive`, `https://www.googleapis.com/auth/calendar.events`, `https://www.googleapis.com/auth/contacts`.

> **Note**: GBackup uses **JWT-with-Subject** to impersonate each user (the proper domain-wide-delegation flow). `option.ImpersonateCredentials` would silently fall back to the service account's own identity and return empty results — don't be fooled by build-time docs that suggest it.

### 2. Create the S3 bucket

Pick any S3-compatible provider. Bucket can be empty; GBackup will use the `_meta/`, `drive/`, `gmail/`, `calendar/`, and `contacts/` prefixes. Versioning + object-lock are recommended but optional.

### 3. Configure

```bash
./gbackup init
```

Walks you through every field interactively. The S3 secret is read with `term.ReadPassword` (not echoed). Leaving the access key blank tells the AWS SDK to use the default credential chain (env vars, `~/.aws/credentials`, IAM instance role).

Resulting file lives at `~/.gbackup/gbackup.yaml` with mode `0600`:

```yaml
workspace:
  domain: "mycompany.com"
  admin_email: "admin@mycompany.com"        # used as Subject for the Admin SDK call and as default restore user
  service_account_file: "/secrets/sa.json"

storage:
  bucket: "mycompany-gbackup"
  region: "eu-west-1"
  endpoint: ""                              # set for non-AWS providers (Cubbit, MinIO, R2, ...)
  access_key_id: ""                         # empty => AWS default chain
  secret_access_key: ""

services:
  - drive
  - gmail
  - calendar
  - contacts

users:
  include: "*"                              # or "user1@x.com,user2@x.com"
  exclude:
    - "noreply@mycompany.com"

retention:
  max_age:
    gmail:    "2y"
    drive:    "5y"
    calendar: "1y"
    contacts: ""                            # empty = no limit
```

`retention.max_age` durations accept `d`, `mo`, `y` suffixes (e.g. `30d`, `6mo`, `2y`).

## Usage

```bash
gbackup backup                  # incremental (or first-run full); SIGINT-cancellable
gbackup backup --full           # force full
gbackup status                  # show last backup time per service/user
gbackup serve --port 8080       # localhost dashboard

gbackup restore drive user@x.com                   # dry-run preview, then prompt
gbackup restore gmail user@x.com --target-user other@x.com
gbackup restore calendar user@x.com --date 2026-03-15
gbackup restore contacts user@x.com --dry-run

gbackup meta-backup             # explicit snapshot of the metadata DB (also done automatically after every backup)
gbackup meta-restore            # fetch the most recent _meta/ snapshot and replace the local DB

gbackup wipe                    # delete every gbackup-owned object in the bucket; foreign objects refused without --include-foreign
```

Wire `gbackup backup` to your scheduler of choice (cron, systemd timer, etc.). Example crontab for hourly incrementals plus a weekly full at 03:00 Sunday:

```cron
0  * * * *  /usr/local/bin/gbackup backup            >> /var/log/gbackup.log 2>&1
0  3 * * 0  /usr/local/bin/gbackup backup --full     >> /var/log/gbackup.log 2>&1
```

## How data is stored

```
s3://your-bucket/
├── _meta/
│   ├── 2026-05-18T07-30-00.db.gz       # SQLite metadata snapshots (timestamped)
│   └── ...
├── drive/<user>/files/<file-id>_v<n>[.ext]   # one S3 object per file version
├── gmail/<user>/<YYYY-MM>.tar.gz             # one tar.gz per month (incremental: stream-merged)
├── calendar/<user>/<YYYY>.tar.gz             # one tar.gz per year
└── contacts/<user>/all.tar.gz                # one tar.gz total
```

Archives are streamed straight into S3 via multipart upload — no intermediate buffering, no per-archive size limit. Incremental runs use `archive.StreamMerge`: existing entries flow through `tar.Reader → tar.Writer → gzip → S3` without ever being materialized in a map, with only same-name entries replaced.

Drive content is gzipped per-file (one S3 object each). Google Editors documents (Docs/Sheets/Slides/Drawings) are exported to their Office equivalents (`.docx`, `.xlsx`, `.pptx`, `.png`) instead of PDF. Non-exportable types (Forms, Sites, Maps, Apps Script) are skipped with a log line.

## Restore

Every `restore` command first runs in **dry-run mode**, printing what it would do, then prompts for confirmation before doing anything. Pass `--dry-run` to stop at the preview.

- Drive: creates a folder named `GBackup Restore - <date>` in the target user's My Drive and uploads every backed-up file into it with the original name and extension.
- Gmail: imports messages via `users.messages.import` so the original timestamps and threading are preserved.
- Calendar: inserts events into `primary` (their original IDs are stripped so Google assigns new ones).
- Contacts: re-creates contacts via `people.createContact` with the full Person object (names, phones, emails, addresses, organizations, photos, etc.).

`--date YYYY-MM-DD` selects the most recent state at-or-before that date. For Gmail/Calendar/Contacts the granularity is the archive bundle (per-month / per-year); for Drive it's per-file `modifiedTime`.

## Development

```bash
go test ./...                       # unit tests; no live API calls
go vet ./...
go build -o gbackup .

# Debug-only Gmail probe (gated by build tag, not in default binary):
go build -tags debug -o debuggmail ./cmd/debuggmail
```

Module layout:

```
cmd/                         # cobra subcommands
internal/
  backup/                    # per-service backup logic + runner
  restore/                   # per-service restore logic
  archive/                   # streaming tar.gz writer + StreamMerge
  metadata/                  # SQLite schema + DB ops
  storage/                   # S3 client (manager.Uploader, multipart)
  gws/                       # Google auth helpers (UserTokenSource via JWT-Subject)
  config/                    # YAML config + retention duration parsing
  progress/                  # stdout reporter
web/                         # embedded localhost dashboard
docs/superpowers/            # design specs + implementation plans + review reports
```

## Status & known limitations

GBackup is **early production** for the maintainer's own use, surfaced as a public repo. The full review and rollup of fixes is in [REVIEW.md](REVIEW.md) and [`docs/superpowers/plans/2026-05-17-review-bug-fixes.md`](docs/superpowers/plans/2026-05-17-review-bug-fixes.md). Headline state:

- All Critical and High findings from the 2026-05-17 review are closed.
- Gmail/Calendar/Contacts incremental detection uses SHA-256 of the entry body — the first run after the May 2026 upgrade re-uploads everything once because old metadata stored `entry.Name` as the "checksum".
- Calendar window is `now ± 366d` after expansion of recurring events (`SingleEvents=true`). Pre-existing archives from before this fix may contain instances dated as far out as 2054; re-run with `--full` to overwrite them.
- Drive `'me' in owners` deliberately excludes files owned by a shared drive (those belong to the org, not the user). Shared-drive backup is a separate workstream.
- No native Windows testing — paths use `filepath.Join` but the secret prompt uses `golang.org/x/term` which may behave differently on Windows consoles.
- No transit-encryption or KMS hooks beyond what S3 itself offers; the metadata DB is plain SQLite on disk at `~/.gbackup/meta.db`.

Issues and PRs welcome.
