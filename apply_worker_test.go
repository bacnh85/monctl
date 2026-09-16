// Concurrency-contract tests for the apply worker — untagged so they run
// on every CI runner (and under -race locally).
package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacnh85/monctl/monitor"
)

// TestApplyWorkerSerializes pins the F1 contract: however many reports are
// submitted while an apply is in flight, invocations never overlap and the
// burst coalesces instead of queueing ~30 auto-repeat DDC sweeps.
func TestApplyWorkerSerializes(t *testing.T) {
	var inFlight, maxInFlight, done int64
	apply := func(hk monitor.Hotkey) error {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			max := atomic.LoadInt64(&maxInFlight)
			if cur <= max || atomic.CompareAndSwapInt64(&maxInFlight, max, cur) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond) // stand-in for DDC read-modify-write
		atomic.AddInt64(&inFlight, -1)
		atomic.AddInt64(&done, 1)
		return nil
	}

	w := newApplyWorker(apply)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ { // simulate an auto-repeat burst
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			submit(w, monitor.Hotkey{Keys: "brightness-native", Value: "+10"})
			time.Sleep(200 * time.Microsecond) // reports arrive over ~10ms
		}(i)
	}
	wg.Wait()

	// Wait for the worker to drain whatever it accepted.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&inFlight) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond) // let the last accepted apply finish

	if got := atomic.LoadInt64(&maxInFlight); got != 1 {
		t.Fatalf("max concurrent applies = %d, want 1", got)
	}
	if n := atomic.LoadInt64(&done); n < 1 || n > 50 {
		t.Fatalf("accepted %d applies, want between 1 (coalesced) and 50 (all)", n)
	}
}

// TestSubmitDropsWhenBusy pins the coalescing contract: a full buffer
// drops instead of blocking the caller (the pump/LL-hook thread must
// never wait on DDC).
func TestSubmitDropsWhenBusy(t *testing.T) {
	release := make(chan struct{})
	var applied int32
	w := newApplyWorker(func(hk monitor.Hotkey) error {
		atomic.AddInt32(&applied, 1)
		<-release
		return nil
	})
	submit(w, monitor.Hotkey{Value: "first"})
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&applied) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	} // worker is now busy inside apply
	submit(w, monitor.Hotkey{Value: "queued"})
	done := make(chan struct{})
	go func() {
		submit(w, monitor.Hotkey{Value: "dropped"}) // must not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("submit blocked on a busy worker")
	}
	close(release)
	// The queued item may or may not be accepted (race with the drop),
	// but never more than buffer(1) + in-flight(1) extra applies happen.
	deadline = time.Now().Add(time.Second)
	for atomic.LoadInt32(&applied) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&applied); got > 2 {
		t.Fatalf("applied %d, want <= 2", got)
	}
}
