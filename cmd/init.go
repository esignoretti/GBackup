package cmd

import (
	"fmt"
	"os"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create an interactive configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := &config.Config{}

		fmt.Print("Google Workspace domain: ")
		fmt.Scanln(&cfg.Workspace.Domain)

		fmt.Print("Admin email: ")
		fmt.Scanln(&cfg.Workspace.AdminEmail)

		fmt.Print("Path to Google service account JSON key file: ")
		fmt.Scanln(&cfg.Workspace.ServiceAccountFile)

		fmt.Print("S3 bucket name: ")
		fmt.Scanln(&cfg.Storage.Bucket)

		fmt.Print("S3 region: ")
		fmt.Scanln(&cfg.Storage.Region)

		fmt.Print("S3 endpoint (leave empty for AWS): ")
		fmt.Scanln(&cfg.Storage.Endpoint)

		fmt.Print("S3 access key ID: ")
		fmt.Scanln(&cfg.Storage.AccessKeyID)

		fmt.Print("S3 secret access key: ")
		fmt.Scanln(&cfg.Storage.SecretAccessKey)

		cfg.Services = []string{"drive", "gmail", "calendar", "contacts"}
		cfg.Users.Include = "*"

		path := cfgFile
		if path == "" {
			home, _ := os.UserHomeDir()
			path = home + "/.gbackup/gbackup.yaml"
		}

		if err := config.Save(path, cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("Configuration saved to %s\n", path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
