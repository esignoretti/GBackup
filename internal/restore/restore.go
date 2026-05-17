package restore

import "context"

// Restorer is the common interface for all per-service restore operations.
type Restorer interface {
	// Run performs the restore. If DryRun is set on the underlying struct,
	// the implementation must only preview without making changes.
	Run(ctx context.Context) error
	// SetDryRun toggles dry-run mode.
	SetDryRun(bool)
}
