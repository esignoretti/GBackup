package cmd

import (
	"context"
	"fmt"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var wipeForce bool

var wipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Delete all objects in the backup bucket",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return fmt.Errorf("creating storage client: %w", err)
		}

		keys, err := store.List(context.Background(), "")
		if err != nil {
			return fmt.Errorf("listing objects: %w", err)
		}

		if len(keys) == 0 {
			fmt.Println("Bucket is already empty.")
			return nil
		}

		fmt.Printf("Found %d objects in bucket %s\n", len(keys), cfg.Storage.Bucket)

		if !wipeForce {
			fmt.Print("Are you sure? This cannot be undone. Type 'yes' to confirm: ")
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "yes" {
				return fmt.Errorf("wipe cancelled")
			}
		}

		if err := store.DeleteObjects(context.Background(), keys); err != nil {
			return fmt.Errorf("deleting objects: %w", err)
		}

		fmt.Printf("Deleted %d objects from bucket %s\n", len(keys), cfg.Storage.Bucket)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(wipeCmd)
	wipeCmd.Flags().BoolVarP(&wipeForce, "force", "f", false, "Skip confirmation prompt")
}
