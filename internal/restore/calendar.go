package restore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/esignoretti/gbackup/internal/archive"
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

	archives := groupByArchive(items, r.Date)
	if len(archives) == 0 {
		fmt.Println("No items to restore.")
		return nil
	}

	if r.DryRun {
		var total int
		for _, group := range archives {
			total += len(group.Items)
		}
		fmt.Printf("Would restore %d events from %d archives to %s\n", total, len(archives), targetUser)
		return nil
	}

	var restored int
	for archiveKey, group := range archives {
		rc, err := r.Store.Download(ctx, archiveKey)
		if err != nil {
			return fmt.Errorf("downloading archive %s: %w", archiveKey, err)
		}

		wanted := group.ItemPathSet()
		err = archive.Read(rc, func(entry archive.ArchiveEntry) error {
			if !wanted[entry.Name] {
				return nil
			}
			var event calendar.Event
			if err := json.Unmarshal(entry.Data, &event); err != nil {
				return fmt.Errorf("unmarshaling event %s: %w", entry.Name, err)
			}
			event.Id = ""
			if _, err := svc.Events.Insert("primary", &event).Context(ctx).Do(); err != nil {
				return fmt.Errorf("creating event %s: %w", entry.Name, err)
			}
			restored++
			return nil
		})
		rc.Close()
		if err != nil {
			return err
		}
	}

	fmt.Printf("Restored %d events\n", restored)
	return nil
}
