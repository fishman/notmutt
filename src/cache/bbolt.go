// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"bytes"
	"encoding/gob"
	"time"

	"go.etcd.io/bbolt"

	"notmutt/core"
)

var (
	bucket     = []byte("atts")
	metaBucket = []byte("meta")
	schemaKey  = []byte("schema")
	// schemaVersion identifies ScanAttachments' classifier semantics: a
	// bump clears every cached list on open, so a classifier change
	// re-scans messages instead of showing stale attachment markers.
	schemaVersion = []byte("2")
)

// openTimeout bounds the flock wait: bbolt's infinite retry would
// hang a second instance, but the cache is disposable (R13) - run
// cacheless instead.
const openTimeout = time.Second

// Bbolt is the default Cache backend: file 0600 (F5); corrupt payloads are discarded, never fatal.
type Bbolt struct {
	db *bbolt.DB
}

func Open(path string) (*Bbolt, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: openTimeout})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		atts, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		meta, err := tx.CreateBucketIfNotExists(metaBucket)
		if err != nil {
			return err
		}
		if !bytes.Equal(meta.Get(schemaKey), schemaVersion) {
			if err := atts.ForEach(func(k, _ []byte) error { return atts.Delete(k) }); err != nil {
				return err
			}
			return meta.Put(schemaKey, schemaVersion)
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Bbolt{db: db}, nil
}

func (b *Bbolt) Get(k Key) ([]core.Attachment, bool, error) {
	var atts []core.Attachment
	var hit, valid bool
	err := b.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucket).Get([]byte(k.String()))
		if v == nil {
			return nil
		}
		hit = true
		atts, valid = decode(v)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !hit {
		return nil, false, nil
	}
	if !valid {
		b.Delete(k) // corrupt entry: discard
		return nil, false, nil
	}
	// A hit with nil atts means "no attachments", never a miss.
	return atts, true, nil
}

func decode(v []byte) ([]core.Attachment, bool) {
	var atts []core.Attachment
	if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&atts); err != nil {
		return nil, false
	}
	return atts, true
}

func (b *Bbolt) Put(k Key, atts []core.Attachment) error {
	return b.PutBatch([]Entry{{Key: k, Atts: atts}})
}

// PutBatch commits all entries in ONE transaction: per-message commits
// would fsync each write (default NoSync=false), ~1k/s on SSD.
func (b *Bbolt) PutBatch(entries []Entry) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		for _, e := range entries {
			v, err := encode(e.Atts)
			if err != nil {
				return err
			}
			if err := tx.Bucket(bucket).Put([]byte(e.Key.String()), v); err != nil {
				return err
			}
		}
		return nil
	})
}

func encode(atts []core.Attachment) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(atts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *Bbolt) Delete(k Key) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(k.String()))
	})
}

func (b *Bbolt) Close() error {
	return b.db.Close()
}
