// Command serve is the claudingtin backend: a websocket upgrade endpoint at /ws
// that reads a versioned hello and tracks one connection per account key, plus
// GET /status for the maintainer view. Configuration is environment only — PORT,
// default 8080. Logs are structured JSON on stdout and carry no account key and
// no frame text.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/server"
)

const (
	shutdownTimeout   = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()

	h := hub.New()
	go h.Run(hubCtx)

	srv := &http.Server{
		Addr:    net.JoinHostPort("", port),
		Handler: server.New(h, logger),
		// Only ReadHeaderTimeout is set on purpose: a whole-request ReadTimeout
		// or WriteTimeout would tear down the long-lived /ws connections.
		ReadHeaderTimeout: readHeaderTimeout,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-signalCtx.Done()
		logger.Info("shutdown requested")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "err", err.Error())
		}
		stopHub()
	}()

	logger.Info("serve listening", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listen failed", "err", err.Error())
		os.Exit(1)
	}

	// ListenAndServe returned http.ErrServerClosed, i.e. Shutdown was called.
	// Wait for the drain (and hub stop) to finish before exiting.
	<-shutdownDone
	logger.Info("serve stopped")
}
