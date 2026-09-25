package modal

import "testing"

func TestBottomReservesFooterAndCapsBody(t *testing.T) {
	box, ok := Bottom(40, 10, 10, 2)
	if !ok || box.X != 0 || box.Y != 1 || box.Width != 40 || box.Height != 7 || box.BodyRows != 5 {
		t.Fatalf("placement = %+v, %t", box, ok)
	}
	if _, ok := Bottom(2, 10, 1, 2); ok {
		t.Fatal("narrow border fit accepted")
	}
	if _, ok := Bottom(40, 5, 1, 3); ok {
		t.Fatal("short frame overwrote footer")
	}
}

func TestWrapKeepsWideRuneAndCursorVisible(t *testing.T) {
	text := "\u4e2d\u56fdabc"
	rows, row, col := Wrap(text, len(text), 4, 2)
	if len(rows) != 2 || rows[0] != "\u4e2d\u56fd" || rows[1] != "abc" || row != 1 || col != 3 {
		t.Fatalf("wrap = %q cursor %d,%d", rows, row, col)
	}
}

func TestWrapCannotDrawWideRuneThroughOneCell(t *testing.T) {
	text := "\u4e2d"
	rows, row, col := Wrap(text, len(text), 1, 1)
	if len(rows) != 1 || rows[0] != "?" || row != 0 || col != 1 {
		t.Fatalf("narrow Unicode fallback = %q at %d,%d", rows, row, col)
	}
}

func TestWrapWindowsToLongInputCursor(t *testing.T) {
	text := "abcdefghijklmnop"
	rows, row, col := Wrap(text, len(text), 4, 2)
	if len(rows) != 2 || rows[0] != "ijkl" || rows[1] != "mnop" || row != 1 || col != 4 {
		t.Fatalf("cursor window = %q at %d,%d", rows, row, col)
	}
}
