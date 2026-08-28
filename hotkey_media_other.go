//go:build !darwin

package main

// No native F-key interception off macOS (Windows brightness keys are
// vendor-specific; use config hotkeys).

func extraHotkeys() []nativeKeyBinding { return nil }
