//go:build !darwin && !windows

package main

import "time"

// anyModifierPressed is unsupported here; report nothing held.
func anyModifierPressed() bool { return false }

// waitModifiersReleased is a no-op without a key-state API.
func waitModifiersReleased(max time.Duration) {}
