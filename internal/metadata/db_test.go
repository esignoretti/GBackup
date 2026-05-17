package metadata

import (
	"os"
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
		ItemPath:   "",
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
		ItemPath:   "",
		ItemID:     "x1",
		Size:       100,
		Checksum:   "oldhash",
		ModifiedAt: time.Now().Add(-1 * time.Hour),
	}
	db.TrackItem(item)

	// Same checksum — not modified
	modified, err := db.IsModified("drive", "u@t.com", "x1", "oldhash")
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Fatal("expected not modified for same hash")
	}

	// Different checksum — modified
	modified, err = db.IsModified("drive", "u@t.com", "x1", "newhash")
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("expected modified for different hash")
	}

	// Unknown item — always modified
	modified, err = db.IsModified("drive", "u@t.com", "unknown_id", "hash")
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
		{Service: "drive", User: "u@t.com", ObjectKey: "k1", ItemPath: "", ItemID: "i1", ModifiedAt: time.Now()},
		{Service: "drive", User: "u@t.com", ObjectKey: "k2", ItemPath: "", ItemID: "i2", ModifiedAt: time.Now()},
		{Service: "gmail", User: "u@t.com", ObjectKey: "k3", ItemPath: "", ItemID: "i3", ModifiedAt: time.Now()},
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
	db.TrackItem(&Item{Service: "drive", User: "u@t.com", ObjectKey: "k1", ItemPath: "", ItemID: "i1", ModifiedAt: time.Now()})
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

func TestRestoreDBAtomicAndBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "meta.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TrackItem(&Item{Service: "drive", User: "old@t.com", ObjectKey: "k_old", ItemID: "i_old", ModifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db.Close()

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

	if _, err := os.Stat(dbPath + ".bak"); err != nil {
		t.Fatalf("expected backup at %s.bak: %v", dbPath, err)
	}

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

	bad := filepath.Join(dir, "bad.db.gz")
	if err := os.WriteFile(bad, []byte("not-a-real-gzip"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := RestoreDB(bad, dbPath); err == nil {
		t.Fatal("expected error on bad gzip")
	}

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
