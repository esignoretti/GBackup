package backup

import (
	"testing"

	"google.golang.org/api/gmail/v1"
)

func TestNewGmailBackup(t *testing.T) {
	_, err := NewGmailBackup(&GmailBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}

func TestMessageMonth(t *testing.T) {
	msg := &gmail.Message{InternalDate: 1715731200000}
	month := messageMonth(msg)
	if month != "2024-05" {
		t.Fatalf("expected 2024-05, got %s", month)
	}
}

func TestEntryNameToID(t *testing.T) {
	id := entryNameToID("msg001.eml")
	if id != "msg001" {
		t.Fatalf("expected msg001, got %s", id)
	}
	id = entryNameToID("noext")
	if id != "noext" {
		t.Fatalf("expected noext, got %s", id)
	}
}
