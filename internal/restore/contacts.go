package restore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"

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

	if r.DryRun {
		fmt.Printf("Would restore %d contacts to %s\n", len(items), targetUser)
		for _, item := range items {
			fmt.Printf("  %s (%d bytes)\n", item.ItemID, item.Size)
		}
		return nil
	}

	svc, err := people.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes("https://www.googleapis.com/auth/contacts"),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating people service: %w", err)
	}

	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
	}

	fmt.Printf("Restored %d contacts\n", len(items))
	return nil
}

func (r *ContactsRestore) restoreOne(ctx context.Context, svc *people.Service, item *metadata.Item) error {
	rc, err := r.Store.Download(ctx, item.ObjectKey)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", item.ObjectKey, err)
	}
	defer rc.Close()

	gr, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer gr.Close()

	data, err := io.ReadAll(gr)
	if err != nil {
		return fmt.Errorf("reading contact data: %w", err)
	}

	var person people.Person
	if err := json.Unmarshal(data, &person); err != nil {
		return fmt.Errorf("unmarshaling contact: %w", err)
	}

	contact := &people.Person{}
	if len(person.Names) > 0 {
		contact.Names = []*people.Name{
			{GivenName: person.Names[0].GivenName, FamilyName: person.Names[0].FamilyName},
		}
	}
	contact.EmailAddresses = person.EmailAddresses
	contact.PhoneNumbers = person.PhoneNumbers
	contact.Organizations = person.Organizations
	contact.Addresses = person.Addresses
	contact.Birthdays = person.Birthdays

	_, err = svc.People.CreateContact(contact).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating contact %s: %w", item.ItemID, err)
	}

	return nil
}
