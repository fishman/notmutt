package app

import (
	"strconv"
	"strings"

	"github.com/gen2brain/beeep"
)

// notifyBeeep shows the platform notification (beeep: dbus, falling
// back to notify-send and kdialog per show on linux; osascript on
// darwin): the title is static, the body is the count plus the
// priority subjects - the same payload as the command backend's
// {count}/{subjects}, never bodies or ids (F6).
func notifyBeeep(entries int, subjects []string) {
	body := strconv.Itoa(entries) + " new messages"
	if s := strings.Join(subjects, "\n"); s != "" {
		body += ": " + s
	}
	if err := beeep.Notify("notmutt", body, ""); err != nil {
		diag.Warn("notify", "err", err.Error())
	}
}
