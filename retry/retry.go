package retry

import (
	"context"
	"errors"
	"time"
)

type RetryFn func() error

type RetryFnRetVal func() (any, error)

type RetryCondFn func(error) bool

func retryRetValInt(ctx context.Context, retryFnRetVal RetryFnRetVal, retryCondFn RetryCondFn, bf Backoff, retries int) (any, error) {
	if retryFnRetVal == nil {
		return nil, errors.New("retryFnRetVal cannot be nil")
	}
	if retryCondFn == nil {
		return nil, errors.New("retryCondFn cannot be nil")
	}
	delay := bf.GetBase()
	timer := time.NewTimer(delay)
	var (
		err error
		out any
	)

	for i := 0; i < retries+1; i++ {
		out, err = retryFnRetVal()
		if err == nil {
			return out, nil
		}
		if !retryCondFn(err) {
			break
		}
		if i == retries {
			break
		}
		if i > 0 {
			timer.Reset(delay)
		}
		select {
		case <-timer.C:
			delay = bf.ComputeDelay()
			continue
		case <-ctx.Done():
			return nil, err
		}
	}
	return out, err
}

func RetryRetValWithLinearBackOff(ctx context.Context, retryFnRetVal RetryFnRetVal, retryCondFn RetryCondFn,
	delay time.Duration, retries int) (any, error) {
	lb := &LinearBackoff{
		Base:  delay,
		Delay: delay,
		Cap:   24 * time.Hour,
	}
	return retryRetValInt(ctx, retryFnRetVal, retryCondFn, lb, retries)
}
