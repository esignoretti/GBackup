package backup

import (
	"context"
	"fmt"
)

type ContactsBackup struct{}

func NewContactsBackup() *ContactsBackup { return &ContactsBackup{} }

func (c *ContactsBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	return 0, fmt.Errorf("contacts backup not yet implemented")
}
