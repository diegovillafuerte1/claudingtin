// Command serve is the claudingtin backend: the /v1 HTTPS long-poll transport
// (versioned hello, held event polls, sends; one live handle per account key),
// plus GET /status for the maintainer view. Configuration is environment only —
// PORT (default 8080) and DB_PATH, the SQLite policy store (default
// claudingtin.db; Story 4.1). Logs are structured JSON on stdout and carry no
// account key and no frame text.
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

	"github.com/diegovillafuerte1/claudingtin/backend"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/server"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/store"
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

	// Validate the embedded opener set before the hub goroutine starts. The hub
	// calls backend.Openers() (which panics on a bad set) from inside its own
	// goroutine, so without this a malformed openers.txt would surface as an
	// unrecovered panic racing a listening socket rather than a clean startup
	// failure (Epic 2 retro F8).
	if _, err := backend.LoadOpeners(); err != nil {
		logger.Error("opener set is invalid; refusing to start", "err", err.Error())
		os.Exit(1)
	}

	// The policy store: migrated, then loaded into the hub before it runs, so
	// the very first match already honours every block, cooldown and ban. Any
	// failure here is a refusal to start, never a server running without policy.
	db, h, err := openPolicyHub(signalCtx, os.Getenv("DB_PATH"), time.Now(), logger)
	if err != nil {
		logger.Error("policy store unusable; refusing to start", "db_path", dbPathOrDefault(os.Getenv("DB_PATH")), "err", err.Error())
		os.Exit(1)
	}
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		h.Run(hubCtx)
	}()
	// The store closes only once the hub has stopped, so no write-through can
	// land on a closed database.
	closeStore := func() {
		stopHub()
		<-hubDone
		_ = db.Close()
	}

	srv := &http.Server{
		Addr:    net.JoinHostPort("", port),
		Handler: server.New(h, logger),
		// Only ReadHeaderTimeout is set on purpose: a whole-request ReadTimeout
		// or WriteTimeout would tear down the held /v1/events polls.
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
		closeStore()
		os.Exit(1)
	}

	// ListenAndServe returned http.ErrServerClosed, i.e. Shutdown was called.
	// Wait for the drain (and hub stop) to finish before exiting.
	<-shutdownDone
	closeStore()
	logger.Info("serve stopped")
}

// defaultDBPath is DB_PATH when it is unset or empty.
const defaultDBPath = "claudingtin.db"

func dbPathOrDefault(p string) string {
	if p == "" {
		return defaultDBPath
	}
	return p
}

// openPolicyHub opens and migrates the store at dbPath (defaultDBPath when
// empty), loads the policy live at now, and builds the hub enforcing it with
// the store as its write-through. Any error means serve must not start.
func openPolicyHub(ctx context.Context, dbPath string, now time.Time, logger *slog.Logger) (*store.Store, *hub.Hub, error) {
	db, err := store.Open(ctx, dbPathOrDefault(dbPath))
	if err != nil {
		return nil, nil, err
	}
	policy, err := db.LoadPolicy(ctx, now.UnixMilli())
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	logger.Info("policy loaded", "blocks", len(policy.Blocks), "cooldowns", len(policy.Cooldowns), "bans", len(policy.Bans))
	h := hub.New(
		hub.WithPolicy(policy, db),
		hub.WithWriteError(func(err error) {
			logger.Error("policy write-through failed; kept in memory", "err", err.Error())
		}),
	)
	return db, h, nil
}
