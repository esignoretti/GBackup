package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

		switch service {
		case "drive":
			r := &restore.DriveRestore{
				Store:              store,
				MetaDB:             db,
				ServiceAccountFile: cfg.Workspace.AdminEmail + ".json",
				AdminEmail:         cfg.Workspace.AdminEmail,
				User:               user,
				Date:               restoreDate,
				DryRun:             true,
				TargetUser:         restoreTarget,
			}
			if err := r.Run(context.Background()); err != nil {
				return err
			}

			if !restoreDryRun {
				fmt.Print("Proceed with restore? [y/N]: ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					return fmt.Errorf("restore cancelled")
				}
				r.DryRun = false
				if err := r.Run(context.Background()); err != nil {
					return err
				}
			}
		case "contacts":
			r := &restore.ContactsRestore{
				Store:              store,
				MetaDB:             db,
				ServiceAccountFile: cfg.Workspace.AdminEmail + ".json",
				AdminEmail:         cfg.Workspace.AdminEmail,
				User:               user,
				Date:               restoreDate,
				DryRun:             true,
				TargetUser:         restoreTarget,
			}
			if err := r.Run(context.Background()); err != nil {
				return err
			}

			if !restoreDryRun {
				fmt.Print("Proceed with restore? [y/N]: ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					return fmt.Errorf("restore cancelled")
				}
				r.DryRun = false
				if err := r.Run(context.Background()); err != nil {
					return err
				}
			}
		case "calendar":
			r := &restore.CalendarRestore{
				Store:              store,
				MetaDB:             db,
				ServiceAccountFile: cfg.Workspace.AdminEmail + ".json",
				AdminEmail:         cfg.Workspace.AdminEmail,
				User:               user,
				Date:               restoreDate,
				DryRun:             true,
				TargetUser:         restoreTarget,
			}
			if err := r.Run(context.Background()); err != nil {
				return err
			}
			if !restoreDryRun {
				fmt.Print("Proceed with restore? [y/N]: ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					return fmt.Errorf("restore cancelled")
				}
				r.DryRun = false
				if err := r.Run(context.Background()); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("restore for %s not yet implemented", service)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.Flags().StringVar(&restoreDate, "date", "latest", "Point-in-time date (YYYY-MM-DD)")
	restoreCmd.Flags().BoolVar(&restoreDryRun, "dry-run", false, "Preview without restoring")
	restoreCmd.Flags().StringVar(&restoreTarget, "target-user", "", "Restore to a different user")
}
