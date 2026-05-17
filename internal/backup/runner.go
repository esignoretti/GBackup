package backup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
	"golang.org/x/sync/errgroup"
)

const perServiceUserConcurrency = 5

type RunnerConfig struct {
	Config   *config.Config
	Store    *storage.Client
	MetaDB   *metadata.DB
	DirAuth  *gws.AuthConfig
	Progress *progress.Reporter
}

type Runner struct {
	cfg *RunnerConfig
}

func NewRunner(cfg *RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

type ServiceResult struct {
	Skipped    bool
	SkipReason string
	Err        error
}

type RunResult struct {
	Services map[string]ServiceResult
}

// AnyRan reports whether at least one service ran to completion without error.
func (r RunResult) AnyRan() bool {
	for _, s := range r.Services {
		if !s.Skipped && s.Err == nil {
			return true
		}
	}
	return false
}

// FirstError returns the first non-nil per-service error, if any.
func (r RunResult) FirstError() error {
	for _, s := range r.Services {
		if s.Err != nil {
			return s.Err
		}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, full bool) (RunResult, error) {
	result := RunResult{Services: map[string]ServiceResult{}}
	if r.cfg == nil || r.cfg.Config == nil {
		return result, fmt.Errorf("runner not initialized: missing config")
	}
	if r.cfg.Store == nil {
		return result, fmt.Errorf("runner not initialized: missing storage client")
	}

	users := r.resolveUsers(ctx)

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, svc := range r.cfg.Config.Services {
		svc := svc
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.runService(ctx, svc, users, full)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && gws.IsServiceDisabled(err) {
				result.Services[svc] = ServiceResult{Skipped: true, SkipReason: "API not enabled"}
				fmt.Fprintf(os.Stderr, "  %s: API not enabled in Google Cloud project, skipping\n", svc)
				return
			}
			if err != nil {
				result.Services[svc] = ServiceResult{Err: fmt.Errorf("%s: %w", svc, err)}
				return
			}
			result.Services[svc] = ServiceResult{}
		}()
	}
	wg.Wait()
	return result, result.FirstError()
}

func (r *Runner) runService(ctx context.Context, service string, users []string, full bool) error {
	var fn func(context.Context, []string, bool) error
	switch service {
	case "drive":
		fn = r.runDriveBackup
	case "contacts":
		fn = r.runContactsBackup
	case "calendar":
		fn = r.runCalendarBackup
	case "gmail":
		fn = r.runGmailBackup
	default:
		return fmt.Errorf("unknown service: %s", service)
	}
	return fn(ctx, users, full)
}

func (r *Runner) runDriveBackup(ctx context.Context, users []string, full bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(perServiceUserConcurrency)
	for _, user := range users {
		user := user
		g.Go(func() error {
			b, err := newDriveBackup(r.cfg)
			if err != nil {
				return err
			}
			_, err = b.BackupUser(ctx, user, full)
			if err != nil {
				return fmt.Errorf("drive backup for %s: %w", user, err)
			}
			return nil
		})
	}
	return g.Wait()
}

func (r *Runner) runContactsBackup(ctx context.Context, users []string, full bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(perServiceUserConcurrency)
	for _, user := range users {
		user := user
		g.Go(func() error {
			cb, err := newContactsBackup(r.cfg)
			if err != nil {
				return err
			}
			_, err = cb.BackupUser(ctx, user, full)
			if err != nil {
				return fmt.Errorf("contacts backup for %s: %w", user, err)
			}
			return nil
		})
	}
	return g.Wait()
}

func (r *Runner) runCalendarBackup(ctx context.Context, users []string, full bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(perServiceUserConcurrency)
	for _, user := range users {
		user := user
		g.Go(func() error {
			cb, err := newCalendarBackup(r.cfg)
			if err != nil {
				return err
			}
			_, err = cb.BackupUser(ctx, user, full)
			if err != nil {
				return fmt.Errorf("calendar backup for %s: %w", user, err)
			}
			return nil
		})
	}
	return g.Wait()
}

func (r *Runner) runGmailBackup(ctx context.Context, users []string, full bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(perServiceUserConcurrency)
	for _, user := range users {
		user := user
		g.Go(func() error {
			gb, err := newGmailBackup(r.cfg)
			if err != nil {
				return err
			}
			_, err = gb.BackupUser(ctx, user, full)
			if err != nil {
				return fmt.Errorf("gmail backup for %s: %w", user, err)
			}
			return nil
		})
	}
	return g.Wait()
}

func newDriveBackup(cfg *RunnerConfig) (*DriveBackup, error) {
	b, err := NewDriveBackup(&DriveBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	b.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		b.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("drive"); maxAge > 0 {
		b.WithMaxAge(maxAge)
	}
	return b, nil
}

func newContactsBackup(cfg *RunnerConfig) (*ContactsBackup, error) {
	cb, err := NewContactsBackup(&ContactsBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	cb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		cb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("contacts"); maxAge > 0 {
		cb.WithMaxAge(maxAge)
	}
	return cb, nil
}

func newCalendarBackup(cfg *RunnerConfig) (*CalendarBackup, error) {
	cb, err := NewCalendarBackup(&CalendarBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	cb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		cb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("calendar"); maxAge > 0 {
		cb.WithMaxAge(maxAge)
	}
	return cb, nil
}

func newGmailBackup(cfg *RunnerConfig) (*GmailBackup, error) {
	gb, err := NewGmailBackup(&GmailBackupConfig{
		ServiceAccountFile: cfg.DirAuth.ServiceAccountFile,
		AdminEmail:         cfg.DirAuth.AdminEmail,
	})
	if err != nil {
		return nil, err
	}
	gb.WithMetaDB(cfg.MetaDB).WithStorage(cfg.Store)
	if cfg.Progress != nil {
		gb.WithProgress(cfg.Progress)
	}
	if maxAge := cfg.Config.Retention.MaxAgeFor("gmail"); maxAge > 0 {
		gb.WithMaxAge(maxAge)
	}
	return gb, nil
}

func (r *Runner) resolveUsers(ctx context.Context) []string {
	cfg := r.cfg.Config
	if cfg.Users.Include == "*" {
		if r.cfg.DirAuth != nil {
			dirSvc, err := gws.NewDirectoryService(r.cfg.DirAuth)
			if err != nil {
				fmt.Printf("  directory API unavailable (falling back to admin email): %v\n", err)
			} else {
				users, err := dirSvc.ListUsers(ctx)
				if err != nil {
					fmt.Printf("  listing users failed (falling back to admin email): %v\n", err)
				} else {
					var emails []string
					for _, u := range users {
						if !u.IsSuspended {
							emails = append(emails, u.PrimaryEmail)
						}
					}
					if len(emails) > 0 {
						return emails
					}
					fmt.Println("  directory API returned no users (falling back to admin email)")
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
