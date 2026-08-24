package zaman

import "unicode/utf8"

// trie is a prefix tree for maximal-munch CJK tokenization. A terminal
// node carries the tokens its word produces.
type trie struct {
	children map[rune]*trie
	toks     []Token
}

func (t *trie) insert(word string, toks []Token) {
	n := t
	for _, r := range word {
		if n.children == nil {
			n.children = make(map[rune]*trie)
		}
		child := n.children[r]
		if child == nil {
			child = &trie{}
			n.children[r] = child
		}
		n = child
	}
	n.toks = toks
}

// walk returns the longest terminal match starting at pos, or ok=false.
func (t *trie) walk(s string, pos int) (toks []Token, end int, ok bool) {
	n := t
	i := pos
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		child := n.children[r]
		if child == nil {
			break
		}
		n = child
		i += sz
		if len(n.toks) > 0 {
			toks, end, ok = n.toks, i, true
		}
	}
	return toks, end, ok
}
