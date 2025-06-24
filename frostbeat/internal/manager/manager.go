package manager

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/krateoplatformops/logstack/frostbeat/internal/streamer"
	"github.com/krateoplatformops/logstack/frostbeat/internal/writers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultLogChanSize   = 500
	defaultBatchSize     = 20
	defaultBatchPeriod   = 5 * time.Second
	defaultRetryChanSize = 10
)

type PodLogManager struct {
	clientset     *kubernetes.Clientset
	namespace     string
	labelSelector string

	batchPeriod time.Duration
	batchSize   int

	logChan   chan string
	retryChan chan []string

	writer writers.LogWriter

	ctx    context.Context
	cancel context.CancelFunc

	streamCancelMu  sync.Mutex
	streamCancelMap map[string]context.CancelFunc

	wg *sync.WaitGroup

	log *slog.Logger
}

type PodLogManagerOptions struct {
	Clientset     *kubernetes.Clientset
	Namespace     string
	LabelSelector string
	BatchPeriod   time.Duration
	BatchSize     int
	LogChanSize   int
	RetryChanSize int
	Logger        *slog.Logger
}

func NewPodLogManager(opts PodLogManagerOptions) *PodLogManager {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	if opts.BatchSize < defaultBatchSize {
		opts.BatchSize = defaultBatchSize
	}

	if opts.BatchPeriod <= 1*time.Second {
		opts.BatchPeriod = defaultBatchPeriod
	}

	if opts.LogChanSize < defaultLogChanSize {
		opts.LogChanSize = defaultLogChanSize
	}

	opts.RetryChanSize = max(int(opts.LogChanSize/100), defaultRetryChanSize)

	return &PodLogManager{
		clientset:     opts.Clientset,
		namespace:     opts.Namespace,
		labelSelector: opts.LabelSelector,

		batchPeriod: opts.BatchPeriod,
		batchSize:   opts.BatchSize,

		logChan:   make(chan string, opts.LogChanSize),
		retryChan: make(chan []string, opts.RetryChanSize),

		streamCancelMap: make(map[string]context.CancelFunc),

		log: opts.Logger.With(
			slog.String("component", "pod-logs-manager"),
			slog.String("namespace", opts.Namespace),
		),
	}
}

// Start starts everything (watcher + writer + retry handler)
func (m *PodLogManager) Start(ctx context.Context, wg *sync.WaitGroup, writer writers.LogWriter) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.wg = wg
	m.writer = writer

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.startLogWriter()
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.startRetryHandler()
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.monitorPressure()
	}()

	return m.startPodWatcher()
}

func (m *PodLogManager) Shutdown() {
	m.log.Info("shutting down")
	m.cancel()

	m.streamCancelMu.Lock()
	for name, cancel := range m.streamCancelMap {
		m.log.Info("stopping logs streamer for pod", slog.Any("name", name))
		cancel()
	}
	m.streamCancelMu.Unlock()

	close(m.logChan)
	close(m.retryChan)
}

// startPodWatcher starts watcher and streamer
func (m *PodLogManager) startPodWatcher() error {
	pods, err := m.clientset.CoreV1().Pods(m.namespace).List(m.ctx, metav1.ListOptions{
		LabelSelector: m.labelSelector,
	})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			m.startStreamer(pod.Name)
		}
	}

	go m.watchPods()

	return nil
}

// watcher loop
func (m *PodLogManager) watchPods() {
	watcher, err := m.clientset.CoreV1().Pods(m.namespace).Watch(m.ctx, metav1.ListOptions{
		LabelSelector: m.labelSelector,
	})
	if err != nil {
		m.log.Error("unable to watch pods",
			slog.String("selector", m.labelSelector),
			slog.Any("err", err))
		return
	}
	defer watcher.Stop()

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				m.log.Warn("watcher result channel was closed, stopping pods watcher")
				return
			}
			m.handlePodEvent(event)
		case <-m.ctx.Done():
			m.log.Info("context done, stop watching")
			return
		}
	}
}

func (m *PodLogManager) handlePodEvent(event watch.Event) {
	pod, ok := event.Object.(*corev1.Pod)
	if !ok {
		m.log.Warn("discarding invalid pod event")
		return
	}

	switch event.Type {
	case watch.Added, watch.Modified:
		if pod.Status.Phase == corev1.PodRunning {
			m.startStreamer(pod.Name)
		} else {
			m.stopStreamer(pod.Name)
		}
	case watch.Deleted:
		m.stopStreamer(pod.Name)
	case watch.Error:
		m.log.Error("received error event from pod watcher", slog.Any("event", event))
	}
}

func (m *PodLogManager) startStreamer(podName string) {
	m.streamCancelMu.Lock()
	defer m.streamCancelMu.Unlock()

	if _, exists := m.streamCancelMap[podName]; exists {
		return
	}

	podCtx, podCancel := context.WithCancel(m.ctx)
	m.streamCancelMap[podName] = podCancel

	streamer := streamer.NewPodLogStreamer(podCtx, streamer.PodLogStreamerOptions{
		Clientset: m.clientset,
		Namespace: m.namespace,
		PodName:   podName,
		Output:    m.logChan,
	})

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		streamer.Start()

		m.streamCancelMu.Lock()
		delete(m.streamCancelMap, podName)
		m.streamCancelMu.Unlock()
	}()
}

func (m *PodLogManager) stopStreamer(podName string) {
	m.streamCancelMu.Lock()
	defer m.streamCancelMu.Unlock()
	if cancel, exists := m.streamCancelMap[podName]; exists {
		cancel()
		delete(m.streamCancelMap, podName)
	}
}

// startLogWriter handle batch writes with retry
func (m *PodLogManager) startLogWriter() {
	ticker := time.NewTicker(m.batchPeriod)
	defer ticker.Stop()

	batch := make([]string, 0, m.batchSize)

	for {
		select {
		case line, ok := <-m.logChan:
			if !ok {
				// Channel closed, flush and exit
				if len(batch) > 0 {
					_ = m.writer.WriteBatch(batch)
				}
				return
			}
			batch = append(batch, line)
			if len(batch) >= m.batchSize {
				if err := m.writer.WriteBatch(batch); err != nil {
					m.retryChan <- batch
				}
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				if err := m.writer.WriteBatch(batch); err != nil {
					m.retryChan <- batch
				}
				batch = batch[:0]
			}
		case <-m.ctx.Done():
			// Flush before exit
			if len(batch) > 0 {
				_ = m.writer.WriteBatch(batch)
			}
			return
		}
	}
}

// startRetryHandler handles retries
func (m *PodLogManager) startRetryHandler() {
	for {
		select {
		case batch, ok := <-m.retryChan:
			if !ok {
				return
			}
			m.log.Info("Retrying batch...")
			for _, line := range batch {
				m.logChan <- line
			}
		case <-m.ctx.Done():
			return
		}
	}
}

func (m *PodLogManager) monitorPressure() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			usage := float64(len(m.logChan)) / float64(cap(m.logChan))
			if usage > 0.8 {
				m.log.Warn("logChan nearing capacity", slog.Float64("usage", usage))
			}
		case <-m.ctx.Done():
			return
		}
	}
}
