package app

import (
	"os/exec"
	"strconv"
	"strings"

	"notmutt/config"
)

// notifyNewMail runs the [notify] command for one processed batch
// (the filter job's completion event, R2 side effects): {count} in
// the argv is replaced with the processed entry count. The count is
// the only payload - never mail content (F6). The caller spawns the
// goroutine; an absent command or no entries is a no-op.
func notifyNewMail(cfg config.Config, entries int) {
	argv := cfg.Notify.Command
	if len(argv) == 0 || entries <= 0 {
		return
	}
	n := strconv.Itoa(entries)
	argv = append([]string(nil), argv...)
	for i, a := range argv {
		argv[i] = strings.ReplaceAll(a, "{count}", n)
	}
	if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
		diag.Warn("notify", "err", err.Error())
	}
}
