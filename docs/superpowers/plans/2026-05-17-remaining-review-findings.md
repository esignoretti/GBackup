# Remaining Review Findings — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the deferred Critical streaming work (C6, C7) and the Low-severity items from the 2026-05-17 code review.

**Architecture:** Four phases. Phase 1 introduces a streaming archive API and switches S3 to multipart uploads — this is the foundation. Phase 2 rewrites Gmail's fetch loop on top of it. Phase 3 swaps name-as-checksum for real content hashes so incremental detection finally works. Phase 4 sweeps up small polish items.

**Tech Stack:** Go 1.22+, AWS SDK v2 (`feature/s3/manager`), Google API client, `io.Pipe`, `errgroup`.

---

## File Structure

Created in this plan:
- `internal/archive/writer.go` — streaming `Writer` type (tar+gzip into an `io.Writer`)
- `internal/archive/merge.go` — streaming "copy existing archive + skip names + append new entries"

Heavily modified:
- `internal/archive/archive.go` — `Create`/`AppendToArchive` shrink to thin wrappers around the streaming API, kept for tests/back-compat
- `internal/storage/s3.go` — `Upload` switches to `manager.Uploader.Upload` (multipart, streaming)
- `internal/backup/gmail.go` — producer/consumer pipeline, per-month streaming writers
- `internal/backup/{calendar,contacts}.go` — use streaming archive API
- `internal/backup/runner.go` — swap manual `sync.WaitGroup` for `errgroup`
- `cmd/{backup,meta}.go` — check `os.MkdirAll` errors
- `internal/backup/calendar.go` — dedupe `eventYear`/`eventStartTime` parser
- `internal/progress/progress.go` — actually wire `Fetching`/`FetchDone`/`Skip` into Gmail
- `internal/gws/auth.go` — drop unused `AdminEmail` from `AuthConfig` once Directory owns it

Build-tagged or relocated:
- `cmd/debuggmail/` → `cmd/_debug/debuggmail/` plus `//go:build debug` tag

---

## Phase 1 — Streaming archive API + S3 multipart upload (C6)

### Task 1.1: New streaming `archive.Writer`

**Files:**
- Create: `internal/archive/writer.go`
- Test: `internal/archive/writer_test.go`

- [ ] **Step 1: Write the failing test**

`internal/archive/writer_test.go`:

```go
package archive

import (
	"bytes"
	"testing"
)

func TestWriterStreamsEntries(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ArchiveEntry{Name: "b.txt", Data: []byte("world")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	if err := Read(bytes.NewReader(buf.Bytes()), func(e ArchiveEntry) error {
		seen[e.Name] = string(e.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen["a.txt"] != "hello" || seen["b.txt"] != "world" {
		t.Fatalf("unexpected entries: %v", seen)
	}
}

func TestWriterRejectsInvalidName(t *testing.T) {
	w := NewWriter(&bytes.Buffer{})
	if err := w.Append(ArchiveEntry{Name: "../escape.txt", Data: []byte("bad")}); err == nil {
		t.Fatal("expected error on traversal")
	}
}
```

- [ ] **Step 2: Run test, expect FAIL (Writer not defined)**

`go test ./internal/archive/ -run TestWriter -v`

- [ ] **Step 3: Implement `Writer`**

`internal/archive/writer.go`:

```go
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
```

- [ ] **Step 4: Run test, expect PASS**

`go test ./internal/archive/ -run TestWriter -v`

---

### Task 1.2: Streaming merge — copy existing + skip + append

**Files:**
- Create: `internal/archive/merge.go`
- Test: `internal/archive/merge_test.go`

- [ ] **Step 1: Write the failing test**

`internal/archive/merge_test.go`:

