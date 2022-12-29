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
