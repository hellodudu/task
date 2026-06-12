package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Reproduces the patrol-style race: Stop() is called while Run is still busy,
// then an async handler Add's a task before stop callbacks run.
// Old Tasker silently dropped tasks once running=false.
func TestAddDuringGracefulShutdownIsExecuted(t *testing.T) {
	var secondTaskExecuted atomic.Bool

	var mu sync.Mutex
	order := make([]string, 0, 3)
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}

	blockFirstTask := make(chan struct{})
	firstTaskStarted := make(chan struct{})

	tasker := NewTasker()
	tasker.Init(
		WithUpdateInterval(10*time.Millisecond),
		WithTimeout(time.Hour),
		WithStopFns(func() {
			record("stopFn")
		}),
	)

	ctx := context.Background()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = tasker.Run(ctx)
	}()

	waitUntil(t, func() bool {
		return tasker.IsRunning()
	}, time.Second, "tasker did not start")

	if err := tasker.Add(context.Background(), func(_ context.Context, _ ...interface{}) error {
		close(firstTaskStarted)
		record("blockTask")
		<-blockFirstTask
		return nil
	}); err != nil {
		t.Fatalf("queue blocking task failed: %v", err)
	}

	select {
	case <-firstTaskStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking task did not start")
	}

	stopCh := tasker.Stop()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- tasker.AddWait(context.Background(), func(_ context.Context, _ ...interface{}) error {
			secondTaskExecuted.Store(true)
			record("lateTask")
			return nil
		})
	}()

	close(blockFirstTask)

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("AddWait during graceful shutdown should succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AddWait blocked unexpectedly")
	}

	select {
	case <-stopCh:
	case <-time.After(time.Second):
		t.Fatal("tasker did not stop")
	}

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return")
	}

	if !secondTaskExecuted.Load() {
		t.Fatal("task added during graceful shutdown was not executed")
	}

	mu.Lock()
	gotOrder := append([]string(nil), order...)
	mu.Unlock()

	wantOrder := []string{"blockTask", "lateTask", "stopFn"}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("unexpected execution order: %v, want %v", gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("unexpected execution order: %v, want %v", gotOrder, wantOrder)
		}
	}
}

func TestAddAfterFullyStoppedReturnsErrTaskNotRunning(t *testing.T) {
	tasker := NewTasker()
	tasker.Init(
		WithUpdateInterval(10*time.Millisecond),
		WithTimeout(time.Hour),
	)

	ctx := context.Background()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = tasker.Run(ctx)
	}()

	waitUntil(t, func() bool {
		return tasker.IsRunning()
	}, time.Second, "tasker did not start")

	stopCh := tasker.Stop()
	select {
	case <-stopCh:
	case <-time.After(time.Second):
		t.Fatal("tasker did not stop")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return")
	}

	err := tasker.Add(context.Background(), func(_ context.Context, _ ...interface{}) error {
		return nil
	})
	if !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Add after stop should return ErrTaskNotRunning, got %v", err)
	}
}

func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}
