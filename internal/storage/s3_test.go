package storage

import (
	"strings"
	"testing"

	"github.com/esignoretti/gbackup/internal/config"
)

func TestNewClient(t *testing.T) {
	cfg := &config.StorageConfig{
		Bucket:          "test",
		Region:          "us-east-1",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestObjectKey(t *testing.T) {
	key := ObjectKey("drive", "user@test.com", "files/abc123")
	expected := "drive/user@test.com/files/abc123"
	if key != expected {
		t.Fatalf("expected %s, got %s", expected, key)
	}
}

func TestMetaKey(t *testing.T) {
	key := MetaKey("2026-05-13T10-00-00.db.gz")
	expected := "_meta/2026-05-13T10-00-00.db.gz"
	if key != expected {
		t.Fatalf("expected %s, got %s", expected, key)
	}
}

func TestParseMetaKey(t *testing.T) {
	name := ParseMetaKey("_meta/2026-05-13T10-00-00.db.gz")
	if name != "2026-05-13T10-00-00.db.gz" {
		t.Fatalf("expected 2026-05-13T10-00-00.db.gz, got %s", name)
	}
	name = ParseMetaKey("drive/user/file")
	if name != "" {
		t.Fatalf("expected empty, got %s", name)
	}
}

func TestServicePrefix(t *testing.T) {
	p := ServicePrefix("drive", "user@test.com")
	if !strings.HasPrefix(p, "drive/user@test.com") {
		t.Fatalf("unexpected prefix: %s", p)
	}
}
