// Hotkey watch daemon: registers global hotkeys and applies monitor actions.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.design/x/hotkey"

	"github.com/bacnh85/monctl/monitor"
)

var hkMod = map[string]hotkey.Modifier{
	"ctrl":    hotkey.ModCtrl,
	"control": hotkey.ModCtrl,
	"shift":   hotkey.ModShift,
	"alt":     hotkey.ModAlt,
	"option":  hotkey.ModAlt,
}

var hkKey = map[string]hotkey.Key{
	"up": hotkey.KeyUp, "down": hotkey.KeyDown,
	"left": hotkey.KeyLeft, "right": hotkey.KeyRight,
	"p": hotkey.KeyP,
}

// parseHotkey parses "ctrl+alt+up" into (mods, key).
func parseHotkey(spec string) ([]hotkey.Modifier, hotkey.Key, error) {
	var mods []hotkey.Modifier
	var key hotkey.Key
	found := false
	for _, part := range strings.Split(strings.ToLower(spec), "+") {
		if m, ok := hkMod[part]; ok {
			mods = append(mods, m)
			continue
		}
		if k, ok := hkKey[part]; ok {
			key = k
			found = true
			continue
		}
		return nil, 0, fmt.Errorf("unknown key %q in %q", part, spec)
	}
	if !found {
		return nil, 0, fmt.Errorf("no key in %q", spec)
	}
	return mods, key, nil
}

// applyAction performs one hotkey action using the same code path as the CLI.
func applyAction(ctrl monitor.Controller, hk monitor.Hotkey) error {
	code, ok := monitor.KnownVCPCodes()[strings.ToLower(hk.Action)]
	if !ok {
		return fmt.Errorf("unknown action %q", hk.Action)
	}
	targets, err := monitor.ResolveTargets(ctrl, hk.Target)
	if err != nil {
		return err
	}
	for _, m := range targets {
		if err := applySet(ctrl, m, code, strings.ToLower(hk.Action), hk.Value); err != nil {
			return err
		}
	}
	return nil
}

func cmdWatch(argv []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config path (default: UserConfigDir/monctl/config.json)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	path := *cfgPath
	if path == "" {
		p, err := monitor.ConfigPath()
		if err != nil {
			return fail(err)
		}
		path = p
	}
	if err := monitor.WriteDefaultConfig(path); err != nil {
		return fail(err)
	}
	cfg, err := monitor.LoadConfig(path)
	if err != nil {
		return fail(err)
	}
	ctrl := monitor.New()

	for _, h := range cfg.Hotkeys {
		mods, key, err := parseHotkey(h.Keys)
		if err != nil {
			// Skip bad bindings; log but keep the rest of the daemon alive.
			fmt.Fprintf(os.Stderr, "monctl watch: skipping %q: %v\n", h.Keys, err)
			continue
		}
		hk := hotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			return fail(fmt.Errorf("register %s: %w", h.Keys, err))
		}
		binding := h // copy for goroutine
		go func() {
			for range hk.Keydown() {
				if err := applyAction(ctrl, binding); err != nil {
					fmt.Fprintf(os.Stderr, "monctl watch: %s: %v\n", binding.Keys, err)
				}
			}
		}()
		fmt.Printf("watching %s -> %s %s\n", binding.Keys, binding.Action, binding.Value)
	}
	fmt.Println("monctl watch running; Ctrl+C to quit")
	select {} // block forever; OS cleans up on Ctrl+C
}
