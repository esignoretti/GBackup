# GBackup Review Bug Fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 22 Critical/High/Medium findings from the 2026-05-17 code review, in priority order (data-loss & broken-feature bugs first, then robustness, then polish).

**Architecture:** Six phases. Each phase ends with `go vet ./... && go test ./... && git commit`. Within a phase, tasks are ordered so later tasks can build on earlier ones. No backwards-compat shims — direct fixes.

**Tech Stack:** Go 1.22+, SQLite (mattn/go-sqlite3), AWS SDK v2, Google API client, cobra/yaml.

---

## File Structure

Files created in this plan:
- `internal/gws/errors.go` — typed Google API error classification
- `internal/restore/restore.go` — shared restore interface and confirm helper
- `cmd/restore_internal_test.go` — coverage for restore command construction

Files heavily modified:
- `cmd/restore.go`, `cmd/wipe.go`, `cmd/meta.go`, `cmd/init.go`, `cmd/backup.go`
- `internal/backup/{runner,gmail,drive,calendar,contacts}.go`
- `internal/restore/{drive,gmail,calendar,contacts,helpers}.go`
- `internal/metadata/db.go`, `internal/archive/archive.go`
- `internal/storage/s3.go`, `internal/config/config.go`, `internal/gws/directory.go`
- `web/server.go`

---

## Phase A — Critical correctness (data-loss & broken-feature fixes)

### Task A1: Extract restore boilerplate + fix ServiceAccountFile typo (C1, M12)

**Files:**
- Create: `internal/restore/restore.go`
- Modify: `cmd/restore.go` (full rewrite)
- Test: `cmd/restore_internal_test.go`

- [ ] **Step 1: Define shared interface and helper**

Write `internal/restore/restore.go`:

```go
package restore

import "context"

// Restorer is the common interface for all per-service restore operations.
type Restorer interface {
	// Run performs the restore. If DryRun is true on the underlying struct,
	// the implementation must only preview without making changes.
	Run(ctx context.Context) error
	// SetDryRun toggles dry-run mode.
	SetDryRun(bool)
}
```

- [ ] **Step 2: Implement SetDryRun on the four restore types**

In each of `internal/restore/drive.go`, `gmail.go`, `calendar.go`, `contacts.go`, add at the bottom of the file:

```go
func (r *DriveRestore) SetDryRun(v bool)    { r.DryRun = v }
```

(and same shape for `GmailRestore`, `CalendarRestore`, `ContactsRestore`)

- [ ] **Step 3: Rewrite `cmd/restore.go`**

Replace the file with:

```go
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/restore"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var (
	restoreDate   string
	restoreDryRun bool
	restoreTarget string
)

var restoreCmd = &cobra.Command{
	Use:   "restore [service] [user]",
	Short: "Restore data from S3 backup",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		service := args[0]
		user := ""
		if len(args) > 1 {
			user = args[1]
		}

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		if user == "" {
			user = cfg.Workspace.AdminEmail
		}

		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}

		home, _ := os.UserHomeDir()
		dbPath := filepath.Join(home, ".gbackup", "meta.db")
		db, err := metadata.New(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		r, err := buildRestorer(service, cfg, store, db, user)
		if err != nil {
			return err
		}

		ctx := context.Background()

		// Always do a dry run first.
		r.SetDryRun(true)
		if err := r.Run(ctx); err != nil {
			return err
		}
		if restoreDryRun {
			return nil
		}

		if !confirm("Proceed with restore? [y/N]: ") {
			return fmt.Errorf("restore cancelled")
		}
		r.SetDryRun(false)
		return r.Run(ctx)
	},
}

func buildRestorer(service string, cfg *config.Config, store *storage.Client, db *metadata.DB, user string) (restore.Restorer, error) {
	switch service {
	case "drive":
		return &restore.DriveRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "contacts":
		return &restore.ContactsRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "calendar":
		return &restore.CalendarRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "gmail":
		return &restore.GmailRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	default:
		return nil, fmt.Errorf("restore for %s not yet implemented", service)
	}
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(s.Text()))
	return ans == "y" || ans == "yes"
}

func init() {
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.Flags().StringVar(&restoreDate, "date", "latest", "Point-in-time date (YYYY-MM-DD)")
	restoreCmd.Flags().BoolVar(&restoreDryRun, "dry-run", false, "Preview without restoring")
	restoreCmd.Flags().StringVar(&restoreTarget, "target-user", "", "Restore to a different user")
}
```

- [ ] **Step 4: Add construction test that locks down the typo fix**

Write `cmd/restore_internal_test.go`:

```go
package cmd

import (
	"testing"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/restore"
)

func TestBuildRestorerUsesServiceAccountFile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workspace.AdminEmail = "admin@example.com"
	cfg.Workspace.ServiceAccountFile = "/secrets/sa.json"

	for _, svc := range []string{"drive", "gmail", "calendar", "contacts"} {
		r, err := buildRestorer(svc, cfg, nil, nil, "u@example.com")
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		var got string
		switch v := r.(type) {
		case *restore.DriveRestore:
			got = v.ServiceAccountFile
		case *restore.GmailRestore:
			got = v.ServiceAccountFile
		case *restore.CalendarRestore:
			got = v.ServiceAccountFile
		case *restore.ContactsRestore:
			got = v.ServiceAccountFile
		default:
			t.Fatalf("%s: unexpected type %T", svc, r)
		}
		if got != "/secrets/sa.json" {
			t.Fatalf("%s: ServiceAccountFile = %q, want /secrets/sa.json", svc, got)
		}
	}
}

func TestBuildRestorerUnknownService(t *testing.T) {
	cfg := &config.Config{}
	if _, err := buildRestorer("notreal", cfg, nil, nil, "u@x.com"); err == nil {
		t.Fatal("expected error for unknown service")
	}
}
```

- [ ] **Step 5: Run tests**

`go test ./cmd/... ./internal/restore/...` — expected PASS.

---

### Task A2: Atomic `RestoreDB` with backup of prior DB (C3)

**Files:**
- Modify: `internal/metadata/db.go`
- Test: `internal/metadata/db_test.go`

- [ ] **Step 1: Write failing test for atomic restore**

Add to `internal/metadata/db_test.go`:

