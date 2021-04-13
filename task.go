package task

import (
	"context"
	"errors"
	"time"
)

var (
	TaskDefaultExecuteTimeout = time.Second * 5          // execute timeout
	TaskDefaultTimeout        = time.Hour * 24 * 30 * 12 // default timeout
	TaskDefaultSleep          = time.Millisecond * 500   // sleep time 500ms
	ErrTimeout                = errors.New("time out")
)

type TaskHandler func(context.Context, ...interface{}) error
type Task struct {
	c context.Context // control run timeout
	f TaskHandler     // handle function
	e chan<- error    // error returned
	p []interface{}   // params in order
}

type Tasker struct {
	opts  *TaskerOptions
	tasks chan *Task
}

func NewTasker(max int32) *Tasker {
	return &Tasker{
		opts:  &TaskerOptions{},
		tasks: make(chan *Task, max),
	}
}

func (t *Tasker) Init(opts ...TaskerOption) {
	t.opts = defaultTaskerOptions()

	for _, o := range opts {
		o(t.opts)
	}
}

func (t *Tasker) ResetTimer() {
	tm := t.opts.timer
	if tm != nil && !tm.Stop() {
		<-tm.C
	}
	tm.Reset(t.opts.d)
}

func (t *Tasker) Add(ctx context.Context, f TaskHandler, p ...interface{}) error {
	subCtx, cancel := context.WithTimeout(ctx, TaskDefaultExecuteTimeout)
	defer cancel()

	e := make(chan error, 1)
	tk := &Task{
		c: subCtx,
		f: f,
		e: e,
		p: make([]interface{}, 0, len(p)),
	}
	tk.p = append(tk.p, p...)
	t.tasks <- tk

	select {
	case err := <-e:
		return err
	case <-subCtx.Done():
		return subCtx.Err()
	}
}

func (t *Tasker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			if t.opts.ctxCb != nil {
				t.opts.ctxCb() // context done callback
			}
			return nil

		case h, ok := <-t.tasks:
			if !ok {
				return nil
			} else {
				err := h.f(h.c, h.p...)
				h.e <- err // handle result
				if err == nil {
					continue
				}
			}

		case <-t.opts.timer.C:
			return ErrTimeout

		default:
			now := time.Now()
			if t.opts.updateCb != nil {
				t.opts.updateCb() // update callback
			}
			d := time.Since(now)
			time.Sleep(t.opts.sleep - d)
		}
	}
}

func (t *Tasker) Stop() {
	close(t.tasks)
	t.opts.timer.Stop()
}
