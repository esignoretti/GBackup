package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultConfig(t *testing.T) {
	home, _ := os.UserHomeDir()
	expectedPath := filepath.Join(home, ".gbackup", "gbackup.yaml")
	if defaultConfigPath() != expectedPath {
		t.Fatalf("expected %s, got %s", expectedPath, defaultConfigPath())
	}
}

func TestLoadConfigFromPath(t *testing.T) {
	content := []byte(`
workspace:
  domain: "test.com"
  admin_email: "admin@test.com"
  service_account_file: "/path/to/key.json"
storage:
  bucket: "test-bucket"
  region: "us-east-1"
  access_key_id: "AKID"
  secret_access_key: "secret"
services:
  - drive
`)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, content, 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace.Domain != "test.com" {
		t.Fatalf("expected test.com, got %s", cfg.Workspace.Domain)
	}
	if cfg.Storage.Bucket != "test-bucket" {
		t.Fatalf("expected test-bucket, got %s", cfg.Storage.Bucket)
	}
	if len(cfg.Services) != 1 || cfg.Services[0] != "drive" {
		t.Fatalf("expected [drive], got %v", cfg.Services)
	}
}

func TestConfigValidateMissingBucket(t *testing.T) {
	cfg := &Config{
		Workspace: WorkspaceConfig{Domain: "d", AdminEmail: "a@d", ServiceAccountFile: "/k.json"},
		Storage:   StorageConfig{Region: "us-east-1"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfg := &Config{
		Workspace: WorkspaceConfig{Domain: "test.com", AdminEmail: "admin@test.com", ServiceAccountFile: "/k.json"},
		Storage: StorageConfig{
			Bucket:          "b",
			Region:          "us-east-1",
			AccessKeyID:     "ak",
			SecretAccessKey: "sk",
		},
		Services: []string{"drive"},
		Users:    UsersConfig{Include: "*"},
	}
	if err := Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspace.Domain != "test.com" {
		t.Fatal("round-trip failed")
	}
}