```go
func TestRestoreDBAtomicAndBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "meta.db")

	// Seed an existing DB so RestoreDB has something to back up.
	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TrackItem(&Item{Service: "drive", User: "old@t.com", ObjectKey: "k_old", ItemID: "i_old", ModifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Create a valid backup of a different DB to restore from.
	srcDB := filepath.Join(dir, "src.db")
	db2, err := New(srcDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db2.TrackItem(&Item{Service: "drive", User: "new@t.com", ObjectKey: "k_new", ItemID: "i_new", ModifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db2.Close()

	gzPath := filepath.Join(dir, "src.db.gz")
	if err := BackupDB(srcDB, gzPath); err != nil {
		t.Fatal(err)
	}

	if err := RestoreDB(gzPath, dbPath); err != nil {
		t.Fatal(err)
	}

	// Old DB must have been preserved as .bak.
	if _, err := os.Stat(dbPath + ".bak"); err != nil {
		t.Fatalf("expected backup at %s.bak: %v", dbPath, err)
	}

	// Restored DB must contain the new data, not the old.
	restored, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	items, _ := restored.ItemsByService("drive", "new@t.com")
	if len(items) != 1 {
		t.Fatalf("expected 1 new item, got %d", len(items))
	}
}

func TestRestoreDBTruncatedSourceLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "meta.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TrackItem(&Item{Service: "drive", User: "u@t.com", ObjectKey: "k1", ItemID: "i1", ModifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Truncated gzip file.
	bad := filepath.Join(dir, "bad.db.gz")
	if err := os.WriteFile(bad, []byte("not-a-real-gzip"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := RestoreDB(bad, dbPath); err == nil {
		t.Fatal("expected error on bad gzip")
	}

	// Original DB still openable and intact.
	db2, err := New(dbPath)
	if err != nil {
		t.Fatalf("original db corrupted: %v", err)
	}
	defer db2.Close()
	items, _ := db2.ItemsByService("drive", "u@t.com")
	if len(items) != 1 {
		t.Fatalf("expected original item to survive, got %d", len(items))
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

`go test ./internal/metadata/ -run RestoreDB -v`
Expected: both new tests FAIL.

- [ ] **Step 3: Rewrite `RestoreDB` and harden `BackupDB`**

Replace the bottom of `internal/metadata/db.go` (the `BackupDB` and `RestoreDB` functions) with:

```go
func BackupDB(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating dest: %w", err)
	}

	gw := gzip.NewWriter(dst)
	if _, err := io.Copy(gw, src); err != nil {
		gw.Close()
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("compressing: %w", err)
	}
	if err := gw.Close(); err != nil {
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("closing gzip writer: %w", err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("syncing dest: %w", err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("closing dest: %w", err)
	}
	return nil
}

func RestoreDB(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()

	gr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gr.Close()

	tmpPath := destPath + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating temp: %w", err)
	}

	if _, err := io.Copy(tmp, gr); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("decompressing: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp: %w", err)
	}

	// Back up existing dest if present, then atomically replace.
	if _, err := os.Stat(destPath); err == nil {
		if err := os.Rename(destPath, destPath+".bak"); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("backing up existing db: %w", err)
		}
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("installing restored db: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

`go test ./internal/metadata/ -v` — expected all PASS.

---

### Task A3: `meta-restore` filters by timestamp pattern (C2)

**Files:**
- Modify: `cmd/meta.go`
- Test: `cmd/meta_internal_test.go` (new)

- [ ] **Step 1: Write test for selection logic**

Write `cmd/meta_internal_test.go`:

```go
package cmd

import "testing"

func TestPickLatestMeta(t *testing.T) {
	keys := []string{
		"_meta/2026-01-02T03-04-05.db.gz",
		"_meta/2026-05-17T18-30-00.db.gz",
		"_meta/garbage.txt",
		"_meta/zzz-not-a-snapshot.db.gz",
		"_meta/2025-12-31T23-59-59.db.gz",
	}
	got, err := pickLatestMeta(keys)
	if err != nil {
		t.Fatal(err)
	}
	want := "_meta/2026-05-17T18-30-00.db.gz"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPickLatestMetaEmpty(t *testing.T) {
	if _, err := pickLatestMeta(nil); err == nil {
		t.Fatal("expected error for empty list")
	}
	if _, err := pickLatestMeta([]string{"_meta/garbage.txt"}); err == nil {
		t.Fatal("expected error when no snapshot matches")
	}
}
```

- [ ] **Step 2: Implement `pickLatestMeta` and wire it into `meta-restore`**

Replace `cmd/meta.go` with:

```go
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var metaSnapshotRE = regexp.MustCompile(`^_meta/(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2})\.db\.gz$`)

func pickLatestMeta(keys []string) (string, error) {
	var bestKey string
	var bestT time.Time
	for _, k := range keys {
		m := metaSnapshotRE.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		t, err := time.Parse("2006-01-02T15-04-05", m[1])
		if err != nil {
			continue
		}
		if bestKey == "" || t.After(bestT) {
			bestKey = k
			bestT = t
		}
	}
	if bestKey == "" {
		return "", fmt.Errorf("no metadata snapshot found in _meta/ matching pattern YYYY-MM-DDTHH-MM-SS.db.gz")
	}
	return bestKey, nil
}

var metaBackupCmd = &cobra.Command{
	Use:   "meta-backup",
	Short: "Manually backup SQLite metadata to S3",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}
		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		dbPath := filepath.Join(dbDir, "meta.db")
		backupName := time.Now().UTC().Format("2006-01-02T15-04-05") + ".db.gz"
		backupPath := filepath.Join(dbDir, backupName)

		if err := metadata.BackupDB(dbPath, backupPath); err != nil {
			return fmt.Errorf("backing up metadata: %w", err)
		}
		defer os.Remove(backupPath)

		f, err := os.Open(backupPath)
		if err != nil {
			return err
		}
		defer f.Close()

		key := storage.MetaKey(backupName)
		if err := store.Upload(context.Background(), key, f); err != nil {
			return fmt.Errorf("uploading metadata: %w", err)
		}
		fmt.Printf("Metadata backed up to s3://%s/%s\n", cfg.Storage.Bucket, key)
		return nil
	},
}

var metaRestoreCmd = &cobra.Command{
	Use:   "meta-restore",
	Short: "Restore SQLite metadata from S3",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}

		keys, err := store.List(context.Background(), "_meta/")
		if err != nil {
			return fmt.Errorf("listing metadata: %w", err)
		}
		latest, err := pickLatestMeta(keys)
		if err != nil {
			return err
		}

		rc, err := store.Download(context.Background(), latest)
		if err != nil {
			return err
		}
		defer rc.Close()

		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		if err := os.MkdirAll(dbDir, 0700); err != nil {
			return fmt.Errorf("creating db dir: %w", err)
		}
		dbPath := filepath.Join(dbDir, "meta.db")

		tmpPath := filepath.Join(dbDir, "meta_restore.db.gz")
		tmp, err := os.Create(tmpPath)
		if err != nil {
			return err
		}
		if _, err := tmp.ReadFrom(rc); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			return err
		}
		defer os.Remove(tmpPath)

		if err := metadata.RestoreDB(tmpPath, dbPath); err != nil {
			return fmt.Errorf("restoring metadata: %w", err)
		}
		fmt.Printf("Metadata restored from %s\n", latest)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(metaBackupCmd)
	rootCmd.AddCommand(metaRestoreCmd)
}
```

- [ ] **Step 3: Run tests**

`go test ./cmd/... -v -run PickLatest` — expected PASS.

---

### Task A4: `wipe` scoped to gbackup prefixes (C8)

**Files:**
- Modify: `cmd/wipe.go`
- Test: `cmd/wipe_internal_test.go` (new)

- [ ] **Step 1: Write test for prefix filter**

Write `cmd/wipe_internal_test.go`:

```go
package cmd

import (
	"reflect"
	"sort"
	"testing"
)

func TestFilterGBackupKeys(t *testing.T) {
	in := []string{
		"_meta/2026-01-01T00-00-00.db.gz",
		"drive/u@x.com/files/abc",
		"gmail/u@x.com/2026-01.tar.gz",
		"calendar/u@x.com/2026.tar.gz",
		"contacts/u@x.com/all.tar.gz",
		"random/keepme.txt",
		"someone-elses-bucket-data.json",
	}
	owned, foreign := splitGBackupKeys(in)
	sort.Strings(owned)
	sort.Strings(foreign)
	wantOwned := []string{
		"_meta/2026-01-01T00-00-00.db.gz",
		"calendar/u@x.com/2026.tar.gz",
		"contacts/u@x.com/all.tar.gz",
		"drive/u@x.com/files/abc",
		"gmail/u@x.com/2026-01.tar.gz",
	}
	wantForeign := []string{
		"random/keepme.txt",
		"someone-elses-bucket-data.json",
	}
	if !reflect.DeepEqual(owned, wantOwned) {
		t.Fatalf("owned mismatch:\n got: %v\nwant: %v", owned, wantOwned)
	}
	if !reflect.DeepEqual(foreign, wantForeign) {
		t.Fatalf("foreign mismatch:\n got: %v\nwant: %v", foreign, wantForeign)
	}
}
```

