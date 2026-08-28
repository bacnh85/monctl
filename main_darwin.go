//go:build darwin

// darwin main entry: hotkey event taps require all hotkey work to run on
// the OS main thread with a live NSApplication runloop (see
// golang.design/x/hotkey README). mainthread.Init blocks on the runloop and
// runs our CLI in a goroutine; os.Exit is called by Init's goroutine.
package main

import (
	"os"

	"golang.design/x/hotkey/mainthread"
)

func main() {
	mainthread.Init(func() { os.Exit(run(os.Args[1:])) })
}
