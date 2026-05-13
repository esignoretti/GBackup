package backup

import (
	"context"
	"fmt"
)

type CalendarBackup struct{}

func NewCalendarBackup() *CalendarBackup { return &CalendarBackup{} }

func (c *CalendarBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	return 0, fmt.Errorf("calendar backup not yet implemented")
}
