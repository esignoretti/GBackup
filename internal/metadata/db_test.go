package metadata

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewDB(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
}

func TestTrackAndGetItem(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	item := &Item{
		Service:    "drive",
		User:       "user@test.com",
		ObjectKey:  "drive/user@test.com/files/abc123",
		ItemID:     "abc123",
		Size:       1024,
		Checksum:   "sha256-hash",
		ModifiedAt: time.Now(),
	}
	if err := db.TrackItem(item); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetItem("drive", "user@test.com", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 1024 {
		t.Fatalf("expected size 1024, got %d", got.Size)
	}
}

func TestIsModified(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	item := &Item{
		Service:    "drive",
		User:       "u@t.com",
		ObjectKey:  "drive/u@t.com/files/x1",
		ItemID:     "x1",
		Size:       100,
		Checksum:   "oldhash",
		ModifiedAt: time.Now().Add(-1 * time.Hour),
	}
	db.TrackItem(item)

	// Same checksum — not modified
	modified, err := db.IsModified("drive", "u@t.com", "x1", "oldhash", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Fatal("expected not modified for same hash")
	}

	// Different checksum — modified
	modified, err = db.IsModified("drive", "u@t.com", "x1", "newhash", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("expected modified for different hash")
	}

	// Unknown item — always modified
	modified, err = db.IsModified("drive", "u@t.com", "unknown_id", "hash", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("expected modified for unknown item")
	}
}

func TestLastBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.LastBackupTime("drive", "u@t.com")
	if err == nil {
		t.Fatal("expected error when no backup exists")
	}

	db.RecordBackup("drive", "u@t.com", "full")
	_, err = db.LastBackupTime("drive", "u@t.com")
	if err != nil {
		t.Fatal(err)
	}
}

func TestItemsByService(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	items := []*Item{
		{Service: "drive", User: "u@t.com", ObjectKey: "k1", ItemID: "i1", ModifiedAt: time.Now()},
		{Service: "drive", User: "u@t.com", ObjectKey: "k2", ItemID: "i2", ModifiedAt: time.Now()},
		{Service: "gmail", User: "u@t.com", ObjectKey: "k3", ItemID: "i3", ModifiedAt: time.Now()},
	}
	for _, it := range items {
		db.TrackItem(it)
	}
	result, err := db.ItemsByService("drive", "u@t.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 drive items, got %d", len(result))
	}
}

func TestBackupAndRestoreDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "meta.db")
	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.TrackItem(&Item{Service: "drive", User: "u@t.com", ObjectKey: "k1", ItemID: "i1", ModifiedAt: time.Now()})
	db.Close()

	backupPath := filepath.Join(dir, "meta_backup.db.gz")
	if err := BackupDB(dbPath, backupPath); err != nil {
		t.Fatal(err)
	}

	restorePath := filepath.Join(dir, "meta_restored.db")
	if err := RestoreDB(backupPath, restorePath); err != nil {
		t.Fatal(err)
	}

	db2, err := New(restorePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	items, _ := db2.ItemsByService("drive", "u@t.com")
	if len(items) != 1 {
		t.Fatal("restored DB should contain items")
	}
}