```go
package archive

import (
	"bytes"
	"errors"
	"testing"
)

func TestStreamMergeAppendsAndOverwrites(t *testing.T) {
	// Existing archive contains "a.txt" and "b.txt".
	var existing bytes.Buffer
	w := NewWriter(&existing)
	_ = w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("old-a")})
	_ = w.Append(ArchiveEntry{Name: "b.txt", Data: []byte("old-b")})
	w.Close()

	// Merge: replace "a.txt", add "c.txt", leave "b.txt" alone.
	newEntries := []ArchiveEntry{
		{Name: "a.txt", Data: []byte("new-a")},
		{Name: "c.txt", Data: []byte("new-c")},
	}
	var merged bytes.Buffer
	if err := StreamMerge(bytes.NewReader(existing.Bytes()), &merged, newEntries); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	if err := Read(bytes.NewReader(merged.Bytes()), func(e ArchiveEntry) error {
		got[e.Name] = string(e.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a.txt": "new-a", "b.txt": "old-b", "c.txt": "new-c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestStreamMergeNoChangesReturnsSentinel(t *testing.T) {
	var existing bytes.Buffer
	w := NewWriter(&existing)
	_ = w.Append(ArchiveEntry{Name: "a.txt", Data: []byte("same")})
	w.Close()

	err := StreamMerge(bytes.NewReader(existing.Bytes()), &bytes.Buffer{}, []ArchiveEntry{
		{Name: "a.txt", Data: []byte("same")},
	})
	if !errors.Is(err, ErrNoChanges) {
		t.Fatalf("expected ErrNoChanges, got %v", err)
	}
}
```

- [ ] **Step 2: Implement `StreamMerge`**

`internal/archive/merge.go`:

```go
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// StreamMerge writes a new archive to `out` containing every entry from
// `existing` whose name does not appear in `newEntries`, followed by every
// entry from `newEntries`. New entries overwrite existing entries with the
// same name. Only entry names (not bodies) are held in memory — bodies stream
// through the tar reader/writer.
//
// Returns ErrNoChanges if the merged archive would be identical to the
// existing one (i.e. every newEntry is byte-identical to its existing twin
// and no names are introduced). Bodies of existing entries are streamed but
// also hashed-compared against newEntries that share their name; if they
// differ a copy is taken.
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
			// Compare bytes to detect a no-op replacement.
			var existingBody bytes.Buffer
			if _, err := io.Copy(&existingBody, tr); err != nil {
				return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
			}
			if !bytes.Equal(existingBody.Bytes(), newE.Data) {
				changed = true
			}
			// Skip — the new version will be written below.
			delete(newByName, hdr.Name)
			if err := w.Append(newE); err != nil {
				return err
			}
			continue
		}
		// Copy through unchanged.
		buf := make([]byte, hdr.Size)
		if _, err := io.ReadFull(tr, buf); err != nil {
			return fmt.Errorf("reading data for %s: %w", hdr.Name, err)
		}
		if err := w.Append(ArchiveEntry{Name: hdr.Name, Data: buf, ModTime: hdr.ModTime}); err != nil {
			return err
		}
	}

	// Whatever's left in newByName is genuinely new.
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
```

- [ ] **Step 3: Run tests**

`go test ./internal/archive/ -count=1 -v`

- [ ] **Step 4: Note about memory**

The current implementation reads each entry's body into a `[]byte` before forwarding it. For genuinely huge entries (single tar entry > available RAM) this is still a problem — but Gmail/Calendar/Contacts entries are individual messages/events/contacts (KB range), so per-entry buffering is fine. The headline win is avoiding double-buffering the *whole archive* and avoiding holding all entries in a map at once. Document this in a comment at the top of `merge.go`.

---

### Task 1.3: Switch S3 `Upload` to multipart streaming