- [ ] **Step 2: Rewrite `cmd/wipe.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var (
	wipeForce         bool
	wipeIncludeForeign bool
)

var gbackupPrefixes = []string{"_meta/", "drive/", "gmail/", "calendar/", "contacts/"}

func splitGBackupKeys(keys []string) (owned, foreign []string) {
	for _, k := range keys {
		matched := false
		for _, p := range gbackupPrefixes {
			if strings.HasPrefix(k, p) {
				matched = true
				break
			}
		}
		if matched {
			owned = append(owned, k)
		} else {
			foreign = append(foreign, k)
		}
	}
	return
}

var wipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Delete gbackup-owned objects in the backup bucket",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return fmt.Errorf("creating storage client: %w", err)
		}

		keys, err := store.List(context.Background(), "")
		if err != nil {
			return fmt.Errorf("listing objects: %w", err)
		}

		owned, foreign := splitGBackupKeys(keys)

		if len(owned) == 0 && len(foreign) == 0 {
			fmt.Println("Bucket is already empty.")
			return nil
		}

		fmt.Printf("Found %d gbackup-owned objects in bucket %s\n", len(owned), cfg.Storage.Bucket)
		if len(foreign) > 0 {
			fmt.Printf("Found %d foreign (non-gbackup) objects:\n", len(foreign))
			limit := 10
			if limit > len(foreign) {
				limit = len(foreign)
			}
			for _, k := range foreign[:limit] {
				fmt.Printf("  %s\n", k)
			}
			if len(foreign) > limit {
				fmt.Printf("  ... and %d more\n", len(foreign)-limit)
			}
			if !wipeIncludeForeign {
				fmt.Println("Refusing to wipe foreign objects. Re-run with --include-foreign to delete them too.")
			}
		}

		toDelete := owned
		if wipeIncludeForeign {
			toDelete = append(toDelete, foreign...)
		}
		if len(toDelete) == 0 {
			return nil
		}

		if !wipeForce {
			fmt.Printf("About to delete %d objects. Type 'yes' to confirm: ", len(toDelete))
			if !confirmExact("yes") {
				return fmt.Errorf("wipe cancelled")
			}
		}

		if err := store.DeleteObjects(context.Background(), toDelete); err != nil {
			return fmt.Errorf("deleting objects: %w", err)
		}

		fmt.Printf("Deleted %d objects from bucket %s\n", len(toDelete), cfg.Storage.Bucket)
		return nil
	},
}

func confirmExact(want string) bool {
	var got string
	fmt.Scanln(&got)
	return strings.TrimSpace(got) == want
}

func init() {
	rootCmd.AddCommand(wipeCmd)
	wipeCmd.Flags().BoolVarP(&wipeForce, "force", "f", false, "Skip confirmation prompt")
	wipeCmd.Flags().BoolVar(&wipeIncludeForeign, "include-foreign", false, "Also delete objects outside of gbackup prefixes")
}
```

- [ ] **Step 3: Run tests**

`go test ./cmd/... -run FilterGBackup -v` — expected PASS.

---

### Task A5: Propagate `TrackItem`/`RecordBackup` errors (C4 part 1)

**Files:**
- Modify: `internal/backup/{gmail,drive,calendar,contacts}.go`

- [ ] **Step 1: Gmail — propagate errors**

In `internal/backup/gmail.go`, change `uploadMonth` (~line 268-298):

Replace the existing trailing `for _, entry := range entries {...}` block with:

```go
for _, entry := range entries {
	if g.metaDB != nil {
		if err := g.metaDB.TrackItem(&metadata.Item{
			Service:   "gmail",
			User:      user,
			ObjectKey: objKey,
			ItemPath:  entry.Name,
			ItemID:    entryNameToID(entry.Name),
			Size:      int64(len(entry.Data)),
			Checksum:  entry.Name,
		}); err != nil {
			return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
		}
	}
}
return len(entries), nil
```

And change the `RecordBackup` call near line 263:

```go
if g.metaDB != nil {
	if err := g.metaDB.RecordBackup("gmail", user, runType); err != nil {
		return totalCount, fmt.Errorf("recording backup: %w", err)
	}
}
```

Where `runType` is computed once at the top of `BackupUser`.

- [ ] **Step 2: Drive — propagate errors**

In `internal/backup/drive.go`, lines 191-201:

```go
if d.metaDB != nil {
	if err := d.metaDB.TrackItem(&metadata.Item{
		Service:    "drive",
		User:       user,
		ObjectKey:  objKey,
		ItemID:     f.Id,
		ItemPath:   f.Name,
		Size:       int64(buf.Len()),
		Checksum:   checksum,
		ModifiedAt: modTime,
	}); err != nil {
		return count, fmt.Errorf("tracking %s: %w", f.Id, err)
	}
}
```

(Also adds `ItemPath: f.Name` — needed by Task A8.)

And lines 215-217:

```go
if d.metaDB != nil {
	if err := d.metaDB.RecordBackup("drive", user, runType); err != nil {
		return count, fmt.Errorf("recording backup: %w", err)
	}
}
```

Where `runType` is hoisted from the existing inline expression.

- [ ] **Step 3: Calendar — propagate errors**

In `internal/backup/calendar.go` lines 168-187:

```go
for _, entry := range entries {
	if c.metaDB != nil {
		if err := c.metaDB.TrackItem(&metadata.Item{
			Service:   "calendar",
			User:      user,
			ObjectKey: objKey,
			ItemPath:  entry.Name,
			ItemID:    strings.TrimSuffix(entry.Name, ".json"),
			Size:      int64(len(entry.Data)),
			Checksum:  entry.Name,
		}); err != nil {
			return totalCount, fmt.Errorf("tracking %s: %w", entry.Name, err)
		}
	}
	totalCount++
}
```

And bottom:

```go
if c.metaDB != nil {
	if err := c.metaDB.RecordBackup("calendar", user, runType); err != nil {
		return totalCount, fmt.Errorf("recording backup: %w", err)
	}
}
```

- [ ] **Step 4: Contacts — propagate errors**

In `internal/backup/contacts.go` lines 145-157 and 159-161 — same pattern:

```go
for _, entry := range allEntries {
	if c.metaDB != nil {
		if err := c.metaDB.TrackItem(&metadata.Item{
			Service:   "contacts",
			User:      user,
			ObjectKey: objKey,
			ItemPath:  entry.Name,
			ItemID:    entry.Name,
			Size:      int64(len(entry.Data)),
			Checksum:  entry.Name,
		}); err != nil {
			return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
		}
	}
}

if c.metaDB != nil {
	if err := c.metaDB.RecordBackup("contacts", user, runType); err != nil {
		return len(allEntries), fmt.Errorf("recording backup: %w", err)
	}
}
```

- [ ] **Step 5: Run tests**

`go test ./internal/backup/...` — expected PASS.

---

### Task A6: Gmail `uploadMonth` does download-then-append on incremental (C5)

**Files:**
- Modify: `internal/backup/gmail.go`
- Test: `internal/backup/gmail_test.go`

- [ ] **Step 1: Plumb `full` into `uploadMonth`**

Change the call site in `BackupUser` (around line 241):
```go
n, err := g.uploadMonth(ctx, user, month, entries, full)
```

Change `uploadMonth` signature and body:

```go
func (g *GmailBackup) uploadMonth(ctx context.Context, user, month string, entries []archive.ArchiveEntry, full bool) (int, error) {
	objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))

	var archiveData []byte
	if !full {
		existing, err := g.store.Download(ctx, objKey)
		if err == nil {
			data, appendErr := archive.AppendToArchive(existing, entries)
			existing.Close()
			if appendErr != nil {
				return 0, fmt.Errorf("appending to archive %s: %w", objKey, appendErr)
			}
			archiveData = data
		} else {
			data, createErr := archive.Create(entries)
			if createErr != nil {
				return 0, fmt.Errorf("creating archive %s: %w", objKey, createErr)
			}
			archiveData = data
		}
	} else {
		data, err := archive.Create(entries)
		if err != nil {
			return 0, fmt.Errorf("creating archive %s: %w", objKey, err)
		}
		archiveData = data
	}

	if err := g.store.Upload(ctx, objKey, bytes.NewReader(archiveData)); err != nil {
		return 0, fmt.Errorf("uploading %s: %w", objKey, err)
	}

	if g.progress != nil {
		fmt.Printf("  ↑ %s\n", objKey)
	}

	for _, entry := range entries {
		if g.metaDB != nil {
			if err := g.metaDB.TrackItem(&metadata.Item{
				Service:   "gmail",
				User:      user,
				ObjectKey: objKey,
				ItemPath:  entry.Name,
				ItemID:    entryNameToID(entry.Name),
				Size:      int64(len(entry.Data)),
				Checksum:  entry.Name,
			}); err != nil {
				return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
			}
		}
	}
	return len(entries), nil
}
```

Note: same `Calendar` import style (`existing.Close()` after the append/read).

> Note about Calendar/Contacts: existing code does `existing, err := c.store.Download(...)` then passes `existing` (an `io.ReadCloser`) into `AppendToArchive` but never closes it. We will fix that too in Task F4 — but for Gmail, do it right here.

- [ ] **Step 2: Add gmail_test for download-then-append path**

In `internal/backup/gmail_test.go`, add a test that exercises `uploadMonth` against a mock storage that has an existing archive. If the existing test file uses helpers, follow them; else use a fakeStore approach. Pattern:

