package archive

import (
	"bytes"
	"testing"
)

func TestWriterStreamsEntries(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ArchiveEntry{Name: "b.txt", Data: []byte("world")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	if err := Read(bytes.NewReader(buf.Bytes()), func(e ArchiveEntry) error {
		seen[e.Name] = string(e.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen["a.txt"] != "hello" || seen["b.txt"] != "world" {
		t.Fatalf("unexpected entries: %v", seen)
	}
}

func TestWriterRejectsInvalidName(t *testing.T) {
	w := NewWriter(&bytes.Buffer{})
	if err := w.Append(ArchiveEntry{Name: "../escape.txt", Data: []byte("bad")}); err == nil {
		t.Fatal("expected error on traversal")
	}
}
