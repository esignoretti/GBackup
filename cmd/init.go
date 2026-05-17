package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func readLine(scanner *bufio.Scanner, prompt string) string {
	fmt.Print(prompt)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create an interactive configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := &config.Config{}
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 1024), 1024*1024)

		cfg.Workspace.Domain = readLine(scanner, "Google Workspace domain: ")
		cfg.Workspace.AdminEmail = readLine(scanner, "Admin email: ")
		cfg.Workspace.ServiceAccountFile = readLine(scanner, "Path to Google service account JSON key file: ")
		cfg.Storage.Bucket = readLine(scanner, "S3 bucket name: ")
		cfg.Storage.Region = readLine(scanner, "S3 region: ")
		cfg.Storage.Endpoint = readLine(scanner, "S3 endpoint (leave empty for AWS): ")
		cfg.Storage.AccessKeyID = readLine(scanner, "S3 access key ID (empty to use AWS default chain): ")
		if cfg.Storage.AccessKeyID != "" {
			s, err := readSecret("S3 secret access key: ")
			if err != nil {
				return fmt.Errorf("reading secret: %w", err)
			}
			cfg.Storage.SecretAccessKey = s
		}

		cfg.Services = []string{"drive", "gmail", "calendar", "contacts"}
		cfg.Users.Include = "*"

		path := cfgFile
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".gbackup", "gbackup.yaml")
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
