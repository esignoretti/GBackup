package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var metaSnapshotRE = regexp.MustCompile(`^_meta/(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2})\.db\.gz$`)

func pickLatestMeta(keys []string) (string, error) {
	var bestKey string
	var bestT time.Time
	for _, k := range keys {
		m := metaSnapshotRE.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		t, err := time.Parse("2006-01-02T15-04-05", m[1])
		if err != nil {
			continue
		}
		if bestKey == "" || t.After(bestT) {
			bestKey = k
			bestT = t
		}
	}
	if bestKey == "" {
		return "", fmt.Errorf("no metadata snapshot found in _meta/ matching pattern YYYY-MM-DDTHH-MM-SS.db.gz")
	}
	return bestKey, nil
}

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
		latest, err := pickLatestMeta(keys)
		if err != nil {
			return err
		}

		rc, err := store.Download(context.Background(), latest)
		if err != nil {
			return err
		}
		defer rc.Close()

		home, _ := os.UserHomeDir()
		dbDir := filepath.Join(home, ".gbackup")
		if err := os.MkdirAll(dbDir, 0700); err != nil {
			return fmt.Errorf("creating db dir: %w", err)
		}
		dbPath := filepath.Join(dbDir, "meta.db")

		tmpPath := filepath.Join(dbDir, "meta_restore.db.gz")
		tmp, err := os.Create(tmpPath)
		if err != nil {
			return err
		}
		if _, err := tmp.ReadFrom(rc); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			return err
		}
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
