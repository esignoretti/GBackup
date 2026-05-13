package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show last backup state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		dbPath := os.Getenv("HOME") + "/.gbackup/meta.db"
		db, err := metadata.New(dbPath)
		if err != nil {
			return fmt.Errorf("opening metadata: %w", err)
		}
		defer db.Close()

		fmt.Printf("Domain: %s\n", cfg.Workspace.Domain)
		fmt.Printf("Bucket: %s (%s)\n", cfg.Storage.Bucket, cfg.Storage.Region)
		fmt.Printf("Services: %v\n", cfg.Services)
		fmt.Println()

		for _, svc := range cfg.Services {
			for _, user := range resolveStatusUsers(cfg) {
				t, err := db.LastBackupTime(svc, user)
				if err != nil {
					fmt.Printf("  %s/%s: never backed up\n", svc, user)
					continue
				}
				items, _ := db.ItemsByService(svc, user)
				fmt.Printf("  %s/%s: last backup %s (%d items)\n",
					svc, user, t.Format(time.RFC3339), len(items))
			}
		}
		return nil
	},
}

func resolveStatusUsers(cfg *config.Config) []string {
	if cfg.Users.Include == "*" {
		return []string{cfg.Workspace.AdminEmail}
	}
	return cfg.Users.Exclude
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