```go
func TestUploadMonthIncrementalAppends(t *testing.T) {
	// Existing archive contains msg id "old".
	existing, err := archive.Create([]archive.ArchiveEntry{{Name: "old.eml", Data: []byte("OLD")}})
	if err != nil {
		t.Fatal(err)
	}

	fake := newFakeStorage()
	fake.objects["gmail/u@t.com/2026-01.tar.gz"] = existing

	g := &GmailBackup{cfg: &GmailBackupConfig{}, store: fake.client()}
	entries := []archive.ArchiveEntry{{Name: "new.eml", Data: []byte("NEW")}}

	if _, err := g.uploadMonth(context.Background(), "u@t.com", "2026-01", entries, false); err != nil {
		t.Fatal(err)
	}

	got := fake.objects["gmail/u@t.com/2026-01.tar.gz"]
	names := readArchiveNames(t, got)
	wantNames := map[string]bool{"old.eml": true, "new.eml": true}
	for n := range wantNames {
		if !contains(names, n) {
			t.Fatalf("missing %q in resulting archive (got %v)", n, names)
		}
	}
}
```

If a `fakeStorage` does not already exist, define a minimal stub inline in the test file that wraps a real `*storage.Client` is not feasible — instead make `uploadMonth` accept an interface OR refactor to introduce a tiny `storer` interface inside `internal/backup/gmail.go`:

```go
type storer interface {
	Upload(ctx context.Context, key string, r io.Reader) error
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}
```

Change `g.store` field type to `storer` and accept both real and fake. Provide `readArchiveNames` and `contains` helpers in the test.

- [ ] **Step 3: Run test, expect fail, implement, expect pass**

`go test ./internal/backup/ -run UploadMonthIncremental -v`

---

### Task A7: Drive restore uses original file name (H8)

**Files:**
- Modify: `internal/restore/drive.go`
- Modify: `internal/backup/drive.go` (already done in A5 — `ItemPath: f.Name`)
- Test: `internal/restore/drive_test.go` (new)

- [ ] **Step 1: Use `item.ItemPath` for restored name; fallback to extractFileName**

Replace `f := &drive.File{Name: extractFileName(item.ObjectKey)...}` in `internal/restore/drive.go` line 94-96 with:

```go
name := item.ItemPath
if name == "" {
	name = extractFileName(item.ObjectKey)
}
f := &drive.File{
	Name:    name,
	Parents: []string{root.Id},
}
```

- [ ] **Step 2: Write test for extractFileName edge case**

Write `internal/restore/drive_test.go`:

```go
package restore

import "testing"

func TestExtractFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"drive/u@x.com/files/abc123_v5", "abc123"},
		{"drive/u@x.com/files/file_v3.pdf_v2", "file_v3.pdf"},
		{"drive/u@x.com/files/noversion", "noversion"},
	}
	for _, c := range cases {
		if got := extractFileName(c.in); got != c.want {
			t.Errorf("extractFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 3: Run tests**

`go test ./internal/restore/ -v` — expected PASS.

---

### Phase A verification & commit

- [ ] **Step 1: Run full test suite**

`cd /Users/esignoretti/Documents/OpenCode/GBackup && go vet ./... && go test ./...`

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "fix(critical): restore service-account path, gmail incremental data loss, atomic meta restore, wipe scoping

- cmd/restore: fix typo passing AdminEmail+.json as service account file (data-loss for all restore commands)
- internal/backup/gmail: download-then-append in uploadMonth on incremental runs (prevents overwriting per-month archives)
- internal/metadata: RestoreDB writes via temp file + rename, backs up prior db to .bak; BackupDB fsyncs and checks Close errors
- cmd/meta: meta-restore filters keys by snapshot-name regex and picks max timestamp instead of lex-last
- cmd/wipe: only deletes gbackup-owned prefixes; --include-foreign required for anything else
- internal/backup/*: propagate TrackItem/RecordBackup errors instead of silently dropping them
- internal/restore/drive: restore original file names from metadata.ItemPath instead of GUID key segments
- cmd/restore: extract shared Restorer interface, dry-run-then-confirm flow, eliminates 4x duplication"
```

---

## Phase B — Resource & robustness

### Task B1: Propagate `ctx` through `runner.Run` (H4)

**Files:**
- Modify: `internal/backup/runner.go`
- Modify: `cmd/backup.go` (signal handler)

- [ ] **Step 1: Fix `Runner.Run` to use the passed ctx**

In `internal/backup/runner.go` line 51, change:
```go
err := r.runService(context.Background(), svc, users, full)
```
to:
```go
err := r.runService(ctx, svc, users, full)
```

Also in `resolveUsers`, take ctx as parameter and pass through:

```go
func (r *Runner) resolveUsers(ctx context.Context) []string { ... }
```

