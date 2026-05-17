package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Workspace WorkspaceConfig `yaml:"workspace"`
	Storage   StorageConfig   `yaml:"storage"`
	Schedule  ScheduleConfig  `yaml:"schedule"`
	Services  []string        `yaml:"services"`
	Users     UsersConfig     `yaml:"users"`
	Retention RetentionConfig `yaml:"retention"`
}

type WorkspaceConfig struct {
	Domain            string `yaml:"domain"`
	AdminEmail        string `yaml:"admin_email"`
	ServiceAccountFile string `yaml:"service_account_file"`
}

type StorageConfig struct {
	Bucket          string `yaml:"bucket"`
	Region          string `yaml:"region"`
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
}

type ScheduleConfig struct {
	Incremental string          `yaml:"incremental"`
	Full        string          `yaml:"full"`
	Retention   RetentionConfig `yaml:"retention"`
}

type RetentionConfig struct {
	IncrementalDays int               `yaml:"incremental_days"`
	FullBackups     int               `yaml:"full_backups"`
	AutoPrune       bool              `yaml:"auto_prune"`
	MaxAge          map[string]string `yaml:"max_age"` // service -> duration string like "2y", "6mo", "30d"
}

// ParseDuration parses a simplified duration string like "30d", "6mo", "2y".
// Returns 0 for empty or "0" input.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if len(s) >= 2 && s[len(s)-2:] == "mo" {
		numStr := s[:len(s)-2]
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'y':
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: expected d, mo, or y", s)
	}
}

// MaxAgeFor returns the max age duration for a given service name.
// Returns 0 if no retention is configured for the service.
func (r RetentionConfig) MaxAgeFor(service string) time.Duration {
	if r.MaxAge == nil {
		return 0
	}
	s, ok := r.MaxAge[service]
	if !ok || s == "" {
		return 0
	}
	d, err := ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

type UsersConfig struct {
	Include string   `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gbackup", "gbackup.yaml")
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	if c.Workspace.Domain == "" {
		errs = append(errs, fmt.Errorf("workspace.domain is required"))
	}
	if c.Workspace.AdminEmail == "" {
		errs = append(errs, fmt.Errorf("workspace.admin_email is required"))
	}
	if c.Workspace.ServiceAccountFile == "" {
		errs = append(errs, fmt.Errorf("workspace.service_account_file is required"))
	}
	if c.Storage.Bucket == "" {
		errs = append(errs, fmt.Errorf("storage.bucket is required"))
	}
	if c.Storage.Region == "" {
		errs = append(errs, fmt.Errorf("storage.region is required"))
	}
	if (c.Storage.AccessKeyID == "") != (c.Storage.SecretAccessKey == "") {
		errs = append(errs, fmt.Errorf("storage: provide both access_key_id and secret_access_key, or neither (to use the AWS default credential chain)"))
	}
	if len(c.Services) == 0 {
		errs = append(errs, fmt.Errorf("services: at least one service required"))
	}
	return errors.Join(errs...)
}

func Save(path string, cfg *Config) error {
	if path == "" {
		path = defaultConfigPath()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}
