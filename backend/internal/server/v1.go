package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// The /v1 HTTPS transport (Story 7.2) puts the mod in front of the unchanged hub
// without a socket:
//
//   - POST /v1/hello mints an opaque handle (welcome) or answers please_update.
//   - GET  /v1/events?after=N long-polls the handle's one ordered event queue
//     (AD-19): every server→client message for the handle, each stamped with a
//     monotonic seq. after=N acknowledges, so events with seq ≤ N are dropped.
//   - POST /v1/send carries one client→server message to the hub.
//
// Each live handle owns exactly one hub.Session at a time. A pump goroutine
// drains that session's Outbound into the event queue. A lease (AD-17) keeps the
// handle connected while a poll is in flight and for leaseAfter after the last
// one ends. When the lease lapses the handle calls hub.Unregister, which runs
// the hub's existing reconnect grace window. The handle's next poll or send
// registers a fresh hub.Session that names the last matched session_id, so the
// hub's resume path picks the pairing back up.
//
// The handle registry below is transport state, not queue/pairing state (that
// stays in the hub, AD-8). One mutex guards it, and hub calls that must stay
// ordered with it (Register/Unregister/Ready/…) are made while holding it. That
// cannot deadlock: the hub goroutine never takes this lock and never blocks on a
// delivery.

// handleHeader carries the handle on /v1/events and /v1/send. The account key
// travels only in the hello body (AD-3).
const handleHeader = "X-Claudingtin-Handle"

// maxV1Body caps every /v1 request body. Per-message content caps are Epic 4's.
const maxV1Body = 64 << 10

// v1Outbound is the hub.Session Outbound buffer. The
// pump drains it continuously into the event queue, so a full event queue — not
// this channel — is where overflow is detected.
const v1Outbound = 32

// v1Config is the /v1 timing and sizing. A struct, not consts, so tests can
// shrink it.
type v1Config struct {
	pollHold   time.Duration // how long an idle GET /v1/events is held
	leaseAfter time.Duration // how long a handle stays connected after its last poll ends
	handleTTL  time.Duration // how long a lapsed or superseded handle is kept before it is forgotten
	eventCap   int           // max unacknowledged events before overflow ends the session
}

func defaultV1Config() v1Config {
	return v1Config{
		pollHold:   20 * time.Second,
		leaseAfter: 5 * time.Second,
		handleTTL:  60 * time.Second,
		eventCap:   256,
	}
}

type v1API struct {
	hub    *hub.Hub
	logger *slog.Logger
	cfg    v1Config

	mu       sync.Mutex
	byHandle map[string]*handle
	byKey    map[string]*handle
}

func newV1API(h *hub.Hub, logger *slog.Logger, cfg v1Config) *v1API {
	return &v1API{
		hub:      h,
		logger:   logger,
		cfg:      cfg,
		byHandle: make(map[string]*handle),
		byKey:    make(map[string]*handle),
	}
}

func (a *v1API) register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/hello", methodOnly(http.MethodPost, a.handleHello))
	mux.HandleFunc("/v1/events", methodOnly(http.MethodGet, a.handleEvents))
	mux.HandleFunc("/v1/send", methodOnly(http.MethodPost, a.handleSend))
}

