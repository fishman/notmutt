package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// MessageProperty represents a key/value pair of a message.
type MessageProperty struct {
	Key   string
	Value string
}

func (p *MessageProperty) String() string {
	return p.Key + "=" + p.Value
}
