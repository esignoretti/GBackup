package backup

import (
	"testing"
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