(already takes ctx — verify it's the passed ctx.)

- [ ] **Step 2: Add signal handler in `cmd/backup.go`**

Replace the `ctx := context.Background()` in `cmd/backup.go:56`:

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()
```

Add imports: `"os/signal"`, `"syscall"`.

- [ ] **Step 3: Run tests**

`go test ./internal/backup/... ./cmd/...`

---

### Task B2: Per-call API timeouts (H10, M2)

**Files:**
- Modify: `internal/backup/{calendar,contacts,drive}.go`, `internal/gws/directory.go`

- [ ] **Step 1: Calendar list timeout**

In `internal/backup/calendar.go`, replace the list loop's `.Do()` call (line 76-85) with a timed version:

```go
listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
call := svc.Events.List("primary").
	Context(listCtx).
	SingleEvents(true).
	MaxResults(2500).
	ShowDeleted(false).
	TimeMin(time.Now().Add(-366 * 24 * time.Hour).Format(time.RFC3339))
if pageToken != "" {
	call.PageToken(pageToken)
}
resp, err := call.Do()
listCancel()
if err != nil {
	return 0, fmt.Errorf("listing events: %w", err)
}
```

- [ ] **Step 2: Contacts list timeout**

In `internal/backup/contacts.go`, similar wrap around the `svc.People.Connections.List` `.Do()` call.

- [ ] **Step 3: Drive list timeout**

In `internal/backup/drive.go` line 128-142, wrap the `Files.List().Do()`:

```go
listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
call := d.driveSvc.Files.List().
	Context(listCtx).
	... (existing chain)
if pageToken != "" {
	call.PageToken(pageToken)
}
fileList, err := call.Do()
listCancel()
```

- [ ] **Step 4: Directory list timeout**

In `internal/gws/directory.go` line 38-48, wrap similarly with a 60s WithTimeout.

- [ ] **Step 5: Run tests + vet**

`go vet ./... && go test ./...`

---

### Task B3: Errgroup limits + Gmail parallel (H5)

**Files:**
- Modify: `internal/backup/runner.go`

- [ ] **Step 1: Add concurrency limit constant**

At top of `internal/backup/runner.go`:

```go
const perServiceUserConcurrency = 5
```

- [ ] **Step 2: Apply `SetLimit` to each errgroup**

In each of `runDriveBackup`, `runContactsBackup`, `runCalendarBackup`:

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(perServiceUserConcurrency)
```

- [ ] **Step 3: Convert `runGmailBackup` to parallel**

Replace `runGmailBackup` with:

```go
func (r *Runner) runGmailBackup(ctx context.Context, users []string, full bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(perServiceUserConcurrency)
	for _, user := range users {
		user := user
		g.Go(func() error {
			gb, err := newGmailBackup(r.cfg)
			if err != nil {
				return err
			}
			_, err = gb.BackupUser(ctx, user, full)
			if err != nil {
				return fmt.Errorf("gmail backup for %s: %w", user, err)
			}
			return nil
		})
	}
	return g.Wait()
}
```

- [ ] **Step 4: Run tests**

`go test ./internal/backup/...`

---

### Task B4: Gmail fetch loop uses errgroup with ctx cancel (M6)

**Files:**
- Modify: `internal/backup/gmail.go`

- [ ] **Step 1: Replace sem+WaitGroup with errgroup**

In `internal/backup/gmail.go` around line 145-220, replace the manual `sync.WaitGroup` + `sem` pattern with:

```go
limiter := rate.NewLimiter(rate.Limit(gmailRateLimit), gmailRateBurst)

eg, egCtx := errgroup.WithContext(ctx)
eg.SetLimit(gmailConcurrency)

buckets := make(map[string][]archive.ArchiveEntry)
var mu sync.Mutex
var fetched int

for _, id := range allMsgIDs {
	id := id
	eg.Go(func() error {
		if err := limiter.Wait(egCtx); err != nil {
			return fmt.Errorf("rate limiter: %w", err)
		}
		msg, err := fetchWithRetry(egCtx, svc, user, id)
		if err != nil {
			return err
		}
		if g.maxAge > 0 {
			msgTime := time.UnixMilli(msg.InternalDate)
			if time.Since(msgTime) > g.maxAge {
				return nil
			}
		}
		raw, err := base64.URLEncoding.DecodeString(msg.Raw)
		if err != nil {
			return fmt.Errorf("decoding message %s: %w", id, err)
		}
		month := messageMonth(msg)
		entryName := fmt.Sprintf("%s.eml", id)

		mu.Lock()
		buckets[month] = append(buckets[month], archive.ArchiveEntry{
			Name: entryName,
			Data: raw,
		})
		fetched++
		if g.progress != nil && fetched%100 == 0 {
			fmt.Printf("  fetched %d messages so far...\n", fetched)
		}
		mu.Unlock()
		return nil
	})
}

if err := eg.Wait(); err != nil {
	return fetched, err
}
```

Add import: `"golang.org/x/sync/errgroup"`.

- [ ] **Step 2: Run tests**

`go test ./internal/backup/... -v`

---

### Phase B verification & commit

- [ ] `go vet ./... && go test ./...`
- [ ] Commit:

```bash
git commit -am "fix(robustness): propagate ctx, per-call timeouts, bounded fanout, errgroup cancellation

- runner: pass caller ctx into per-service goroutines (was hardcoded Background)
- cmd/backup: install signal handler to cancel on SIGINT/SIGTERM
- backup/{drive,calendar,contacts,gws}: wrap every Google API list call in 60s WithTimeout
- runner: SetLimit on per-user errgroups; convert Gmail to parallel like the others
- backup/gmail: replace manual semaphore+WaitGroup with errgroup so first error cancels remaining fetches"
```

---

## Phase C — Errors & retries

### Task C1: Typed Google API error classifier (M4)

**Files:**
- Create: `internal/gws/errors.go`
- Create: `internal/gws/errors_test.go`
- Modify: `internal/backup/{runner,gmail}.go` to use the shared helpers

- [ ] **Step 1: Write test**

`internal/gws/errors_test.go`:

```go
package gws

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", &googleapi.Error{Code: 429}, true},
		{"500", &googleapi.Error{Code: 500}, true},
		{"503", &googleapi.Error{Code: 503}, true},
		{"403", &googleapi.Error{Code: 403}, false},
		{"400", &googleapi.Error{Code: 400}, false},
		{"wrapped 503", fmt.Errorf("wrapping: %w", &googleapi.Error{Code: 503}), true},
		{"plain error", errors.New("nope"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRetryable(c.err); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsServiceDisabled(t *testing.T) {
	e := &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}}
	if !IsServiceDisabled(e) {
		t.Fatal("expected IsServiceDisabled true")
	}
	if IsServiceDisabled(&googleapi.Error{Code: 500}) {
		t.Fatal("expected IsServiceDisabled false for 500")
	}
}
```

- [ ] **Step 2: Implement**

`internal/gws/errors.go`:

```go
package gws

import (
	"errors"

	"google.golang.org/api/googleapi"
)

// IsRetryable returns true for transient Google API errors:
// HTTP 429 and 5xx.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return ge.Code == 429 || (ge.Code >= 500 && ge.Code < 600)
	}
	return false
}

// IsServiceDisabled returns true when the underlying API is not enabled
// in the Google Cloud project (HTTP 403 with reason accessNotConfigured / SERVICE_DISABLED).
func IsServiceDisabled(err error) bool {
	if err == nil {
		return false
	}
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	if ge.Code != 403 {
		return false
	}
	for _, item := range ge.Errors {
		switch item.Reason {
		case "accessNotConfigured", "SERVICE_DISABLED":
			return true
		}
	}
	// Some payloads put it only in the message body.
	if ge.Message != "" && (containsAny(ge.Message, "SERVICE_DISABLED", "accessNotConfigured", "not been used in project")) {
		return true
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && stringContains(s, sub) {
			return true
		}
	}
	return false
}

// stringContains exists so we can avoid importing strings and keep the
// pure-error-classification API minimal.
func stringContains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Replace per-package classifiers**

In `internal/backup/gmail.go`:
- Delete `isRetryableError` and `isServiceDisabled` (and the duplicated one in runner.go if present).
- Replace call sites with `gws.IsRetryable(err)` and `gws.IsServiceDisabled(err)`.

In `internal/backup/runner.go`:
- Replace `isServiceDisabled(err)` with `gws.IsServiceDisabled(err)`.

- [ ] **Step 4: Run tests**

`go test ./internal/gws/... ./internal/backup/...`

---

### Task C2: Fix Gmail retry backoff math (M3)

**Files:**
- Modify: `internal/backup/gmail.go`

- [ ] **Step 1: Replace `fetchWithRetry` backoff calc with cleaner version**

Replace `fetchWithRetry`:

```go
func fetchWithRetry(ctx context.Context, svc *gmail.Service, user, id string) (*gmail.Message, error) {
	const baseBackoff = 200 * time.Millisecond
	const maxBackoff = 5 * time.Second

	var msg *gmail.Message
	var err error
	for attempt := 0; attempt < gmailMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := baseBackoff << (attempt - 1)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			// Add 0-100% jitter on top.
			jitter := time.Duration(rand.Int63n(int64(backoff)))
			backoff += jitter
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		msg, err = svc.Users.Messages.Get(user, id).Context(fetchCtx).Format("raw").Do()
		cancel()
		if err == nil {
			return msg, nil
		}
		if gws.IsRetryable(err) {
			continue
		}
		return nil, fmt.Errorf("getting message %s: %w", id, err)
	}
	return nil, fmt.Errorf("getting message %s after %d retries: %w", id, gmailMaxRetries, err)
}
```

Add import: `"github.com/esignoretti/gbackup/internal/gws"` (if not already present).

- [ ] **Step 2: Run tests**

`go test ./internal/backup/...`

---

### Task C3: Drive — log `IsModified` error, retry per-file, continue on per-file failure (H3, M10, H9)

**Files:**
- Modify: `internal/backup/drive.go`

- [ ] **Step 1: Add per-file retry wrapper around download**

At the top of `internal/backup/drive.go`, add:

```go
const driveMaxRetries = 3
```

Wrap `downloadFile` with retry:

```go
func (d *DriveBackup) downloadFileRetry(ctx context.Context, fileID, category string) (io.ReadCloser, error) {
	const base = 200 * time.Millisecond
	const max = 5 * time.Second
	var lastErr error
	for attempt := 0; attempt < driveMaxRetries; attempt++ {
		if attempt > 0 {
			delay := base << (attempt - 1)
			if delay > max {
				delay = max
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		rc, err := d.downloadFile(ctx, fileID, category)
		if err == nil {
			return rc, nil
		}
		lastErr = err
		if !gws.IsRetryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("download failed after %d retries: %w", driveMaxRetries, lastErr)
}
```

Add imports if missing: `"time"`, `"github.com/esignoretti/gbackup/internal/gws"`.

- [ ] **Step 2: Make per-file errors non-fatal and log `IsModified` errors**

Inside the file loop in `BackupUser` (after the `IsModified` block and the download), accumulate errors and continue:

```go
if !full && d.metaDB != nil {
	modified, err := d.metaDB.IsModified("drive", user, f.Id, checksum)
	if err != nil {
		fmt.Printf("  drive: warning: IsModified failed for %s: %v (re-uploading)\n", f.Id, err)
	} else if !modified {
		continue
	}
}

content, err := d.downloadFileRetry(ctx, f.Id, category)
if err != nil {
	fmt.Printf("  drive: skipping %s (%s): %v\n", f.Name, f.Id, err)
	continue
}
fetched++

version := f.Version
objKey := storage.ObjectKey("drive", user, fmt.Sprintf("files/%s_v%d", f.Id, version))

var buf bytes.Buffer
gw := gzip.NewWriter(&buf)
_, copyErr := io.Copy(gw, content)
content.Close()
closeErr := gw.Close()
if joinedErr := errors.Join(copyErr, closeErr); joinedErr != nil {
	fmt.Printf("  drive: compression failed for %s: %v\n", f.Name, joinedErr)
	continue
}

if err := d.store.Upload(ctx, objKey, bytes.NewReader(buf.Bytes())); err != nil {
	fmt.Printf("  drive: upload failed for %s: %v\n", objKey, err)
	continue
}
```

(Replace the existing block in `BackupUser` from `if !full && d.metaDB != nil` through the `count++` line.)

- [ ] **Step 3: Run tests**

`go test ./internal/backup/...`

---

### Task C4: Drive mime category mapping (M9)

**Files:**
- Modify: `internal/backup/drive.go`

- [ ] **Step 1: Replace `mimeCategory` and `downloadFile` to support proper exports**

```go
// Maps Google Docs Editors MIME types to the export MIME and file extension.
var driveExportMap = map[string]struct {
	exportMIME string
	ext        string
}{
	"application/vnd.google-apps.document":     {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	"application/vnd.google-apps.spreadsheet":  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	"application/vnd.google-apps.presentation": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
	"application/vnd.google-apps.drawing":      {"image/png", ".png"},
}

func mimeCategory(mime string) string {
	if mime == "application/vnd.google-apps.folder" {
		return "folder"
	}
	if _, ok := driveExportMap[mime]; ok {
		return "google-doc"
	}
	if strings.HasPrefix(mime, "application/vnd.google-apps") {
		// Forms / Sites / Maps / Apps Script — not exportable.
		return "non-exportable"
	}
	return "binary"
}
```

In `BackupUser`, after computing `category`:

```go
if category == "folder" {
	continue
}
if category == "non-exportable" {
	fmt.Printf("  drive: skipping non-exportable %s (%s)\n", f.Name, f.MimeType)
	continue
}
```

In `downloadFile`, switch on the export map for `google-doc`:

```go
func (d *DriveBackup) downloadFile(ctx context.Context, file *drive.File, category string) (io.ReadCloser, string, error) {
	if category == "google-doc" {
		entry, ok := driveExportMap[file.MimeType]
		if !ok {
			return nil, "", fmt.Errorf("no export mapping for %s", file.MimeType)
		}
		resp, err := d.driveSvc.Files.Export(file.Id, entry.exportMIME).Context(ctx).Download()
		if err != nil {
			return nil, "", err
		}
		return resp.Body, entry.ext, nil
	}
	resp, err := d.driveSvc.Files.Get(file.Id).Context(ctx).SupportsAllDrives(true).Download()
	if err != nil {
		return nil, "", err
	}
	return resp.Body, "", nil
}
```

Update the call site in `BackupUser`/`downloadFileRetry` to pass the `*drive.File` and capture the extension. Update `downloadFileRetry`:

```go
func (d *DriveBackup) downloadFileRetry(ctx context.Context, file *drive.File, category string) (io.ReadCloser, string, error) { ... }
```

Update `objKey` to include the extension when present (so Drive restore can pick the right MIME later):

```go
content, ext, err := d.downloadFileRetry(ctx, f, category)
if err != nil { ... }

objKey := storage.ObjectKey("drive", user, fmt.Sprintf("files/%s_v%d%s", f.Id, version, ext))
```

Also update `extractFileName` in `internal/restore/drive.go` to handle the optional extension after the version suffix:

```go
func extractFileName(key string) string {
	parts := strings.Split(key, "/")
	last := parts[len(parts)-1]
	if idx := strings.LastIndex(last, "_v"); idx >= 0 {
		// Keep extension portion after the version digits, if any.
		// e.g. "abc_v3.xlsx" → "abc.xlsx"
		rest := last[idx+2:]
		dotIdx := strings.Index(rest, ".")
		if dotIdx >= 0 {
			last = last[:idx] + rest[dotIdx:]
		} else {
			last = last[:idx]
		}
	}
	return last
}
```

Update tests in `internal/restore/drive_test.go` accordingly:

```go
func TestExtractFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"drive/u@x.com/files/abc123_v5", "abc123"},
		{"drive/u@x.com/files/abc123_v5.xlsx", "abc123.xlsx"},
		{"drive/u@x.com/files/noversion", "noversion"},
	}
	...
}
```

- [ ] **Step 2: Run tests**

`go test ./internal/backup/... ./internal/restore/...`

---

### Phase C verification & commit

- [ ] `go vet ./... && go test ./...`
- [ ] Commit:

```bash
git commit -am "fix(errors): typed Google API retry classifier, drive per-file retry+skip, sane backoff, mime mapping

- gws/errors: IsRetryable / IsServiceDisabled assert *googleapi.Error and inspect Code/Reason
- backup/gmail: replace fragile string-match retry classifier; backoff now actually doubles up to 5s with jitter
- backup/drive: retry transient download failures, log+skip per-file errors instead of aborting whole user
- backup/drive: proper export-format map for Docs/Sheets/Slides/Drawings; skip non-exportable Forms/Sites
- restore/drive: extractFileName preserves the new extension suffix on object keys"
```

---

## Phase D — Restore polish

### Task D1: Drive restore date boundary fix (H1)

**Files:**
- Modify: `internal/restore/drive.go`

- [ ] **Step 1: Change inclusive boundary**

In `internal/restore/drive.go` line 54, change:
```go
if !item.ModifiedAt.After(cutoff.Add(24 * time.Hour)) {
```
to:
```go
endOfDay := cutoff.Add(24 * time.Hour)
for _, item := range items {
	if item.ModifiedAt.Before(endOfDay) {
		filtered = append(filtered, item)
	}
}
```

(In context: the loop becomes `for _, item := range items { if item.ModifiedAt.Before(endOfDay) { filtered = append(filtered, item) } }`.)

Add a test:

```go
func TestDriveRestoreDateFilter(t *testing.T) {
	may15 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	may16Start := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	items := []*metadata.Item{
		{ItemID: "a", ModifiedAt: may15.Add(12 * time.Hour)}, // included
		{ItemID: "b", ModifiedAt: may16Start},                // excluded (next day)
		{ItemID: "c", ModifiedAt: may16Start.Add(-time.Second)}, // included (last second of may 15)
	}
	cutoff := may15
	endOfDay := cutoff.Add(24 * time.Hour)
	var got []string
	for _, it := range items {
		if it.ModifiedAt.Before(endOfDay) {
			got = append(got, it.ItemID)
		}
	}
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
```

(Add imports `reflect`, `time`, `metadata` to the test file.)

- [ ] **Step 2: Run test**

`go test ./internal/restore/...`

---

### Task D2: Populate `ModifiedAt` for gmail/calendar/contacts (H2)

**Files:**
- Modify: `internal/backup/{gmail,calendar,contacts}.go`

- [ ] **Step 1: Gmail — set ModifiedAt from InternalDate**

Pass the msg through to `uploadMonth` if needed, or attach the time per-entry. Simplest: enrich `archive.ArchiveEntry` with a `ModTime time.Time` field for use only by the metadata tracking step.

Add to `internal/archive/archive.go`:
```go
type ArchiveEntry struct {
	Name    string
	Data    []byte
	ModTime time.Time
}
```

(Import `"time"`. Existing tar header still uses `Mode/Size`, but if we want to preserve mtime in the tar header for free, add `ModTime: e.ModTime` to the `*tar.Header` in `Create` — verify no existing tests break.)

In gmail fetch loop, set `ModTime: time.UnixMilli(msg.InternalDate)` on the `ArchiveEntry`. In `uploadMonth` `TrackItem`, set `ModifiedAt: entry.ModTime`.

- [ ] **Step 2: Calendar — set ModifiedAt from event.Updated**

```go
modTime := time.Now()
if t, err := time.Parse(time.RFC3339, event.Updated); err == nil {
	modTime = t
}
buckets[year] = append(buckets[year], archive.ArchiveEntry{
	Name:    entryName,
	Data:    data,
	ModTime: modTime,
})
```

And in `TrackItem`: `ModifiedAt: entry.ModTime`.

- [ ] **Step 3: Contacts — set ModifiedAt from person metadata sources**

```go
modTime := time.Now()
if person.Metadata != nil {
	for _, src := range person.Metadata.Sources {
		if src.UpdateTime != "" {
			if t, err := time.Parse(time.RFC3339, src.UpdateTime); err == nil {
				modTime = t
				break
			}
		}
	}
}
allEntries = append(allEntries, archive.ArchiveEntry{
	Name:    entryName,
	Data:    data,
	ModTime: modTime,
})
```

And in `TrackItem`: `ModifiedAt: entry.ModTime`.

- [ ] **Step 4: Run tests**

`go test ./internal/backup/... ./internal/archive/...`

---

### Task D3: Contacts restore preserves full Person object (H7)

**Files:**
- Modify: `internal/restore/contacts.go`

- [ ] **Step 1: Strip metadata, then send the full person**

Replace the inner restore body in `Run`:

```go
var person people.Person
if err := json.Unmarshal(entry.Data, &person); err != nil {
	return fmt.Errorf("unmarshaling contact %s: %w", entry.Name, err)
}
// CreateContact rejects ResourceName, Etag, and Metadata on input — clear them.
person.ResourceName = ""
person.Etag = ""
person.Metadata = nil
if _, err := svc.People.CreateContact(&person).Context(ctx).Do(); err != nil {
	return fmt.Errorf("creating contact %s: %w", entry.Name, err)
}
restored++
return nil
```

- [ ] **Step 2: Run tests**

`go test ./internal/restore/...`

---

### Phase D verification & commit

- [ ] `go vet ./... && go test ./...`
- [ ] Commit:

```bash
git commit -am "fix(restore): correct date boundary, populate ModifiedAt across services, preserve full Person

- restore/drive: include items strictly before endOfDay, exclude the next-day boundary
- archive: ArchiveEntry now carries a ModTime so backup metadata reflects actual item mtime
- backup/{gmail,calendar,contacts}: pass real mtime (InternalDate / event.Updated / metadata.Sources.UpdateTime) into TrackItem
- restore/contacts: pass full Person to CreateContact (clearing immutable fields) instead of copying 6 hand-picked sub-fields"
```

---

## Phase E — Config, secrets, server

### Task E1: S3 default credential chain (H6)

**Files:**
- Modify: `internal/storage/s3.go`

- [ ] **Step 1: Conditional credential provider**

Replace `NewClient`:

```go
func NewClient(cfg *gconfig.StorageConfig) (*Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithRetryMode(aws.RetryModeAdaptive),
		awsconfig.WithRetryMaxAttempts(10),
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		)))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}

	return &Client{
		client: s3.NewFromConfig(awsCfg, s3Opts...),
		bucket: cfg.Bucket,
	}, nil
}
```

- [ ] **Step 2: Relax Validate so empty keys fall back to default chain**

In `internal/config/config.go` `Validate`, remove the two checks for `AccessKeyID`/`SecretAccessKey`. Keep `Bucket` and `Region` required.

Update `internal/config/config_test.go` accordingly (drop tests asserting access-key required, add one asserting empty creds are OK).

- [ ] **Step 3: Run tests**

`go test ./internal/storage/... ./internal/config/...`

---

### Task E2: `cmd/init.go` — bufio + masked secret + filepath.Join (M13)

**Files:**
- Modify: `cmd/init.go`

- [ ] **Step 1: Replace `Scanln` with `bufio.Scanner`, mask the secret**

```go
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func readLine(scanner *bufio.Scanner, prompt string) string {
	fmt.Print(prompt)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create an interactive configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := &config.Config{}
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 1024), 1024*1024)

		cfg.Workspace.Domain = readLine(scanner, "Google Workspace domain: ")
		cfg.Workspace.AdminEmail = readLine(scanner, "Admin email: ")
		cfg.Workspace.ServiceAccountFile = readLine(scanner, "Path to Google service account JSON key file: ")
		cfg.Storage.Bucket = readLine(scanner, "S3 bucket name: ")
		cfg.Storage.Region = readLine(scanner, "S3 region: ")
		cfg.Storage.Endpoint = readLine(scanner, "S3 endpoint (leave empty for AWS): ")
		cfg.Storage.AccessKeyID = readLine(scanner, "S3 access key ID (empty to use AWS default chain): ")
		if cfg.Storage.AccessKeyID != "" {
			s, err := readSecret("S3 secret access key: ")
			if err != nil {
				return fmt.Errorf("reading secret: %w", err)
			}
			cfg.Storage.SecretAccessKey = s
		}

		cfg.Services = []string{"drive", "gmail", "calendar", "contacts"}
		cfg.Users.Include = "*"

		path := cfgFile
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".gbackup", "gbackup.yaml")
		}

		if err := config.Save(path, cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("Configuration saved to %s\n", path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
```

- [ ] **Step 2: Add `golang.org/x/term` dependency**

```bash
cd /Users/esignoretti/Documents/OpenCode/GBackup && go get golang.org/x/term && go mod tidy
```

- [ ] **Step 3: Build**

`go build ./...`

---

### Task E3: `web/server.go` timeouts + bind validation + error logging (H11)

**Files:**
- Modify: `web/server.go`

- [ ] **Step 1: Use `http.Server` with timeouts, log handler errors**

Replace `Start`:

```go
func (s *Server) Start() error {
	if err := s.validateAddr(); err != nil {
		return err
	}

	mux := http.NewServeMux()

	tmplContent, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		return fmt.Errorf("reading template: %w", err)
	}
	tmpl := template.Must(template.New("dashboard").Parse(string(tmplContent)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if err := tmpl.Execute(w, DashboardData{
			Domain: s.Config.Workspace.Domain,
			Bucket: s.Config.Storage.Bucket,
			Region: s.Config.Storage.Region,
		}); err != nil {
			log.Printf("dashboard render error: %v", err)
		}
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"domain": s.Config.Workspace.Domain,
			"bucket": s.Config.Storage.Bucket,
		}); err != nil {
			log.Printf("status encode error: %v", err)
		}
	})

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Printf("Web dashboard starting on %s\n", s.Addr)
	return srv.ListenAndServe()
}

