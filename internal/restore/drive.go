package restore

import (
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/gws"
	"github.com/esignoretti/gbackup/internal/metadata"
	"github.com/esignoretti/gbackup/internal/storage"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type DriveRestore struct {
	Store              *storage.Client
	MetaDB             *metadata.DB
	ServiceAccountFile string
	AdminEmail         string
	User               string
	Date               string
	DryRun             bool
	TargetUser         string
}

func (r *DriveRestore) Run(ctx context.Context) error {
	targetUser := r.TargetUser
	if targetUser == "" {
		targetUser = r.User
	}

	ts, err := gws.UserTokenSource(ctx, r.ServiceAccountFile, targetUser, []string{drive.DriveScope})
	if err != nil {
		return fmt.Errorf("creating drive token: %w", err)
	}
	svc, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return fmt.Errorf("creating drive service: %w", err)
	}

	items, err := r.MetaDB.ItemsByService("drive", r.User)
	if err != nil {
		return fmt.Errorf("listing drive items: %w", err)
	}

	if r.Date != "" && r.Date != "latest" {
		cutoff, err := time.Parse("2006-01-02", r.Date)
		if err != nil {
			return fmt.Errorf("invalid date format (use YYYY-MM-DD): %w", err)
		}
		endOfDay := cutoff.Add(24 * time.Hour)
		var filtered []*metadata.Item
		for _, item := range items {
			if item.ModifiedAt.Before(endOfDay) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	if r.DryRun {
		fmt.Printf("Would restore %d files to %s\n", len(items), targetUser)
		for _, item := range items {
			fmt.Printf("  %s (%d bytes)\n", item.ObjectKey, item.Size)
		}
		return nil
	}

	dateStr := r.Date
	if dateStr == "" || dateStr == "latest" {
		dateStr = time.Now().Format("2006-01-02")
	}
	rootFolder := &drive.File{
		Name:     fmt.Sprintf("GBackup Restore - %s", dateStr),
		MimeType: "application/vnd.google-apps.folder",
	}
	root, err := svc.Files.Create(rootFolder).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating restore folder: %w", err)
	}

	for _, item := range items {
		rc, err := r.Store.Download(ctx, item.ObjectKey)
		if err != nil {
			return fmt.Errorf("downloading %s: %w", item.ObjectKey, err)
		}

		gr, err := gzip.NewReader(rc)
		if err != nil {
			rc.Close()
			return err
		}

		name := item.ItemPath
		if name == "" {
			name = extractFileName(item.ObjectKey)
		}
		f := &drive.File{
			Name:    name,
			Parents: []string{root.Id},
		}
		_, err = svc.Files.Create(f).Context(ctx).Media(gr).Do()
		gr.Close()
		rc.Close()
		if err != nil {
			return fmt.Errorf("uploading restored file %s: %w", item.ObjectKey, err)
		}
	}

	fmt.Printf("Restored %d files to folder '%s'\n", len(items), rootFolder.Name)
	return nil
}

func extractFileName(key string) string {
	parts := strings.Split(key, "/")
	last := parts[len(parts)-1]
	if idx := strings.LastIndex(last, "_v"); idx >= 0 {
		rest := last[idx+2:]
		if dot := strings.Index(rest, "."); dot >= 0 {
			last = last[:idx] + rest[dot:]
		} else {
			last = last[:idx]
		}
	}
	return last
}

func (r *DriveRestore) SetDryRun(v bool) { r.DryRun = v }
