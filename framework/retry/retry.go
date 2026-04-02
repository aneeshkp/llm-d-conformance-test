// Package retry provides retry and timeout utilities for the test framework.
package retry

import (
	"context"
	"fmt"
	"time"
)

// Options configures retry behavior.
type Options struct {
	Timeout  time.Duration
	Interval time.Duration
	Name     string
}

// UntilSuccess retries fn until it succeeds, the context is canceled, or the timeout is reached.
func UntilSuccess(ctx context.Context, opts Options, fn func() error) error {
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.Interval == 0 {
		opts.Interval = 10 * time.Second
	}

	deadline := time.Now().Add(opts.Timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var lastErr error
	attempts := 0

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%s timed out after %d attempts: %w", opts.Name, attempts, lastErr)
			}
			return fmt.Errorf("%s timed out after %d attempts: %w", opts.Name, attempts, ctx.Err())
		default:
		}

		attempts++
		if err := fn(); err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s failed after %d attempts: %w", opts.Name, attempts, lastErr)
			case <-time.After(opts.Interval):
				continue
			}
		}
		return nil
	}
}

// WithTimeout runs fn with a timeout, returning an error if it doesn't complete in time.
func WithTimeout(ctx context.Context, timeout time.Duration, name string, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- fn(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%s timed out after %s", name, timeout)
	}
}
