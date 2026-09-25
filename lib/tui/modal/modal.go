// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

type Box struct{ X, Y, Width, Height, BodyRows int }

// Bottom reserves one tab row and the requested footer rows.
func Bottom(width, height, bodyRows, footerRows int) (Box, bool) {
	available := height - footerRows - 3
	if width < 3 || footerRows < 0 || available < 1 {
		return Box{}, false
	}
	if bodyRows < 1 {
		bodyRows = 1
	}
	bodyRows = min(bodyRows, available)
	boxHeight := bodyRows + 2
	return Box{Y: height - footerRows - boxHeight, Width: width, Height: boxHeight, BodyRows: bodyRows}, true
}

type span struct{ lo, hi int }

// Wrap windows text by display cells while keeping the byte-offset cursor visible.
func Wrap(text string, cursorByte, cellWidth, maxRows int) ([]string, int, int) {
	cellWidth = max(1, cellWidth)
	maxRows = max(1, maxRows)
	cursorByte = min(max(cursorByte, 0), len(text))
	for cursorByte > 0 && cursorByte < len(text) && !utf8.RuneStart(text[cursorByte]) {
		cursorByte--
	}
	spans := make([]span, 0, 1+len(text)/cellWidth)
	lo, cells := 0, 0
	for index, r := range text {
		width := min(runewidth.RuneWidth(r), cellWidth)
		if cells+width > cellWidth && index > lo {
			spans = append(spans, span{lo, index})
			lo, cells = index, 0
		}
		cells += width
	}
	spans = append(spans, span{lo, len(text)})
	cursorRow, cursorCol := 0, 0
	for index, s := range spans {
		if cursorByte <= s.hi || index == len(spans)-1 {
			cursorRow = index
			for _, r := range text[s.lo:cursorByte] {
				cursorCol += min(runewidth.RuneWidth(r), cellWidth)
			}
			break
		}
	}
	start := max(0, cursorRow-maxRows+1)
	if start > len(spans)-maxRows {
		start = max(0, len(spans)-maxRows)
	}
	end := min(len(spans), start+maxRows)
	rows := make([]string, 0, end-start)
	for _, s := range spans[start:end] {
		row := text[s.lo:s.hi]
		if runewidth.StringWidth(row) > cellWidth {
			var displayed strings.Builder
			for _, r := range row {
				if runewidth.RuneWidth(r) > cellWidth {
					displayed.WriteByte('?')
				} else {
					displayed.WriteRune(r)
				}
			}
			row = displayed.String()
		}
		rows = append(rows, row)
	}
	return rows, cursorRow - start, cursorCol
}
