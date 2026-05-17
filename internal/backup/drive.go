package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/progress"
	"github.com/esignoretti/gbackup/internal/storage"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type DriveBackupConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

type DriveBackup struct {
	driveSvc *drive.Service
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *DriveBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func NewDriveBackup(cfg *DriveBackupConfig) (*DriveBackup, error) {
	return &DriveBackup{cfg: cfg}, nil
}

func (d *DriveBackup) WithDriveService(svc *drive.Service) *DriveBackup {
	d.driveSvc = svc
	return d
}

func (d *DriveBackup) WithMetaDB(db *metadata.DB) *DriveBackup {
	d.metaDB = db
	return d
}

func (d *DriveBackup) WithStorage(s *storage.Client) *DriveBackup {
	d.store = s
	return d
}

func (d *DriveBackup) WithProgress(p *progress.Reporter) *DriveBackup {
	d.progress = p
	return d
}

func (d *DriveBackup) WithMaxAge(dur time.Duration) *DriveBackup {
	d.maxAge = dur
	return d
}

func (d *DriveBackup) initDriveService(ctx context.Context) error {
	if d.driveSvc != nil {
		return nil
	}
	svc, err := drive.NewService(ctx,
		option.WithCredentialsFile(d.cfg.ServiceAccountFile),
		option.WithScopes(gws.ScopesForService("drive")...),
		option.ImpersonateCredentials(d.cfg.AdminEmail),
	)
	if err != nil {
		return fmt.Errorf("creating drive service: %w", err)
	}
	d.driveSvc = svc
	return nil
}

func mimeCategory(mime string) string {
	if strings.HasPrefix(mime, "application/vnd.google-apps") {
		switch mime {
		case "application/vnd.google-apps.folder":
			return "folder"
		default:
			return "google-doc"
		}
	}
	return "binary"
}

func filterUsers(users, exclude []string) []string {
	excludeSet := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		excludeSet[e] = true
	}
	var result []string
	for _, u := range users {
		if !excludeSet[u] {
			result = append(result, u)
		}
	}
	return result
}

func (d *DriveBackup) buildQuery(user string) string {
	q := fmt.Sprintf("'%s' in owners", user)
	if d.maxAge > 0 {
		cutoff := time.Now().Add(-d.maxAge).Format(time.RFC3339)
		q += fmt.Sprintf(" and modifiedTime > '%s'", cutoff)
	}
	return q
}

func (d *DriveBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	if err := d.initDriveService(ctx); err != nil {
		return 0, err
	}

	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if d.progress != nil {
		d.progress.Service("drive", user, runType)
	}

	var count int
	pageToken := ""
	var fetched int
	for {
		listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
		call := d.driveSvc.Files.List().
			Context(listCtx).
			Corpora("allDrives").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			Q(d.buildQuery(user)).
			PageSize(100).
			Fields("nextPageToken, files(id, name, mimeType, size, md5Checksum, modifiedTime, parents, version)")
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		fileList, err := call.Do()
		listCancel()
		if err != nil {
			return count, fmt.Errorf("listing files: %w", err)
		}


		for _, f := range fileList.Files {
			category := mimeCategory(f.MimeType)
			if category == "folder" {
				continue
			}

			checksum := f.Md5Checksum
			modTime, err := time.Parse(time.RFC3339, f.ModifiedTime)
			if err != nil {
				return count, fmt.Errorf("parsing modified time for %s: %w", f.Id, err)
			}

			if !full && d.metaDB != nil {
				modified, err := d.metaDB.IsModified("drive", user, f.Id, checksum)
				if err == nil && !modified {
					continue
				}
			}

			content, err := d.downloadFile(ctx, f.Id, category)
			if err != nil {
				return count, fmt.Errorf("downloading %s: %w", f.Name, err)
			}
			fetched++

			version := f.Version
			objKey := storage.ObjectKey("drive", user, fmt.Sprintf("files/%s_v%d", f.Id, version))

			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			_, copyErr := io.Copy(gw, content)
			content.Close()
			closeErr := gw.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return count, fmt.Errorf("compression failed: %w", err)
			}

			if err := d.store.Upload(ctx, objKey, bytes.NewReader(buf.Bytes())); err != nil {
				return count, fmt.Errorf("uploading %s: %w", objKey, err)
			}

			if d.progress != nil {
				d.progress.Upload(objKey)
			}

			if d.metaDB != nil {
				if err := d.metaDB.TrackItem(&metadata.Item{
					Service:    "drive",
					User:       user,
					ObjectKey:  objKey,
					ItemPath:   f.Name,
					ItemID:     f.Id,
					Size:       int64(buf.Len()),
					Checksum:   checksum,
					ModifiedAt: modTime,
				}); err != nil {
					return count, fmt.Errorf("tracking %s: %w", f.Id, err)
				}
			}
			count++
		}

		pageToken = fileList.NextPageToken
		if pageToken == "" {
			break
		}
	}

	if d.progress != nil {
		d.progress.FetchDone("files", fetched)
	}

	if d.metaDB != nil {
		if err := d.metaDB.RecordBackup("drive", user, runType); err != nil {
			return count, fmt.Errorf("recording backup: %w", err)
		}
	}
	return count, nil
}

func (d *DriveBackup) downloadFile(ctx context.Context, fileID, category string) (io.ReadCloser, error) {
	if category == "google-doc" {
		resp, err := d.driveSvc.Files.Export(fileID, "application/pdf").Context(ctx).Download()
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}
	resp, err := d.driveSvc.Files.Get(fileID).Context(ctx).SupportsAllDrives(true).Download()
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
