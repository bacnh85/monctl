//go:build darwin

package main

import (
	"testing"
	"time"
)

// Smoke test: with no modifier held, the wait returns well under its cap.
func TestWaitModifiersReleasedNoKeys(t *testing.T) {
	if anyModifierPressed() {
		t.Skip("a modifier key is physically held")
	}
	done := make(chan struct{})
	go func() {
		waitModifiersReleased(50 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitModifiersReleased did not return")
	}
}
