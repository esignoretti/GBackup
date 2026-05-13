package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/esignoretti/gbackup/internal/backup"
	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var full bool

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Run a backup (full or incremental)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return fmt.Errorf("creating storage client: %w", err)
		}

		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		os.MkdirAll(dbDir, 0700)
		dbPath := filepath.Join(dbDir, "meta.db")
		db, err := metadata.New(dbPath)
		if err != nil {
			return fmt.Errorf("opening metadata db: %w", err)
		}
		defer db.Close()

		runner := backup.NewRunner(&backup.RunnerConfig{
			Config: cfg,
			Store:  store,
			MetaDB: db,
			DirAuth: &gws.AuthConfig{
				ServiceAccountFile: cfg.Workspace.ServiceAccountFile,
				AdminEmail:         cfg.Workspace.AdminEmail,
			},
		})

		ctx := context.Background()
		if err := runner.Run(ctx, full); err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}

		metaTS := time.Now().UTC().Format("2006-01-02T15-04-05") + ".db.gz"
		backupPath := filepath.Join(dbDir, metaTS)
		if err := metadata.BackupDB(dbPath, backupPath); err != nil {
			return fmt.Errorf("backing up metadata: %w", err)
		}
		defer os.Remove(backupPath)

		metaKey := storage.MetaKey(metaTS)
		f, err := os.Open(backupPath)
		if err != nil {
			return fmt.Errorf("opening metadata backup: %w", err)
		}
		defer f.Close()

		if err := store.Upload(ctx, metaKey, f); err != nil {
			return fmt.Errorf("uploading metadata: %w", err)
		}

		fmt.Println("Backup completed successfully.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
	backupCmd.Flags().BoolVar(&full, "full", false, "Force a full backup")
}
