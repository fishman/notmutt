package compose

import "regexp"

// ContentTypeOf derives the body part's MIME type: text/plain by
// default, text/markdown when the body carries markdown syntax. ONE
// definition - the compose row and Assemble share it. The heuristic is
// conservative: at least TWO distinct constructs must match, so a plain
// reply (quote lines, one signature) never flips.
var (
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s`)
	mdFence   = regexp.MustCompile("(?m)^\\s*```")
	mdLink    = regexp.MustCompile(`\[[^\]]+\]\([^)]+\)`)
	mdBullet  = regexp.MustCompile(`(?m)^\s*[-*+]\s`)
	mdBold    = regexp.MustCompile(`\*\*[^*]+\*\*|__[^_]+__`)
)

func ContentTypeOf(body string) string {
	n := 0
	for _, re := range []*regexp.Regexp{mdHeading, mdFence, mdLink, mdBullet, mdBold} {
		if re.MatchString(body) {
			n++
		}
	}
	if n >= 2 {
		return "text/markdown"
	}
	return "text/plain"
}
