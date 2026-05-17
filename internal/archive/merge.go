// Package archive: StreamMerge implements a streaming "copy existing + skip
// names + append new entries" operation.
//
// Each tar entry is read into a []byte once (so single entries up to a few
// MiB are fine — Gmail messages, calendar events, contacts). The headline win
// over AppendToArchive is that the whole archive is never buffered twice:
// existing entries flow tar-reader → tar-writer → gzip → io.Writer without
// being collected into a map first.
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// StreamMerge writes a new archive to `out` containing every entry from
// `existing` (with same-name entries replaced by `newEntries`) followed by any
// `newEntries` whose names did not appear in `existing`.
//
// Returns ErrNoChanges if the merged archive would be byte-identical to the
// existing one — i.e. every newEntry has an existing twin with identical
// bytes and no new names are introduced.
func StreamMerge(existing io.Reader, out io.Writer, newEntries []ArchiveEntry) error {
	gr, err := gzip.NewReader(existing)
	if err != nil {
		return fmt.Errorf("reading existing gzip: %w", err)
	}
	defer gr.Close()

	newByName := make(map[string]ArchiveEntry, len(newEntries))
	for _, e := range newEntries {
		newByName[e.Name] = e
	}

	w := NewWriter(out)
	tr := tar.NewReader(gr)

	changed := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}
		if newE, replaced := newByName[hdr.Name]; replaced {
			var existingBody bytes.Buffer
			if _, err := io.Copy(&existingBody, tr); err != nil {
				return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
			}
			if !bytes.Equal(existingBody.Bytes(), newE.Data) {
				changed = true
			}
			delete(newByName, hdr.Name)
			if err := w.Append(newE); err != nil {
				return err
			}
			continue
		}
		buf := make([]byte, hdr.Size)
		if _, err := io.ReadFull(tr, buf); err != nil {
			return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
		}
		if err := w.Append(ArchiveEntry{Name: hdr.Name, Data: buf, ModTime: hdr.ModTime}); err != nil {
			return err
		}
	}

	if len(newByName) > 0 {
		changed = true
		for _, e := range newByName {
			if err := w.Append(e); err != nil {
				return err
			}
		}
	}

	if err := w.Close(); err != nil {
		return err
	}
	if !changed {
		return ErrNoChanges
	}
	return nil
}
