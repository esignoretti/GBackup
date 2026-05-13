package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Workspace WorkspaceConfig `yaml:"workspace"`
	Storage   StorageConfig   `yaml:"storage"`
	Schedule  ScheduleConfig  `yaml:"schedule"`
	Services  []string        `yaml:"services"`
	Users     UsersConfig     `yaml:"users"`
}

type WorkspaceConfig struct {
	Domain     string `yaml:"domain"`
	AdminEmail string `yaml:"admin_email"`
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
	IncrementalDays int  `yaml:"incremental_days"`
	FullBackups     int  `yaml:"full_backups"`
	AutoPrune       bool `yaml:"auto_prune"`
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
	if c.Workspace.Domain == "" {
		return fmt.Errorf("workspace.domain is required")
	}
	if c.Workspace.AdminEmail == "" {
		return fmt.Errorf("workspace.admin_email is required")
	}
	if c.Storage.Bucket == "" {
		return fmt.Errorf("storage.bucket is required")
	}
	if c.Storage.Region == "" {
		return fmt.Errorf("storage.region is required")
	}
	if c.Storage.AccessKeyID == "" {
		return fmt.Errorf("storage.access_key_id is required")
	}
	if c.Storage.SecretAccessKey == "" {
		return fmt.Errorf("storage.secret_access_key is required")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("services: at least one service required")
	}
	return nil
}

func Save(path string, cfg *Config) error {
	if path == "" {
		path = defaultConfigPath()
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
