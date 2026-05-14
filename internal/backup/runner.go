package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
)

func isServiceDisabled(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SERVICE_DISABLED") ||
		strings.Contains(msg, "accessNotConfigured") ||
		strings.Contains(msg, "not been used in project")
}

type RunnerConfig struct {
	Config  *config.Config
	Store   *storage.Client
	MetaDB  *metadata.DB
	DirAuth *gws.AuthConfig
}

type Runner struct {
	cfg *RunnerConfig
}

func NewRunner(cfg *RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, full bool) error {
	if r.cfg == nil || r.cfg.Config == nil {
		return fmt.Errorf("runner not initialized: missing config")
	}
	if r.cfg.Store == nil {
		return fmt.Errorf("runner not initialized: missing storage client")
	}

	for _, svc := range r.cfg.Config.Services {
		if err := r.runService(ctx, svc, full); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runService(ctx context.Context, service string, full bool) error {
	users := r.resolveUsers(ctx)
	var err error
	switch service {
	case "drive":
		err = r.runDriveBackup(ctx, users, full)
	case "contacts":
		err = r.runContactsBackup(ctx, users, full)
	case "calendar":
		err = r.runCalendarBackup(ctx, users, full)
	case "gmail":
		err = r.runGmailBackup(ctx, users, full)
	}
	if err != nil && isServiceDisabled(err) {
		fmt.Printf("  %s: API not enabled in Google Cloud project, skipping\n", service)
		return nil
	}
	return err
}

func (r *Runner) runDriveBackup(ctx context.Context, users []string, full bool) error {
	dbCfg := &DriveBackupConfig{
		ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         r.cfg.DirAuth.AdminEmail,
	}
	b, err := NewDriveBackup(dbCfg)
	if err != nil {
		return err
	}
	b.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
	for _, user := range users {
		if _, err := b.BackupUser(ctx, user, full); err != nil {
			return fmt.Errorf("drive backup for %s: %w", user, err)
		}
	}
	return nil
}

func (r *Runner) runContactsBackup(ctx context.Context, users []string, full bool) error {
	cbCfg := &ContactsBackupConfig{
		ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         r.cfg.DirAuth.AdminEmail,
	}
	cb, err := NewContactsBackup(cbCfg)
	if err != nil {
		return err
	}
	cb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
	for _, user := range users {
		if _, err := cb.BackupUser(ctx, user, full); err != nil {
			return fmt.Errorf("contacts backup for %s: %w", user, err)
		}
	}
	return nil
}

func (r *Runner) runCalendarBackup(ctx context.Context, users []string, full bool) error {
	ccfg := &CalendarBackupConfig{
		ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         r.cfg.DirAuth.AdminEmail,
	}
	cb, err := NewCalendarBackup(ccfg)
	if err != nil {
		return err
	}
	cb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
	for _, user := range users {
		if _, err := cb.BackupUser(ctx, user, full); err != nil {
			return fmt.Errorf("calendar backup for %s: %w", user, err)
		}
	}
	return nil
}

func (r *Runner) runGmailBackup(ctx context.Context, users []string, full bool) error {
	gcfg := &GmailBackupConfig{
		ServiceAccountFile: r.cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         r.cfg.DirAuth.AdminEmail,
	}
	gb, err := NewGmailBackup(gcfg)
	if err != nil {
		return err
	}
	gb.WithMetaDB(r.cfg.MetaDB).WithStorage(r.cfg.Store)
	for _, user := range users {
		if _, err := gb.BackupUser(ctx, user, full); err != nil {
			return fmt.Errorf("gmail backup for %s: %w", user, err)
		}
	}
	return nil
}

func (r *Runner) resolveUsers(ctx context.Context) []string {
	cfg := r.cfg.Config
	if cfg.Users.Include == "*" {
		if r.cfg.DirAuth != nil {
			dirSvc, err := gws.NewDirectoryService(r.cfg.DirAuth)
			if err == nil {
				users, err := dirSvc.ListUsers(ctx)
				if err == nil {
					var emails []string
					for _, u := range users {
						if !u.IsSuspended {
							emails = append(emails, u.PrimaryEmail)
						}
					}
					if len(emails) > 0 {
						return emails
					}
				}
			}
		}
		return []string{cfg.Workspace.AdminEmail}
	}
	parts := strings.Split(cfg.Users.Include, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return filterUsers(parts, cfg.Users.Exclude)
}
