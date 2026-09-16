// Serialized DDC-apply worker. Pure scheduling logic, untagged so the
// concurrency contract is unit-tested on every CI runner.
package main

import "github.com/bacnh85/monctl/monitor"

// newApplyWorker returns a send-side channel that applies hotkeys on a
// single worker goroutine, coalescing requests while one is in flight
// (drop when busy). Holding the native brightness key auto-repeats at
// ~30 reports/s; without this, per-report goroutines race the
// read-modify-write in applySet (GetVCP then SetVCP) and lose/duplicate
// steps. Coalescing bounds lag: relative ±10 steps converge from
// whatever state the last accepted apply reads.
func newApplyWorker(apply func(hk monitor.Hotkey) error) chan<- monitor.Hotkey {
	ch := make(chan monitor.Hotkey, 1)
	go func() {
		for hk := range ch {
			_ = apply(hk)
		}
	}()
	return ch
}

// submit enqueues hk on w, dropping it when an apply is already queued
// or in flight.
func submit(w chan<- monitor.Hotkey, hk monitor.Hotkey) {
	select {
	case w <- hk:
	default:
	}
}