func methodOnly(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

type event struct {
	seq int64
	raw []byte
}

// handle is one minted /v1 handle. Every field below id/key is guarded by
// v1API.mu.
type handle struct {
	id  string
	key string

	sess   *hub.Session // the hub session registered most recently for this handle
	pump   *pump        // the pump draining sess
	lapsed bool         // lease lapsed: sess is unregistered from the hub
	closed bool         // superseded or expired: no new events, no re-register

	events     []event // unacknowledged events, seq ascending
	lastSeq    int64
	lastWasEnd bool   // the most recent event is session_ended (never two in a row)
	overflowed bool   // overflow ended the session; drop relays until the next matched
	sessionID  string // session_id of the current match, "" when not matched
	wantsMatch bool   // the client's last intent was ready (replayed on re-register)

	inflight int // GET /v1/events requests currently held
	leaseGen int // bumps invalidate a pending lease timer
	gcTimer  *time.Timer
	notify   chan struct{} // closed and replaced whenever a held poll should re-check
}

// pump drains one hub.Session's Outbound (and watches its Evict) into its
// handle's event queue. When a handle re-registers, the new pump's pred is the
// old pump. The hub delivers to the old session only until it processes the new
// Register, so the first message on the new Outbound means the old one is final.
// The new pump then finishes its pred (drain, exit) before pushing, which keeps
// hub order across the switch.
//
// pred is atomic because reregister also reads it, to tell a pump two
// generations back that its session is final (see reregister). That keeps the
// chain at most three pumps long, however often a handle lapses.
type pump struct {
	sess     *hub.Session
	pred     atomic.Pointer[pump]
	stop     chan struct{}
	stopOnce sync.Once
	exited   chan struct{}
}

func (p *pump) signalStop() { p.stopOnce.Do(func() { close(p.stop) }) }

// finish stops p and waits for it to drain and exit. Never call it holding
// v1API.mu: p's goroutine takes the lock to push.
func (p *pump) finish() {
	p.signalStop()
	<-p.exited
}

func (a *v1API) startPump(h *handle, s *hub.Session, pred *pump) *pump {
	p := &pump{sess: s, stop: make(chan struct{}), exited: make(chan struct{})}
	p.pred.Store(pred)
	go a.runPump(h, p)
	return p
}

func (a *v1API) runPump(h *handle, p *pump) {
	defer close(p.exited)
	finishPred := func() {
		if q := p.pred.Swap(nil); q != nil {
			q.finish()
		}
	}
	drainOwn := func() {
		for {
			select {
			case m := <-p.sess.Outbound:
				a.push(h, m)
			default:
				return
			}
		}
	}
	for {
		select {
		case m := <-p.sess.Outbound:
			finishPred()
			a.push(h, m)
		case <-p.sess.Evict:
			// The hub took this session over. On the handle's current pump that
			// is another /v1 hello for the key, which has already closed the
			// handle, so this arm is defensive. On an older pump it is this
			// handle's own re-register displacing a stale session, which is not
			// a reason to close the handle.
			finishPred()
			drainOwn()
			a.mu.Lock()
			if !h.closed && h.pump == p {
				a.closeHandle(h)
				a.logger.Info("v1 taken over")
			}
			a.mu.Unlock()
			return
		case <-p.stop:
			finishPred()
			drainOwn()
			return
		}
	}
}

// push turns one hub-delivered message into the next event on h's queue.
func (a *v1API) push(h *handle, m any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h.closed {
		return
	}
	switch msg := m.(type) {
	case proto.Matched:
		h.overflowed = false
		h.sessionID = msg.SessionID
	case proto.SessionEnded:
		a.appendEnd(h)
		return
	case proto.ChatMsg:
		if h.overflowed {
			return
		}
		if len(h.events) >= a.cfg.eventCap {
			// AD-19: a full queue ends the session rather than dropping
			// silently. Only chat lines count against the cap. State events
			// (queued, matched, session_ended) are few, so they are always
			// appended and never lost. The peer gets its session_ended from
			// the hub; this handle gets one past the cap. The leave ends the
			// burst's intent here as it does in the hub, so a later lapse does
			// not replay ready.
			h.overflowed = true
			h.wantsMatch = false
			a.hub.Leave(h.sess)
			a.appendEnd(h)
			a.logger.Info("v1 event queue overflow")
			return
		}
	}
	a.appendEvent(h, m)
}

// appendEnd appends session_ended unless the previous event already is one.
// It is the per-handle sentEnd latch. It also gives an idle
// handle that is taken over a terminal session_ended before its 410.
func (a *v1API) appendEnd(h *handle) {
	h.sessionID = ""
	if h.lastWasEnd {
		return
	}
	a.appendEvent(h, proto.SessionEnded{})
}

func (a *v1API) appendEvent(h *handle, m any) {
	raw, err := proto.EncodeEvent(h.lastSeq+1, m)
	if err != nil {
		a.logger.Error("v1 event encode failed", "err", err.Error())
		return
	}
	h.lastSeq++
	h.events = append(h.events, event{seq: h.lastSeq, raw: raw})
	_, h.lastWasEnd = m.(proto.SessionEnded)
	a.wake(h)
}

func (a *v1API) wake(h *handle) {
	close(h.notify)
	h.notify = make(chan struct{})
}

// closeHandle retires h (superseded or taken over): a terminal session_ended,
// no more re-registers, its key freed, and the handle itself forgotten after
// handleTTL. Until then its polls return any events still pending, and 410 once
// none are. Called with a.mu held.
func (a *v1API) closeHandle(h *handle) {
	a.appendEnd(h)
	h.closed = true
	h.leaseGen++
	if a.byKey[h.key] == h {
		delete(a.byKey, h.key)
	}
	if h.pump != nil {
		h.pump.signalStop()
	}
	a.armGC(h)
	a.wake(h)
}

func (a *v1API) armGC(h *handle) {
	if h.gcTimer != nil {
		h.gcTimer.Stop()
	}
	h.gcTimer = time.AfterFunc(a.cfg.handleTTL, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.byHandle[h.id] != h || !(h.closed || (h.lapsed && h.inflight == 0)) {
			return
		}
		a.forget(h)
		a.logger.Info("v1 handle expired")
	})
}

