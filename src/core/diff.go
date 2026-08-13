package core

type OpKind int

const (
	OpInsert OpKind = iota
	OpRemove
	OpMove
)

type Op[T any] struct {
	Kind OpKind
	Item T      // OpInsert payload
	Key  string // OpRemove, OpMove
	From int    // OpRemove, OpMove: index in the current list
	To   int    // OpInsert, OpMove: index in the current list
}

// DiffSorted merges old and new (both sorted by less, unique keys) into
// replayable ops. Positions are in the current list at the time each op
// applies, so Apply(old, ops) == new. A remove+insert of the same key
// collapses into a Move (second pass, still O(n+m)). Equality is
// key-order equality: matched keys keep the old element (values are not
// updated), and moved elements retain old values. Callers reconcile
// element fields from the incoming snapshot. less must be a strict total
// order on keys.
func DiffSorted[T any](old, new []T, less func(a, b T) bool, key func(T) string) []Op[T] {
	var ops []Op[T]
	i, j := 0, 0
	for i < len(old) && j < len(new) {
		if key(old[i]) == key(new[j]) {
			i++
			j++
			continue
		}
		if less(old[i], new[j]) {
			ops = append(ops, Op[T]{Kind: OpRemove, Key: key(old[i]), From: j})
			i++
		} else {
			ops = append(ops, Op[T]{Kind: OpInsert, Item: new[j], To: j})
			j++
		}
	}
	for ; i < len(old); i++ {
		ops = append(ops, Op[T]{Kind: OpRemove, Key: key(old[i]), From: j})
	}
	for ; j < len(new); j++ {
		ops = append(ops, Op[T]{Kind: OpInsert, Item: new[j], To: j})
	}
	return collapseMoves(ops, key)
}

// collapseMoves turns an ADJACENT remove-then-insert pair of the same key
// into a Move. Only adjacent pairs collapse: between them the walk
// advances via matched pairs alone, so From and To reference the same
// frame and the collapsed op is equivalent to the original two (the
// property test is the gate). Any intervening op shifts the frames, and
// the reverse order (insert then remove - an element rising) never
// collapses: both stay as remove+insert churn.
func collapseMoves[T any](ops []Op[T], key func(T) string) []Op[T] {
	out := make([]Op[T], 0, len(ops))
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		if op.Kind == OpRemove && i+1 < len(ops) && ops[i+1].Kind == OpInsert && key(ops[i+1].Item) == op.Key {
			out = append(out, Op[T]{Kind: OpMove, Key: op.Key, From: op.From, To: ops[i+1].To})
			i++
			continue
		}
		out = append(out, op)
	}
	return out
}

func removeAt[T any](items []T, i int) []T {
	return append(items[:i], items[i+1:]...)
}

func insertAtIdx[T any](items []T, item T, i int) []T {
	items = append(items, *new(T))
	copy(items[i+1:], items[i:])
	items[i] = item
	return items
}

// Apply replays ops in order over items; the result equals the new list
// the ops were diffed from. Apply mutates items' backing array in place;
// callers must use the returned slice and must not retain the original
// header or alias the backing array.
func Apply[T any](items []T, ops []Op[T]) []T {
	for _, op := range ops {
		switch op.Kind {
		case OpInsert:
			items = insertAtIdx(items, op.Item, op.To)
		case OpRemove:
			items = removeAt(items, op.From)
		case OpMove:
			item := items[op.From]
			items = removeAt(items, op.From)
			items = insertAtIdx(items, item, op.To)
		}
	}
	return items
}
