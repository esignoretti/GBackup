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
