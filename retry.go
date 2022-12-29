package fast_retry

import (
	"context"
	"errors"
	"time"

	"go.uber.org/atomic"

	"github.com/facebookgo/clock"
)

var ErrRetryQuotaExceeded = errors.New("fast_retry:retry quota exceeded")

type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type retryableFuncResp struct {
	resp interface{}
	err  error
}

type Config struct {
	FastRetryTime time.Duration //  默认 2s，建议设置成当前 API 的 PCT99
	RetryCnt      int           // 最多发送的请求次数，默认 3 次，最少两次，其中一次是快速重试
	RetryWaitTime time.Duration // 重试间隔，默认是 FastRetryTime / 10
	Clock         Clock         // 模拟时钟，mock 用
	MaxRetryRate  float64       // 最大重试百分比，0.05 代表 5%，默认 5%
}

type Retry struct {
	*Config
	score         atomic.Int64
	oneRetryScore int64
}

func New(config Config) *Retry {
	r := &Retry{Config: &config}
	if r.FastRetryTime == 0 {
		r.FastRetryTime = time.Second * 2
	}
	if r.RetryCnt == 0 {
		r.RetryCnt = 3
	}
	if r.RetryWaitTime == 0 {
		r.RetryWaitTime = r.FastRetryTime / 10
	}
	if r.RetryCnt < 2 {
		panic("bad RetryTimes")
	}
	if r.Clock == nil {
		r.Clock = clock.New()
	}
	if r.MaxRetryRate == 0 {
		r.MaxRetryRate = 0.05
	}
	if r.MaxRetryRate <= 0 || r.MaxRetryRate >= 1 {
		panic("bad MaxRetryRate")
	}
	r.oneRetryScore = int64(1 / r.MaxRetryRate)
	return r
}

func (r *Retry) BackupRetry(ctx context.Context, retryableFunc func() (resp interface{}, err error)) (resp interface{}, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.score.Add(1)
	doFunc := func() retryableFuncResp {
		resp, err := retryableFunc()
		return retryableFuncResp{resp: resp, err: err}
	}
	result := make(chan retryableFuncResp, r.RetryCnt)
	fastRetryJustNow := make(chan bool, 2)
	go func() {
		r.Clock.Sleep(r.FastRetryTime)
		fastRetryJustNow <- true
	}()
	go func() {
		<-fastRetryJustNow
		if ctx.Err() == nil {
			if r.score.Load() < 0 {
				// 重试配额消耗完毕
				result <- retryableFuncResp{err: ErrRetryQuotaExceeded}
				return
			}
			r.score.Add(-1 * r.oneRetryScore)
			result <- doFunc()
		}
	}()
	go func() {
		// 快速重试消耗一次配额，所以这里从2开始
		for i := 2; i <= r.RetryCnt; i++ {
			if i > 2 {
				r.Clock.Sleep(r.RetryWaitTime)
			}
			if ctx.Err() != nil {
				break
			}
			result <- doFunc()
			// 如果普通重试已经完毕，那么快速重试无需等待2s
			if i == r.RetryCnt {
				fastRetryJustNow <- true
			}
		}
	}()
	var firstResp retryableFuncResp
	for i := 0; i < r.RetryCnt; i++ {
		select {
		case r := <-result:
			if r.err == nil {
				return r.resp, r.err
			}
			if i == 0 {
				firstResp = r
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return firstResp.resp, firstResp.err
}

type Line struct {
	Type    string
	UseTime float64
	Err     bool
}
