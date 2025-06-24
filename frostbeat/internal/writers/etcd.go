package writers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdOption func(w *etcdWriterImpl)

func TTL(ttl time.Duration) EtcdOption {
	return func(w *etcdWriterImpl) {
		w.ttl = int64(ttl.Seconds())
	}
}

func Logger(log *slog.Logger) EtcdOption {
	return func(w *etcdWriterImpl) {
		w.logger = log
	}
}

func Etcd(client *clientv3.Client, opts ...EtcdOption) LogWriter {
	impl := &etcdWriterImpl{
		client:  client,
		timeOut: defaultTimeout,
		ttl:     defaultTTL,
	}

	for _, opt := range opts {
		opt(impl)
	}

	if impl.logger == nil {
		impl.logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	return impl
}

const (
	defaultTimeout = 200 * time.Millisecond
	defaultTTL     = -1
)

type etcdWriterImpl struct {
	client  *clientv3.Client
	timeOut time.Duration
	ttl     int64
	logger  *slog.Logger
}

func (w *etcdWriterImpl) WriteBatch(batch []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	kv := clientv3.NewKV(w.client)
	txn := kv.Txn(ctx)
	ops := []clientv3.Op{}

	for _, raw := range batch {
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			w.logger.Warn("unable to unmarshal log data", slog.Any("err", err))
			continue
		}
		txID, _ := data["traceId"].(string)
		if txID == "" {
			w.logger.Warn("skipping record since 'traceId' not found in log data")
			continue
		}

		rawTime, _ := data["time"].(string)
		if rawTime == "" {
			w.logger.Warn("skipping record since 'time' not found in log data")
			continue
		}

		parsedTime, err := time.Parse(time.RFC3339Nano, rawTime)
		if err != nil {
			w.logger.Warn("invalid time format", slog.Any("err", err))
			continue
		}

		key := fmt.Sprintf("logs/%s/%d", txID, parsedTime.UnixNano())

		opopt := []clientv3.OpOption{}
		if w.ttl > 0 {
			lease := clientv3.NewLease(w.client)
			res, err := lease.Grant(context.Background(), w.ttl)
			if err == nil {
				opopt = append(opopt, clientv3.WithLease(res.ID))
			}
		}
		ops = append(ops, clientv3.OpPut(key, raw, opopt...))
	}

	if len(ops) == 0 {
		return nil
	}

	txn = txn.Then(ops...)
	_, err := txn.Commit()
	return err
}
