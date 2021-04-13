package task

import "time"

type ContextDoneCb func()
type UpdateCb func()

type TaskerOption func(*TaskerOptions)
type TaskerOptions struct {
	ctxCb    ContextDoneCb // context done callback
	timer    *time.Timer
	d        time.Duration // timeout duration
	updateCb UpdateCb      // default update callback
	sleep    time.Duration // sleep duration
}

func defaultTaskerOptions() *TaskerOptions {
	return &TaskerOptions{
		d:        TaskDefaultTimeout,
		ctxCb:    nil,
		timer:    time.NewTimer(TaskDefaultTimeout),
		updateCb: nil,
		sleep:    TaskDefaultSleep,
	}
}

func WithContextDoneCb(h ContextDoneCb) TaskerOption {
	return func(o *TaskerOptions) {
		o.ctxCb = h
	}
}

func WithTimeout(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.d = d
	}
}

func WithUpdateCb(du UpdateCb) TaskerOption {
	return func(o *TaskerOptions) {
		o.updateCb = du
	}
}

func WithSleep(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.sleep = d
	}
}