// forget drops h from the registry; later requests on it get 410.
func (a *v1API) forget(h *handle) {
	delete(a.byHandle, h.id)
	if a.byKey[h.key] == h {
		delete(a.byKey, h.key)
	}
	h.closed = true
	if h.pump != nil {
		h.pump.signalStop()
	}
	a.wake(h)
}

// armLease starts the lease countdown; a lapse unregisters the hub session.
// Called with a.mu held.
func (a *v1API) armLease(h *handle) {
	h.leaseGen++
	gen := h.leaseGen
	time.AfterFunc(a.cfg.leaseAfter, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if h.leaseGen != gen || h.closed || h.lapsed || h.inflight > 0 {
			return
		}
		h.lapsed = true
		// Under the lock, so a re-register can never reach the hub ahead of
		// this Unregister (that order would look like a takeover).
		a.hub.Unregister(h.sess)
		a.armGC(h)
		a.logger.Info("v1 lease lapsed")
	})
}

// reregister brings a lapsed handle back: a fresh hub session naming the last
// matched session_id (the hub resumes the pairing if its grace window still
// holds it, else tells this session it ended), then the client's ready replayed
// if that was its last intent. Called with a.mu held.
func (a *v1API) reregister(h *handle) {
	// The session two generations back is final: the hub received the
	// Unregister of the current one, so it has finished the Register that
	// replaced the older one. Let that pump drain and exit now. Its successor
	// still waits for it before pushing, so order is kept.
	if h.pump != nil {
		if q := h.pump.pred.Load(); q != nil {
			q.signalStop()
		}
	}
	s := newHubSession(h.key, h.sessionID)
	h.sess = s
	h.lapsed = false
	if h.gcTimer != nil {
		h.gcTimer.Stop()
		h.gcTimer = nil
	}
	a.hub.Register(s)
	h.pump = a.startPump(h, s, h.pump)
	if h.wantsMatch {
		a.hub.Ready(s)
	}
}

func newHubSession(key, resumeSessionID string) *hub.Session {
	return &hub.Session{
		Key:             key,
		Evict:           make(chan struct{}),
		Outbound:        make(chan any, v1Outbound),
		ResumeSessionID: resumeSessionID,
	}
}

func newHandleID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("server: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// lookup returns the handle named by r's header, or nil. Called with a.mu held.
func (a *v1API) lookup(r *http.Request) *handle {
	id := r.Header.Get(handleHeader)
	if id == "" {
		return nil
	}
	return a.byHandle[id]
}

// handleHello answers POST /v1/hello with welcome or please_update.
func (a *v1API) handleHello(w http.ResponseWriter, r *http.Request) {
	remote := remoteHost(r)
	msg, err := readV1Msg(w, r)
	if err != nil {
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_frame", Msg: "body could not be decoded"})
		a.logger.Info("v1 bad hello", "remote", remote, "reason", "undecodable")
		return
	}
	hello, ok := msg.(proto.Hello)
	if !ok {
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "expected_hello", Msg: "body must be hello"})
		a.logger.Info("v1 bad hello", "remote", remote, "reason", "not_hello")
		return
	}
	if hello.AccountKey == "" {
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_frame", Msg: "empty account key"})
		a.logger.Info("v1 bad hello", "remote", remote, "reason", "empty_account_key")
		return
	}
	pv := hello.ProtocolVersion
	// The version gate (AD-5): current, previous, or newer is served.
	if pv < proto.PROTOCOL_VERSION-1 {
		writeV1(w, http.StatusOK, proto.PleaseUpdate{})
		a.logger.Info("v1 please_update", "remote", remote, "pv", pv)
		return
	}

	h := &handle{
		id:        newHandleID(),
		key:       hello.AccountKey,
		notify:    make(chan struct{}),
		sessionID: hello.SessionID,
	}
	a.mu.Lock()
	if old := a.byKey[h.key]; old != nil {
		// AD-16: one active handle per key. Retire the old one before the new
		// hub session registers; the hub then takes the old session over (or
		// resumes its grace-held pairing if this hello names its session_id).
		a.closeHandle(old)
		a.logger.Info("v1 superseded", "remote", remote)
	}
	h.sess = newHubSession(h.key, hello.SessionID)
	a.byHandle[h.id] = h
	a.byKey[h.key] = h
	a.hub.Register(h.sess)
	h.pump = a.startPump(h, h.sess, nil)
	a.armLease(h)
	a.mu.Unlock()

	writeV1(w, http.StatusOK, proto.Welcome{Handle: h.id})
	a.logger.Info("v1 hello", "remote", remote, "pv", pv)
}

