package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/krateoplatformops/logstack/auger/internal/handlers"
	etcdutil "github.com/krateoplatformops/logstack/auger/internal/util/etcd"
	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/kubeutil"
	"github.com/krateoplatformops/plumbing/server/use"
	"github.com/krateoplatformops/plumbing/server/use/cors"
	"github.com/krateoplatformops/plumbing/slogs/pretty"
)

const (
	serviceName = "auger"
)

var (
	build string
)

func main() {
	debugOn := flag.Bool("debug", env.Bool("DEBUG", false), "enable or disable debug logs")
	port := flag.Int("port", env.ServicePort("PORT", 8081), "port to listen on")
	etcdServers := flag.String("etcd-servers", env.String("ETCD_SERVERS", "localhost:2379"),
		"Comma-separated list of etcd endpoints used to store and retrieve logs.")

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}

	flag.Parse()

	os.Setenv("DEBUG", strconv.FormatBool(*debugOn))

	logLevel := slog.LevelInfo
	if *debugOn {
		logLevel = slog.LevelDebug
	}

	lh := pretty.New(&slog.HandlerOptions{
		Level:     logLevel,
		AddSource: false,
	},
		pretty.WithDestinationWriter(os.Stderr),
		pretty.WithColor(),
		pretty.WithOutputEmptyAttrs(),
	)

	log := slog.New(lh).With(slog.String("service", serviceName))
	if *debugOn {
		log.Debug("environment variables", slog.Any("env", os.Environ()))
	}

	etcdClient, err := etcdutil.NewEtcdClient(strings.Split(*etcdServers, ","))
	if err != nil {
		log.Error("unable to create Etcd client", slog.Any("err", err))
		os.Exit(1)
	}
	defer etcdClient.Close()

	chain := use.NewChain(
		use.TraceId(),
		use.Logger(log),
	)

	mux := http.NewServeMux()

	//mux.Handle("GET /swagger/", httpSwagger.WrapHandler)
	mux.Handle("GET /health", handlers.HealthCheck(serviceName, build, kubeutil.ServiceAccountNamespace))
	mux.Handle("GET /logs", chain.Then(handlers.Logs(etcdClient)))

	ctx, stop := signal.NotifyContext(context.Background(), []os.Signal{
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGKILL,
		syscall.SIGHUP,
		syscall.SIGQUIT,
	}...)
	defer stop()

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", *port),
		Handler: use.CORS(cors.Options{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{
				"Accept",
				"Authorization",
				"Content-Type",
				"X-Auth-Code",
				"X-Krateo-TraceId",
			},
			ExposedHeaders:   []string{"Link"},
			AllowCredentials: true,
			MaxAge:           300, // Maximum value not ignored by any of major browsers
		})(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 50 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server cannot run",
				slog.String("addr", server.Addr),
				slog.Any("err", err))
		}
	}()

	// Listen for the interrupt signal.
	log.Info("server is ready to handle requests", slog.String("addr", server.Addr))
	<-ctx.Done()

	// Restore default behavior on the interrupt signal and notify user of shutdown.
	stop()
	log.Info("server is shutting down gracefully, press Ctrl+C again to force")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server.SetKeepAlivesEnabled(false)
	if err := server.Shutdown(ctx); err != nil {
		log.Error("server forced to shutdown", slog.Any("err", err))
	}

	log.Info("server gracefully stopped")
}
