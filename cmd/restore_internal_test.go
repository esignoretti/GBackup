package cmd

import (
	"testing"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/restore"
)

func TestBuildRestorerUsesServiceAccountFile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workspace.AdminEmail = "admin@example.com"
	cfg.Workspace.ServiceAccountFile = "/secrets/sa.json"

	for _, svc := range []string{"drive", "gmail", "calendar", "contacts"} {
		r, err := buildRestorer(svc, cfg, nil, nil, "u@example.com")
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		var got string
		switch v := r.(type) {
		case *restore.DriveRestore:
			got = v.ServiceAccountFile
		case *restore.GmailRestore:
			got = v.ServiceAccountFile
		case *restore.CalendarRestore:
			got = v.ServiceAccountFile
		case *restore.ContactsRestore:
			got = v.ServiceAccountFile
		default:
			t.Fatalf("%s: unexpected type %T", svc, r)
		}
		if got != "/secrets/sa.json" {
			t.Fatalf("%s: ServiceAccountFile = %q, want /secrets/sa.json", svc, got)
		}
	}
}

func TestBuildRestorerUnknownService(t *testing.T) {
	cfg := &config.Config{}
	if _, err := buildRestorer("notreal", cfg, nil, nil, "u@x.com"); err == nil {
		t.Fatal("expected error for unknown service")
	}
}
