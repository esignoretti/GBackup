package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "gbackup",
	Short: "Google Workspace backup to S3-compatible storage",
	Long: `GBackup backs up Google Workspace (Drive, Gmail, Calendar, Contacts)
to any S3-compatible storage provider.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.gbackup/gbackup.yaml)")
}
