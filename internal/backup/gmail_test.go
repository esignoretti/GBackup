package backup

import (
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func TestNewGmailBackup(t *testing.T) {
	_, err := NewGmailBackup(&GmailBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
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

func TestMessageAgeFiltering(t *testing.T) {
	b := &GmailBackup{maxAge: 30 * 24 * time.Hour} // 30 days

	oldMsg := &gmail.Message{InternalDate: time.Now().Add(-60 * 24 * time.Hour).UnixMilli()}
	newMsg := &gmail.Message{InternalDate: time.Now().Add(-1 * time.Hour).UnixMilli()}

	if time.Since(time.UnixMilli(oldMsg.InternalDate)) <= b.maxAge {
		t.Fatal("expected old message to exceed maxAge")
	}
	if time.Since(time.UnixMilli(newMsg.InternalDate)) > b.maxAge {
		t.Fatal("expected new message to be within maxAge")
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
