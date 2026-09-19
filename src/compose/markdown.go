package compose

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
)

// MarkdownSignatureDirective separates the authored body from its signature.
// It is editor-facing Markdown metadata, never emitted on the wire.
const MarkdownSignatureDirective = "<!-- notmutt-signature -->"

var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		highlighting.NewHighlighting(highlighting.WithStyle("dracula"), highlighting.WithGuessLanguage(true)),
	),
)

// MessageBody is the editor and assembly source for a dialogue.
func MessageBody(s State) string {
	if !s.Markdown || s.SignatureBody == "" {
		return BodyWithSig(s.Body, s.SignatureBody)
	}
	return strings.TrimRight(s.Body, "\n") + "\n\n" + MarkdownSignatureDirective + "\n\n" + s.SignatureBody
}

// MarkdownHTML renders compose Markdown for the TUI preview and HTML MIME part.
func MarkdownHTML(source string) ([]byte, error) {
	_, html, err := markdownAlternatives(source)
	return html, err
}

// markdownAlternatives creates the text/plain and text/html alternatives.
func markdownAlternatives(source string) (string, []byte, error) {
	body, signature := splitMarkdownSignature(source)
	plain := markdownText(body)
	if signature != "" {
		plain += "\n\n-- \n" + markdownText(signature)
	}
	html, err := renderMarkdown(body)
	if err != nil {
		return "", nil, err
	}
	if signature != "" {
		sig, err := renderMarkdown(signature)
		if err != nil {
			return "", nil, err
		}
		html = append(html, []byte("<hr>\n")...)
		html = append(html, sig...)
	}
	return plain, html, nil
}

func splitMarkdownSignature(source string) (string, string) {
	parts := strings.SplitN(source, "\n"+MarkdownSignatureDirective+"\n", 2)
	if len(parts) != 2 {
		return source, ""
	}
	return strings.TrimRight(parts[0], "\n"), strings.TrimSpace(parts[1])
}

func renderMarkdown(source string) ([]byte, error) {
	var html bytes.Buffer
	if err := markdownRenderer.Convert([]byte(source), &html); err != nil {
		return nil, err
	}
	return html.Bytes(), nil
}

func markdownText(source string) string {
	var out []string
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-*+ ")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "`", "")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
