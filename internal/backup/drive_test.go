package backup

import (
	"testing"
)

func TestNewDriveBackup(t *testing.T) {
	_, err := NewDriveBackup(&DriveBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}

func TestDriveMimeTypeCategory(t *testing.T) {
	tests := []struct {
		mime     string
		expected string
	}{
		{"application/vnd.google-apps.folder", "folder"},
		{"application/vnd.google-apps.document", "google-doc"},
		{"application/pdf", "binary"},
		{"image/png", "binary"},
	}
	for _, tc := range tests {
		got := mimeCategory(tc.mime)
		if got != tc.expected {
			t.Fatalf("for %s: expected %s, got %s", tc.mime, tc.expected, got)
		}
	}
}

func TestFilterSkippedUsers(t *testing.T) {
	users := []string{"a@c.com", "b@c.com", "c@c.com"}
	exclude := []string{"b@c.com"}
	result := filterUsers(users, exclude)
	if len(result) != 2 {
		t.Fatalf("expected 2 users, got %d", len(result))
	}
	if result[0] != "a@c.com" {
		t.Fatal("expected a@c.com first")
	}
}
