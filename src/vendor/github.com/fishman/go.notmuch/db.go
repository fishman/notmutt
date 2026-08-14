package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"

import (
	"unsafe"
)

const (
	// DBReadOnly is the mode for opening the database in read only.
	DBReadOnly = C.NOTMUCH_DATABASE_MODE_READ_ONLY

	// DBReadWrite is the mode for opening the database in read write.
	DBReadWrite = C.NOTMUCH_DATABASE_MODE_READ_WRITE

	// TagMax is the maximum number of allowed tags.
	TagMax = C.NOTMUCH_TAG_MAX
)

type (
	// DBMode is the mode of the database opening, DBReadOnly or DBReadWrite
	DBMode C.notmuch_database_mode_t

	// DB represents a notmuch database.
	DB cStruct
)

func (db *DB) toC() *C.notmuch_database_t {
	return (*C.notmuch_database_t)(db.cptr)
}

// live reports whether the database is still open. The C library does not
// check for a closed database and would dereference NULL otherwise.
func (db *DB) live() bool {
	return (*cStruct)(db).live()
}

// Close closes the database.
func (db *DB) Close() error {
	return (*cStruct)(db).doClose(func() error {
		return statusErr(C.notmuch_database_destroy(db.toC()))
	})
}

// Create creates a new, empty notmuch database located at 'path'.
func Create(path string) (*DB, error) {
	config := ""
	return CreateWithConfig(&path, &config, nil)
}

// CreateWithConfig creates a new, empty notmuch database located at 'path',
// using the configuration in 'config'.
//
// If 'config' is nil, it will look:
//   - the environment variable $NOTMUCH_CONFIG, if non-empty
//   - $XDG_CONFIG_HOME/notmuch
//   - $HOME/.notmuch-config
//
// If 'config' is an empty string (""), then it will not open any configuration
// file.
//
// If 'profile' is non-nil, append to the directory / file path determined
// for 'config' and 'path'.
func CreateWithConfig(path, config, profile *string) (*DB, error) {
	var cpath *C.char
	if path != nil {
		cpath = C.CString(*path)
		defer C.free(unsafe.Pointer(cpath))
	}

	var cconfig *C.char
	if config != nil {
		cconfig = C.CString(*config)
		defer C.free(unsafe.Pointer(cconfig))
	}

	var cprofile *C.char
	if profile != nil {
		cprofile = C.CString(*profile)
		defer C.free(unsafe.Pointer(cprofile))
	}

	var cdb *C.notmuch_database_t
	err := statusErr(C.notmuch_database_create_with_config(cpath, cconfig, cprofile, &cdb, nil))
	if err != nil {
		return nil, err
	}
	db := &DB{cptr: unsafe.Pointer(cdb)}
	setGcCloseErr(db)
	return db, nil
}

// Open opens the database at the location path using mode. Caller is responsible
// for closing the database when done.
//
// The configuration is searched for in the usual locations. Use
// OpenWithConfig with an empty config string to open without a configuration
// file.
func Open(path string, mode DBMode) (*DB, error) {
	return OpenWithConfig(&path, nil, nil, mode)
}

// OpenWithConfig opens the database at the location 'path' using 'mode' and
// the configuration in 'config'.
//
// If 'path' is nil, use the location specified:
//   - in the environment variable $NOTMUCH_DATABASE, if non-empty
//   - in a configuration file, located as described in 'config'
//   - by $XDG_DATA_HOME/notmuch/<profile>, if profile argument is set
//
// If 'path' is non-nil, but does not appear to be a Xapian database, check
// for a directory '.notmuch/xapian' below 'path'.
//
// If 'config' is nil, it will look:
//   - the environment variable $NOTMUCH_CONFIG, if non-empty
//   - $XDG_CONFIG_HOME/notmuch
//   - $HOME/.notmuch-config
//
// If 'config' is an empty string (""), then it will not open any configuration
// file.
//
// If 'profile' is nil, it will use:
//   - the environment variable $NOTMUCH_PROFILE if defined
//   - otherwise 'default' for directories, and ” for paths
//
// If 'profile' is non-nil, append to the directory / file path determined
// for 'config' and 'path'.
//
// Caller is responsible for closing the database when done.
func OpenWithConfig(path, config, profile *string, mode DBMode) (*DB, error) {
	var cpath *C.char
	if path != nil {
		cpath = C.CString(*path)
		defer C.free(unsafe.Pointer(cpath))
	}

	var cconfig *C.char
	if config != nil {
		cconfig = C.CString(*config)
		defer C.free(unsafe.Pointer(cconfig))
	}

	var cprofile *C.char
	if profile != nil {
		cprofile = C.CString(*profile)
		defer C.free(unsafe.Pointer(cprofile))
	}

	var errMsg string
	cErrMsg := C.CString(errMsg)
	defer C.free(unsafe.Pointer(cErrMsg))

	cmode := C.notmuch_database_mode_t(mode)
	var cdb *C.notmuch_database_t
	cdbptr := (**C.notmuch_database_t)(&cdb)
	err := statusErr(C.notmuch_database_open_with_config(cpath, cmode, cconfig, cprofile, cdbptr, &cErrMsg))
	if err != nil || errMsg != "" {
		return nil, err
	}
	db := &DB{cptr: unsafe.Pointer(cdb)}
	setGcCloseErr(db)
	return db, nil
}

