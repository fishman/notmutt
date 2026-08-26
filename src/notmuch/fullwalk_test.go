// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"notmutt/core"
	"time"
)

// fullwalkHold pins the backend past the walk: the binding's DB wrapper
// has a GC finalizer that destroys the C DB once the wrapper is
// unreachable - a probe that drops the backend mid-walk lets GC kill
// the DB under the C iterator (SIGSEGV). The client is safe (the
// Worker holds the backend for its lifetime); probes must pin it.
var fullwalkHold *CGOBackend

// TestFullWalkProbe measures the full-emission thread walk against the
// real DB: NOTMUTT_FULLWALK=1. One pass emits every thread summary plus
// every message row - the hypothetical replacement for the
// stub-walk + per-thread-fetch two-phase hydration. Read-only.
func TestFullWalkProbe(t *testing.T) {
	if os.Getenv("NOTMUTT_FULLWALK") == "" {
		t.Skip("set NOTMUTT_FULLWALK=1 to run against the real DB")
	}
	const (
		missing28 = "00000000000028e4"
		missing16 = "0000000000016fbf"
	)
	ctx := context.Background()
	b := NewCGO()
	fullwalkHold = b
	if err := b.Open(ctx, ""); err != nil {
		t.Fatal(err)
	}
	uuid, rev, err := b.Revision(ctx)
	fmt.Printf("revision: rev=%d uuid=%s err=%v\n", rev, uuid, err)

	w, err := b.db.NewFullWalk("tag:inbox", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	msgs := 0
	threads := map[string]bool{}
	found28, found16 := false, false
	chunks := 0
	start := time.Now()
	first := time.Now()
	for {
		rows, done, err := w.Next(5000)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if chunks == 0 {
			fmt.Printf("first chunk (%d messages) in %s\n", len(rows), time.Since(first))
		}
		chunks++
		for _, m := range rows {
			msgs++
			threads[m.ThreadID] = true
			if m.ThreadID == missing28 {
				found28 = true
			}
			if m.ThreadID == missing16 {
				found16 = true
			}
		}
	}
	fmt.Printf("FULL WALK: threads=%d msgs=%d chunks=%d elapsed=%s\n",
		len(threads), msgs, chunks, time.Since(start))
	fmt.Printf("missing threads in walk: 28e4=%v 16fbf=%v\n", found28, found16)

	// the client ingest path: the same walk through Backend.Query with
	// the field conversion (DecodeSubject, refsSplit, copies) - the
	// delta against the raw walk is the client-side convert cost
	rows := 0
	var walked []core.Message
	start = time.Now()
	if err := b.Query(ctx, "tag:inbox", 0, false, func(chunk []core.Message) bool {
		rows += len(chunk)
		walked = append(walked, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("CLIENT INGEST: rows=%d elapsed=%s\n", rows, time.Since(start))

	// diff the walk's message ids against the CLI's matched set - the
	// walk must be exactly the query's messages, no more, no less
	out, err := exec.Command("notmuch", "search", "--format=json", "--output=messages", "tag:inbox").Output()
	if err != nil {
		t.Fatal(err)
	}
	var cliIDs []string
	if err := json.Unmarshal(out, &cliIDs); err != nil {
		t.Fatal(err)
	}
	cli := map[string]bool{}
	for _, id := range cliIDs {
		cli[strings.TrimPrefix(id, "id:")] = true
	}
	walk := map[string]bool{}
	for _, m := range walked {
		walk[m.ID] = true
	}
	var extra, missing []string
	for id := range walk {
		if !cli[id] {
			extra = append(extra, id)
		}
	}
	for id := range cli {
		if !walk[id] {
			missing = append(missing, id)
		}
	}
	fmt.Printf("ID DIFF: walk=%d cli=%d extra=%d missing=%d\n", len(walk), len(cli), len(extra), len(missing))
	// the refs the walk shipped: chain id format and exact-row matches
	// (the parent-edge comparison's precondition - bare ids, no
	// brackets, no "id:" prefix). Stock builds pack empty chains.
	refd, chainIDs, matched, prefixed := 0, 0, 0, 0
	var prefixedSample, bracketSample, chainSample []string
	for _, m := range walked {
		if len(m.References) > 0 {
			refd++
		}
		for _, r := range m.References {
			chainIDs++
			if len(chainSample) < 3 {
				chainSample = append(chainSample, m.ID+" -> "+r)
			}
			if walk[r] {
				matched++
			}
			if strings.HasPrefix(r, "id:") {
				prefixed++
				if len(prefixedSample) < 3 {
					prefixedSample = append(prefixedSample, r)
				}
			}
			if strings.ContainsAny(r, "<>") {
				if len(bracketSample) < 3 {
					bracketSample = append(bracketSample, r)
				}
			}
		}
	}
	fmt.Printf("REFS: msgs-with-chains=%d chain-ids=%d exact-row-matches=%d id:-prefixed=%d prefixed=%v brackets=%v samples=%v\n",
		refd, chainIDs, matched, prefixed, prefixedSample, bracketSample, chainSample)
	byID := map[string]core.Message{}
	for _, m := range walked {
		byID[m.ID] = m
	}
	for _, id := range extra {
		r, ok := byID[id]
		ex := "missing row"
		if ok && len(r.Paths) > 0 {
			ex = fmt.Sprintf("path-exists=%v path=%s", fileExists(r.Paths[0]), r.Paths[0])
		}
		fmt.Printf("  EXTRA (in walk, not cli): %s tags=%v %s\n", id, r.Tags, ex)
	}
	for _, id := range missing {
		fmt.Printf("  MISSING (in cli, not walk): %s\n", id)
	}

	// cross-check the walk against the per-thread fetch (the current
	// runtime path): same message ID sets means the walk decodes to
	// exactly what the two-phase design fetched.
	w2, err := b.db.NewFullWalk("thread:"+missing28+" or thread:"+missing16, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for {
		rows, done, err := w2.Next(100)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		for _, m := range rows {
			for _, msg := range m.Msgs {
				got[m.ThreadID] = append(got[m.ThreadID], msg.ID)
			}
		}
	}
	w2.Close()
	for _, tid := range []string{missing28, missing16} {
		fetched, err := b.Thread(ctx, tid)
		fmt.Printf("cross-check %s: fetch=%d walk=%d ids-equal=%v err=%v\n",
			tid, len(fetched), len(got[tid]), idsEqual(fetched, got[tid]), err)
		if tid == missing28 {
			for i, m := range fetched {
				fmt.Printf("  fetch[%d]=%q  walk[%d]=%q\n", i, m.ID, i, got[tid][i])
			}
		}
	}
	// the FLAT walk (the unread/deleted/search shape): one row per
	// MATCHED message, no thread drag - its id set must equal the
	// CLI's --output=messages exactly
	var flatMsgs []core.Message
	start = time.Now()
	if err := b.Query(ctx, "tag:unread", 0, true, func(chunk []core.Message) bool {
		for i, m := range chunk {
			if i < 3 {
				mid := m.ID
				if len(mid) > 20 {
					mid = mid[:20]
				}
				fmt.Printf("  FLAT ROW[%d]: id=%-20s tid=%s tags=%v\n", i, mid, m.ThreadID, m.Tags)
			}
		}
		flatMsgs = append(flatMsgs, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("notmuch", "search", "--format=json", "--output=messages", "tag:unread").Output()
	if err != nil {
		t.Fatal(err)
	}
	var cliU []string
	if err := json.Unmarshal(out, &cliU); err != nil {
		t.Fatal(err)
	}
	cliM := map[string]bool{}
	for _, id := range cliU {
		cliM[strings.TrimPrefix(id, "id:")] = true
	}
	flatM := map[string]bool{}
	for _, m := range flatMsgs {
		flatM[m.ID] = true
	}
	var extraF, missingF []string
	for id := range flatM {
		if !cliM[id] {
			extraF = append(extraF, id)
		}
	}
	for id := range cliM {
		if !flatM[id] {
			missingF = append(missingF, id)
		}
	}
	fmt.Printf("FLAT WALK: rows=%d cli=%d extra=%d missing=%d elapsed=%s\n",
		len(flatM), len(cliM), len(extraF), len(missingF), time.Since(start))
	for _, id := range extraF {
		fmt.Printf("  FLAT EXTRA (in walk, not cli): %s\n", id)
	}
	for _, id := range missingF {
		fmt.Printf("  FLAT MISSING (in cli, not walk): %s\n", id)
	}

	// the DB must survive the walk: finalizer guard
	_, rev, err = b.Revision(ctx)
	fmt.Printf("post-walk revision: rev=%d err=%v (DB alive)\n", rev, err)
}

func fileExists(p string) bool {
	if _, err := os.Stat(p); err != nil {
		return false
	}
	return true
}

func idsEqual(a []core.Message, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, m := range a {
		seen[m.ID]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
