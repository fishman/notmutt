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
	"notmutt/filter"
	"notmutt/notmuch"
)

// sendArgs builds the transport argv (the mutt sendmail contract):
// the configured args first, then the envelope recipients (To + Cc +
// Bcc) - the transport cannot deliver without them (msmtp: "no
// recipients found"). The message flows on stdin; msmtp resolves the
// account from the From header (--read-envelope-from, the default
// config). A fresh slice - the config's args never mutate.
func sendArgs(cfg config.Send, st compose.State) []string {
	args := make([]string, 0, len(cfg.Args)+len(st.To)+len(st.Cc)+len(st.Bcc))
	args = append(args, cfg.Args...)
	args = append(args, st.To...)
	args = append(args, st.Cc...)
	args = append(args, st.Bcc...)
	return args
}

// sendJob runs the send (spec section 8): assemble once, transport
// argv exec with the message on stdin and output captured (F4 - no
// shell, no interpolation), then fcc + reindex + reply tag. Order is
// transport first: what was not delivered is not stored. A delivered
// message never fails the dialogue on a fcc error (a retry would
// double-send) - the note surfaces in the SendResult output. A
// missing mail root (unresolvable at startup) leaves the fcc empty
// and skips it silently.
func sendJob(bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, root string, st compose.State) {
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Err: err})
		return
	}
	// snapshot the bytes: exec drains the buffer reading stdin, and the
	// fcc must be the exact delivered bytes. The wire message drops the
	// Bcc header (envelope-only, mutt write_bcc default off); the fcc
	// copy keeps it - the sender's record shows the blind recipients
	// (mutt's FCC mode).
	data := buf.Bytes()
	cmd := exec.Command(cfg.Send.Command, sendArgs(cfg.Send, st)...)
	cmd.Stdin = bytes.NewReader(compose.DropBcc(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Output: string(out), Err: err})
		return
	}
	var note string
	sent := st.Fcc
	if sent == "" {
		sent = sentPath(root, st.Account, cfg.Accounts[st.Account])
	}
	if sent != "" && !cfg.Accounts[st.Account].NoFcc {
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
// Unique name, 0600 (F5). The name follows the maildir spec -
// seconds since the epoch, pid, hostname - so the client's files
// carry the same shape as the sync tool's own (mbsync stamps the
// host).
func writeFcc(dir string, data []byte) error {
	sub := filepath.Join(dir, "new")
	if err := os.MkdirAll(sub, 0700); err != nil {
		return err
	}
	host, _ := os.Hostname()
	name := filepath.Join(sub, fmt.Sprintf("%d.%d.%s", time.Now().Unix(), os.Getpid(), host))
	return os.WriteFile(name, data, 0600)
}

// sentPath derives the account's sent folder: the notmuch mail root
// plus the account's folder space plus the sent folder candidates,
// resolved through the mover's own machinery (first existing wins,
// else the first candidate - the sync tool creates the folder). A
// hard tag never needs a config option; an empty root (notmuch
// config unresolvable at startup) leaves the fcc empty.
func sentPath(root, name string, a config.Account) string {
	if root == "" {
		return ""
	}
	cs := filter.Candidates(a, "sent")
	if len(cs) == 0 {
		cs = []string{"Sent"}
	}
	return filepath.Join(root, filter.ResolveFolder(root, a.Tag(name), cs))
}

// saveDraft writes the composition into the account's draft folder
// (the muttrc $postponed path as data, the same writeFcc shape - the
// draft lands in the maildir new/ slot and notmuch new picks it up
// with the draft folder rule). The write is local, no transport; the
// error keeps the composition open. A missing mail root (unresolvable
// at startup) is an error - dropping the buffer silently loses the
// mail.
func saveDraft(bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, root string, st compose.State) error {
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		return err
	}
	dir := draftPath(root, st.Account, cfg.Accounts[st.Account])
	if dir == "" {
		return fmt.Errorf("no mail root - the draft folder is unresolvable")
	}
	if err := writeFcc(dir, buf.Bytes()); err != nil {
		return err
	}
	worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	bus.Publish(core.ViewDiff{View: view.Name})
	return nil
}

// draftPath derives the account's draft folder like sentPath ("draft"
// candidates from the preset/account data, the muttrc $postponed
// reference).
func draftPath(root, name string, a config.Account) string {
	if root == "" {
		return ""
	}
	cs := filter.Candidates(a, "draft")
	if len(cs) == 0 {
		cs = []string{"Drafts"}
	}
	return filepath.Join(root, filter.ResolveFolder(root, a.Tag(name), cs))
}