// Compact compacts a notmuch database, backing up the original database to the
// given path. The database will be opened with DBReadWrite to ensure no writes
// are made.
func Compact(path, backup string) error {
	cpath := C.CString(path)
	cbackup := C.CString(backup)
	defer func() {
		C.free(unsafe.Pointer(cpath))
		C.free(unsafe.Pointer(cbackup))
	}()

	return statusErr(C.notmuch_database_compact(cpath, cbackup, nil, nil))
}

// Atomic opens an atomic transaction in the database and calls the callback.
//
// Closing the database inside the callback aborts the transaction: no changes
// are committed and the callback must not return the database to use.
func (db *DB) Atomic(callback func(*DB)) error {
	if !db.live() {
		return ErrClosedDatabase
	}
	if err := statusErr(C.notmuch_database_begin_atomic(db.toC())); err != nil {
		return err
	}
	callback(db)
	if !(*cStruct)(db).live() {
		// Database was closed inside the callback, which aborts the
		// transaction.
		return nil
	}
	return statusErr(C.notmuch_database_end_atomic(db.toC()))
}

// Reopen reopens an open database in the given mode, e.g. to switch between
// read-only and read-write, or to recover from ErrOperationInvalidated.
func (db *DB) Reopen(mode DBMode) error {
	if !db.live() {
		return ErrClosedDatabase
	}
	return statusErr(C.notmuch_database_reopen(db.toC(), C.notmuch_database_mode_t(mode)))
}

// NewQuery creates a new query from a string following xapian format. On a
// closed database it returns a query that yields no results.
func (db *DB) NewQuery(queryString string) *Query {
	if !db.live() {
		return &Query{}
	}
	cstr := C.CString(queryString)
	defer C.free(unsafe.Pointer(cstr))
	cquery := C.notmuch_query_create(db.toC(), cstr)
	query := &Query{
		cptr:   unsafe.Pointer(cquery),
		parent: (*cStruct)(db),
	}
	setGcClose(query)
	return query
}

// Version returns the database version.
func (db *DB) Version() int {
	if !db.live() {
		return 0
	}
	return int(C.notmuch_database_get_version(db.toC()))
}

// Revision returns the database revision and its UUID. The revision
// increments on every message index and tag change; the UUID identifies
// the database generation and changes on upgrade. Both are used to
// detect stale handles (notmuch_database_get_revision).
func (db *DB) Revision() (string, uint64, error) {
	if !db.live() {
		return "", 0, ErrClosedDatabase
	}
	var uuid *C.char
	rev := C.notmuch_database_get_revision(db.toC(), &uuid)
	return C.GoString(uuid), uint64(rev), nil
}

// LastStatus retrieves last status string for the notmuch database.
func (db *DB) LastStatus() string {
	if !db.live() {
		return ""
	}
	return C.GoString(C.notmuch_database_status_string(db.toC()))
}

// Path returns the database path of the database.
func (db *DB) Path() string {
	if !db.live() {
		return ""
	}
	return C.GoString(C.notmuch_database_get_path(db.toC()))
}

// NeedsUpgrade returns true if the database can be upgraded. This will always
// return false if the database was opened with DBReadOnly.
//
// If this function returns true then the caller may call
// Upgrade() to upgrade the database.
func (db *DB) NeedsUpgrade() bool {
	if !db.live() {
		return false
	}
	cbool := C.notmuch_database_needs_upgrade(db.toC())
	return int(cbool) != 0
}

