package restore

import (
	"strings"
	"time"

	"github.com/esignoretti/gbackup/internal/metadata"
)

type ArchiveGroup struct {
	Items []*metadata.Item
}

func (g *ArchiveGroup) ItemPathSet() map[string]bool {
	s := make(map[string]bool, len(g.Items))
	for _, item := range g.Items {
		s[item.ItemPath] = true
	}
	return s
}

func groupByArchive(items []*metadata.Item, date string) map[string]*ArchiveGroup {
	groups := make(map[string]*ArchiveGroup)
	for _, item := range items {
		if !archiveMatchesDate(item.ObjectKey, date) {
			continue
		}
		key := item.ObjectKey
		if groups[key] == nil {
			groups[key] = &ArchiveGroup{}
		}
		groups[key].Items = append(groups[key].Items, item)
	}
	return groups
}

func archiveMatchesDate(archiveKey, date string) bool {
	if date == "" || date == "latest" {
		return true
	}
	parts := strings.Split(archiveKey, "/")
	if len(parts) < 2 {
		return true
	}
	archiveName := parts[len(parts)-1]
	archiveName = strings.TrimSuffix(archiveName, ".tar.gz")

	target, err := time.Parse("2006-01-02", date)
	if err != nil {
		return true
	}

	if len(archiveName) == 7 {
		archiveMonth, err := time.Parse("2006-01", archiveName)
		if err != nil {
			return true
		}
		return !archiveMonth.After(target)
	}

	if len(archiveName) == 4 {
		archiveYear, err := time.Parse("2006", archiveName)
		if err != nil {
			return true
		}
		return !archiveYear.After(target)
	}

	return true
}
