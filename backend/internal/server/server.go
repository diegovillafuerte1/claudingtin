// Package server is the backend's HTTP surface: the websocket upgrade at /ws and
// GET /status. It owns no connection state — that lives in package hub — and it
// speaks nothing on the wire that is not a proto message.
//
// The only two routes that exist are /ws and /status. Every other path is 404;
// a non-GET /status is 405 (architecture decision AD-13). Once a websocket is
// open, a client-facing failure is a proto error frame, never a transport close
// code — the one exception is the AD-5 please_update flow, which is its own
// frame followed by a normal close.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// defaultHelloDeadline bounds how long a freshly opened socket may stay silent
// before it must have sent its first frame.
const defaultHelloDeadline = 10 * time.Second

// frameWriteTimeout bounds a single outbound frame write.
const frameWriteTimeout = 5 * time.Second

type server struct {
	hub           *hub.Hub
	logger        *slog.Logger
	helloDeadline time.Duration
}

// New returns the backend HTTP handler. logger may be nil (slog.Default is used).
func New(h *hub.Hub, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return (&server{hub: h, logger: logger, helloDeadline: defaultHelloDeadline}).routes()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/", s.handleFallback)
	return mux
}

// handleFallback is every path other than /ws and /status.
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

// handleWS upgrades to a websocket and hands off to serveConn.
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	remote := remoteHost(r)
	// The companion is a native client, not browser JavaScript, so there is no
	// Origin to police and no cookie-borne authority to protect against CSRF —
	// identity is the account key inside the hello frame.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Info("ws upgrade failed", "remote", remote, "err", err.Error())
		return
	}

	s.serveConn(conn, remote)
}

