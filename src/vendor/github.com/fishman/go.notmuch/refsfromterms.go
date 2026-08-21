//go:build refsfromterms

package notmuch

/*
#cgo CFLAGS: -DNOTMUCH_HAS_REF_GETTERS=1
*/
import "C"
