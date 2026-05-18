package cmd

import (
	"errors"
	"fmt"
	"testing"
)

func TestFormatError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := formatError(nil); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
	t.Run("wrapped chain", func(t *testing.T) {
		base := errors.New("open /h/x: no such file or directory")
		wrapped := fmt.Errorf("reading config: %w", base)
		outer := fmt.Errorf("loading config: %w", wrapped)
		top := fmt.Errorf("backup failed: %w", outer)
		want := "backup failed\n  → loading config\n  → reading config\n  → open /h/x: no such file or directory"
		if got := formatError(top); got != want {
			t.Fatalf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("single", func(t *testing.T) {
		if got := formatError(errors.New("boom")); got != "boom" {
			t.Fatalf("got %q, want boom", got)
		}
	})
}
