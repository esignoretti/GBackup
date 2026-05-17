package restore

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *GmailRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	svc, err := gmail.NewService(ctx,
		option.WithCredentialsFile(r.ServiceAccountFile),
		option.WithScopes(gmail.GmailInsertScope),
		option.ImpersonateCredentials(targetUser),
	)
	if err != nil {
		return fmt.Errorf("creating gmail service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("gmail", r.User)
	if err != nil {
		return fmt.Errorf("listing gmail items: %w", err)
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
		fmt.Printf("Would restore %d messages from %d archives to %s\n", total, len(archives), targetUser)
		for archiveKey, group := range archives {
			fmt.Printf("  %s: %d messages\n", archiveKey, len(group.Items))
		}
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
			msg := &gmail.Message{
				Raw: base64.URLEncoding.EncodeToString(entry.Data),
			}
			if _, err := svc.Users.Messages.Import("me", msg).Context(ctx).Do(); err != nil {
				return fmt.Errorf("importing message %s: %w", entry.Name, err)
			}
			restored++
			return nil
		})
		rc.Close()
		if err != nil {
			return err
		}
	}

	fmt.Printf("Restored %d messages\n", restored)
	return nil
}

func (r *GmailRestore) SetDryRun(v bool) { r.DryRun = v }
