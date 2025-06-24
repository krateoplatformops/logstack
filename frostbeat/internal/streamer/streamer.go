package streamer

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type PodLogStreamerOptions struct {
	Clientset   *kubernetes.Clientset
	Namespace   string
	PodName     string
	Output      chan<- string
	Ctx         context.Context
	MaxRetries  int
	InitialWait time.Duration
	Logger      *slog.Logger
}

type PodLogStreamer struct {
	clientset   *kubernetes.Clientset
	namespace   string
	podName     string
	output      chan<- string
	ctx         context.Context
	maxRetries  int
	initialWait time.Duration
	log         *slog.Logger
}

func NewPodLogStreamer(ctx context.Context, opts PodLogStreamerOptions) *PodLogStreamer {
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}

	if opts.InitialWait <= 0 {
		opts.InitialWait = 3 * time.Second
	}

	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	return &PodLogStreamer{
		clientset:   opts.Clientset,
		namespace:   opts.Namespace,
		podName:     opts.PodName,
		output:      opts.Output,
		maxRetries:  opts.MaxRetries,
		initialWait: opts.InitialWait,
		ctx:         ctx,
		log: opts.Logger.With(
			slog.String("component", "pod-logs-streamer"),
			slog.String("name", opts.PodName),
			slog.String("namespace", opts.Namespace),
		),
	}
}

func (p *PodLogStreamer) Start() {
	retries := 0
	delay := p.initialWait

	for {
		if p.ctx.Err() != nil {
			p.log.Warn("requeste stop", slog.String("name", p.podName))
			return
		}

		p.log.Info("starting logs stream", slog.Int("attempt", retries+1), slog.String("name", p.podName))
		if p.streamOnce() {
			return
		}

		retries++
		if retries >= p.maxRetries {
			p.log.Warn("too many errors, leaving logs stream", slog.String("name", p.podName))
			return
		}

		p.log.Info("retry logs stream", slog.Duration("delay", delay), slog.String("name", p.podName))
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(delay):
			delay *= 2
		}
	}
}

func (p *PodLogStreamer) streamOnce() bool {
	logOpts := &corev1.PodLogOptions{Follow: true}
	req := p.clientset.CoreV1().Pods(p.namespace).GetLogs(p.podName, logOpts)
	stream, err := req.Stream(p.ctx)
	if err != nil {
		p.log.Error("unable to open logs stream", slog.String("name", p.podName), slog.Any("err", err))
		return false
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		select {
		case <-p.ctx.Done():
			return true
		default:
			line := scanner.Bytes()
			if len(line) > 0 {
				p.output <- string(bytes.TrimSpace(line))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		p.log.Error("error while scanning stream", slog.String("name", p.podName), slog.Any("err", err))
		return false
	}

	p.log.Info("ending stream without errors", slog.String("name", p.podName))

	return true
}
