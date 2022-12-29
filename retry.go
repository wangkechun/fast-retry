package fast_retry

import (
	"context"
	"time"

	"github.com/facebookgo/clock"
)

type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type retryableFuncResp struct {
	resp interface{}
	err  error
}

type Option struct {
	FastRetryTime time.Duration //  默认 2s，建议设置成当前 API 的 PCT99
	RetryCnt      int           // 最多发送的请求次数，默认 3 次，最少两次，其中一次是快速重试
	RetryWaitTime time.Duration // 重试间隔，默认是 FastRetryTime / 10
	Clock         Clock         // 模拟时钟，mock 用
}

// 可以降低慢请求概率的重试，Option 全部参数为选填
// nolint:gomnd
func BackupRetry(ctx context.Context, option Option, retryableFunc func() (resp interface{}, err error)) (resp interface{}, err error) {
	if option.FastRetryTime == 0 {
		option.FastRetryTime = time.Second * 2
	}
	if option.RetryCnt == 0 {
		option.RetryCnt = 3
	}
	if option.RetryWaitTime == 0 {
		option.RetryWaitTime = option.FastRetryTime / 10
	}
	if option.RetryCnt < 2 {
		panic("bad RetryTimes")
	}
	if option.Clock == nil {
		option.Clock = clock.New()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	doFunc := func() retryableFuncResp {
		resp, err := retryableFunc()
		return retryableFuncResp{resp: resp, err: err}
	}
	result := make(chan retryableFuncResp, option.RetryCnt)
	fastRetryJustNow := make(chan bool, 2)
	go func() {
		option.Clock.Sleep(option.FastRetryTime)
		fastRetryJustNow <- true
	}()
	go func() {
		<-fastRetryJustNow
		if ctx.Err() == nil {
			result <- doFunc()
		}
	}()
	go func() {
		// 快速重试消耗一次配额，所以这里从2开始
		for i := 2; i <= option.RetryCnt; i++ {
			if ctx.Err() != nil {
				break
			}
			if i > 2 {
				option.Clock.Sleep(option.RetryWaitTime / 10)
			}
			result <- doFunc()
			// 如果普通重试已经完毕，那么快速重试无需等待2s
			if i == option.RetryCnt {
				fastRetryJustNow <- true
			}
		}
	}()
	var firstResp retryableFuncResp
	for i := 0; i < option.RetryCnt; i++ {
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
