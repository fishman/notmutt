package compose

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
)

var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		highlighting.NewHighlighting(highlighting.WithStyle("dracula"), highlighting.WithGuessLanguage(true)),
	),
)

// markdownAlternatives returns the plain representation and safe rendered HTML.
func markdownAlternatives(source string) (string, []byte, error) {
	var html bytes.Buffer
	if err := markdownRenderer.Convert([]byte(source), &html); err != nil {
		return "", nil, err
	}
	return markdownText(source), html.Bytes(), nil
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
