package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
)

// Writer streams tar+gzip-encoded entries to an underlying io.Writer.
// Callers must Close to flush the gzip trailer.
type Writer struct {
	gz *gzip.Writer
	tw *tar.Writer
}

func NewWriter(w io.Writer) *Writer {
	gz := gzip.NewWriter(w)
	return &Writer{gz: gz, tw: tar.NewWriter(gz)}
}

func (w *Writer) Append(e ArchiveEntry) error {
	if err := validateName(e.Name); err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:     e.Name,
		Mode:     0644,
		Size:     int64(len(e.Data)),
		Typeflag: tar.TypeReg,
		ModTime:  e.ModTime,
	}
	if err := w.tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("writing header for %s: %w", e.Name, err)
	}
	if _, err := w.tw.Write(e.Data); err != nil {
		return fmt.Errorf("writing data for %s: %w", e.Name, err)
	}
	return nil
}

func (w *Writer) Close() error {
	if err := w.tw.Close(); err != nil {
		w.gz.Close()
		return err
	}
	return w.gz.Close()
}
