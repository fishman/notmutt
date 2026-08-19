// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strconv"

	"github.com/gen2brain/beeep"

	"notmutt/core"
)

// notifyBeeep shows the platform notification (beeep: dbus, falling
// back to notify-send and kdialog per show on linux; osascript on
// darwin): the title is the deduped sender list, the body the count
// plus the aligned sender/subject/time rows - the same payload as the
// command backend's {count}/{subjects}, never bodies or ids (F6).
// beeep.AppName is the notification daemon's source label: it defaults
// to "DefaultAppName" and must be set explicitly.
func notifyBeeep(entries int, head []core.NotifyHeadline) {
	beeep.AppName = "notmutt"
	body := strconv.Itoa(entries) + " new messages"
	if rows := notifyRows(head); rows != "" {
		body += "\n" + rows
	}
	if err := beeep.Notify(notifyTitle(head), body, ""); err != nil {
		diag.Warn("notify", "err", err.Error())
	}
}
