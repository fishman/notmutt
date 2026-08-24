// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package app

import (
	"errors"
	"os"
)

// scheduleLock is the spool mutex. The flock implementation is unix
// (schedule_lock_unix.go); on other platforms the check refuses to
// run rather than deliver without the mutual exclusion (notmuch does
// not build there anyway).
type scheduleLock struct{ f *os.File }

func acquireScheduleLock(dir string) (*scheduleLock, error) {
	return nil, errors.New("scheduled mail requires the unix flock")
}

func (l *scheduleLock) Close() error { return nil }
