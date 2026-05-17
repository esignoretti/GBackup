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
	metaDB   *metadata.DB
	store    *storage.Client
	cfg      *DriveBackupConfig
	progress *progress.Reporter
	maxAge   time.Duration
}

func NewDriveBackup(cfg *DriveBackupConfig) (*DriveBackup, error) {
	return &DriveBackup{cfg: cfg}, nil
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

func (d *DriveBackup) driveServiceForUser(ctx context.Context, user string) (*drive.Service, error) {
	ts, err := gws.UserTokenSource(ctx, d.cfg.ServiceAccountFile, user, gws.ScopesForService("drive"))
	if err != nil {
		return nil, err
	}
	return drive.NewService(ctx, option.WithTokenSource(ts))
}

const driveMaxRetries = 3

// driveExportMap maps Google Docs Editors MIME types to the export MIME
// and the file extension to append to the object key.
var driveExportMap = map[string]struct {
	exportMIME string
	ext        string
}{
	"application/vnd.google-apps.document":     {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	"application/vnd.google-apps.spreadsheet":  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	"application/vnd.google-apps.presentation": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
	"application/vnd.google-apps.drawing":      {"image/png", ".png"},
}

func mimeCategory(mime string) string {
	if mime == "application/vnd.google-apps.folder" {
		return "folder"
	}
	if _, ok := driveExportMap[mime]; ok {
		return "google-doc"
	}
	if strings.HasPrefix(mime, "application/vnd.google-apps") {
		return "non-exportable"
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

func (d *DriveBackup) buildQuery() string {
	q := "'me' in owners"
	if d.maxAge > 0 {
		cutoff := time.Now().Add(-d.maxAge).Format(time.RFC3339)
		q += fmt.Sprintf(" and modifiedTime > '%s'", cutoff)
	}
	return q
}

func (d *DriveBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	driveSvc, err := d.driveServiceForUser(ctx, user)
	if err != nil {
		return 0, fmt.Errorf("creating drive service for %s: %w", user, err)
	}

	runType := map[bool]string{true: "full", false: "incremental"}[full]
	if d.progress != nil {
		d.progress.Service("drive", user, runType)
	}

	var runID int64
	if d.metaDB != nil {
		id, err := d.metaDB.StartBackup("drive", user, runType)
		if err != nil {
			return 0, fmt.Errorf("recording backup start: %w", err)
		}
		runID = id
	}

	var count int
	pageToken := ""
	var fetched int
	for {
		listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
		call := driveSvc.Files.List().
			Context(listCtx).
			Corpora("allDrives").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			Q(d.buildQuery()).
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
			if category == "non-exportable" {
				fmt.Printf("  drive: skipping non-exportable %s (%s)\n", f.Name, f.MimeType)
				continue
			}

			checksum := f.Md5Checksum
			modTime, err := time.Parse(time.RFC3339, f.ModifiedTime)
			if err != nil {
				fmt.Printf("  drive: skipping %s (bad modifiedTime %q): %v\n", f.Name, f.ModifiedTime, err)
				continue
			}

			if !full && d.metaDB != nil {
				modified, isModErr := d.metaDB.IsModified("drive", user, f.Id, checksum)
				if isModErr != nil {
					fmt.Printf("  drive: warning: IsModified failed for %s: %v (re-uploading)\n", f.Id, isModErr)
				} else if !modified {
					continue
				}
			}

			content, ext, err := d.downloadFileRetry(ctx, driveSvc, f, category)
			if err != nil {
				fmt.Printf("  drive: skipping %s (%s): %v\n", f.Name, f.Id, err)
				continue
			}
			fetched++

			version := f.Version
			objKey := storage.ObjectKey("drive", user, fmt.Sprintf("files/%s_v%d%s", f.Id, version, ext))

			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			_, copyErr := io.Copy(gw, content)
			content.Close()
			closeErr := gw.Close()
			if joinedErr := errors.Join(copyErr, closeErr); joinedErr != nil {
				fmt.Printf("  drive: compression failed for %s: %v\n", f.Name, joinedErr)
				continue
			}

			if err := d.store.Upload(ctx, objKey, bytes.NewReader(buf.Bytes())); err != nil {
				fmt.Printf("  drive: upload failed for %s: %v\n", objKey, err)
				continue
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

	if d.metaDB != nil && runID != 0 {
		if err := d.metaDB.CompleteBackup(runID); err != nil {
			return count, fmt.Errorf("completing backup record: %w", err)
		}
	}
	return count, nil
}

func (d *DriveBackup) downloadFile(ctx context.Context, svc *drive.Service, file *drive.File, category string) (io.ReadCloser, string, error) {
	if category == "google-doc" {
		entry, ok := driveExportMap[file.MimeType]
		if !ok {
			return nil, "", fmt.Errorf("no export mapping for %s", file.MimeType)
		}
		resp, err := svc.Files.Export(file.Id, entry.exportMIME).Context(ctx).Download()
		if err != nil {
			return nil, "", err
		}
		return resp.Body, entry.ext, nil
	}
	resp, err := svc.Files.Get(file.Id).Context(ctx).SupportsAllDrives(true).Download()
	if err != nil {
		return nil, "", err
	}
	return resp.Body, "", nil
}

func (d *DriveBackup) downloadFileRetry(ctx context.Context, svc *drive.Service, file *drive.File, category string) (io.ReadCloser, string, error) {
	const base = 200 * time.Millisecond
	const maxDelay = 5 * time.Second
	var lastErr error
	for attempt := 0; attempt < driveMaxRetries; attempt++ {
		if attempt > 0 {
			delay := base << (attempt - 1)
			if delay > maxDelay {
				delay = maxDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}
		rc, ext, err := d.downloadFile(ctx, svc, file, category)
		if err == nil {
			return rc, ext, nil
		}
		lastErr = err
		if !gws.IsRetryable(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("download failed after %d retries: %w", driveMaxRetries, lastErr)
}
