package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/restore"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var (
	restoreDate   string
	restoreDryRun bool
	restoreTarget string
)

var restoreCmd = &cobra.Command{
	Use:   "restore [service] [user]",
	Short: "Restore data from S3 backup",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		service := args[0]
		user := ""
		if len(args) > 1 {
			user = args[1]
		}

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		if user == "" {
			user = cfg.Workspace.AdminEmail
		}

		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}

		home, _ := os.UserHomeDir()
		dbPath := filepath.Join(home, ".gbackup", "meta.db")
		db, err := metadata.New(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		r, err := buildRestorer(service, cfg, store, db, user)
		if err != nil {
			return err
		}

		ctx := context.Background()

		// Always do a dry run first so the operator sees what would happen.
		r.SetDryRun(true)
		if err := r.Run(ctx); err != nil {
			return err
		}
		if restoreDryRun {
			return nil
		}

		if !confirm("Proceed with restore? [y/N]: ") {
			return fmt.Errorf("restore cancelled")
		}
		r.SetDryRun(false)
		return r.Run(ctx)
	},
}

func buildRestorer(service string, cfg *config.Config, store *storage.Client, db *metadata.DB, user string) (restore.Restorer, error) {
	switch service {
	case "drive":
		return &restore.DriveRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "contacts":
		return &restore.ContactsRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "calendar":
		return &restore.CalendarRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	case "gmail":
		return &restore.GmailRestore{
			Store:              store,
			MetaDB:             db,
			ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
			AdminEmail:         cfg.Workspace.AdminEmail,
			User:               user,
			Date:               restoreDate,
			TargetUser:         restoreTarget,
		}, nil
	default:
		return nil, fmt.Errorf("restore for %s not yet implemented", service)
	}
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(s.Text()))
	return ans == "y" || ans == "yes"
}

func init() {
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.Flags().StringVar(&restoreDate, "date", "latest", "Point-in-time date (YYYY-MM-DD)")
	restoreCmd.Flags().BoolVar(&restoreDryRun, "dry-run", false, "Preview without restoring")
	restoreCmd.Flags().StringVar(&restoreTarget, "target-user", "", "Restore to a different user")
}
