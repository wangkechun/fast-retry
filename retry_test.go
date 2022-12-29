// nolint
package fast_retry

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
)

func TestRetryNormal(t *testing.T) {
	// 如果正确，应该只发起一次请求
	ctx := context.Background()
	var n atomic.Int32
	_, err := BackupRetry(ctx, Option{}, func() (resp interface{}, err error) {
		n.Inc()
		return "hello", nil
	})
	require.Nil(t, err)
	require.Equal(t, int32(1), n.Load())
}

func TestRetryAllFailed(t *testing.T) {
	// 如果请求可以全部失败，那么也能快速返回
	ctx := context.Background()
	var n atomic.Int32
	_, err := BackupRetry(ctx, Option{}, func() (resp interface{}, err error) {
		n.Inc()
		return nil, errors.New("xx")
	})
	require.NotNil(t, err)
	require.Equal(t, int32(3), n.Load())
}

func TestRetryCancelBug(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var n atomic.Int32
	_, err := BackupRetry(ctx, Option{}, func() (resp interface{}, err error) {
		n.Inc()
		return "hello", nil
	})
	require.Equal(t, context.Canceled, err)
	require.Equal(t, int32(0), n.Load())
}

func TestRetryCancelBug2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	go func() {
		time.Sleep(time.Second / 100)
		cancel()
	}()
	_, err := BackupRetry(ctx, Option{}, func() (resp interface{}, err error) {
		time.Sleep(time.Second * 10)
		return "hello", nil
	})
	require.Equal(t, context.Canceled, err)
	require.True(t, time.Since(now).Seconds() < 0.1)
}

func TestRetryBench(t *testing.T) {
	l := sync.Mutex{}
	lines := make([]Line, 0)
	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)
	for i := 0; i < 100000; i++ {
		fn := func() (resp interface{}, err error) {
			if rand.Float64() < 0.99 {
				if rand.Float64() < 0.99 {
					time.Sleep(time.Second * 1)
					return "ok", nil
				} else {
					time.Sleep(time.Second * 1)
					return nil, errors.New("bad req")
				}
			}
			if rand.Float64() < 0.5 {
				time.Sleep(time.Second * 11)
				return "ok", nil
			}
			time.Sleep(time.Second * 11)
			return nil, errors.New("timeout")
		}
		g.Go(func() error {
			start := time.Now()
			_, err := BackupRetry(ctx, Option{}, fn)
			useTime := time.Since(start)
			l.Lock()
			lines = append(lines, Line{Type: "retry", UseTime: useTime.Seconds(), Err: err != nil})
			l.Unlock()
			return nil
		})
		g.Go(func() error {
			start := time.Now()
			var err error
			for i := 0; i < 3; i++ {
				_, err = fn()
				if err == nil {
					break
				}
			}
			useTime := time.Since(start)
			l.Lock()
			lines = append(lines, Line{Type: "normal", UseTime: useTime.Seconds(), Err: err != nil})
			l.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	//buf, err := json.Marshal(lines)
	//require.Nil(t, err)
	//err = ioutil.WriteFile("out.json", buf, 0644)
	//require.Nil(t, err)

	normalErr := 0
	retryErr := 0
	normalSlow := 0
	retrySlow := 0
	for _, line := range lines {
		if line.Err && line.Type == "normal" {
			normalErr++
		}
		if line.Err && line.Type == "retry" {
			retryErr++
		}
		if line.UseTime > 5 && line.Type == "normal" {
			normalSlow++
		}
		if line.UseTime > 5 && line.Type == "retry" {
			retrySlow++
		}
	}
	t.Log("normalErr", normalErr, "retryErr", retryErr, "normalSlow", normalSlow, "retrySlow", retrySlow)
}