// Upgrade upgrades the current database to the latest supported version. The
// database must be opened with DBReadWrite.
func (db *DB) Upgrade() error {
	if !db.live() {
		return ErrClosedDatabase
	}
	return statusErr(C.notmuch_database_upgrade(db.toC(), nil, nil))
}

// AddMessage adds a new message to the current database or associate an
// additional filename with an existing message.
func (db *DB) AddMessage(filename string) (*Message, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	var cmsg *C.notmuch_message_t
	err := statusErr(C.notmuch_database_index_file(db.toC(), cfilename, nil, &cmsg))

	if err != nil && err != ErrDuplicateMessageID {
		return nil, err
	}
	msg := &Message{
		cptr:   unsafe.Pointer(cmsg),
		parent: (*cStruct)(db),
	}
	setGcClose(msg)
	return msg, err
}

// RemoveMessage remove a message filename from the current database. If the
// message has no more filenames, remove the message.
func (db *DB) RemoveMessage(filename string) error {
	if !db.live() {
		return ErrClosedDatabase
	}
	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	return statusErr(C.notmuch_database_remove_message(db.toC(), cfilename))
}

// FindMessage finds a message with the given message_id.
func (db *DB) FindMessage(id string) (*Message, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	var cmsg *C.notmuch_message_t
	if err := statusErr(C.notmuch_database_find_message(db.toC(), cid, &cmsg)); err != nil {
		return nil, err
	}
	if cmsg == nil {
		return nil, ErrNotFound
	}
	msg := &Message{
		cptr:   unsafe.Pointer(cmsg),
		parent: (*cStruct)(db),
	}
	setGcClose(msg)
	return msg, nil
}

// FindMessageByFilename finds a message with the given filename.
func (db *DB) FindMessageByFilename(filename string) (*Message, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	var cmsg *C.notmuch_message_t
	if err := statusErr(C.notmuch_database_find_message_by_filename(db.toC(), cfilename, &cmsg)); err != nil {
		return nil, err
	}
	if cmsg == nil {
		return nil, ErrNotFound
	}
	msg := &Message{
		cptr:   unsafe.Pointer(cmsg),
		parent: (*cStruct)(db),
	}
	setGcClose(msg)
	return msg, nil
}

// Tags returns the list of all tags in the database.
func (db *DB) Tags() (*Tags, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	ctags := C.notmuch_database_get_all_tags(db.toC())
	if ctags == nil {
		return nil, ErrOutOfMemory
	}
	tags := &Tags{
		cptr:   unsafe.Pointer(ctags),
		parent: (*cStruct)(db),
	}
	setGcClose(tags)
	return tags, nil
}

// GetConfigList returns the config list, which can be used to iterate over all
// set options starting with prefix. The list contains pairs from the database,
// the configuration file, and built-in defaults; pairs with empty values are
// skipped.
func (db *DB) GetConfigList(prefix string) (*ConfigList, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cstr := C.CString(prefix)
	defer C.free(unsafe.Pointer(cstr))

	ccl := C.notmuch_config_get_pairs(db.toC(), cstr)
	if ccl == nil {
		return nil, ErrOutOfMemory
	}
	cl := &ConfigList{
		cptr:   unsafe.Pointer(ccl),
		parent: (*cStruct)(db),
	}
	setGcClose(cl)
	return cl, nil
}

// GetConfig gets config value of key.
//
// The empty string is not distinguishable from an unset key by the C API;
// ErrNotFound is returned for both.
func (db *DB) GetConfig(key string) (string, error) {
	if !db.live() {
		return "", ErrClosedDatabase
	}
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))
	var cval *C.char
	err := statusErr(C.notmuch_database_get_config(db.toC(), ckey, &cval))
	if err != nil {
		return "", err
	}
	defer C.free(unsafe.Pointer(cval))
	val := C.GoString(cval)
	if val == "" {
		return "", ErrNotFound
	}
	return val, nil
}

// SetConfig sets config key to value.
func (db *DB) SetConfig(key, value string) error {
	if !db.live() {
		return ErrClosedDatabase
	}
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))
	cval := C.CString(value)
	defer C.free(unsafe.Pointer(cval))

	return statusErr(C.notmuch_database_set_config(db.toC(), ckey, cval))
}
