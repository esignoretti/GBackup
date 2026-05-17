package restore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type ContactsRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *ContactsRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	items, err := r.MetaDB.ItemsByService("contacts", r.User)
	if err != nil {
		return fmt.Errorf("listing contacts: %w", err)
	}

	if len(items) == 0 {
		fmt.Println("No contacts to restore.")
		return nil
	}

	if r.DryRun {
		fmt.Printf("Would restore %d contacts to %s\n", len(items), targetUser)
		for _, item := range items {
			fmt.Printf("  %s (%d bytes)\n", item.ItemID, item.Size)
		}
		return nil
	}

	ts, err := gws.UserTokenSource(ctx, r.ServiceAccountFile, targetUser, []string{"https://www.googleapis.com/auth/contacts"})
	if err != nil {
		return fmt.Errorf("creating people token: %w", err)
	}
	svc, err := people.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return fmt.Errorf("creating people service: %w", err)
	}

	archiveKey := storage.ObjectKey("contacts", r.User, "all.tar.gz")
	rc, err := r.Store.Download(ctx, archiveKey)
	if err != nil {
		return fmt.Errorf("downloading archive %s: %w", archiveKey, err)
	}
	defer rc.Close()

	wanted := make(map[string]bool)
	for _, item := range items {
		wanted[item.ItemPath] = true
	}

	var restored int
	err = archive.Read(rc, func(entry archive.ArchiveEntry) error {
		if !wanted[entry.Name] {
			return nil
		}
		var person people.Person
		if err := json.Unmarshal(entry.Data, &person); err != nil {
			return fmt.Errorf("unmarshaling contact %s: %w", entry.Name, err)
		}
		// CreateContact rejects ResourceName, Etag, and Metadata on input.
		person.ResourceName = ""
		person.Etag = ""
		person.Metadata = nil
		if _, err := svc.People.CreateContact(&person).Context(ctx).Do(); err != nil {
			return fmt.Errorf("creating contact %s: %w", entry.Name, err)
		}
		restored++
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("Restored %d contacts\n", restored)
	return nil
}

func (r *ContactsRestore) SetDryRun(v bool) { r.DryRun = v }
