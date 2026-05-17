package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

type ArchiveEntry struct {
	Name    string
	Data    []byte
	ModTime time.Time
}

func Create(entries []ArchiveEntry) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		if err := validateName(e.Name); err != nil {
			return nil, err
		}
		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     0644,
			Size:     int64(len(e.Data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("writing header for %s: %w", e.Name, err)
		}
		if _, err := tw.Write(e.Data); err != nil {
			return nil, fmt.Errorf("writing data for %s: %w", e.Name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ErrNoChanges is returned by AppendToArchive when no entries differ from the
// existing archive — callers can skip the upload entirely.
var ErrNoChanges = errors.New("archive: no changes to append")

func AppendToArchive(existing io.Reader, entries []ArchiveEntry) ([]byte, error) {
	gr, err := gzip.NewReader(existing)
	if err != nil {
		return nil, fmt.Errorf("reading existing gzip: %w", err)
	}
	defer gr.Close()

	existingByName := make(map[string][]byte)
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading data for %s: %w", hdr.Name, err)
		}
		existingByName[hdr.Name] = data
	}

	if len(entries) == 0 {
		return nil, ErrNoChanges
	}

	changed := false
	for _, e := range entries {
		old, present := existingByName[e.Name]
		if !present || !bytes.Equal(old, e.Data) {
			changed = true
		}
		existingByName[e.Name] = e.Data
	}
	if !changed {
		return nil, ErrNoChanges
	}

	all := make([]ArchiveEntry, 0, len(existingByName))
	for name, data := range existingByName {
		all = append(all, ArchiveEntry{Name: name, Data: data})
	}

	return Create(all)
}

func CreateReader(entries []ArchiveEntry) (io.Reader, error) {
	data, err := Create(entries)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func Read(r io.Reader, fn func(ArchiveEntry) error) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("reading gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
		}
		if err := fn(ArchiveEntry{Name: hdr.Name, Data: data}); err != nil {
			return err
		}
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid entry name: empty")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid entry name (absolute): %q", name)
	}
	clean := path.Clean(name)
	if clean != name {
		return fmt.Errorf("invalid entry name (non-canonical): %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("invalid entry name (path traversal): %q", name)
		}
	}
	return nil
}
