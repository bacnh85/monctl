//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrayPrefer(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "monctl.exe")
	tray := filepath.Join(dir, "monctl-tray.exe")
	for _, f := range []string{exe, tray} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := trayPrefer(exe); got != tray {
		t.Fatalf("expected -tray sibling, got %q", got)
	}
	// no sibling -> unchanged
	lonely := filepath.Join(dir, "only.exe")
	if got := trayPrefer(lonely); got != lonely || !strings.HasSuffix(got, "only.exe") {
		t.Fatalf("fallback broken: %q", got)
	}
}
