package archive

import (
	"bytes"
	"errors"
	"testing"
)

func TestStreamMergeAppendsAndOverwrites(t *testing.T) {
	var existing bytes.Buffer
	w := NewWriter(&existing)
	_ = w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("old-a")})
	_ = w.Append(ArchiveEntry{Name: "b.txt", Data: []byte("old-b")})
	w.Close()

	newEntries := []ArchiveEntry{
		{Name: "a.txt", Data: []byte("new-a")},
		{Name: "c.txt", Data: []byte("new-c")},
	}
	var merged bytes.Buffer
	if err := StreamMerge(bytes.NewReader(existing.Bytes()), &merged, newEntries); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	if err := Read(bytes.NewReader(merged.Bytes()), func(e ArchiveEntry) error {
		got[e.Name] = string(e.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a.txt": "new-a", "b.txt": "old-b", "c.txt": "new-c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestStreamMergeNoChangesReturnsSentinel(t *testing.T) {
	var existing bytes.Buffer
	w := NewWriter(&existing)
	_ = w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("same")})
	w.Close()

	err := StreamMerge(bytes.NewReader(existing.Bytes()), &bytes.Buffer{}, []ArchiveEntry{
		{Name: "a.txt", Data: []byte("same")},
	})
	if !errors.Is(err, ErrNoChanges) {
		t.Fatalf("expected ErrNoChanges, got %v", err)
	}
}
