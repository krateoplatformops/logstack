package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"context"

	"github.com/krateoplatformops/logstack/frostbeat/internal/manager"
	etcdutil "github.com/krateoplatformops/logstack/frostbeat/internal/util/etcd"
	"github.com/krateoplatformops/logstack/frostbeat/internal/writers"
	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/slogs/pretty"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/client-go/kubernetes"
)

const (
	serviceName = "frostbeat"
)

var (
	build string
)

func main() {
	kconfig := flag.String(clientcmd.RecommendedConfigPathFlag, "", "Absolute path to the kubeconfig file")
	debugOn := flag.Bool("debug", env.Bool("DEBUG", false), "Enable or disable debug logs")
	batchSize := flag.Int("batch-size", env.Int("BATCH_SIZE", 20),
		"Maximum number of log entries to send in a single batch to the destination backend.")
	batchPeriod := flag.Duration("batch-period", env.Duration("BATCH_PERIOD", 5*time.Second),
		"Maximum time to wait before flushing a batch, even if the batch is not full.")
	namespace := flag.String("namespace", env.String("NAMESPACE", "demo-system"),
		"Kubernetes namespace to watch for pods and collect logs from.")
	selector := flag.String("label-selector", env.String("SELECTOR", "app=snowplow"),
		"Label selector used to filter which pods to watch for log collection.")
	etcdServers := flag.String("etcd-servers", env.String("ETCD_SERVERS", "localhost:2379"),
		"Comma-separated list of etcd endpoints used to store and retrieve logs.")
	logChanSize := flag.Int("log-chan-size", env.Int("LOG_CHAN_SIZE", 1000),
		"Size of the buffered channel used to queue log entries before processing. A higher value allows better handling of bursts, but increases memory usage. The retry queue size is derived proportionally from this value.")
	ttl := flag.Duration("ttl", env.Duration("TTL", 48*time.Hour),
		"TTL (Time-To-Live) duration for keys stored in etcd. After this period, keys expire and are removed automatically. Use duration format (e.g., \"24h\", \"30m\"). Zero or negative disables TTL.")

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}

	flag.Parse()

	logLevel := slog.LevelInfo
	if *debugOn {
		logLevel = slog.LevelDebug
	}

	log := slog.New(pretty.New(&slog.HandlerOptions{
		Level:     logLevel,
		AddSource: false,
	},
		pretty.WithDestinationWriter(os.Stderr),
		pretty.WithColor(),
		pretty.WithOutputEmptyAttrs(),
	)).With(slog.String("service", serviceName))

	if strings.TrimSpace(*namespace) == "" {
		log.Error("namespace cannot be empty")
		os.Exit(1)
	}

	if strings.TrimSpace(*selector) == "" {
		log.Error("pod label selector cannot be empty")
		os.Exit(1)
	}

	var cfg *rest.Config
	var err error
	if len(*kconfig) > 0 {
		cfg, err = clientcmd.BuildConfigFromFlags("", *kconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Error("unable to resolve rest.Config", slog.Any("err", err))
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Error("unable to create kubernetes.Clientset", slog.Any("err", err))
		os.Exit(1)
	}

	etcdClient, err := etcdutil.NewEtcdClient(strings.Split(*etcdServers, ","))
	if err != nil {
		log.Error("unable to create Etcd client", slog.Any("err", err))
		os.Exit(1)
	}
	defer etcdClient.Close()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager := manager.NewPodLogManager(manager.PodLogManagerOptions{
		Clientset:     clientset,
		Namespace:     *namespace,
		LabelSelector: *selector,
		BatchPeriod:   *batchPeriod,
		BatchSize:     *batchSize,
		LogChanSize:   *logChanSize,
	})

	err = manager.Start(ctx, &wg, writers.Etcd(etcdClient, writers.TTL(*ttl)))
	if err != nil {
		log.Error("unable to start PodLogManager", slog.Any("err", err))
		os.Exit(1)
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Info("Signal received, shutting down...", slog.Any("signal", sig))
	case <-ctx.Done():
		log.Info("Context done, shutting down...")
	}

	manager.Shutdown()

	wg.Wait()
	log.Info("Shutdown complete")
}
