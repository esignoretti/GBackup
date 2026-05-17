package backup

import (
	"context"
	"io"

	"github.com/esignoretti/gbackup/internal/storage"
)

// streamUpload runs `write` in a goroutine, piping its output into a single
// streaming S3 upload at `key`. The write function must NOT close the writer
// itself — io.Pipe is closed for it once write returns. streamUpload returns
// the first non-nil error from write or the upload.
func streamUpload(ctx context.Context, store *storage.Client, key string, write func(io.Writer) error) error {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := write(pw)
		pw.CloseWithError(err)
		errCh <- err
	}()
	uploadErr := store.Upload(ctx, key, pr)
	pr.Close()
	writeErr := <-errCh
	if writeErr != nil {
		return writeErr
	}
	return uploadErr
}
