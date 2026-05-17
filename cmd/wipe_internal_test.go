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
