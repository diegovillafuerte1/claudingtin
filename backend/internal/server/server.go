// Package server is the backend's HTTP surface: the /v1 HTTPS long-poll
// transport (POST /v1/hello, GET /v1/events, POST /v1/send — see v1.go) and
// GET /status. Queue and pairing state live in package hub; this package holds
// only transport state (the /v1 handles), and it speaks nothing on the wire
// that is not a proto message.
//
// Those four routes are the only ones. Every other path is 404; a route hit
// with the wrong method is 405 (architecture decision AD-13).
package server

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
)

type server struct {
	hub    *hub.Hub
	logger *slog.Logger
}

// New returns the backend HTTP handler. logger may be nil (slog.Default is used).
func New(h *hub.Hub, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return (&server{hub: h, logger: logger}).routes()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	newV1API(s.hub, s.logger, defaultV1Config()).register(mux)
	mux.HandleFunc("/", s.handleFallback)
	return mux
}

// handleFallback is every path other than /status and the /v1 routes.
func (s *server) handleFallback(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// handleStatus answers GET /status with {"concurrent_users": N}. Any other
// method is 405.
func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		s.logger.Info("status rejected", "method", r.Method, "remote", remoteHost(r))
		return
	}

	n := s.hub.Count()
	body, err := json.Marshal(struct {
		ConcurrentUsers int `json:"concurrent_users"`
	}{ConcurrentUsers: n})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		s.logger.Error("status marshal failed", "err", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	// Debug, not Info: uptime probes hit this constantly and must not flood stdout.
	s.logger.Debug("status", "remote", remoteHost(r), "concurrent_users", n)
}

// remoteHost is r.RemoteAddr without the port. It never contains an account key.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
