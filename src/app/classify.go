// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lastClassifyPath is the reconciling poll's floor file (the
// classify-state store, poll-classify-reconcile design): the last
// database revision the filter applied folder rules through. It shares
// the poll stamp's cache directory (pollStampPath) so the CLI poll and
// every running client read and advance the same floor.
func lastClassifyPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "last-classify"
	}
	return filepath.Join(base, "notmutt", "last-classify")
}

// readLastClassify loads the persisted L revision. A missing file is
// "no L yet" (the caller baselines to the present); a corrupt file
// warns and is treated as no L - re-classify the discovery bracket,
// never a full backfill.
func readLastClassify() (uint64, bool) {
	b, err := os.ReadFile(lastClassifyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false
		}
		diag.Warn("last-classify", "err", err.Error())
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		diag.Warn("last-classify", "corrupt", string(b))
		return 0, false
	}
	return v, true
}

// writeLastClassify persists L atomically (temp + rename, 0600). The
// higher-of-two guard keeps a concurrent process's advance from being
// regressed; a regressed floor would only cost an idempotent re-run,
// but the guard keeps the common quiet-mailbox case quiet.
func writeLastClassify(v uint64) error {
	p := lastClassifyPath()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("last-classify: %w", err)
	}
	tmp := filepath.Join(dir, ".last-classify.tmp")
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(v, 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("last-classify: %w", err)
	}
	if cur, err := os.ReadFile(p); err == nil {
		if have, err := strconv.ParseUint(strings.TrimSpace(string(cur)), 10, 64); err == nil && have > v {
			os.Remove(tmp)
			return nil
		}
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("last-classify: %w", err)
	}
	return nil
}
