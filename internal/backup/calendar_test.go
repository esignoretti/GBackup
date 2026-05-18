package backup

import (
	"testing"
	"time"

	"google.golang.org/api/calendar/v3"
)

func TestNewCalendarBackup(t *testing.T) {
	_, err := NewCalendarBackup(&CalendarBackupConfig{
		ServiceAccountFile: "/nonexistent/key.json",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}

func TestEventYear(t *testing.T) {
	tests := []struct {
		event    *calendar.Event
		expected string
	}{
		{event: &calendar.Event{Start: &calendar.EventDateTime{Date: "2025-06-15"}}, expected: "2025"},
		{event: &calendar.Event{Start: &calendar.EventDateTime{DateTime: "2026-12-25T10:00:00Z"}}, expected: "2026"},
		{event: &calendar.Event{}, expected: time.Now().Format("2006")},
	}
	for _, tc := range tests {
		got := eventYear(tc.event)
		if got != tc.expected {
			t.Fatalf("eventYear(%+v) = %s, want %s", tc.event, got, tc.expected)
		}
	}
}
