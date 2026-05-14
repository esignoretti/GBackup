package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type ContactsBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type ContactsBackup struct {
	metaDB *metadata.DB
	store  *storage.Client
	cfg    *ContactsBackupConfig
}

func NewContactsBackup(cfg *ContactsBackupConfig) (*ContactsBackup, error) {
	return &ContactsBackup{cfg: cfg}, nil
}

func (c *ContactsBackup) WithMetaDB(db *metadata.DB) *ContactsBackup {
	c.metaDB = db
	return c
}

func (c *ContactsBackup) WithStorage(s *storage.Client) *ContactsBackup {
	c.store = s
	return c
}

func (c *ContactsBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	svc, err := people.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("contacts")...),
		option.ImpersonateCredentials(user),
	)
	if err != nil {
		return 0, fmt.Errorf("creating people service for %s: %w", user, err)
	}

	var count int
	pageToken := ""
	for {
		call := svc.People.Connections.List("people/me").
			Context(ctx).
			PersonFields("names,emailAddresses,phoneNumbers,organizations,birthdays,addresses,photos,metadata").
			PageSize(1000)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return count, fmt.Errorf("listing connections: %w", err)
		}

		for _, person := range resp.Connections {
			resourceName := person.ResourceName
			etag := person.Etag

			if !full && c.metaDB != nil && etag != "" {
				modified, err := c.metaDB.IsModified("contacts", user, resourceName, etag)
				if err == nil && !modified {
					continue
				}
			}

			data, err := json.Marshal(person)
			if err != nil {
				return count, fmt.Errorf("marshaling contact %s: %w", resourceName, err)
			}

			objKey := storage.ObjectKey("contacts", user, fmt.Sprintf("contacts/%s.json", resourceName))
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			if _, err := gw.Write(data); err != nil {
				gw.Close()
				return count, fmt.Errorf("gzip write: %w", err)
			}
			if err := gw.Close(); err != nil {
				return count, fmt.Errorf("gzip close: %w", err)
			}

			if err := c.store.Upload(ctx, objKey, &buf); err != nil {
				return count, fmt.Errorf("uploading %s: %w", objKey, err)
			}

			if c.metaDB != nil {
				c.metaDB.TrackItem(&metadata.Item{
					Service:   "contacts",
					User:      user,
					ObjectKey: objKey,
					ItemID:    resourceName,
					Size:      int64(buf.Len()),
					Checksum:  etag,
				})
			}
			count++
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if c.metaDB != nil {
		c.metaDB.RecordBackup("contacts", user, map[bool]string{true: "full", false: "incremental"}[full])
	}
	return count, nil
}
