package restore

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/esignoretti/gbackup/internal/metadata"
)

func setupTestDB(t *testing.T) (*metadata.DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := metadata.New(path)
	if err != nil {
		t.Fatal(err)
	}
	return db, dir
}

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

func gzipJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestContactsRestore_DryRun(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	if err := db.TrackItem(&metadata.Item{
		Service:    "contacts",
		User:       "test@example.com",
		ObjectKey:  "contacts/test@example.com/contact1_v1.json.gz",
		ItemID:     "contact1",
		Size:       123,
		Checksum:   "abc",
		ModifiedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	r := &ContactsRestore{
		Store:              nil,
		MetaDB:             db,
		ServiceAccountFile: "nonexistent.json",
		AdminEmail:         "admin@example.com",
		User:               "test@example.com",
		Date:               "latest",
		DryRun:             true,
	}

	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("dry run should not fail: %v", err)
	}
}

func TestContactsRestore_DryRunNoItems(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	r := &ContactsRestore{
		Store:              nil,
		MetaDB:             db,
		ServiceAccountFile: "nonexistent.json",
		AdminEmail:         "admin@example.com",
		User:               "test@example.com",
		Date:               "latest",
		DryRun:             true,
	}

	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("dry run with no items should not fail: %v", err)
	}
}