func (s *Server) validateAddr() error {
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", s.Addr, err)
	}
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return nil
	default:
		return fmt.Errorf("refusing to bind to %q: dashboard must be localhost-only", host)
	}
}
```

Add imports: `"log"`, `"net"`, `"time"`.

- [ ] **Step 2: Run tests + build**

`go vet ./web/... && go build ./web/...`

---

### Phase E verification & commit

- [ ] `go vet ./... && go test ./...`
- [ ] Commit:

```bash
git commit -am "fix(config/server): default-chain S3 creds, masked init prompt, dashboard timeouts + bind validation

- storage/s3: only force static creds when keys provided; otherwise use AWS default chain (env, profile, IAM role)
- config: stop requiring access/secret in validation
- cmd/init: bufio scanner (handles paths with spaces), terminal password read for secret, filepath.Join
- web/server: http.Server with read/write/idle timeouts; refuse to bind anything other than localhost; log handler errors instead of dropping them"
```

---

## Phase F — Metadata & polish

### Task F1: `backup_runs` proper start/complete (M8)

**Files:**
- Modify: `internal/metadata/db.go`, `internal/backup/{gmail,drive,calendar,contacts}.go`

- [ ] **Step 1: Add `StartBackup` + `CompleteBackup`**

In `internal/metadata/db.go`, replace `RecordBackup` with:

```go
func (d *DB) StartBackup(service, user, runType string) (int64, error) {
	res, err := d.db.Exec(`
		INSERT INTO backup_runs (service, user_email, run_type, started_at)
		VALUES (?, ?, ?, ?)`,
		service, user, runType, time.Now(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) CompleteBackup(id int64) error {
	_, err := d.db.Exec(`UPDATE backup_runs SET completed_at = ? WHERE id = ?`, time.Now(), id)
	return err
}
```

Keep `RecordBackup` as a thin shim for any leftover callers:

```go
func (d *DB) RecordBackup(service, user, runType string) error {
	id, err := d.StartBackup(service, user, runType)
	if err != nil {
		return err
	}
	return d.CompleteBackup(id)
}
```

- [ ] **Step 2: Wire start/complete into each service backup**

In each of `gmail.go`, `drive.go`, `calendar.go`, `contacts.go`, at the start of `BackupUser` (after argument validation, before doing work):

```go
var runID int64
if g.metaDB != nil {
	id, err := g.metaDB.StartBackup("gmail", user, runType)
	if err != nil {
		return 0, fmt.Errorf("recording backup start: %w", err)
	}
	runID = id
}
```

Replace the `RecordBackup` at the end with:

```go
if g.metaDB != nil && runID != 0 {
	if err := g.metaDB.CompleteBackup(runID); err != nil {
		return totalCount, fmt.Errorf("completing backup record: %w", err)
	}
}
```

(Apply the same pattern to drive/calendar/contacts with their own receiver and counts.)

- [ ] **Step 3: Add test**

In `internal/metadata/db_test.go`:

```go
func TestStartCompleteBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.StartBackup("drive", "u@t.com", "full")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected nonzero id")
	}

	if err := db.CompleteBackup(id); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 4: Run tests**

`go test ./internal/metadata/... ./internal/backup/...`

---

### Task F2: `LastBackupTime` uses `sql.NullTime` (M1)

**Files:**
- Modify: `internal/metadata/db.go`

- [ ] **Step 1: Replace body**

```go
func (d *DB) LastBackupTime(service, user string) (time.Time, error) {
	row := d.db.QueryRow(`
		SELECT MAX(completed_at) FROM backup_runs
		WHERE service = ? AND user_email = ? AND completed_at IS NOT NULL`,
		service, user,
	)
	var t sql.NullTime
	if err := row.Scan(&t); err != nil {
		return time.Time{}, err
	}
	if !t.Valid {
		return time.Time{}, sql.ErrNoRows
	}
	return t.Time, nil
}
```

- [ ] **Step 2: Run tests**

`go test ./internal/metadata/...`

---

### Task F3: `validateName` tolerates `..` inside filenames (M5)

**Files:**
- Modify: `internal/archive/archive.go`
- Test: `internal/archive/archive_test.go`

- [ ] **Step 1: Add tests covering edge cases**

```go
func TestValidateNameAllowsDoubleDotInFilename(t *testing.T) {
	good := []string{"foo..bar", "..hidden", "a.b..c.txt"}
	for _, n := range good {
		_, err := Create([]ArchiveEntry{{Name: n, Data: []byte("ok")}})
		if err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
}

func TestValidateNameRejectsTraversal(t *testing.T) {
	bad := []string{"../escape.txt", "a/../b", "/abs.txt", "../../foo"}
	for _, n := range bad {
		_, err := Create([]ArchiveEntry{{Name: n, Data: []byte("bad")}})
		if err == nil {
			t.Errorf("%q accepted, want error", n)
		}
	}
}
```

- [ ] **Step 2: Tighten `validateName`**

Replace:

```go
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid entry name: empty")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid entry name (absolute): %q", name)
	}
	clean := path.Clean(name)
	if clean != name {
		return fmt.Errorf("invalid entry name (non-canonical): %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("invalid entry name (path traversal): %q", name)
		}
	}
	return nil
}
```

Replace `filepath` import with `path` (or import both; `path` is more correct here since archive entry names are always slash-separated). Remove the now-unused `filepath` import if no other uses.

- [ ] **Step 3: Run tests**

`go test ./internal/archive/...`

---

### Task F4: `AppendToArchive` skips work when nothing new + closes existing reader (M11)

**Files:**
- Modify: `internal/archive/archive.go`, `internal/backup/{calendar,contacts}.go`

- [ ] **Step 1: Skip rewrite when no new entries**

Add early return in `AppendToArchive`:

```go
func AppendToArchive(existing io.Reader, entries []ArchiveEntry) ([]byte, error) {
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

	if len(entries) == 0 {
		return nil, ErrNoChanges
	}

	changed := false
	for _, e := range entries {
		old, present := existingByName[e.Name]
		if !present || !bytes.Equal(old, e.Data) {
			changed = true
		}
		existingByName[e.Name] = e.Data
	}
	if !changed {
		return nil, ErrNoChanges
	}

	all := make([]ArchiveEntry, 0, len(existingByName))
	for name, data := range existingByName {
		all = append(all, ArchiveEntry{Name: name, Data: data})
	}
	return Create(all)
}

var ErrNoChanges = errors.New("archive: no changes to append")
```

Add imports `"bytes"` and `"errors"`.

- [ ] **Step 2: Update callers to skip upload on `ErrNoChanges`**

In `internal/backup/calendar.go` after `archive.AppendToArchive(...)`:

```go
data, appendErr := archive.AppendToArchive(existing, entries)
existing.Close()
if errors.Is(appendErr, archive.ErrNoChanges) {
	continue // nothing changed for this year
}
if appendErr != nil {
	return totalCount, fmt.Errorf("appending to archive %s: %w", objKey, appendErr)
}
archiveData = data
```

(Move the `existing.Close()` out of the deferred path so we close the reader as soon as we're done with it.)

Same fix in `internal/backup/contacts.go` and the gmail incremental path added in Task A6.

Add `"errors"` import if missing.

- [ ] **Step 3: Update test**

Already existing `TestAppendToArchive_DuplicateReplaces` will break because it appends a *changed* `a.txt` and expects 1 entry. That still passes (changed=true). Add an additional test:

```go
func TestAppendToArchive_NoChangeReturnsSentinel(t *testing.T) {
	orig := []ArchiveEntry{{Name: "a.txt", Data: []byte("same")}}
	data, _ := Create(orig)
	_, err := AppendToArchive(bytes.NewReader(data), []ArchiveEntry{{Name: "a.txt", Data: []byte("same")}})
	if !errors.Is(err, ErrNoChanges) {
		t.Fatalf("expected ErrNoChanges, got %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

`go test ./internal/archive/... ./internal/backup/...`

---

### Task F5: Runner returns structured per-service results (M7)

**Files:**
- Modify: `internal/backup/runner.go`, `cmd/backup.go`

- [ ] **Step 1: Define `RunResult`**

In `internal/backup/runner.go`:

```go
type RunResult struct {
	Services map[string]ServiceResult
}

type ServiceResult struct {
	Skipped     bool
	SkipReason  string
	Err         error
}

func (r RunResult) AnyRan() bool {
	for _, s := range r.Services {
		if !s.Skipped && s.Err == nil {
			return true
		}
	}
	return false
}

func (r RunResult) FirstError() error {
	for _, s := range r.Services {
		if s.Err != nil {
			return s.Err
		}
	}
	return nil
}
```

Change `Run` to return `(RunResult, error)`:

```go
func (r *Runner) Run(ctx context.Context, full bool) (RunResult, error) {
	result := RunResult{Services: map[string]ServiceResult{}}
	if r.cfg == nil || r.cfg.Config == nil {
		return result, fmt.Errorf("runner not initialized: missing config")
	}
	if r.cfg.Store == nil {
		return result, fmt.Errorf("runner not initialized: missing storage client")
	}

	users := r.resolveUsers(ctx)

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, svc := range r.cfg.Config.Services {
		svc := svc
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.runService(ctx, svc, users, full)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && gws.IsServiceDisabled(err) {
				result.Services[svc] = ServiceResult{Skipped: true, SkipReason: "API not enabled"}
				fmt.Fprintf(os.Stderr, "  %s: API not enabled in Google Cloud project, skipping\n", svc)
				return
			}
			if err != nil {
				result.Services[svc] = ServiceResult{Err: fmt.Errorf("%s: %w", svc, err)}
				return
			}
			result.Services[svc] = ServiceResult{}
		}()
	}
	wg.Wait()
	return result, result.FirstError()
}
```

Add imports `"sync"`, `"os"`, `"github.com/esignoretti/gbackup/internal/gws"`.

- [ ] **Step 2: Wire up `cmd/backup.go`**

In `cmd/backup.go`:

```go
result, err := runner.Run(ctx, full)
if err != nil {
	return fmt.Errorf("backup failed: %w", err)
}
if !result.AnyRan() {
	return fmt.Errorf("backup failed: all services skipped (check Google Cloud API enablement)")
}
```

- [ ] **Step 3: Run tests**

`go test ./internal/backup/... ./cmd/...`

---

### Phase F verification & commit

- [ ] `go vet ./... && go test ./...`
- [ ] Commit:

```bash
git commit -am "fix(metadata/archive): real start/complete times, NullTime parse, archive validate+no-op, structured run result

- metadata: split RecordBackup into Start/Complete so backup_runs reflects real start time and NULL completed_at on crash
- metadata: LastBackupTime scans sql.NullTime instead of fragile string format
- archive: validateName allows .. inside filenames (e.g. foo..bar) but rejects real traversal
- archive: AppendToArchive returns sentinel ErrNoChanges so callers can skip uploads when nothing actually changed
- backup/{calendar,contacts,gmail}: skip upload on ErrNoChanges; close existing-archive reader immediately
- runner: returns RunResult with per-service status; cmd/backup exits non-zero if every service was skipped"
```

---

## Final verification

- [ ] `cd /Users/esignoretti/Documents/OpenCode/GBackup && go vet ./... && go test ./... && go build ./...`
- [ ] All commits made cleanly on top of the user's prior commit.
- [ ] Optionally write a short post-mortem in `docs/superpowers/notes/`.
