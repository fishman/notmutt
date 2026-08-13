package cache

import (
	"bytes"
	"encoding/gob"

	"go.etcd.io/bbolt"

	"notmutt/core"
)

var bucket = []byte("atts")

// Bbolt is the default Cache backend. The file is 0600 (F5); corrupt
// payloads are discarded, never fatal (defensive parse).
type Bbolt struct {
	db *bbolt.DB
}

func Open(path string) (*Bbolt, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Bbolt{db: db}, nil
}

func (b *Bbolt) Get(k Key) ([]core.Attachment, bool, error) {
	var atts []core.Attachment
	var found bool
	err := b.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucket).Get([]byte(k.String()))
		if v == nil {
			return nil
		}
		atts = decode(v)
		found = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if found && atts == nil {
		b.Delete(k) // corrupt entry: discard
		return nil, false, nil
	}
	return atts, found, nil
}

func decode(v []byte) []core.Attachment {
	var atts []core.Attachment
	if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&atts); err != nil {
		return nil
	}
	return atts
}

func (b *Bbolt) Put(k Key, atts []core.Attachment) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(atts); err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(k.String()), buf.Bytes())
	})
}

func (b *Bbolt) Delete(k Key) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(k.String()))
	})
}

func (b *Bbolt) Close() error {
	return b.db.Close()
}
