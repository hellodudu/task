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
	ticker   *time.Ticker
	running  atomic.Bool
	draining atomic.Bool
	chStop   chan bool

	started  atomic.Bool
	stopOnce sync.Once
}

func NewTasker() *Tasker {
	return &Tasker{
		opts:   &TaskerOptions{},
		tasks:  NewQueue(),
		ticker: time.NewTicker(TaskDefaultUpdateInterval),
		chStop: make(chan bool, 1),
	}
}

func (t *Tasker) Init(opts ...TaskerOption) {
	t.opts = defaultTaskerOptions()

	for _, o := range opts {
		o(t.opts)
	}

	t.running.Store(true)
	t.draining.Store(false)
	t.ticker.Reset(t.opts.updateInterval)
}

func (t *Tasker) ResetTimeout() {
	if t.opts.timeout != nil {
		// Some callers may provide an uninitialized *time.Timer (zero value),
		// for which Stop() panics. Be defensive: timeout is only a safety net.
		func() {
			defer func() { _ = recover() }()
			t.opts.timeout.Stop()
		}()
	}
	t.opts.timeout = time.NewTimer(t.opts.d)
}

func (t *Tasker) GetTaskNum() int {
	return int(t.tasks.Size())
}

func (t *Tasker) IsRunning() bool {
	return t.running.Load()
}

func (t *Tasker) IsDraining() bool {
	return t.draining.Load()
}

func (t *Tasker) canEnqueue() bool {
	if t.running.Load() {
		return true
	}
	return t.started.Load() && t.draining.Load()
}

func (t *Tasker) AddWait(ctx context.Context, f TaskHandler, p ...interface{}) error {
	if !t.canEnqueue() {
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

func (t *Tasker) Add(ctx context.Context, f TaskHandler, p ...interface{}) error {
	if !t.canEnqueue() {
		return ErrTaskNotRunning
	}

	tk := &Task{
		c: ctx,
		f: f,
		e: nil,
		p: make([]interface{}, 0, len(p)),
	}
	tk.p = append(tk.p, p...)
	t.tasks.Push(tk)
	return nil
}

func (t *Tasker) executeTask(tk *Task) {
	select {
	case <-tk.c.Done():
		return
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

func (t *Tasker) processPendingTasks() {
	for {
		h := t.tasks.Pop()
		if h == nil {
			break
		}
		t.executeTask(h.(*Task))
	}
}

func (t *Tasker) drainPendingTasks() {
	idleRounds := 0
	const maxIdleRounds = 5

	for idleRounds < maxIdleRounds {
		h := t.tasks.Pop()
		if h != nil {
			t.executeTask(h.(*Task))
			idleRounds = 0
			continue
		}

		if t.tasks.Size() > 0 {
			continue
		}

		idleRounds++
		time.Sleep(time.Millisecond)
	}
}

func (t *Tasker) Run(ctx context.Context) (reterr error) {
	t.ResetTimeout()
	t.started.Store(true)

	defer func() {
		t.draining.Store(false)
		t.stop()
		if err := recover(); err != nil {
			stack := string(debug.Stack())
			t.opts.logger.Printf("catch exception:%v, panic recovered with stack:%s", err, stack)
			reterr = ErrTaskPanic
		}
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

			case <-t.chStop:
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

			case <-t.chStop:
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
				t.drainPendingTasks()
				return nil

			case <-t.chStop:
				t.drainPendingTasks()
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
					t.drainPendingTasks()
					t.draining.Store(false)
					return nil
				}

				if t.tasks.Size() <= 0 {
					time.Sleep(time.Millisecond * 50)
					continue
				}

				t.processPendingTasks()
			}
		}
	}
}

func (t *Tasker) Stop() <-chan bool {
	// t.tasks = NewQueue()
	t.ticker.Stop()

	// Enter graceful shutdown: keep accepting tasks until drain completes.
	t.draining.Store(true)
	t.running.Store(false)

	// If Run() hasn't started yet, it may never reach defer t.stop().
	// Trigger stop asynchronously so Stop() itself never blocks on stopFns.
	if !t.started.Load() {
		t.draining.Store(false)
		go t.stop()
	}

	return t.chStop
}

func (t *Tasker) stop() {
	t.stopOnce.Do(func() {
		if len(t.opts.stopFns) > 0 {
			for _, fn := range t.opts.stopFns {
				fn()
			}
		}

		tm := t.opts.timeout
		if tm != nil {
			func() {
				defer func() { _ = recover() }()
				tm.Stop()
			}()
		}
		t.running.Store(false)
		t.draining.Store(false)

		select {
		case <-t.chStop:
		default:
			close(t.chStop)
		}
	})
}
