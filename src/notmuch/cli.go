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
	return cmd.CombinedOutput()
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
	ID        string   `json:"id"`
}

// Query runs one search run plus one files run, pairing paths by index
// (maildir: one file per message; a count mismatch leaves Paths short).
func (b *CLIBackend) Query(ctx context.Context, query string, limit int) ([]core.Message, error) {
	args := []string{"search", "--format=json", "--sort=newest-first"}
	if limit > 0 {
		args = append(args, "--limit="+strconv.Itoa(limit))
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
		msgs[i] = core.Message{ID: it.ID, ThreadID: it.Thread, Timestamp: it.Timestamp, Author: it.Authors, Subject: it.Subject, Tags: it.Tags}
	}
	paths, err := b.run(ctx, "notmuch", []string{"search", "--output=files", "--sort=newest-first", query})
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(paths)), "\n")
		if lines[0] != "" {
			for i := range msgs {
				if i >= len(lines) {
					break
				}
				msgs[i].Paths = append(msgs[i].Paths, lines[i])
			}
		}
	}
	return msgs, nil
}

type showItem struct {
	ID         string   `json:"id"`
	Thread     string   `json:"thread"`
	Timestamp  int64    `json:"timestamp"`
	Authors    string   `json:"authors"`
	Subject    string   `json:"subject"`
	Tags       []string `json:"tags"`
	References []string `json:"references"`
}

// Thread fetches one thread's messages with references (show json is
// grouped by thread: a list of lists).
func (b *CLIBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	out, err := b.run(ctx, "notmuch", []string{"show", "--format=json", "--body=false", "thread:" + threadID})
	if err != nil {
		return nil, fmt.Errorf("notmuch show: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var groups [][]showItem
	if err := json.Unmarshal(out, &groups); err != nil {
		return nil, fmt.Errorf("notmuch show: parse: %w", err)
	}
	var msgs []core.Message
	for _, g := range groups {
		for _, it := range g {
			msgs = append(msgs, core.Message{
				ID: it.ID, ThreadID: it.Thread, Timestamp: it.Timestamp,
				Author: it.Authors, Subject: it.Subject, Tags: it.Tags, References: it.References,
			})
		}
	}
	return msgs, nil
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
