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
