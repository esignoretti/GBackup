package gws

import (
	"testing"
)

func TestServiceName(t *testing.T) {
	if driveScope != "https://www.googleapis.com/auth/drive.readonly" {
		t.Fatal("unexpected drive scope")
	}
}

func TestScopesForServiceDrive(t *testing.T) {
	scopes := ScopesForService("drive")
	if len(scopes) == 0 {
		t.Fatal("expected at least 1 scope for drive")
	}
}

func TestScopesForServiceAll(t *testing.T) {
	all := AllScopes()
	if len(all) == 0 {
		t.Fatal("expected non-empty scopes")
	}
}