// serveConn runs one websocket connection start to finish: read the first frame
// under the hello deadline, gate the protocol version, register the key, then
// translate inbound ready / busy frames into hub commands and write whatever
// frame the hub hands back on sess.Outbound (queued / matched / session_ended),
// until the client goes away or the hub evicts this connection. This handler is
// the only goroutine that writes frames to conn.
func (s *server) serveConn(conn *websocket.Conn, remote string) {
	// coder/websocket warns against using the request context after Accept, so
	// the connection gets its own lifetime context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer conn.CloseNow()

	firstCtx, firstCancel := context.WithTimeout(ctx, s.helloDeadline)
	_, data, err := conn.Read(firstCtx)
	firstCancel()
	if err != nil {
		// No first frame before the deadline, or the client vanished. Nothing is
		// registered; just close.
		_ = conn.Close(websocket.StatusPolicyViolation, "")
		s.logger.Info("ws closed before hello", "remote", remote)
		return
	}

	msg, err := proto.Decode(data)
	if err != nil {
		s.writeFrame(ctx, conn, proto.Error{Code: "bad_frame", Msg: "first frame could not be decoded"})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		s.logger.Info("ws bad first frame", "remote", remote, "reason", "undecodable")
		return
	}

	hello, ok := msg.(proto.Hello)
	if !ok {
		s.writeFrame(ctx, conn, proto.Error{Code: "expected_hello", Msg: "first frame must be hello"})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		s.logger.Info("ws bad first frame", "remote", remote, "reason", "not_hello")
		return
	}

	if hello.AccountKey == "" {
		// An empty key would register every keyless client under "" — they would
		// all collide and mutually evict. Reject it like any other bad first frame.
		s.writeFrame(ctx, conn, proto.Error{Code: "bad_frame", Msg: "empty account key"})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		s.logger.Info("ws bad first frame", "remote", remote, "reason", "empty_account_key")
		return
	}

	pv := hello.ProtocolVersion
	// Accept the current protocol version, the previous one, and anything newer
	// than this backend. Only a client strictly older than that gets asked to
	// update — one please_update frame, then a normal close, never an error
	// frame and never a bare close.
	if pv < proto.PROTOCOL_VERSION-1 {
		s.writeFrame(ctx, conn, proto.PleaseUpdate{})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		s.logger.Info("ws please_update", "remote", remote, "pv", pv)
		return
	}

	// Outbound is buffered so the hub goroutine's non-blocking send never drops a
	// frame just because this handler is briefly between reads.
	sess := &hub.Session{
		Key:      hello.AccountKey,
		Conn:     conn,
		Evict:    make(chan struct{}),
		Outbound: make(chan any, 32),
	}
	s.hub.Register(sess)
	s.logger.Info("ws connected", "remote", remote, "pv", pv)

	defer func() {
		s.hub.Unregister(sess)
		s.logger.Info("ws disconnected", "remote", remote)
	}()

	// The read loop lives on its own goroutine so the handler's select can also
	// wait on eviction and on hub-delivered outbound frames. Post-hello frames
	// are decoded; only ready / busy / leave / chat_msg do anything — every other
	// frame, and any decode error, is ignored (the v1 discard behaviour).
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			msg, err := proto.Decode(data)
			if err != nil {
				continue
			}
			switch m := msg.(type) {
			case proto.Ready:
				s.hub.Ready(sess)
			case proto.Busy:
				s.hub.Busy(sess)
			case proto.Leave:
				// A client leave ends the matched session the same way busy
				// does: the hub routes it through teardownPair, the peer gets a
				// byte-identical session_ended, and nothing is sent back to the
				// leaver. A no-op in the hub if this session is not paired.
				s.hub.Leave(sess)
			case proto.ChatMsg:
				// Relay to the paired peer only. The hub drops it if this
				// session is not paired. No log line in either direction — even
				// a content-free one would leak chat volume and timing.
				s.hub.Relay(sess, m)
			}
		}
	}()

	// sentEnd tracks whether this connection has already been told the CURRENT
	// matched session ended. It is the handler-level guarantee that a connection
	// writes at most one session_ended per matched session: the same socket can
	// be told by both the hub teardown path (an Outbound SessionEnded) and the
	// Evict case (a taken-over connection that is also someone's peer), and only
	// the first write goes out. A fresh proto.Matched re-arms it, because one
	// companion socket outlives many matches.
	sentEnd := false

	for {
		select {
		case <-readDone:
			// Client closed the socket or the connection dropped. The deferred
			// Unregister removes the key, drops any queue entry, and tears down
			// any pairing (the peer gets a session_ended).
			return
		case msg := <-sess.Outbound:
			// The hub matched or tore down this session. This handler is the
			// sole writer for conn, so the frame is written here.
			switch msg.(type) {
			case proto.Matched:
				// A fresh match re-arms the per-session session_ended guard:
				// one companion socket outlives many matches.
				sentEnd = false
				s.logger.Info("ws matched", "remote", remote)
			case proto.SessionEnded:
				if sentEnd {
					// Already ended this session (the Evict case, or an earlier
					// teardown frame, wrote it). Drop the duplicate — no write,
					// no log.
					continue
				}
				sentEnd = true
				s.logger.Info("ws session ended", "remote", remote)
			}
			s.writeFrame(ctx, conn, msg)
		case <-sess.Evict:
			// A newer connection for this key took over. Tell this client its
			// session ended — unless the hub teardown path already sent one for
			// this session (takeover racing a concurrent peer end) — close
			// normally, then cancel the connection context so the read loop
			// returns even if the close alone does not unblock it, and wait for
			// that loop to unwind.
			if !sentEnd {
				s.writeFrame(ctx, conn, proto.SessionEnded{})
				sentEnd = true
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
			cancel()
			<-readDone
			s.logger.Info("ws taken over", "remote", remote)
			return
		}
	}
}

// writeFrame encodes msg with proto.Encode and writes it as one text frame.
// Failures are logged, not surfaced — the caller is already on a close path.
func (s *server) writeFrame(ctx context.Context, conn *websocket.Conn, msg any) {
	b, err := proto.Encode(msg)
	if err != nil {
		s.logger.Error("frame encode failed", "err", err.Error())
		return
	}
	wctx, cancel := context.WithTimeout(ctx, frameWriteTimeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, b); err != nil {
		s.logger.Info("frame write failed", "err", err.Error())
	}
}

// remoteHost is r.RemoteAddr without the port. It never contains an account key.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