**Files:**
- Modify: `internal/storage/s3.go`
- Test: `internal/storage/s3_test.go` (sanity test only — we can't easily mock multipart without a fake S3)

- [ ] **Step 1: Add `manager` import and swap `PutObject` for `Uploader.Upload`**

In `internal/storage/s3.go`:

```go
import (
	// ... existing ...
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
)

type Client struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
}
```

Wire the uploader in `NewClient`:

```go
client := s3.NewFromConfig(awsCfg, s3Opts...)
uploader := manager.NewUploader(client, func(u *manager.Uploader) {
	u.PartSize = 8 * 1024 * 1024 // 8 MiB parts
	u.Concurrency = 4
})
return &Client{
	client:   client,
	uploader: uploader,
	bucket:   cfg.Bucket,
}, nil
```

Replace `Upload`:

```go
func (c *Client) Upload(ctx context.Context, key string, reader io.Reader) error {
	_, err := c.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   reader,
	})
	return err
}
```

- [ ] **Step 2: Pull in the new dep**

```bash
cd /Users/esignoretti/Documents/OpenCode/GBackup && go get github.com/aws/aws-sdk-go-v2/feature/s3/manager && go mod tidy
```

- [ ] **Step 3: Run tests**

`go test ./internal/storage/ -count=1`
Expected: PASS (existing tests still call `Upload` with a small reader — multipart upgrades transparently).

---

### Task 1.4: Stream `Calendar` and `Contacts` archives via `io.Pipe`

**Files:**
- Modify: `internal/backup/calendar.go`, `internal/backup/contacts.go`

These services currently build an entire archive in memory before calling `Upload`. They become a "writer goroutine + pipe" pattern.

- [ ] **Step 1: Add a helper for streaming uploads**

In `internal/backup/runner.go` (or a new `internal/backup/upload.go`):

```go
// streamUpload runs `write` in a goroutine, piping its output into a single
// streaming S3 upload at `key`. The write function should call w.Close() when
// done (deferring it is fine). streamUpload returns the first error — either
// from the writer or the uploader.
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
	if uploadErr != nil {
		return uploadErr
	}
	return writeErr
}
```

- [ ] **Step 2: Switch Calendar to streaming**

In `internal/backup/calendar.go`, replace the archive-building block with:

```go
for year, entries := range buckets {
	objKey := storage.ObjectKey("calendar", user, fmt.Sprintf("%s.tar.gz", year))

	uploadErr := streamUpload(ctx, c.store, objKey, func(w io.Writer) error {
		if !full {
			existing, dlErr := c.store.Download(ctx, objKey)
			if dlErr == nil {
				err := archive.StreamMerge(existing, w, entries)
				existing.Close()
				return err
			}
		}
		aw := archive.NewWriter(w)
		for _, e := range entries {
			if err := aw.Append(e); err != nil {
				return err
			}
		}
		return aw.Close()
	})
	if errors.Is(uploadErr, archive.ErrNoChanges) {
		// Nothing changed — skip metadata churn entirely for this year.
		continue
	}
	if uploadErr != nil {
		return totalCount, fmt.Errorf("writing archive %s: %w", objKey, uploadErr)
	}

	// ... existing TrackItem loop and totalCount++ ...
}
```

- [ ] **Step 3: Switch Contacts to streaming**

Same shape as calendar — single archive at `contacts/<user>/all.tar.gz`.

- [ ] **Step 4: Run tests**

`go test ./internal/backup/ -count=1`

---

### Task 1.5: Commit Phase 1

- [ ] `go vet ./... && go test ./... -count=1`
- [ ] `git add -A && git commit -m "feat(archive/storage): streaming archive API + S3 multipart uploads

- archive.Writer: tar+gzip into any io.Writer; no whole-archive buffering
- archive.StreamMerge: piped 'copy existing + skip + append' for incremental
- storage.Upload: uses s3 manager.Uploader (multipart, removes 5GB limit)
- backup/{calendar,contacts}: pipe straight from archive into S3 instead of buffering bytes

Closes C6 from 2026-05-17 review."`

---

## Phase 2 — Gmail streaming pipeline (C7)

### Task 2.1: Producer/consumer fetch with bounded channels

**Files:**
- Modify: `internal/backup/gmail.go`

The current `BackupUser` does: list-all-IDs → fetch-all-bodies-into-buckets → upload-all-buckets. Replace with three pipeline stages:

1. **Lister** (1 goroutine): paginates messages, sends IDs into `idCh chan string` (buffered, capacity 1000).
2. **Fetchers** (N=25 workers): consume `idCh`, fetch raw body, route into `monthCh map[string]chan archive.ArchiveEntry`. Each month gets a lazily-created channel with capacity 100.
3. **Uploaders** (1 per month): lazily started on first message for that month. Each runs `streamUpload(... StreamMerge or NewWriter ...)` consuming from its channel.

- [ ] **Step 1: Sketch the structure**

```go
func (g *GmailBackup) BackupUser(ctx context.Context, user string, full bool) (int, error) {
	// ... start-of-run setup unchanged ...

	idCh := make(chan string, 1000)
	type bucketCh struct {
		ch chan archive.ArchiveEntry
		wg *sync.WaitGroup // signals when uploader has finished
	}
	buckets := struct {
		sync.Mutex
		m map[string]*bucketCh
	}{m: map[string]*bucketCh{}}

	getBucket := func(month string) *bucketCh { ... }  // lazy-creates + spawns uploader
	finalizeAll := func() error { ... }                 // closes channels, waits for uploaders, aggregates errors

	eg, egCtx := errgroup.WithContext(ctx)

	// Lister
	eg.Go(func() error {
		defer close(idCh)
		return g.listMessages(egCtx, svc, user, idCh)
	})

	// Fetchers
	for i := 0; i < gmailConcurrency; i++ {
		eg.Go(func() error {
			for id := range idCh {
				entry, month, err := g.fetchOne(egCtx, svc, user, id)
				if err != nil {
					return err
				}
				if entry == nil {
					continue // filtered by maxAge
				}
				bc := getBucket(month)
				select {
				case bc.ch <- *entry:
				case <-egCtx.Done():
					return egCtx.Err()
				}
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		_ = finalizeAll()
		return 0, err
	}
	return totalCount, finalizeAll()
}
```

- [ ] **Step 2: Write `listMessages` and `fetchOne`**

Extract the existing list-pagination logic into `func (g *GmailBackup) listMessages(ctx, svc, user, out chan<- string) error` and the per-message fetch into `func (g *GmailBackup) fetchOne(ctx, svc, user, id string) (*archive.ArchiveEntry, string, error)`. Move `messageMonth`/maxAge filtering inside `fetchOne`.

- [ ] **Step 3: Implement `getBucket` and the per-month uploader**

```go
getBucket := func(month string) *bucketCh {
	buckets.Lock()
	defer buckets.Unlock()
	if bc, ok := buckets.m[month]; ok {
		return bc
	}
	ch := make(chan archive.ArchiveEntry, 100)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	bc := &bucketCh{ch: ch, wg: wg}
	buckets.m[month] = bc

	go func() {
		defer wg.Done()
		objKey := storage.ObjectKey("gmail", user, fmt.Sprintf("%s.tar.gz", month))
		uploadErr := streamUpload(ctx, g.store, objKey, func(w io.Writer) error {
			// Pump from ch into the right archive writer.
			if !full {
				existing, dlErr := g.store.Download(ctx, objKey)
				if dlErr == nil {
					return streamMergeFromChan(existing, w, ch)
				}
			}
			aw := archive.NewWriter(w)
			for e := range ch {
				if err := aw.Append(e); err != nil {
					return err
				}
			}
			return aw.Close()
		})
		// Stash uploadErr somewhere (e.g. a shared []error guarded by buckets.Mutex)
	}()
	return bc
}
```

`streamMergeFromChan` is a variant of `StreamMerge` that pulls newEntries from a channel instead of a slice. Add it to `internal/archive/merge.go`:

```go
func StreamMergeFromChan(existing io.Reader, out io.Writer, newEntries <-chan ArchiveEntry) error {
	// Drain the channel into a map up to the first entry — actually we can't
	// easily stream both at once. Compromise: drain `newEntries` first into a
	// slice (names only stored as keys, bodies kept), then call StreamMerge.
	// For Gmail, a single month's new messages comfortably fits in memory.
	var entries []ArchiveEntry
	for e := range newEntries {
		entries = append(entries, e)
	}
	return StreamMerge(existing, out, entries)
}
```

This is a deliberate compromise — fully bidirectional streaming would require a more complex coroutine and the memory win is small (one month of new messages, not all months × bodies).

- [ ] **Step 4: Write `finalizeAll`**

```go
finalizeAll := func() error {
	buckets.Lock()
	for _, bc := range buckets.m {
		close(bc.ch)
	}
	bcs := make([]*bucketCh, 0, len(buckets.m))
	for _, bc := range buckets.m {
		bcs = append(bcs, bc)
	}
	buckets.Unlock()
	for _, bc := range bcs {
		bc.wg.Wait()
	}
	// TODO: aggregate per-bucket upload errors stored in step 3
	return nil
}
```

- [ ] **Step 5: Per-message metadata tracking**

`TrackItem` currently runs in `uploadMonth` after the archive is written. With streaming, we don't get a single "all entries for this month are ready" moment. Solutions:

- **Track at fetch time**: as soon as `fetchOne` produces an entry, also call `TrackItem`. This means the metadata DB records items before they're durably uploaded — risky.
- **Track in the uploader**: wrap the channel pump to also call `TrackItem` per entry as it's pulled, but only after the archive `Close()` succeeds and `streamUpload` returns nil.

Pick option 2:

```go
uploadErr := streamUpload(ctx, g.store, objKey, func(w io.Writer) error {
	var pending []archive.ArchiveEntry
	aw := archive.NewWriter(w)
	for e := range ch {
		pending = append(pending, e)
		if err := aw.Append(e); err != nil {
			return err
		}
	}
	if err := aw.Close(); err != nil {
		return err
	}
	// Defer TrackItem to after we return — but we already wrote everything.
	// Track now since upload hasn't completed yet... bah. See note.
	return nil
})
if uploadErr == nil {
	for _, e := range pending /* captured how? */ {
		g.metaDB.TrackItem(...)
	}
}
```

Capturing `pending` across the closure boundary is awkward. Cleanest: have the uploader push tracked entries onto a per-bucket slice that gets drained after `bc.wg.Wait()` returns successfully:

```go
type bucketCh struct {
	ch      chan archive.ArchiveEntry
	wg      *sync.WaitGroup
	flushed []archive.ArchiveEntry
	err     error
}
```

The uploader writes into `bc.flushed` only if the archive Close + upload succeed (which means re-entering after upload completion — re-architect to a `result` channel).

Simplest correct version: uploader function returns `(entries, err)`, stored on `bc`. After `wg.Wait`, iterate the stored entries and `TrackItem` each.

- [ ] **Step 6: Run tests**

`go test ./internal/backup/ -count=1`

The existing `TestNewGmailBackup`, `TestMessageMonth`, `TestMessageAgeFiltering`, `TestEntryNameToID` should still pass. The plan does not add integration tests for the pipeline because they would require a fake Gmail service — that's worth doing but tracked separately under Task 4.5.

---

### Task 2.2: Commit Phase 2

- [ ] `go vet ./... && go test ./... -count=1`
- [ ] `git add -A && git commit -m "feat(gmail): producer/consumer streaming pipeline

Replace 'list all IDs then fetch all bodies then upload all months' with a
bounded pipeline: lister → ID channel → 25 fetcher workers → per-month entry
channels → per-month streaming uploaders. No more 200k-element slice or
all-bodies-in-memory map.

Closes C7 from 2026-05-17 review."`

---

## Phase 3 — Real checksums (L10)

### Task 3.1: Compute SHA-256 for gmail/calendar/contacts entries

**Files:**
- Modify: `internal/backup/gmail.go`, `internal/backup/calendar.go`, `internal/backup/contacts.go`

Today, `Item.Checksum` is set to `entry.Name` for all three services. `IsModified` therefore degenerates to "compare entry names" — which never changes for a given message/event/contact ID, so incremental detection silently never says "modified" even when content changes.

- [ ] **Step 1: Helper**

Add to `internal/archive/archive.go`:

```go
import "crypto/sha256"
import "encoding/hex"

func Checksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
```

- [ ] **Step 2: Gmail — checksum the raw bytes**

In `gmail.go` where the entry is built in `fetchOne` (post-extract from Phase 2):

```go
entry := archive.ArchiveEntry{
	Name:    fmt.Sprintf("%s.eml", id),
	Data:    raw,
	ModTime: time.UnixMilli(msg.InternalDate),
}
```

In the TrackItem call (after upload):

```go
Checksum: archive.Checksum(entry.Data),
```

- [ ] **Step 3: Calendar — checksum the marshaled JSON**

Same change in `calendar.go`. The marshaled JSON is already in `entry.Data`.

- [ ] **Step 4: Contacts — checksum the marshaled JSON**

Same in `contacts.go`.

- [ ] **Step 5: Migration note**

Existing rows have `checksum = entry_name`. On the next run after deploying this change, every item's checksum will appear "changed" and the system will re-upload everything once. That's acceptable — log a one-time warning in `cmd/backup.go` when the first run after upgrade detects this. Or simpler: just document the behavior in `REVIEW.md` and move on. Recommendation: document, don't gate.

- [ ] **Step 6: Tests**

Add a test in `internal/archive/archive_test.go`:

```go
func TestChecksumDifferentForDifferentBytes(t *testing.T) {
	if Checksum([]byte("a")) == Checksum([]byte("b")) {
		t.Fatal("sha256 collision in test inputs?")
	}
	if Checksum([]byte("a")) != Checksum([]byte("a")) {
		t.Fatal("deterministic checksum")
	}
}
```

- [ ] **Step 7: Run all tests + commit**

```bash
go vet ./... && go test ./... -count=1
git commit -am "fix(metadata): real SHA-256 checksums for gmail/calendar/contacts items

Item.Checksum was set to entry.Name, so IsModified compared names that never
change, so incremental detection silently did nothing. Now stores
sha256(entry.Data). First post-upgrade run re-uploads everything once."
```

---

## Phase 4 — Polish (L1, L2, L3, L4, L6, L7, L8, L9)

### Task 4.1: Swap runner's `sync.WaitGroup` for `errgroup` (L1)

**Files:**
- Modify: `internal/backup/runner.go`

- [ ] **Step 1: Replace the manual wg+mu pattern in `Run`**

Today:

```go
var mu sync.Mutex
var wg sync.WaitGroup
for _, svc := range r.cfg.Config.Services {
	svc := svc
	wg.Add(1)
	go func() { defer wg.Done(); ... mu.Lock(); ... }()
}
wg.Wait()
```

Replace with:

```go
eg, _ := errgroup.WithContext(ctx)
for _, svc := range r.cfg.Config.Services {
	svc := svc
	eg.Go(func() error {
		err := r.runService(ctx, svc, users, full)
		// classify under mutex
		mu.Lock()
		defer mu.Unlock()
		// ... existing classification ...
		return nil // we always swallow service errors into RunResult, so errgroup never aborts
	})
}
_ = eg.Wait()
```

Note that we deliberately swallow per-service errors into `RunResult` rather than letting errgroup cancel siblings — that semantics matches the current behavior.

- [ ] **Step 2: Run tests + commit**

---

### Task 4.2: Wire `progress.Reporter` methods that Gmail bypasses (L2)

**Files:**
- Modify: `internal/backup/gmail.go`

The `Reporter.Fetching` / `FetchDone` / `Skip` methods are defined but Gmail uses raw `fmt.Printf` for `"  found %d messages to fetch\n"`, `"  fetched %d messages so far...\n"`, `"  fetched %d messages\n"`, etc.

- [ ] **Step 1: Replace the four raw prints in Gmail's fetch flow with `g.progress.Fetching(...)` / `g.progress.FetchDone(...)`**

Specifically:
- `"  found %d messages to fetch\n"` → `g.progress.Fetching("messages", len(allMsgIDs))` (rename Fetching to take a "to fetch" label? Or add a new `WillFetch` method — minimal: just use the same Fetching method).
- `"  fetched %d messages so far...\n"` per 100 → `g.progress.Fetching("messages", fetched)`
- `"  fetched %d messages\n"` at end → `g.progress.FetchDone("messages", fetched)`
- `"  ↑ %s\n"` per upload → `g.progress.Upload(objKey)` (already exists)

- [ ] **Step 2: Run + commit**

---

### Task 4.3: Drop unused `AuthConfig.AdminEmail` consumers (L3)

**Files:**
- Modify: `internal/gws/auth.go`, `internal/gws/directory.go`, `internal/backup/runner.go`, `cmd/backup.go`

After Phase 1 of the JWT-Subject fix, `AdminEmail` is only used by `Directory`. The three backup services don't read it. Audit and decide.

- [ ] **Step 1: Audit usages**

```bash
grep -rn "AdminEmail\|DirAuth" internal/ cmd/
```

- [ ] **Step 2: If `AdminEmail` is only used by Directory**

Keep `AuthConfig` as-is (it's specifically the directory config). Rename to `DirectoryAuthConfig` or leave alone. Drop the field from `DriveBackupConfig`/`CalendarBackupConfig`/`ContactsBackupConfig` since they no longer use it.

```go
type DriveBackupConfig struct {
	ServiceAccountFile string
	// AdminEmail removed: each user is now impersonated directly.
}
```

Same for `CalendarBackupConfig`, `ContactsBackupConfig`, `GmailBackupConfig`.

- [ ] **Step 3: Run + commit**

---

### Task 4.4: Top-level error formatter (L4)

**Files:**
- Modify: `cmd/root.go`

Errors today print as `backup failed: loading config: reading config: open /h/x.yaml: no such file or directory`. Workable but ugly.

- [ ] **Step 1: Add an unwrap-and-format helper**

```go
func formatError(err error) string {
	if err == nil {
		return ""
	}
	var lines []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		// Strip what's already in the parent's message.
		msg := e.Error()
		if u := errors.Unwrap(e); u != nil {
			msg = strings.TrimSuffix(msg, ": "+u.Error())
		}
		lines = append(lines, msg)
	}
	return strings.Join(lines, "\n  → ")
}
```

- [ ] **Step 2: Use it in `Execute`**

In `cmd/root.go`'s top-level `Execute()`, replace `fmt.Fprintln(os.Stderr, err)` with `fmt.Fprintln(os.Stderr, formatError(err))`.

- [ ] **Step 3: Run + commit**

---

### Task 4.5: Check `os.MkdirAll` errors (L6) + test gaps (L8) + dedupe calendar parser (L9) + gate `cmd/debuggmail` (L7)

These are individually trivial. Bundle them into one commit.

- [ ] **Step 1: Check `MkdirAll`**

```go
// cmd/backup.go
if err := os.MkdirAll(dbDir, 0700); err != nil {
	return fmt.Errorf("creating db dir: %w", err)
}

// cmd/meta.go: already returns error after Task A3 — verify
```

- [ ] **Step 2: Dedupe calendar parser**

Replace `eventYear` and `eventStartTime` with one shared helper that parses `event.Start.Date` or `event.Start.DateTime` once and returns `(time.Time, bool)`. `eventYear` becomes `t.Format("2006")` on top of it.

- [ ] **Step 3: Gate `cmd/debuggmail`**

Add at the top of `cmd/debuggmail/main.go`:

```go
//go:build debug
// +build debug
```

Document in `README.md` or `Makefile`: `go build -tags debug ./cmd/debuggmail`.

- [ ] **Step 4: Test coverage gaps**

Pick at least three from this list (high signal first):

- `cmd/wipe.go` confirmation flow with foreign keys present (mock `confirmExact`).
- `internal/backup/runner.go` `resolveUsers("*")` with a fake `DirectoryService` (extract an interface).
- `cmd/restore.go` confirmation flow via injecting a `confirm` function.

Track the remaining ones in `REVIEW.md` as known coverage gaps.

- [ ] **Step 5: Run + commit**

```bash
go vet ./... && go test ./... -count=1
git commit -am "polish: MkdirAll error checks, calendar parser dedupe, debug-tagged cmd/debuggmail, test coverage backfill"
```

---

## Final verification

- [ ] **All tests green**

```bash
cd /Users/esignoretti/Documents/OpenCode/GBackup
go vet ./...
go test ./... -count=1
go build -o gbackup .
```

- [ ] **End-to-end sanity** (manual; the test suite can't cover this)
  - `./gbackup backup` against the user's real Workspace tenant
  - Calendar archives should still grow only with real new events (real checksums + StreamMerge)
  - Gmail backup should not OOM on a >100k-message mailbox

- [ ] **Merge to main + push**

```bash
git checkout main && git merge --ff-only fix/remaining-review && go test ./... -count=1 && git push origin main
```
