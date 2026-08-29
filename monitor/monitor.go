// Package monitor provides cross-platform DDC/CI access to physical monitors.
package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Info describes one physical monitor.
type Info struct {
	Index       int     `json:"index"`
	Description string  `json:"description"`
	Primary     bool    `json:"primary"`
	Handle      uintptr `json:"-"`
}

// Controller is the platform seam (Windows implemented; macOS phase 2).
type Controller interface {
	List() ([]Info, error)
	GetVCP(info Info, code byte) (current, max int, err error)
	SetVCP(info Info, code byte, value byte) error
}

// ErrNotImplemented is returned by platforms without a backend yet.
var ErrNotImplemented = errors.New("monitor: no backend for this platform")

// New returns the platform Controller.
func New() Controller { return newPlatform() }

// ResolveTargets resolves "-m N" / "@all" style selection against List().
func ResolveTargets(ctrl Controller, sel string) ([]Info, error) {
	all, err := ctrl.List()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, errors.New("no DDC/CI-capable monitor found")
	}
	sel = strings.TrimSpace(sel)
	if sel == "" || sel == "@all" || sel == "all" {
		return all, nil
	}
	var idx int
	if _, err := fmt.Sscanf(sel, "%d", &idx); err != nil {
		return nil, fmt.Errorf("invalid monitor selector %q (use index or @all)", sel)
	}
	for _, m := range all {
		if m.Index == idx {
			return []Info{m}, nil
		}
	}
	return nil, fmt.Errorf("no monitor with index %d", idx)
}

// Hotkey binds a key combo to a control action.
type Hotkey struct {
	Keys   string `json:"keys"`
	Action string `json:"action"` // brightness | contrast | input | power
	Target string `json:"target"` // monitor index as string, or "@all"
	Value  string `json:"value"`  // "10", "+10", "-10", input name, power name
}

// Config drives the watch daemon's hotkey bindings.
// NativeBrightness: macOS only — intercept the physical F1/F2 brightness
// keys and route them to DDC (BetterDisplay-style). nil/true = enabled;
// set false to leave F1/F2 to the system.
type Config struct {
	Hotkeys         []Hotkey `json:"hotkeys"`
	NativeBrightness *bool   `json:"native_brightness,omitempty"`
}

// DefaultConfig returns the shipped example config.
func DefaultConfig() *Config {
	return &Config{
		Hotkeys: []Hotkey{
			{Keys: "ctrl+alt+up", Action: "brightness", Target: "@all", Value: "-10"},
			{Keys: "ctrl+alt+down", Action: "brightness", Target: "@all", Value: "+10"},
			{Keys: "ctrl+cmd+f1", Action: "brightness", Target: "@all", Value: "-10"},
			{Keys: "ctrl+alt+f2", Action: "brightness", Target: "@all", Value: "+10"},
			{Keys: "ctrl+alt+left", Action: "input", Target: "@all", Value: "dp1"},
			{Keys: "ctrl+alt+right", Action: "input", Target: "@all", Value: "hdmi2"},
			// ctrl+alt+shift+p: plain ctrl+alt+p is a common app shortcut
			// (e.g. pi plan mode) and RegisterHotKey swallows it system-wide.
			{Keys: "ctrl+alt+shift+p", Action: "power", Target: "@all", Value: "off"},
		},
	}
}

// ConfigPath returns os.UserConfigDir()/monctl/config.json.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "monctl", "config.json"), nil
}

// LoadConfig reads config.json; missing file -> DefaultConfig.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// WriteDefaultConfig writes the example config if absent (never clobbers).
func WriteDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(DefaultConfig(), "", "  ")
	return os.WriteFile(path, data, 0o644)
}

// SortInfos keeps monitor ordering stable by index.
func SortInfos(infos []Info) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].Index < infos[j].Index })
}
