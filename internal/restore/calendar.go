package restore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *CalendarRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	svc, err := calendar.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes(calendar.CalendarEventsScope),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating calendar service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("calendar", r.User)
	if err != nil {
		return fmt.Errorf("listing calendar items: %w", err)
	}

	if r.DryRun {
		fmt.Printf("Would restore %d events to %s\n", len(items), targetUser)
		return nil
	}

	var restored int
	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
		restored++
	}

	fmt.Printf("Restored %d events\n", restored)
	return nil
}

func (r *CalendarRestore) restoreOne(ctx context.Context, svc *calendar.Service, item *metadata.Item) error {
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
		return fmt.Errorf("reading event data: %w", err)
	}

	var event calendar.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshaling event: %w", err)
	}

	event.Id = ""
	_, err = svc.Events.Insert("primary", &event).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating event: %w", err)
	}
	return nil
}
