package task

import "time"

type StartFn func()
type ContextDoneFn func()
type UpdateFn func()

type TaskerOption func(*TaskerOptions)
type TaskerOptions struct {
	startFn  StartFn       // start callback
	ctxFn    ContextDoneFn // context done callback
	updateFn UpdateFn      // default update callback
	timer    *time.Timer
	d        time.Duration // timeout duration
	sleep    time.Duration // sleep duration
}

func defaultTaskerOptions() *TaskerOptions {
	return &TaskerOptions{
		d:        TaskDefaultTimeout,
		startFn:  nil,
		ctxFn:    nil,
		updateFn: nil,
		timer:    time.NewTimer(TaskDefaultTimeout),
		sleep:    TaskDefaultSleep,
	}
}

func WithStartFn(f StartFn) TaskerOption {
	return func(o *TaskerOptions) {
		o.startFn = f
	}
}

func WithContextDoneFn(f ContextDoneFn) TaskerOption {
	return func(o *TaskerOptions) {
		o.ctxFn = f
	}
}

func WithUpdateFn(f UpdateFn) TaskerOption {
	return func(o *TaskerOptions) {
		o.updateFn = f
	}
}

func WithTimeout(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.d = d
	}
}

func WithSleep(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.sleep = d
	}
}
