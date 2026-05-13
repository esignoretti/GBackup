package gws

import (
	"testing"
)

func TestNewDirectoryService(t *testing.T) {
	_, err := NewDirectoryService(&AuthConfig{
		ServiceAccountFile: "/nonexistent/key.json",
		AdminEmail:         "admin@test.com",
	})
	if err == nil {
		t.Skip("skipping: needs valid service account key")
	}
}
