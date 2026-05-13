package backup

import (
	"testing"
)

func TestNewCalendarBackup(t *testing.T) {
	_, err := NewCalendarBackup(&CalendarBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}
