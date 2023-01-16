package fast_retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
)

func TestRetryNormal(t *testing.T) {
	// 如果正确，应该只发起一次请求
	ctx := context.Background()
	var n atomic.Int32
	r := New(Config{})
	_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
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
	r := New(Config{})
	_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
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
	r := New(Config{})
	_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
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
	r := New(Config{})
	_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
		time.Sleep(time.Second * 10)
		return "hello", nil
	})
	require.Equal(t, context.Canceled, err)
	require.True(t, time.Since(now).Seconds() < 0.1)
}

func TestRetryBench(t *testing.T) {
	t.Skip("用于人工看优化效果")
	l := sync.Mutex{}
	lines := make([]Line, 0)
	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)
	r := New(Config{})
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
			_, err := r.BackupRetry(ctx, fn)
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

func TestMaxRetryRate(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)
	var callbackCnt atomic.Int64
	var errorCnt atomic.Int64
	var successCnt atomic.Int64
	// 模拟 FastRetryTime 设置不当，看请求是否放大两次
	r := New(Config{MaxRetryRate: 0.1, FastRetryTime: time.Second / 15})
	fn := func() (resp interface{}, err error) {
		callbackCnt.Inc()
		time.Sleep(time.Second / 10)
		return "ok", nil
	}
	for i := 0; i < 100; i++ {
		g.Go(func() error {
			for j := 0; j < 100; j++ {
				_, err := r.BackupRetry(ctx, fn)
				if err != nil {
					errorCnt.Inc()
				} else {
					successCnt.Inc()
				}
			}
			return nil
		})
	}
	_ = g.Wait()
	t.Logf("%v %v %v", callbackCnt.Load(), errorCnt.Load(), successCnt.Load())
	require.Equal(t, errorCnt.Load(), int64(0))
	require.True(t, callbackCnt.Load() < int64(float64(successCnt.Load())*1.2))
}

func TestMaxRetryCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	var callbackCnt atomic.Int64
	var errorCnt atomic.Int64
	var successCnt atomic.Int64
	// 模拟 FastRetryTime 设置不当，看请求是否放大两次
	maxRetryCapacity := 100
	r := New(Config{MaxRetryRate: 0.1, FastRetryTime: time.Second / 15, MaxRetryCapacity: maxRetryCapacity})
	slow := false
	fn := func() (resp interface{}, err error) {
		callbackCnt.Inc()
		if slow {
			time.Sleep(time.Second / 10)
		}
		return "ok", nil
	}
	{
		g, ctx := errgroup.WithContext(ctx)
		for i := 0; i < 100; i++ {
			g.Go(func() error {
				for j := 0; j < 100; j++ {
					_, err := r.BackupRetry(ctx, fn)
					if err != nil {
						fmt.Println(err)
						errorCnt.Inc()
					} else {
						successCnt.Inc()
					}
				}
				return nil
			})
		}
		_ = g.Wait()
	}

	slow = true
	{
		g, ctx := errgroup.WithContext(ctx)
		for i := 0; i < 50; i++ {
			g.Go(func() error {
				for j := 0; j < 100; j++ {
					_, err := r.BackupRetry(ctx, fn)
					if err != nil {
						fmt.Println(err)
						errorCnt.Inc()
					} else {
						successCnt.Inc()
					}
				}
				return nil
			})
		}
		_ = g.Wait()
	}
	// 先 10000 个快速请求，然后 5000 个慢请求
	// 5000 里面会有 10% 被重试，之前 10000 会积累一部分 quota
	t.Logf("%v %v %v", callbackCnt.Load(), errorCnt.Load(), successCnt.Load())
	require.Equal(t, errorCnt.Load(), int64(0))
	require.True(t, callbackCnt.Load() < int64(float64(successCnt.Load())*1.2))
	require.True(t, callbackCnt.Load() <= int64(10000+5000+5000*0.1+maxRetryCapacity*2))
}

func BenchmarkRetry(b *testing.B) {
	ctx := context.Background()
	r := New(Config{MaxRetryRate: 0.1, FastRetryTime: time.Second / 100})
	for i := 0; i < b.N; i++ {
		_, _ = r.BackupRetry(ctx, func() (resp interface{}, err error) {
			return "ok", nil
		})
	}
}

