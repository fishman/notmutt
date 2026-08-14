package notmuch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"notmutt/core"
)

type runFn func(ctx context.Context, name string, args []string) ([]byte, error)

// CLIBackend drives the notmuch CLI. argv only, never a shell (F4).
type CLIBackend struct {
	run runFn
}

func NewCLI() *CLIBackend {
	return &CLIBackend{run: defaultRun}
}

func defaultRun(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}

func (b *CLIBackend) Open(ctx context.Context, dbPath string) error {
	return nil
}

func (b *CLIBackend) Close(ctx context.Context) error {
	return nil
}

type searchItem struct {
	Thread    string   `json:"thread"`
	Timestamp int64    `json:"timestamp"`
	Authors   string   `json:"authors"`
	Subject   string   `json:"subject"`
	Tags      []string `json:"tags"`
}

// Query returns one stub per matching thread. The search summary carries
// thread ids and thread-level fields only; per-message data (ids,
// filenames, headers) comes from Thread. The refresh cycle groups by
// ThreadID and fetches full threads, so the stub's ID stays empty.
func (b *CLIBackend) Query(ctx context.Context, query string, limit, offset int) ([]core.Message, error) {
	args := []string{"search", "--format=json", "--sort=newest-first"}
	if limit > 0 {
		args = append(args, "--limit="+strconv.Itoa(limit))
	}
	if offset > 0 {
		args = append(args, "--offset="+strconv.Itoa(offset))
	}
	args = append(args, query)
	out, err := b.run(ctx, "notmuch", args)
	if err != nil {
		return nil, fmt.Errorf("notmuch search: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var items []searchItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("notmuch search: parse: %w", err)
	}
	msgs := make([]core.Message, len(items))
	for i, it := range items {
		msgs[i] = core.Message{ThreadID: it.Thread, Timestamp: it.Timestamp, Author: it.Authors, Subject: it.Subject, Tags: it.Tags}
	}
	return msgs, nil
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
				Author: n.Msg.Headers["From"], Subject: n.Msg.Headers["Subject"],
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

func (b *CLIBackend) New(ctx context.Context) error {
	out, err := b.run(ctx, "notmuch", []string{"new"})
	if err != nil {
		return fmt.Errorf("notmuch new: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
