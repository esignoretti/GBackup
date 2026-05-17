package archive

import (
	"bytes"
	"strings"
	"testing"
)

func TestCreateAndRead(t *testing.T) {
	entries := []ArchiveEntry{
		{Name: "a.txt", Data: []byte("hello")},
		{Name: "b.txt", Data: []byte("world")},
	}

	data, err := Create(entries)
	if err != nil {
		t.Fatal(err)
	}

	var seen []ArchiveEntry
	err = Read(bytes.NewReader(data), func(e ArchiveEntry) error {
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
	data, err := Create(original)
	if err != nil {
		t.Fatal(err)
	}

	newEntries := []ArchiveEntry{
		{Name: "b.txt", Data: []byte("appended")},
	}

	combined, err := AppendToArchive(bytes.NewReader(data), newEntries)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	err = Read(bytes.NewReader(combined), func(e ArchiveEntry) error {
		seen[e.Name] = string(e.Data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 entries after append, got %d", len(seen))
	}
	if seen["a.txt"] != "original" {
		t.Fatalf("a.txt has wrong data: %q", seen["a.txt"])
	}
	if seen["b.txt"] != "appended" {
		t.Fatalf("b.txt has wrong data: %q", seen["b.txt"])
	}
}

func TestAppendToArchive_DuplicateReplaces(t *testing.T) {
	original := []ArchiveEntry{
		{Name: "a.txt", Data: []byte("old")},
	}
	data, err := Create(original)
	if err != nil {
		t.Fatal(err)
	}

	newEntries := []ArchiveEntry{
		{Name: "a.txt", Data: []byte("new")},
	}

	combined, err := AppendToArchive(bytes.NewReader(data), newEntries)
	if err != nil {
		t.Fatal(err)
	}

	var entries []ArchiveEntry
	err = Read(bytes.NewReader(combined), func(e ArchiveEntry) error {
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (replaced), got %d", len(entries))
	}
	if string(entries[0].Data) != "new" {
		t.Fatalf("expected 'new', got '%s'", string(entries[0].Data))
	}
}

func TestCreate_Empty(t *testing.T) {
	data, err := Create([]ArchiveEntry{})
	if err != nil {
		t.Fatal(err)
	}
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