// handleEvents answers GET /v1/events?after=N: pending events with seq > N at
// once, or holds up to pollHold and returns [].
func (a *v1API) handleEvents(w http.ResponseWriter, r *http.Request) {
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_request", Msg: "after must be a non-negative integer"})
			return
		}
		after = n
	}

	a.mu.Lock()
	h := a.lookup(r)
	if h == nil {
		a.mu.Unlock()
		writeGone(w)
		return
	}
	if after > h.lastSeq {
		// Acking a seq this handle never issued would silently drop events
		// not yet delivered (seq restarts at 1 on every handle).
		a.mu.Unlock()
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_request", Msg: "after is beyond the last event"})
		return
	}
	ack(h, after)
	if h.closed && len(h.events) == 0 {
		// A retired handle has nothing left: it is gone for good.
		a.forget(h)
		a.mu.Unlock()
		writeGone(w)
		return
	}
	if h.lapsed && !h.closed {
		a.reregister(h)
	}
	h.inflight++
	h.leaseGen++ // a poll in flight holds the lease

	hold := time.NewTimer(a.cfg.pollHold)
	defer hold.Stop()
	var out [][]byte
	for {
		if len(h.events) > 0 {
			for _, e := range h.events {
				out = append(out, e.raw)
			}
			break
		}
		if h.closed {
			break
		}
		ch := h.notify
		a.mu.Unlock()
		done := false
		select {
		case <-ch:
		case <-hold.C:
			done = true
		case <-r.Context().Done():
			done = true
		}
		a.mu.Lock()
		if done {
			break
		}
	}
	h.inflight--
	if h.inflight == 0 && !h.closed {
		a.armLease(h)
	}
	gone := h.closed && len(out) == 0
	a.mu.Unlock()

	if gone {
		writeGone(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	var body bytes.Buffer
	body.WriteByte('[')
	body.Write(bytes.Join(out, []byte(",")))
	body.WriteByte(']')
	_, _ = w.Write(body.Bytes())
}

// ack drops every event with seq ≤ after. Called with a.mu held.
func ack(h *handle, after int64) {
	i := 0
	for i < len(h.events) && h.events[i].seq <= after {
		i++
	}
	h.events = h.events[i:]
}

// handleSend answers POST /v1/send: one client→server message, routed to the
// hub.
func (a *v1API) handleSend(w http.ResponseWriter, r *http.Request) {
	msg, err := readV1Msg(w, r)

	a.mu.Lock()
	h := a.lookup(r)
	if h == nil || h.closed {
		a.mu.Unlock()
		writeGone(w)
		return
	}
	if err != nil {
		a.mu.Unlock()
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_frame", Msg: "body could not be decoded"})
		return
	}
	if !isClientMessage(msg) {
		a.mu.Unlock()
		writeV1(w, http.StatusBadRequest, proto.Error{Code: "bad_frame", Msg: "not a client message"})
		return
	}
	// Record the new intent before any re-register, so a busy or leave on a
	// lapsed handle is not preceded by a replayed ready.
	switch msg.(type) {
	case proto.Ready:
		h.wantsMatch = true
	case proto.Busy, proto.Leave:
		// The hub never re-enqueues a leaver; neither does a later replay.
		h.wantsMatch = false
	}
	if h.lapsed {
		a.reregister(h)
		if h.inflight == 0 {
			a.armLease(h)
		}
	}
	switch m := msg.(type) {
	case proto.Ready:
		a.hub.Ready(h.sess)
	case proto.Busy:
		a.hub.Busy(h.sess)
		// The hub tells only the peer; this side's match is over all the same,
		// so a later re-register must not name it for resume.
		h.sessionID = ""
	case proto.Leave:
		a.hub.Leave(h.sess)
		h.sessionID = ""
	case proto.ChatMsg:
		// No log line: even a content-free one would leak chat volume and timing.
		a.hub.Relay(h.sess, m)
	}
	// Every other client message is accepted and ignored.
	a.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func isClientMessage(msg any) bool {
	switch msg.(type) {
	case proto.Hello, proto.Ready, proto.Busy, proto.ChatMsg, proto.Leave, proto.Block,
		proto.Report, proto.ProfilePut, proto.ForgetMe, proto.NotePut:
		return true
	}
	return false
}

func readV1Msg(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxV1Body))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty body")
	}
	return proto.Decode(body)
}

func writeGone(w http.ResponseWriter) {
	writeV1(w, http.StatusGone, proto.Error{Code: "handle_gone", Msg: "this handle is no longer active"})
}

// writeV1 writes one proto message as the response body.
func writeV1(w http.ResponseWriter, status int, msg any) {
	b, err := proto.Encode(msg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
