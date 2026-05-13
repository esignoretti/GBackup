package restore

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"

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

	if r.DryRun {
		fmt.Printf("Would restore %d messages to %s\n", len(items), targetUser)
		return nil
	}

	var restored int
	for _, item := range items {
		if err := r.restoreOne(ctx, svc, item); err != nil {
			return err
		}
		restored++
	}

	fmt.Printf("Restored %d messages\n", restored)
	return nil
}

func (r *GmailRestore) restoreOne(ctx context.Context, svc *gmail.Service, item *metadata.Item) error {
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

	raw, err := io.ReadAll(gr)
	if err != nil {
		return fmt.Errorf("reading message data: %w", err)
	}

	msg := &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString(raw),
	}
	_, err = svc.Users.Messages.Import("me", msg).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("importing message: %w", err)
	}
	return nil
}
