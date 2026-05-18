package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:           "gbackup",
	Short:         "Google Workspace backup to S3-compatible storage",
	Long:          `GBackup backs up Google Workspace (Drive, Gmail, Calendar, Contacts) to any S3-compatible storage provider.`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

// formatError unwraps a chain of fmt.Errorf("X: %w", err) calls and prints
// each layer on its own indented line, so:
//
//	"backup failed: loading config: reading config: open /h/x: no such file or directory"
//
// becomes:
//
//	backup failed
//	  → loading config
//	  → reading config
//	  → open /h/x: no such file or directory
func formatError(err error) string {
	if err == nil {
		return ""
	}
	var lines []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		msg := e.Error()
		if u := errors.Unwrap(e); u != nil {
			msg = strings.TrimSuffix(msg, ": "+u.Error())
		}
		lines = append(lines, msg)
	}
	return strings.Join(lines, "\n  → ")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, formatError(err))
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.gbackup/gbackup.yaml)")
}
