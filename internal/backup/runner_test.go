package backup

import (
	"testing"
)

func TestNewRunner(t *testing.T) {
	r := NewRunner(&RunnerConfig{})
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestRunnerNeedsInit(t *testing.T) {
	r := NewRunner(&RunnerConfig{})
	err := r.Run(nil, false)
	if err == nil {
		t.Fatal("expected error without init")
	}
}
