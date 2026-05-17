package progress

import (
	"fmt"
	"io"
	"os"
)

type Reporter struct {
	w io.Writer
}

func New() *Reporter {
	return &Reporter{w: os.Stdout}
}

func (r *Reporter) Service(svc, user, kind string) {
	fmt.Fprintf(r.w, "[%s] %s (%s)\n", svc, user, kind)
}

func (r *Reporter) Fetching(label string, total int) {
	fmt.Fprintf(r.w, "  fetched %d %s so far...\n", total, label)
}

func (r *Reporter) FetchDone(label string, count int) {
	fmt.Fprintf(r.w, "  fetched %d %s\n", count, label)
}

func (r *Reporter) Upload(key string) {
	fmt.Fprintf(r.w, "  \u2191 %s\n", key)
}

func (r *Reporter) Done(count int) {
	fmt.Fprintf(r.w, "  done: %d items\n", count)
}

func (r *Reporter) Skip(reason string) {
	fmt.Fprintf(r.w, "  \u2014 %s\n", reason)
}
