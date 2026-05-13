package backup

import (
	"context"
	"fmt"
)

type GmailBackup struct{}

func NewGmailBackup() *GmailBackup { return &GmailBackup{} }

func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	return 0, fmt.Errorf("gmail backup not yet implemented")
}
