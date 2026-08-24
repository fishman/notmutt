// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
)

// scheduleLock is the spool mutex (flock): multiple notmutt instances
// share the spool, and only the lock holder scans and delivers due
// mail - a second instance's check no-ops, so a mail is never
// delivered twice. The kernel releases the lock on process death, so
// a crashed holder never wedges the spool.
type scheduleLock struct{ f *os.File }

func acquireScheduleLock(dir string) (*scheduleLock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err // busy: another instance is delivering
	}
	return &scheduleLock{f: f}, nil
}

func (l *scheduleLock) Close() error {
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
