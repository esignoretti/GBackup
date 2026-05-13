---
phase: code-review-fix-verification
reviewed: 2026-05-13T17:00:00Z
depth: standard
files_reviewed: 5
files_reviewed_list:
  - cmd/restore.go
  - cmd/status.go
  - internal/backup/drive.go
  - internal/backup/runner.go
  - internal/restore/drive.go
findings:
  critical: 0
  warning: 1
  info: 0
  total: 1
status: issues_found
---

# Code Review Fix Verification Report

**Reviewed:** 2026-05-13T17:00:00Z
**Depth:** standard
**Files Reviewed:** 5
**Status:** issues_found
**Range:** `d5e866c..52f94c2`

## Summary

Verified commit `52f94c2c5d19f83c479d6b32533abb9e4921328e` which addresses all 7 critical issues from the previous review. Six of the seven fixes are **fully correct**. One fix introduces a minor regression in error-reporting priority.

---

## CR Fix Verification

### CR-01: Drive backup should filter files by `'user' in owners` query param

**File:** `internal/backup/drive.go:105`

✅ **Fixed correctly**

The `.Q(fmt.Sprintf("'%s' in owners", user))` call has been added to the `Files.List()` query chain. This correctly scopes the Drive API query to only return files owned by the specified user, preventing the backup from scanning all domain files.

---

### CR-02: Restore should impersonate `targetUser` not `AdminEmail`

**File:** `internal/restore/drive.go:28-37`

✅ **Fixed correctly**

The restore service now impersonates `targetUser` (derived from `r.TargetUser`, falling back to `r.User`) instead of `r.AdminEmail`. When `--target-user` is provided, files are uploaded as that user; otherwise, the source backup user is used.

---

### CR-03: Restore `--date` flag should filter items by ModifiedAt

**File:** `internal/restore/drive.go:47-59`

✅ **Fixed correctly**

Date filtering is now implemented:
- Parses `r.Date` as `YYYY-MM-DD` 
- The predicate `!item.ModifiedAt.After(cutoff.Add(24 * time.Hour))` correctly includes items modified on or before the cutoff date
- `r.Date == "" || r.Date == "latest"` skips filtering entirely
- Filtered list replaces `items` before the restore loop

---

### CR-04: Gzip copy and close errors should be checked

**File:** `internal/backup/drive.go:145-152`

✅ **Fixed correctly** (with a minor concern — see Warnings)

Both `io.Copy` and `gw.Close()` errors are now captured and checked. The `content.Close()` is called before the gzip check, which is the correct ordering. See Warning WR-01 below for the masked error concern.

---

### CR-05: `resolveUsers` with `"*"` should call `DirectoryService.ListUsers()`

**File:** `internal/backup/runner.go:72-90`

✅ **Fixed correctly**

When `cfg.Users.Include == "*"` and `DirAuth` is configured:
1. Creates a `DirectoryService`
2. Calls `ListUsers(ctx)` 
3. Filters out suspended users
4. Returns non-suspended emails
5. Falls back to `AdminEmail` on any failure or empty result

---

### CR-06: `resolveStatusUsers` should split the include list, not return Exclude

**File:** `cmd/status.go:53-62`

✅ **Fixed correctly**

The original bug `return cfg.Users.Exclude` (which returned excluded users as the include list) is replaced with `strings.Split(cfg.Users.Include, ",")` with whitespace trimming, correctly parsing the comma-separated include list.

---

### CR-07: Restore should always dry-run first with confirmation prompt

**File:** `cmd/restore.go`

✅ **Fixed correctly**

The restore command now:
1. Always creates `DriveRestore` with `DryRun: true`
2. First `r.Run()` always runs as a dry-run (preview)
3. If user did NOT pass `--dry-run`: prompts `[y/N]` for confirmation
4. On confirmation, sets `DryRun = false` and re-runs for real
5. If user passed `--dry-run`: skips the prompt entirely

---

## Warnings

### WR-01: Gzip close error may mask copy error

**File:** `internal/backup/drive.go:147-152`

**Issue:** The error-checking order means that if both `io.Copy` and `gw.Close()` fail, the close error is returned and the copy error is silently discarded. In Go's gzip implementation, `io.Copy` returns the writer's `Write` error, which is typically the root cause (e.g., compression failure). The `gw.Close()` error may be a less informative consequence of the same failure.

```go
// current code (lines 145-152):
_, copyErr := io.Copy(gw, content)
content.Close()
if closeErr := gw.Close(); closeErr != nil {
    return count, fmt.Errorf("gzip close: %w", closeErr)  // masked copyErr
}
if copyErr != nil {
    return count, fmt.Errorf("gzip copy: %w", copyErr)
}
```

**Fix:** Combine both errors using `errors.Join` (Go 1.20+) so neither is lost:

```go
_, copyErr := io.Copy(gw, content)
content.Close()
closeErr := gw.Close()
if copyErr != nil || closeErr != nil {
    return count, fmt.Errorf("gzip error: %w", errors.Join(copyErr, closeErr))
}
```

This requires adding `"errors"` to the import block in `internal/backup/drive.go`.

---

## Assessment

| CR | Status |
|---|---|
| CR-01: Drive `.Q(` filter | ✅ Fixed correctly |
| CR-02: Impersonate `targetUser` | ✅ Fixed correctly |
| CR-03: Date filtering by ModifiedAt | ✅ Fixed correctly |
| CR-04: Gzip error checking | ✅ Fixed correctly (minor warning) |
| CR-05: `resolveUsers` Directory API | ✅ Fixed correctly |
| CR-06: `resolveStatusUsers` include split | ✅ Fixed correctly |
| CR-07: Restore always dry-run first | ✅ Fixed correctly |

**6 of 7 fixes are fully correct. 1 fix (CR-04) is functionally correct but introduces a minor error-masking concern.**

**Verdict: Ready to merge** — The WR-01 issue is minor (edge case of double failure on gzip) and does not block shipping. Consider fixing in a follow-up.

---

_Reviewed: 2026-05-13T17:00:00Z_
_Reviewer: gsd-code-reviewer (fix verification)_
_Depth: standard_
