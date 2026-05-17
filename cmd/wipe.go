package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/spf13/cobra"
)

var (
	wipeForce          bool
	wipeIncludeForeign bool
)

var gbackupPrefixes = []string{"_meta/", "drive/", "gmail/", "calendar/", "contacts/"}

func splitGBackupKeys(keys []string) (owned, foreign []string) {
	for _, k := range keys {
		matched := false
		for _, p := range gbackupPrefixes {
			if strings.HasPrefix(k, p) {
				matched = true
				break
			}
		}
		if matched {
			owned = append(owned, k)
		} else {
			foreign = append(foreign, k)
		}
	}
	return
}

var wipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Delete gbackup-owned objects in the backup bucket",
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

		owned, foreign := splitGBackupKeys(keys)

		if len(owned) == 0 && len(foreign) == 0 {
			fmt.Println("Bucket is already empty.")
			return nil
		}

		fmt.Printf("Found %d gbackup-owned objects in bucket %s\n", len(owned), cfg.Storage.Bucket)
		if len(foreign) > 0 {
			fmt.Printf("Found %d foreign (non-gbackup) objects:\n", len(foreign))
			limit := 10
			if limit > len(foreign) {
				limit = len(foreign)
			}
			for _, k := range foreign[:limit] {
				fmt.Printf("  %s\n", k)
			}
			if len(foreign) > limit {
				fmt.Printf("  ... and %d more\n", len(foreign)-limit)
			}
			if !wipeIncludeForeign {
				fmt.Println("Refusing to wipe foreign objects. Re-run with --include-foreign to delete them too.")
			}
		}

		toDelete := owned
		if wipeIncludeForeign {
			toDelete = append(toDelete, foreign...)
		}
		if len(toDelete) == 0 {
			return nil
		}

		if !wipeForce {
			fmt.Printf("About to delete %d objects. Type 'yes' to confirm: ", len(toDelete))
			if !confirmExact("yes") {
				return fmt.Errorf("wipe cancelled")
			}
		}

		if err := store.DeleteObjects(context.Background(), toDelete); err != nil {
			return fmt.Errorf("deleting objects: %w", err)
		}

		fmt.Printf("Deleted %d objects from bucket %s\n", len(toDelete), cfg.Storage.Bucket)
		return nil
	},
}

func confirmExact(want string) bool {
	var got string
	fmt.Scanln(&got)
	return strings.TrimSpace(got) == want
}

func init() {
	rootCmd.AddCommand(wipeCmd)
	wipeCmd.Flags().BoolVarP(&wipeForce, "force", "f", false, "Skip confirmation prompt")
	wipeCmd.Flags().BoolVar(&wipeIncludeForeign, "include-foreign", false, "Also delete objects outside of gbackup prefixes")
}
