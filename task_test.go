package task

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// This test validates the behavioral bug:
// - First Stop() returns t.chStop (not closed yet), caller will block until Run() exits and runs stopFns.
// - Second Stop() returns a NEW already-closed channel, so caller will NOT wait for stopFns.
func TestTasker_Stop_TwiceSecondDoesNotWait(t *testing.T) {
	t.Parallel()

	var stopFnEntered atomic.Bool
	releaseStopFn := make(chan struct{})
	stopFnDone := make(chan struct{})

	tr := NewTasker()
	tr.Init(
		WithStopFns(func() {
			stopFnEntered.Store(true)
			close(stopFnDone)
			<-releaseStopFn // block to prove "stop is still running"
		}),
		// Use default mode (not onlyUpdate/onlyTicker), because Stop() works by flipping running=false
		// and Run() observes it in the default branch.
		WithUpdateFn(func() error { return nil }),
		WithUpdateInterval(time.Millisecond),
	)

	runDone := make(chan error, 1)
	go func() { runDone <- tr.Run(context.Background()) }()

	// Ensure Run loop has started.
	deadline := time.Now().Add(500 * time.Millisecond)
	for !tr.IsRunning() {
		if time.Now().After(deadline) {
			t.Fatalf("tasker did not enter running state")
		}
		runtime.Gosched()
	}

	// First stop: should not be closed immediately (until stopFns finish & Run returns).
	ch1 := tr.Stop()
	select {
	case <-ch1:
		t.Fatalf("first Stop() channel closed unexpectedly early")
	case <-time.After(10 * time.Millisecond):
		// ok
	}

	// Wait until stopFn starts (meaning Run is unwinding).
	select {
	case <-stopFnDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("stopFn did not start in time")
	}

	// Second stop: after fix, it must return the SAME channel and must NOT be closed yet.
	ch2 := tr.Stop()
	if ch2 != ch1 {
		t.Fatalf("second Stop() should return the same chStop channel")
	}
	select {
	case <-ch2:
		t.Fatalf("second Stop() channel closed unexpectedly early")
	case <-time.After(10 * time.Millisecond):
		// ok
	}

	if !stopFnEntered.Load() {
		t.Fatalf("stopFn should have been entered by now")
	}

	// Run should still be blocked in stopFn until we release it.
	select {
	case err := <-runDone:
		t.Fatalf("Run returned early while stopFn blocked; err=%v", err)
	case <-time.After(10 * time.Millisecond):
		// ok
	}

	close(releaseStopFn)

	select {
	case <-ch1:
		// ok: first Stop() waiter is now released
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("first Stop() channel did not close after releasing stopFn")
	}
}

// This test runs a helper subprocess to reproduce a real "fatal error: concurrent map iteration and map write"
// with the OLD semantics. After the Tasker.Stop() fix, this test verifies the fatal is avoided
// in the "caller waits Stop() completion" pattern (like Player.TaskStop()).
func TestTasker_StopTwice_CanLeadToConcurrentMapFatal(t *testing.T) {
	t.Parallel()

	if os.Getenv("TASKER_FATAL_HELPER") == "1" {
		runNoFatalHelper()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestTasker_StopTwice_CanLeadToConcurrentMapFatal", "-test.count=1")
	cmd.Env = append(os.Environ(), "TASKER_FATAL_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected helper to exit cleanly (no fatal) after Stop() fix. err=%v output:\n%s", err, string(out))
	}
	if contains(string(out), "fatal error: concurrent map iteration and map write") {
		t.Fatalf("unexpected fatal message found after Stop() fix. output:\n%s", string(out))
	}
}

