//go:build linux

package localipc

import (
	"net"
	"path/filepath"
	"testing"
)

func TestCheckPeerAcceptsCurrentUser(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "ipc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	errs := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			err = CheckPeer(conn)
		}
		errs <- err
	}()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}
