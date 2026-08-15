package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// sendJob runs the send (spec section 8): assemble once, transport
// argv exec with the message on stdin and output captured (F4 - no
// shell, no interpolation), then fcc + reindex + reply tag. Order is
// transport first: what was not delivered is not stored. A delivered
// message never fails the dialogue on a fcc error (a retry would
// double-send) - the note surfaces in the SendResult output. A
// missing sent_folder skips fcc silently.
func sendJob(bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, st compose.State) {
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Err: err})
		return
	}
	// snapshot the bytes: exec drains the buffer reading stdin, and the
	// fcc must be the exact delivered bytes
	data := buf.Bytes()
	cmd := exec.Command(cfg.Send.Command, cfg.Send.Args...)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Output: string(out), Err: err})
		return
	}
	var note string
	sent := st.Fcc
	if sent == "" {
		sent = cfg.Accounts[st.Account].SentFolder
	}
	if sent != "" {
		if err := writeFcc(compose.ExpandHome(sent), data); err != nil {
			note = "fcc failed: " + err.Error()
		}
	}
	// the sent copy is in the maildir now: index it so the folder rule
	// tags it sent (the R2 filter engine is its own milestone - the
	// copy is physically in the sent folder regardless)
	worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	if st.OriginalID != "" {
		tag := "replied"
		if st.Mode == compose.ModeForward {
			tag = "forwarded"
		}
		worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  idQuery(st.OriginalID),
			TagOps: []notmuch.TagOp{{Tag: tag, Add: true}},
		})
	}
	bus.Publish(core.SendResult{TabID: st.ID, OK: true, Output: note})
	bus.Publish(core.ViewDiff{View: view.Name})
}

// writeFcc lands the sent copy in the maildir new/ slot (maildir
// convention: delivery lands in new, the sync tool flags into cur).
// Unique name, 0600 (F5).
func writeFcc(dir string, data []byte) error {
	sub := filepath.Join(dir, "new")
	if err := os.MkdirAll(sub, 0700); err != nil {
		return err
	}
	name := filepath.Join(sub, fmt.Sprintf("%d.%d.notmutt", time.Now().UnixNano(), os.Getpid()))
	return os.WriteFile(name, data, 0600)
}