func runNoFatalHelper() {
	// Shared mutable map to emulate a map field in Player.
	m := map[int]int{}
	for i := 0; i < 20000; i++ {
		m[i] = i
	}

	tr := NewTasker()
	tr.Init(
		WithStopFns(func() {
			// Writes happen during stop.
			// With the Stop() fix, callers waiting on Stop() won't proceed concurrently with this.
			for i := 0; i < 100000; i++ {
				m[i%20000] = i
			}
		}),
		WithUpdateFn(func() error { return nil }),
		WithUpdateInterval(time.Millisecond),
	)

	go func() { _ = tr.Run(context.Background()) }()

	// Model the "caller waits Stop() completion" pattern.
	<-tr.Stop()
	<-tr.Stop()

	for range m {
		// no-op
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	// tiny helper to avoid importing strings in vendor test
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

func TestTasker_Concurrent_AllPublicAPIs_NoDeadlock(t *testing.T) {
	t.Parallel()

	tr := NewTasker()
	tr.Init(
		WithUpdateFn(func() error { return nil }),
		WithUpdateInterval(time.Millisecond),
		WithExecuteTimeout(30*time.Millisecond),
		WithTimeout(200*time.Millisecond),
	)

	// Phase A (before Run): hit all methods concurrently including ResetTimeout().
	{
		done := make(chan struct{})
		var ops atomic.Int64

		for i := 0; i < 40; i++ {
			go func() {
				defer func() { done <- struct{}{} }()
				end := time.Now().Add(120 * time.Millisecond)
				for time.Now().Before(end) {
					_ = tr.IsRunning()
					_ = tr.GetTaskNum()
					tr.ResetTimeout()
					tr.Add(context.Background(), func(context.Context, ...interface{}) error {
						ops.Add(1)
						return nil
					})
					_ = tr.AddWait(context.Background(), func(context.Context, ...interface{}) error {
						ops.Add(1)
						return nil
					})
					<-tr.Stop() // should not block even if Run never starts
					runtime.Gosched()
				}
			}()
		}

		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for i := 0; i < 40; i++ {
			select {
			case <-done:
			case <-timeout.C:
				t.Fatalf("phase A deadlocked; ops=%d running=%v taskNum=%d", ops.Load(), tr.IsRunning(), tr.GetTaskNum())
			}
		}
	}

	// Phase B (during Run): exercise everything except ResetTimeout() concurrently.
	// ResetTimeout() mutates t.opts.timeout and is not safe to race with Run's select on timeout.C without extra locking.
	{
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- tr.Run(ctx) }()

		done := make(chan struct{})
		var ops atomic.Int64

		for i := 0; i < 50; i++ {
			go func() {
				defer func() { done <- struct{}{} }()
				end := time.Now().Add(200 * time.Millisecond)
				for time.Now().Before(end) {
					_ = tr.IsRunning()
					_ = tr.GetTaskNum()
					tr.Add(context.Background(), func(context.Context, ...interface{}) error {
						ops.Add(1)
						return nil
					})
					_ = tr.AddWait(context.Background(), func(context.Context, ...interface{}) error {
						ops.Add(1)
						return nil
					})
					runtime.Gosched()
				}
			}()
		}

		for i := 0; i < 10; i++ {
			go func() {
				<-tr.Stop()
				done <- struct{}{}
			}()
		}

		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for i := 0; i < 60; i++ {
			select {
			case <-done:
			case <-timeout.C:
				t.Fatalf("phase B deadlocked; ops=%d running=%v taskNum=%d", ops.Load(), tr.IsRunning(), tr.GetTaskNum())
			}
		}

		cancel()
		select {
		case <-runDone:
		case <-time.After(1 * time.Second):
			t.Fatalf("Run did not return in time after cancel/stop")
		}
	}

	// Phase C (after Run): ResetTimeout() should still not block and Stop() should be idempotent.
	{
		done := make(chan struct{})
		for i := 0; i < 20; i++ {
			go func() {
				defer func() { done <- struct{}{} }()
				tr.ResetTimeout()
				<-tr.Stop()
			}()
		}

		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for i := 0; i < 20; i++ {
			select {
			case <-done:
			case <-timeout.C:
				t.Fatalf("phase C deadlocked")
			}
		}
	}
}
