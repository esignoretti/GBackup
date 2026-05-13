package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var metaBackupCmd = &cobra.Command{
	Use:   "meta-backup",
	Short: "Manually backup SQLite metadata to S3",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}
		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		dbPath := filepath.Join(dbDir, "meta.db")
		backupName := time.Now().UTC().Format("2006-01-02T15-04-05") + ".db.gz"
		backupPath := filepath.Join(dbDir, backupName)

		if err := metadata.BackupDB(dbPath, backupPath); err != nil {
			return fmt.Errorf("backing up metadata: %w", err)
		}
		defer os.Remove(backupPath)

		f, err := os.Open(backupPath)
		if err != nil {
			return err
		}
		defer f.Close()

		key := storage.MetaKey(backupName)
		if err := store.Upload(context.Background(), key, f); err != nil {
			return fmt.Errorf("uploading metadata: %w", err)
		}
		fmt.Printf("Metadata backed up to s3://%s/%s\n", cfg.Storage.Bucket, key)
		return nil
	},
}

var metaRestoreCmd = &cobra.Command{
	Use:   "meta-restore",
	Short: "Restore SQLite metadata from S3",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}

		keys, err := store.List(context.Background(), "_meta/")
		if err != nil {
			return fmt.Errorf("listing metadata: %w", err)
		}
		if len(keys) == 0 {
			return fmt.Errorf("no metadata snapshots found in S3")
		}
		latest := keys[len(keys)-1]

		rc, err := store.Download(context.Background(), latest)
		if err != nil {
			return err
		}
		defer rc.Close()

		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		os.MkdirAll(dbDir, 0700)
		dbPath := filepath.Join(dbDir, "meta.db")

		tmpPath := filepath.Join(dbDir, "meta_restore.db.gz")
		tmp, err := os.Create(tmpPath)
		if err != nil {
			return err
		}
		if _, err := tmp.ReadFrom(rc); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		defer os.Remove(tmpPath)

		if err := metadata.RestoreDB(tmpPath, dbPath); err != nil {
			return fmt.Errorf("restoring metadata: %w", err)
		}
		fmt.Printf("Metadata restored from %s\n", latest)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(metaBackupCmd)
	rootCmd.AddCommand(metaRestoreCmd)
}
