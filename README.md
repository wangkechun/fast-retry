# fast-retry

[![Release](https://img.shields.io/github/release/wangkechun/fast-retry.svg?style=flat-square)](https://github.com/wangkechun/fast-retry/releases/latest)
[![Software License](https://img.shields.io/badge/license-MIT-brightgreen.svg?style=flat-square)](LICENSE.md)
[![Go Report Card](https://goreportcard.com/badge/github.com/wangkechun/fast-retry?style=flat-square)](https://goreportcard.com/report/github.com/wangkechun/fast-retry)
[![GoDoc](https://godoc.org/github.com/wangkechun/fast-retry?status.svg&style=flat-square)](http://godoc.org/github.com/wangkechun/fast-retry)


Simple library for Backup Requests

## Instructions

服务中的长尾请求增加了服务的整体延迟，长尾请求一般占比很低，但是响应时间可能比平均时间多出一个数量级。

相当于一部分长尾请求是偶发的，也就是说，立即重发请求，可以正常返回。如果简单的加上重试，无助于减少长尾请求数量。

这个包实现了一种叫 Backup Requests 的技术，在检查到当前请求处理时间超出 PCT99 之后，再发起一个请求，任意一次请求返回我们就认为请求成功，通过增加适当的负载，大大减少了响应时间的波动。

此包还支持配置最大增加的请求比例，防止错误的配置导致发出双倍的请求。

## Example

```go
package main

import (
	"context"
	"fmt"
	"time"

	fast_retry "github.com/wangkechun/fast-retry"
)

func main() {
	ctx := context.Background()
	retry := fast_retry.New(fast_retry.Config{FastRetryTime: time.Second / 10})
	resp, err := retry.BackupRetry(ctx, func() (interface{}, error) {
		return "hello", nil
	})
	if err != nil {
		fmt.Println("err", err)
	} else {
		fmt.Println("resp", resp.(string))
	}
}
```

## Usage

```go
type Config struct {
    FastRetryTime time.Duration // 默认 2s，建议设置成当前 API 的 PCT99
    MaxRetryRate  float64       // 最大重试百分比，0.05 代表 5%，默认 5%，超出返回 ErrRetryQuotaExceeded
    RetryCnt      int           // 最多发送的请求次数，默认 3 次，最少两次，其中一次是快速重试
    RetryWaitTime time.Duration // 重试间隔，默认是 FastRetryTime / 10
    Clock         Clock         // 模拟时钟，mock 用
}
```