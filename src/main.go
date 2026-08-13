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
