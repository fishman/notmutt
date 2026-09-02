// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build cli

package notmuch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"notmutt/core"
)

// CLIBackend drives the notmuch CLI. argv only, never a shell (F4).
type CLIBackend struct {
	run runFn
}

func NewCLI() *CLIBackend {
	return &CLIBackend{run: defaultRun}
}

func (b *CLIBackend) Open(ctx context.Context, dbPath string) error {
	return nil
}

func (b *CLIBackend) Close(ctx context.Context) error {
	return nil
}

// Reopen is a no-op: stateless, every call spawns a fresh subprocess
// that always sees the current state.
func (b *CLIBackend) Reopen(ctx context.Context) error { return nil }

type searchItem struct {
	Thread    string   `json:"thread"`
	Timestamp int64    `json:"timestamp"`
	Authors   string   `json:"authors"`
	Subject   string   `json:"subject"`
	Tags      []string `json:"tags"`
}

// Query walks the whole result in one call: one `notmuch search`
// subprocess, one summary per thread (DB-side fields, zero file
// opens). json emits nothing until the mset is computed (write-at-end,
// measured 4.8s for a 33k-thread inbox; json0 unsupported), so the
// parse is buffered and chunks are sliced from the result. limit
// passes through as `--limit=`; emit false stops early; nil emit
// collects nothing. The refresh groups by ThreadID, so the stub's ID
// stays empty - per-message data comes from Thread, on open only.
func (b *CLIBackend) Query(ctx context.Context, query string, limit int, flat bool, emit func([]core.Message) bool) error {
	// The flat views under the escape hatch: one bare id per matched
	// message (--output=messages). Summary data needs show (file opens) -
	// degraded by design; cgo serves full rows.
	if flat {
		args := []string{"search", "--format=json", "--output=messages"}
		if limit > 0 {
			args = append(args, "--limit="+strconv.Itoa(limit))
		}
		args = append(args, query)
		out, err := b.run(ctx, "notmuch", args)
		if err != nil {
			return fmt.Errorf("notmuch search: %w: %s", err, strings.TrimSpace(string(out)))
		}
		var ids []string
		if err := json.Unmarshal(out, &ids); err != nil {
			return fmt.Errorf("notmuch search: parse: %w", err)
		}
		if emit == nil {
			return nil
		}
		i := 0
		size := firstChunk
		for i < len(ids) {
			hi := min(i+size, len(ids))
			msgs := make([]core.Message, 0, hi-i)
			for _, id := range ids[i:hi] {
				id = strings.TrimPrefix(id, "id:")
				msgs = append(msgs, core.Message{ID: id, ThreadID: id})
			}
			if !emit(msgs) {
				return nil
			}
			i = hi
			size = steadyChunk
		}
		return nil
	}
	args := []string{"search", "--format=json", "--sort=newest-first"}
	if limit > 0 {
		args = append(args, "--limit="+strconv.Itoa(limit))
	}
	args = append(args, query)
	out, err := b.run(ctx, "notmuch", args)
	if err != nil {
		return fmt.Errorf("notmuch search: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var items []searchItem
	if err := json.Unmarshal(out, &items); err != nil {
		return fmt.Errorf("notmuch search: parse: %w", err)
	}
	if emit == nil {
		return nil
	}
	i := 0
	size := firstChunk
	for i < len(items) {
		hi := min(i+size, len(items))
		msgs := make([]core.Message, 0, hi-i)
		for _, it := range items[i:hi] {
			msgs = append(msgs, core.Message{ThreadID: it.Thread, Timestamp: it.Timestamp, Author: it.Authors, Subject: core.DecodeSubject(it.Subject), Tags: it.Tags})
		}
		if !emit(msgs) {
			return nil
		}
		i = hi
		size = steadyChunk
	}
	return nil
}

// CountMsgs returns the message count for the query - the flat fill's
// progress total.
func (b *CLIBackend) CountMsgs(ctx context.Context, query string) (int, error) {
	args := []string{"count", "--output=messages", query}
	out, err := b.run(ctx, "notmuch", args)
	if err != nil {
		return 0, fmt.Errorf("notmuch count: %w: %s", err, strings.TrimSpace(string(out)))
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("notmuch count: parse: %w", err)
	}
	return n, nil
}

// QueryMsgs walks a message-level query (delta scans): one
// `notmuch search --output=messages` subprocess. The json "id:"
// prefix is stripped (the engine re-adds it for query terms).
func (b *CLIBackend) QueryMsgs(ctx context.Context, query string, emit func([]core.Message) bool) error {
	out, err := b.run(ctx, "notmuch", []string{"search", "--format=json", "--output=messages", query})
	if err != nil {
		return fmt.Errorf("notmuch search: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var ids []string
	if err := json.Unmarshal(out, &ids); err != nil {
		return fmt.Errorf("notmuch search: parse: %w", err)
	}
	if emit == nil {
		return nil
	}
	rows := make([]core.Message, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, core.Message{ID: strings.TrimPrefix(id, "id:")})
	}
	for i := 0; i < len(rows); i += steadyChunk {
		hi := min(i+steadyChunk, len(rows))
		if !emit(rows[i:hi]) {
			return nil
		}
	}
	return nil
}

// Snapshots fetches per-message tags and paths via `notmuch show
// --format=json --body=false` - the escape-hatch implementation (cgo
// reads the header cache in-process). Thread id is unknown here and
// never needed.
func (b *CLIBackend) Snapshots(ctx context.Context, ids []string) ([]Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	terms := make([]string, len(ids))
	for i, id := range ids {
		terms[i] = "id:" + id
	}
	out, err := b.run(ctx, "notmuch", []string{"show", "--format=json", "--body=false", "(" + strings.Join(terms, " or ") + ")"})
	if err != nil {
		return nil, fmt.Errorf("notmuch show: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var groups [][]showNode
	if err := json.Unmarshal(out, &groups); err != nil {
		return nil, fmt.Errorf("notmuch show: parse: %w", err)
	}
	got := walkGroups(groups, "")
	byID := make(map[string]*Message, len(got))
	for i := range got {
		byID[strings.TrimPrefix(got[i].ID, "id:")] = &got[i]
	}
	outMsgs := make([]Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			outMsgs = append(outMsgs, *m)
		}
	}
	return outMsgs, nil
}

// Count returns the number of threads matching the query - the fill's
// progress total (the view query is a thread query). argv only (F4).
func (b *CLIBackend) Count(ctx context.Context, query string) (int, error) {
	args := []string{"count", "--output=threads", query}
	out, err := b.run(ctx, "notmuch", args)
	if err != nil {
		return 0, fmt.Errorf("notmuch count: %w: %s", err, strings.TrimSpace(string(out)))
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("notmuch count: parse: %w", err)
	}
	return n, nil
}

type showMsg struct {
	ID        string            `json:"id"`
	Timestamp int64             `json:"timestamp"`
	Tags      []string          `json:"tags"`
	Filename  []string          `json:"filename"`
	Headers   map[string]string `json:"headers"`
}

// showNode is one [message|null, [children]] pair of the show json tree.
type showNode struct {
	Msg      *showMsg
	Children []showNode
}

func (n *showNode) UnmarshalJSON(b []byte) error {
	var parts [2]json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	if string(parts[0]) != "null" {
		var m showMsg
		if err := json.Unmarshal(parts[0], &m); err != nil {
			return err
		}
		n.Msg = &m
	}
	return json.Unmarshal(parts[1], &n.Children)
}

// Thread fetches one thread's messages. show json carries no thread ids
// and no per-message reference lists: ThreadID comes from the query
// argument, References from the tree nesting (root-first chain; the view
// picks the nearest ancestor).
func (b *CLIBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	out, err := b.run(ctx, "notmuch", []string{"show", "--format=json", "--body=false", "thread:" + threadID})
	if err != nil {
		return nil, fmt.Errorf("notmuch show: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var groups [][]showNode
	if err := json.Unmarshal(out, &groups); err != nil {
		return nil, fmt.Errorf("notmuch show: parse: %w", err)
	}
	return walkGroups(groups, threadID), nil
}

func walkGroups(groups [][]showNode, threadID string) []core.Message {
	var msgs []core.Message
	var walk func(nodes []showNode, chain []string)
	walk = func(nodes []showNode, chain []string) {
		for _, n := range nodes {
			if n.Msg == nil {
				walk(n.Children, chain)
				continue
			}
			refs := make([]string, len(chain))
			copy(refs, chain)
			msgs = append(msgs, core.Message{
				ID: n.Msg.ID, ThreadID: threadID, Timestamp: n.Msg.Timestamp,
				Author: n.Msg.Headers["From"], Subject: core.DecodeSubject(n.Msg.Headers["Subject"]),
				Tags: n.Msg.Tags, Paths: n.Msg.Filename, References: refs,
			})
			walk(n.Children, append(refs, n.Msg.ID))
		}
	}
	for _, g := range groups {
		walk(g, nil)
	}
	return msgs
}

func (b *CLIBackend) Addresses(ctx context.Context, query string) ([]core.AddressEntry, error) {
	out, err := b.run(ctx, "notmuch", []string{"address", "--deduplicate=address", "--output=sender", query})
	if err != nil {
		return nil, fmt.Errorf("notmuch address: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var got []core.AddressEntry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if lt := strings.Index(line, " <"); lt >= 0 && strings.HasSuffix(line, ">") {
			got = append(got, core.AddressEntry{Name: line[:lt], Addr: line[lt+2 : len(line)-1]})
		} else {
			got = append(got, core.AddressEntry{Addr: line})
		}
	}
	return got, nil
}

func (b *CLIBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	args := []string{"tag"}
	for _, op := range ops {
		if op.Add {
			args = append(args, "+"+op.Tag)
		} else {
			args = append(args, "-"+op.Tag)
		}
	}
	args = append(args, query)
	out, err := b.run(ctx, "notmuch", args)
	if err != nil {
		return fmt.Errorf("notmuch tag: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Revision parses "count\tuuid\trevision" from `notmuch count --lastmod`.
func (b *CLIBackend) Revision(ctx context.Context) (string, uint64, error) {
	out, err := b.run(ctx, "notmuch", []string{"count", "--lastmod", ""})
	if err != nil {
		return "", 0, fmt.Errorf("notmuch count: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(string(bytes.TrimSpace(out)))
	if len(fields) != 3 {
		return "", 0, fmt.Errorf("notmuch count --lastmod: expected 3 fields, got %q", string(out))
	}
	rev, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("notmuch count --lastmod: bad revision %q", fields[2])
	}
	return fields[1], rev, nil
}

func (b *CLIBackend) New(ctx context.Context) (uint64, uint64, error) {
	_, pre, err := b.Revision(ctx)
	if err != nil {
		return 0, 0, err
	}
	out, err := b.run(ctx, "notmuch", []string{"new"})
	if err != nil {
		return 0, 0, fmt.Errorf("notmuch new: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_, cur, err := b.Revision(ctx)
	if err != nil {
		return 0, 0, err
	}
	return pre, cur, nil
}

// AddPaths/RemovePaths are unsupported on the CLI backend: no
// add/remove-file command exists (`notmuch insert` runs post-new hooks,
// not the mover's shape); the poll's own `notmuch new` reconciles
// moved files one cycle later.
func (b *CLIBackend) AddPaths(ctx context.Context, paths []string) error {
	return fmt.Errorf("notmuch add: unsupported by the cli backend: %w", ErrUnsupported)
}

func (b *CLIBackend) RemovePaths(ctx context.Context, paths []string) error {
	return fmt.Errorf("notmuch remove: unsupported by the cli backend: %w", ErrUnsupported)
}
