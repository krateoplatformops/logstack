package etcd

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	defaultTimeout = 5 * time.Minute
)

func NewEtcdClient(endpoints []string) (cli *clientv3.Client, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	opts := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 2 * time.Second,
	}

	err = retryWithContext(ctx, 5, 2*time.Second, func() error {
		cli, err = clientv3.New(opts)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("unable to create Etcd client after retries: %w", err)
	}

	return cli, nil
}

func retryWithContext(ctx context.Context, attempts int, delay time.Duration, fn func() error) error {
	var err error
	for range attempts {
		// Check if context was canceled or timed out
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err = fn(); err == nil {
			return nil
		}

		// Wait or return early if context is canceled during sleep
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("operation failed after %d attempts: %w", attempts, err)
}
