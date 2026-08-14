package cache

import (
	"fmt"

	"notmutt/core"
)

type Key struct {
	Path  string
	Size  int64
	Mtime int64
}

func (k Key) String() string {
	return fmt.Sprintf("%s\x00%d\x00%d", k.Path, k.Size, k.Mtime)
}

// Entry is one cache write; PutBatch commits them in one transaction.
type Entry struct {
	Key  Key
	Atts []core.Attachment
}

type Cache interface {
	Get(k Key) ([]core.Attachment, bool, error)
	Put(k Key, atts []core.Attachment) error
	PutBatch(entries []Entry) error
	Delete(k Key) error
	Close() error
}
