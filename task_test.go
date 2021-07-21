package task

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTask(t *testing.T) {
	tasker := NewTasker()
	tasker.Init(
		WithStartFns(
			func() {
				fmt.Println("tasker start fn1...")
			},
			func() {
				fmt.Println("tasker start fn2...")
			},
		),

		WithStopFns(
			func() {
				fmt.Println("tasker stop fn1...")
			},
			func() {
				fmt.Println("tasker stop fn2...")
			},
		),

		WithUpdateFn(func() {
			fmt.Println("tasker update...")
		}),

		WithTimeout(time.Second*5),

		WithSleep(time.Millisecond*100),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// task run
	go func() {
		err := tasker.Run(ctx)
		if err != nil {
			fmt.Printf("tasker run failed: %s...\n", err.Error())
		}
		fmt.Println("tasker completed...")
	}()

	// add task
	_ = tasker.AddWait(
		ctx,
		func(c context.Context, p ...interface{}) error {
			p1 := p[0].(int)
			p2 := p[1].(string)
			fmt.Printf("task<%d> handled with param2: %v...\n", p1, p2)
			return nil
		},
		1,
		"parameters",
	)

	// concurrent
	for n := 0; n < 10; n++ {
		go func(taskId int) {
			_ = tasker.AddWait(
				ctx,
				func(c context.Context, p ...interface{}) error {
					p1 := p[0].(int)
					fmt.Printf("task<%d> handled...\n", p1)
					return nil
				},
				taskId,
			)
		}(n + 1000)

	}

	time.Sleep(time.Second * 6)
	tasker.Stop()
}
