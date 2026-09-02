package app

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

// The pager export (the E key): one generic export job over a message,
// PDF the current form. Each form declares its renderer argv - the
// message's html document rides the renderer's stdin, the target path is
// the last element. A future html/txt/print form adds a table row, not a
// new job (F4: argv exec only, never shell-interpolated).

// exportForm describes one message export form.
type exportForm struct {
	ext  string   // the filename suffix (".pdf")
	argv []string // the renderer: binary + fixed flags
}

var exportForms = map[string]exportForm{
	"pdf": {ext: ".pdf", argv: []string{"weasyprint", "--no-http-redirects"}},
}

// exportMessage turns one message into an exported file at target. A
// folder target (a trailing "/" or an existing directory) receives the
// generated YYYYMMDD-<slug>.pdf name from the message's own date and
// subject; anything else is the literal file path. The renderer runs with
// the message's html on stdin (never a temp file with content) and writes
// a 0600 temp that renames over the target only on success - a failed run
// leaves no partial file behind.
func exportMessage(worker workerAPI, bus *core.Bus, views map[string]*core.View, threadID, msgID, target, form string) {
	f, ok := exportForms[form]
	if !ok {
		bus.Publish(core.ExportPdfResult{Err: fmt.Errorf("export: unknown form %q", form)})
		return
	}
	msgs := threadFromViews(views, threadID)
	if msgs == nil {
		var err error
		msgs, err = fetchMsgs(worker, threadID, msgID)
		if err != nil {
			bus.Publish(core.ExportPdfResult{Err: err})
			return
		}
	}
	msg, ok := findMsg(msgs, msgID)
	if !ok || len(msg.Paths) == 0 {
		bus.Publish(core.ExportPdfResult{Err: fmt.Errorf("no message path")})
		return
	}
	doc, err := messageHTML(msg.Paths[0])
	if err != nil {
		bus.Publish(core.ExportPdfResult{Err: err})
		return
	}
	file, err := exportFile(target, f.ext, msg.Subject, msg.Timestamp)
	if err != nil {
		bus.Publish(core.ExportPdfResult{Err: err})
		return
	}
	if err := runExporter(f.argv, doc, file); err != nil {
		bus.Publish(core.ExportPdfResult{Err: err})
		return
	}
	bus.Publish(core.ExportPdfResult{ThreadID: threadID, Path: file})
}

// messageHTML is the document source an export form renders: the message's
// raw html part (weasyprint needs the markup, never the stripped text), or
// a <pre>-wrapped, escaped join of its text parts when the mail is plain.
func messageHTML(path string) (string, error) {
	m, err := mail.ParseMessage(path)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, p := range m.Parts {
		if p.HTML {
			return p.Body, nil
		}
		text.WriteString(p.Body)
		text.WriteByte('\n')
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("message has no readable body")
	}
	return "<html><body><pre>" + html.EscapeString(text.String()) + "</pre></body></html>", nil
}

// exportFile resolves the destination: a folder target (a trailing "/" or
// an existing directory) takes the generated name (message date + subject
// slug) and is created if missing; a bare path is written verbatim.
func exportFile(target, ext, subject string, ts int64) (string, error) {
	if st, err := os.Stat(target); err == nil && st.IsDir() || strings.HasSuffix(target, "/") {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return "", err
		}
		slug := exportSlug(subject)
		if slug == "" {
			slug = "message"
		}
		return filepath.Join(target, fmt.Sprintf("%s-%s%s", time.Unix(ts, 0).UTC().Format("20060102"), slug, ext)), nil
	}
	return target, nil
}

// exportSlug normalizes a subject into a filename piece: lowercase, runs
// of non word/dot characters collapse to a dash, trimmed.
func exportSlug(subject string) string {
	s := strings.ToLower(subject)
	var b strings.Builder
	last := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' {
			b.WriteRune(r)
			last = false
		} else if !last && b.Len() > 0 {
			b.WriteByte('-')
			last = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// runExporter runs the form's renderer over the doc onto file: the html
// rides stdin, the output lands in a 0600 temp beside the target and
// renames over it only on success. A literal-path target's directory must
// exist - it fails like any other write, never silently creating dirs.
func runExporter(argv []string, doc, file string) error {
	dir := filepath.Dir(file)
	out, err := os.CreateTemp(dir, ".notmutt-export-*.tmp")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp) // every return dies with the temp; a success renames it away first
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(append([]string{}, argv[1:]...), "-", tmp)...)
	cmd.Stdin = strings.NewReader(doc)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return fmt.Errorf("%s: %s", argv[0], msg)
		}
		return err
	}
	return os.Rename(tmp, file)
}