func TestMemLeakSimple(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	str := strings.Repeat("1234567890", 1024) // 10kb
	ctx := context.Background()
	r := New(Config{MaxRetryRate: 0.1, FastRetryTime: time.Second / 100})
	g := sync.WaitGroup{}
	for i := 0; i < 100000; i++ {
		g.Add(1)
		go func() {
			_, _ = r.BackupRetry(ctx, func() (resp interface{}, err error) {
				time.Sleep(time.Second / 10)
				if rand.Float64() < 0.1 {
					return nil, errors.New("ok")
				}
				return str + "a", nil
			})
			g.Done()
		}()
		if i%100 == 0 {
			t.Logf("i:%v", i)
		}
	}
	g.Wait()
	time.Sleep(time.Second)
	t.Logf("go:%v", runtime.NumGoroutine())
	require.True(t, runtime.NumGoroutine() < 5)
}

func TestMemLeakFull(t *testing.T) {
	if os.Getenv("TEST_LEAK") == "" {
		t.Skip()
	}
	// TEST_LEAK=1 GODEBUG=gctrace=1 go test -run TestMemLeak -v
	// 内存泄露测试，如果允许内存一直不变，则代表无内存泄露
	// 如果把 m[i] = "ok" 注释回来，则可以模拟泄露的情况

	go func() {
		for {
			time.Sleep(time.Second * 3)
			t.Logf("num:%v", runtime.NumGoroutine())
		}
	}()
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	str := strings.Repeat("1234567890", 1024) // 10kb
	ctx := context.Background()
	r := New(Config{MaxRetryRate: 0.1, FastRetryTime: time.Second / 100})
	// m := map[int]string{}
	for i := 0; i < 100000; i++ {
		// m[i] = "ok"
		go func() {
			_, _ = r.BackupRetry(ctx, func() (resp interface{}, err error) {
				time.Sleep(time.Second)
				if rand.Float64() < 0.1 {
					return nil, errors.New("ok")
				}
				return str + "a", nil
			})
		}()
		if i%100 == 0 {
			t.Logf("i:%v", i)
		}
	}
	time.Sleep(time.Hour)
}

type Line struct {
	Type    string
	UseTime float64
	Err     bool
}

func TestRetryIf(t *testing.T) {
	{
		// 设置了 RetryIf， 重试一次
		ctx := context.Background()
		var n atomic.Int32
		r := New(Config{RetryIf: func(err error) bool {
			if err == io.EOF {
				return false
			}
			return err != nil
		}})
		_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
			n.Inc()
			return "ok", io.EOF
		})
		require.Error(t, err, io.EOF)
		require.Equal(t, int32(1), n.Load())
	}
	{
		// 设置了 RetryIf， 但是非预期错误，重试 3 次
		ctx := context.Background()
		var n atomic.Int32
		r := New(Config{RetryIf: func(err error) bool {
			if err == io.EOF {
				return false
			}
			return err != nil
		}})
		_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
			n.Inc()
			return "ok", errors.New("ok")
		})
		require.Error(t, err, errors.New("ok"))
		require.Equal(t, int32(3), n.Load())
	}
	{
		// 没有设置了 RetryIf， 重试 3 次
		ctx := context.Background()
		var n atomic.Int32
		r := New(Config{RetryIf: func(err error) bool {
			return err != nil
		}})
		_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
			n.Inc()
			return "ok", io.EOF
		})
		require.Error(t, err, io.EOF)
		require.Equal(t, int32(3), n.Load())
	}
}

func TestRetryQuota(t *testing.T) {
	{
		ctx := context.Background()
		var n atomic.Int32
		r := New(Config{FastRetryTime: time.Millisecond * 20, MaxRetryRate: 0.05, RetryIf: func(err error) bool {
			return err == io.EOF
		}})
		{
			_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
				n.Inc()
				time.Sleep(time.Millisecond * 30)
				return "ok", nil
			})
			require.Nil(t, err)
			require.Equal(t, r.score.Load(), int64(-19))
			require.Equal(t, int32(2), n.Load())
		}
		// 此时 r.score 处于小于0的状态
		// 如果 接下来的请求本身成功了，那么还是返回成功，只是 fast retry 没有 quota 自动失效
		{
			_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
				n.Inc()
				time.Sleep(time.Millisecond * 50)
				return "ok", nil
			})
			require.Nil(t, err)
			require.Equal(t, r.score.Load(), int64(-18))
			require.Equal(t, int32(3), n.Load())
		}
		// 如果 接下来的请求本身失败了，那么返回真实的错误
		{
			_, err := r.BackupRetry(ctx, func() (resp interface{}, err error) {
				n.Inc()
				time.Sleep(time.Millisecond * 50)
				return "ok", io.EOF
			})
			require.Equal(t, r.score.Load(), int64(-17))
			require.Equal(t, int32(5), n.Load())
			require.Equal(t, err, io.EOF)
		}
	}
}
