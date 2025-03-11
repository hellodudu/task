package task

import (
	"io"
	"log"
	"time"
)

type StartFn func()
type StopFn func()
type UpdateFn func() error

type TaskerOption func(*TaskerOptions)
type TaskerOptions struct {
	uniqueId       string
	startFns       []StartFn // start callback
	stopFns        []StopFn  // task stop callback
	updateFn       UpdateFn  // default update callback
	timeout        *time.Timer
	d              time.Duration // timeout duration
	updateInterval time.Duration // update interval duration
	executeTimeout time.Duration // execute timeout
	onlyTicker     bool          // only execute ticker
	onlyUpdate     bool          // only execute update
	logger         *log.Logger
}

func defaultTaskerOptions() *TaskerOptions {
	return &TaskerOptions{
		uniqueId:       "",
		d:              TaskDefaultTimeout,
		startFns:       make([]StartFn, 0, 5),
		stopFns:        make([]StopFn, 0, 5),
		updateFn:       nil,
		timeout:        time.NewTimer(TaskDefaultTimeout),
		updateInterval: TaskDefaultUpdateInterval,
		executeTimeout: TaskDefaultExecuteTimeout,
		onlyTicker:     false,
		onlyUpdate:     false,
		logger:         log.Default(),
	}
}

func WithStartFns(f ...StartFn) TaskerOption {
	return func(o *TaskerOptions) {
		o.startFns = o.startFns[:0]
		o.startFns = append(o.startFns, f...)
	}
}

func WithStopFns(f ...StopFn) TaskerOption {
	return func(o *TaskerOptions) {
		o.stopFns = o.stopFns[:0]
		o.stopFns = append(o.stopFns, f...)
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

func WithUpdateInterval(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.updateInterval = d
	}
}

func WithExecuteTimeout(d time.Duration) TaskerOption {
	return func(o *TaskerOptions) {
		o.executeTimeout = d
	}
}

func WithOnlyTicker(onlyTicker bool) TaskerOption {
	return func(o *TaskerOptions) {
		o.onlyTicker = onlyTicker
	}
}

func WithOnlyUpdate(onlyUpdate bool) TaskerOption {
	return func(o *TaskerOptions) {
		o.onlyUpdate = onlyUpdate
	}
}

func WithUniqueId(id string) TaskerOption {
	return func(o *TaskerOptions) {
		o.uniqueId = id
	}
}

func WithOutput(output io.Writer) TaskerOption {
	return func(o *TaskerOptions) {
		o.logger = log.New(output, "tasker: ", log.Lmsgprefix|log.LstdFlags)
		log.SetOutput(output)
	}
}
