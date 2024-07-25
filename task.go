package task

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/atomic"
)

var (
	TaskDefaultExecuteTimeout = time.Second * 5      // execute timeout
	TaskDefaultTimeout        = time.Hour * 24 * 356 // default timeout
	TaskDefaultUpdateInterval = time.Second          // update interval
	ErrTimeout                = errors.New("time out")
	ErrTaskPanic              = errors.New("task panic")
	ErrTaskFailed             = errors.New("task failed")
	ErrTaskFulled             = errors.New("task fulled")
	ErrTaskNotRunning         = errors.New("task not running")
)

type TaskHandler func(context.Context, ...interface{}) error
type Task struct {
	c context.Context // control run timeout
	f TaskHandler     // handle function
	e chan<- error    // error returned
	p []interface{}   // params in order
}

type Tasker struct {
	opts     *TaskerOptions
	tasks    *Queue
	stopChan chan struct{}
	ticker   *time.Ticker
	stopOnce *sync.Once
	running  atomic.Bool
}

func NewTasker() *Tasker {
	return &Tasker{
		opts:     &TaskerOptions{},
		tasks:    NewQueue(),
		stopChan: make(chan struct{}, 1),
		ticker:   time.NewTicker(TaskDefaultUpdateInterval),
		stopOnce: new(sync.Once),
	}
}

func (t *Tasker) Init(opts ...TaskerOption) {
	t.opts = defaultTaskerOptions()

	for _, o := range opts {
		o(t.opts)
	}

	t.running.Store(true)
	t.ticker.Reset(t.opts.updateInterval)
}

func (t *Tasker) ResetTimeout() {
	tm := t.opts.timeout
	if tm != nil && !tm.Stop() {
		<-tm.C
	}
	tm.Reset(t.opts.d)
}

func (t *Tasker) GetTaskNum() int {
	return int(t.tasks.Size())
}

func (t *Tasker) IsRunning() bool {
	return t.running.Load()
}

func (t *Tasker) AddWait(ctx context.Context, f TaskHandler, p ...interface{}) error {
	if !t.IsRunning() {
		return ErrTaskNotRunning
	}

	subCtx, cancel := context.WithTimeout(ctx, t.opts.executeTimeout)
	defer cancel()

	e := make(chan error, 1)
	tk := &Task{
		c: subCtx,
		f: f,
		e: e,
		p: make([]interface{}, 0, len(p)),
	}
	tk.p = append(tk.p, p...)

	t.tasks.Push(tk)

	// wait result
	select {
	case err := <-e:
		if err == nil {
			return nil
		}
		return fmt.Errorf("task wait channel result with error:%w, chan buff size:%d", err, t.tasks.Size())
	case <-subCtx.Done():
		if subCtx.Err() == nil {
			return nil
		}
		return fmt.Errorf("task wait channel result with timeout:%w, chan buff size:%d", subCtx.Err(), t.tasks.Size())
	}
}

func (t *Tasker) Add(ctx context.Context, f TaskHandler, p ...interface{}) {
	if !t.IsRunning() {
		return
	}

	tk := &Task{
		c: ctx,
		f: f,
		e: nil,
		p: make([]interface{}, 0, len(p)),
	}
	tk.p = append(tk.p, p...)
	t.tasks.Push(tk)
}

func (t *Tasker) Run(ctx context.Context) (reterr error) {
	t.ResetTimeout()

	defer func() {
		if err := recover(); err != nil {
			stack := string(debug.Stack())
			t.opts.logger.Printf("catch exception:%v, panic recovered with stack:%s", err, stack)
			reterr = ErrTaskPanic
		}
		t.stop()
	}()

	if len(t.opts.startFns) > 0 {
		for _, fn := range t.opts.startFns {
			fn()
		}
	}

	// only update ticker
	if t.opts.onlyTicker {
		for {
			select {
			case <-ctx.Done():
				return nil

			case <-t.opts.timeout.C:
				return ErrTimeout

			case <-t.ticker.C:
				if t.opts.updateFn != nil {
					// grace stop task when update
					if err := t.opts.updateFn(); err != nil {
						return err
					}
				}
			}
		}
	} else if t.opts.onlyUpdate {
		for {
			select {
			case <-ctx.Done():
				return nil

			case <-t.opts.timeout.C:
				return ErrTimeout

			default:

				tm := time.Now()
				if t.opts.updateFn != nil {
					if err := t.opts.updateFn(); err != nil {
						return err
					}
				}

				elapse := time.Since(tm)
				if elapse < t.opts.updateInterval {
					time.Sleep(t.opts.updateInterval - elapse)
				} else {
					time.Sleep(0)
				}
			}
		}
	} else {
		for {
			select {
			case <-ctx.Done():
				return nil

			case <-t.opts.timeout.C:
				return ErrTimeout

			case <-t.ticker.C:
				if t.opts.updateFn != nil {
					// grace stop task when update
					if err := t.opts.updateFn(); err != nil {
						return err
					}
				}

			default:
				if !t.IsRunning() {
					return nil
				}

				if t.tasks.Size() <= 0 {
					time.Sleep(time.Millisecond * 50)
					continue
				}

				for {
					h := t.tasks.Pop()
					if h == nil {
						break
					}

					tk := h.(*Task)
					select {
					case <-tk.c.Done():
						continue
					default:
					}

					err := tk.f(tk.c, tk.p...)
					if tk.e != nil {
						tk.e <- err // handle result
					}

					if err != nil {
						funcName := runtime.FuncForPC(reflect.ValueOf(tk.f).Pointer()).Name()
						t.opts.logger.Printf("execute %s with error:%v\n", funcName, err)
					}
				}
			}
		}
	}
}

func (t *Tasker) Stop() {
	t.stopOnce.Do(func() {
		t.tasks = NewQueue()
		t.ticker.Stop()
		t.running.Store(false)
		<-t.stopChan
	})
}

func (t *Tasker) stop() {
	if len(t.opts.stopFns) > 0 {
		for _, fn := range t.opts.stopFns {
			fn()
		}
	}

	t.opts.timeout.Stop()
	t.running.Store(false)

	select {
	case <-t.stopChan:
		return
	default:
		close(t.stopChan)
	}
}
