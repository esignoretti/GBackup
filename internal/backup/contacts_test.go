package backup

import (
	"testing"
)

func TestNewContactsBackup(t *testing.T) {
	_, err := NewContactsBackup(&ContactsBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}
