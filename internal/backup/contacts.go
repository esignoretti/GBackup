package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/archive"
	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type ContactsBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type ContactsBackup struct {
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *ContactsBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
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

func (c *ContactsBackup) WithProgress(p *progress.Reporter) *ContactsBackup {
	c.progress = p
	return c
}

func (c *ContactsBackup) WithMaxAge(d time.Duration) *ContactsBackup {
	c.maxAge = d
	return c
}

func (c *ContactsBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if c.progress != nil {
		c.progress.Service("contacts", user, runType)
	}

	svc, err := people.NewService(ctx,
		option.WithCredentialsFile(c.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("contacts")...),
		option.ImpersonateCredentials(user),
	)
	if err != nil {
		return 0, fmt.Errorf("creating people service for %s: %w", user, err)
	}

	var allEntries []archive.ArchiveEntry
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
			return 0, fmt.Errorf("listing connections: %w", err)
		}


		for _, person := range resp.Connections {
			data, err := json.Marshal(person)
			if err != nil {
				return 0, fmt.Errorf("marshaling contact %s: %w", person.ResourceName, err)
			}
			entryName := fmt.Sprintf("%s.json", strings.ReplaceAll(person.ResourceName, "/", "_"))
			allEntries = append(allEntries, archive.ArchiveEntry{
				Name: entryName,
				Data: data,
			})
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if c.progress != nil {
		c.progress.FetchDone("contacts", len(allEntries))
	}

	if len(allEntries) == 0 {
		return 0, nil
	}

	objKey := storage.ObjectKey("contacts", user, "all.tar.gz")

	var archiveData []byte
	if !full {
		existing, err := c.store.Download(ctx, objKey)
		if err == nil {
			archiveData, err = archive.AppendToArchive(existing, allEntries)
			if err != nil {
				return 0, fmt.Errorf("appending to archive: %w", err)
			}
		} else {
			archiveData, err = archive.Create(allEntries)
			if err != nil {
				return 0, fmt.Errorf("creating archive: %w", err)
			}
		}
	} else {
		archiveData, err = archive.Create(allEntries)
		if err != nil {
			return 0, fmt.Errorf("creating archive: %w", err)
		}
	}

	if err := c.store.Upload(ctx, objKey, bytes.NewReader(archiveData)); err != nil {
		return 0, fmt.Errorf("uploading %s: %w", objKey, err)
	}

	if c.progress != nil {
		c.progress.Upload(objKey)
	}

	for _, entry := range allEntries {
		if c.metaDB != nil {
			if err := c.metaDB.TrackItem(&metadata.Item{
				Service:   "contacts",
				User:      user,
				ObjectKey: objKey,
				ItemPath:  entry.Name,
				ItemID:    entry.Name,
				Size:      int64(len(entry.Data)),
				Checksum:  entry.Name,
			}); err != nil {
				return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
			}
		}
	}

	if c.metaDB != nil {
		if err := c.metaDB.RecordBackup("contacts", user, runType); err != nil {
			return len(allEntries), fmt.Errorf("recording backup: %w", err)
		}
	}
	return len(allEntries), nil
}
