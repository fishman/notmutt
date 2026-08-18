// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"notmutt/app"
)

func main() {
	if err := app.Run(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
