package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	var runID int64
	if c.metaDB != nil {
		id, err := c.metaDB.StartBackup("contacts", user, runType)
		if err != nil {
			return 0, fmt.Errorf("recording backup start: %w", err)
		}
		runID = id
	}

	ts, err := gws.UserTokenSource(ctx, c.cfg.ServiceAccountFile, user, gws.ScopesForService("contacts"))
	if err != nil {
		return 0, fmt.Errorf("creating people token for %s: %w", user, err)
	}
	svc, err := people.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return 0, fmt.Errorf("creating people service for %s: %w", user, err)
	}

	var allEntries []archive.ArchiveEntry
	pageToken := ""
	for {
		listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
		call := svc.People.Connections.List("people/me").
			Context(listCtx).
			PersonFields("names,emailAddresses,phoneNumbers,organizations,birthdays,addresses,photos,metadata").
			PageSize(1000)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		listCancel()
		if err != nil {
			return 0, fmt.Errorf("listing connections: %w", err)
		}


		for _, person := range resp.Connections {
			data, err := json.Marshal(person)
			if err != nil {
				return 0, fmt.Errorf("marshaling contact %s: %w", person.ResourceName, err)
			}
			entryName := fmt.Sprintf("%s.json", strings.ReplaceAll(person.ResourceName, "/", "_"))

			modTime := time.Now()
			if person.Metadata != nil {
				for _, src := range person.Metadata.Sources {
					if src.UpdateTime != "" {
						if t, parseErr := time.Parse(time.RFC3339, src.UpdateTime); parseErr == nil {
							modTime = t
							break
						}
					}
				}
			}

			allEntries = append(allEntries, archive.ArchiveEntry{
				Name:    entryName,
				Data:    data,
				ModTime: modTime,
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
	skipUpload := false
	if !full {
		existing, downloadErr := c.store.Download(ctx, objKey)
		if downloadErr == nil {
			data, appendErr := archive.AppendToArchive(existing, allEntries)
			existing.Close()
			if errors.Is(appendErr, archive.ErrNoChanges) {
				skipUpload = true
			} else if appendErr != nil {
				return 0, fmt.Errorf("appending to archive: %w", appendErr)
			} else {
				archiveData = data
			}
		} else {
			data, createErr := archive.Create(allEntries)
			if createErr != nil {
				return 0, fmt.Errorf("creating archive: %w", createErr)
			}
			archiveData = data
		}
	} else {
		data, createErr := archive.Create(allEntries)
		if createErr != nil {
			return 0, fmt.Errorf("creating archive: %w", createErr)
		}
		archiveData = data
	}

	if !skipUpload {
		if err := c.store.Upload(ctx, objKey, bytes.NewReader(archiveData)); err != nil {
			return 0, fmt.Errorf("uploading %s: %w", objKey, err)
		}
	}

	if c.progress != nil {
		c.progress.Upload(objKey)
	}

	for _, entry := range allEntries {
		if c.metaDB != nil {
			if err := c.metaDB.TrackItem(&metadata.Item{
				Service:    "contacts",
				User:       user,
				ObjectKey:  objKey,
				ItemPath:   entry.Name,
				ItemID:     entry.Name,
				Size:       int64(len(entry.Data)),
				Checksum:   entry.Name,
				ModifiedAt: entry.ModTime,
			}); err != nil {
				return 0, fmt.Errorf("tracking %s: %w", entry.Name, err)
			}
		}
	}

	if c.metaDB != nil && runID != 0 {
		if err := c.metaDB.CompleteBackup(runID); err != nil {
			return len(allEntries), fmt.Errorf("completing backup record: %w", err)
		}
	}
	return len(allEntries), nil
}
