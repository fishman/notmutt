// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestProbeKittyFile(t *testing.T) {
	query := kittyGraphicsQuery()
	for _, tc := range []struct {
		name, reply, pending string
		want                 bool
	}{
		{"supported", probeAck, "", true},
		// A reply larger than the read buffer must span multiple reads.
		{"multiple-reads-with-input", "key\x1b_G" + strings.Repeat("p=1,", 80) + "i=" + kittyProbeID + ";OK\x1b\\", "key", true},
		{"timeout-with-input", "key", "key", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			client, server := os.NewFile(uintptr(fds[0]), "probe"), os.NewFile(uintptr(fds[1]), "terminal")
			defer client.Close()
			defer server.Close()
			done := make(chan error, 1)
			go func() {
				buf := make([]byte, len(query))
				if _, err := io.ReadFull(server, buf); err != nil {
					done <- err
					return
				}
				if !bytes.Equal(buf, query) {
					done <- fmt.Errorf("unexpected query: %q", buf)
					return
				}
				_, err := server.Write([]byte(tc.reply))
				done <- err
			}()
			const timeout = kittyProbeTimeout
			start := time.Now()
			supported, pending := probeKittyFile(client, timeout)
			if elapsed := time.Since(start); elapsed > time.Second || (!tc.want && elapsed < timeout) {
				t.Fatalf("probe did not respect its deadline: %s", elapsed)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("terminal did not receive the query")
			}
			if supported != tc.want || string(pending) != tc.pending {
				t.Fatalf("got (%t, %q), want (%t, %q)", supported, pending, tc.want, tc.pending)
			}
		})
	}
}
