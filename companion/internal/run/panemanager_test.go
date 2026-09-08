package run

import (
	"context"
	"strings"
	"testing"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/chatui"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/statusline"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// newPaneManagerFor builds a paneManager wired to a fresh pair of lockedBuffers
// (Out and Err kept separate so the breadcrumb-routing rows can tell them
// apart) and a fixed resume phase. It reuses the run_test.go helpers so the new
// direct tests and the untouched loop suite share fakes.
func newPaneManagerFor(t *testing.T, resume statusline.Phase) (pm *paneManager, out, errb *lockedBuffer) {
	t.Helper()
	out, errb = &lockedBuffer{}, &lockedBuffer{}
	cfg := Config{Out: out, Err: errb}
	pm = newPaneManager(context.Background(), cfg, statusline.New(out), func() statusline.Phase { return resume })
	return pm, out, errb
}

// --- Matrix: chat launch on `matched` ------------------------------------------

func TestPaneManagerLaunchChatWiresChannels(t *testing.T) {
	made := stubChatProgram(t)
	pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	waitChat(t, made)
	t.Cleanup(func() { pm.stopChat(false) })

	if !pm.chatActive() {
		t.Fatal("chatActive() false after launchChat")
	}
	if pm.chatDone() == nil {
		t.Fatal("chatDone() nil while chat is active")
	}
	if pm.intents() == nil {
		t.Fatal("intents() nil while chat is active")
	}
	if pm.sends() == nil {
		t.Fatal("sends() nil while chat is active")
	}
}

// --- Matrix: chat clean teardown --------------------------------------------

func TestPaneManagerStopChatCleanQuitClearsState(t *testing.T) {
	made := stubChatProgram(t)
	pm, out, errb := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	f := waitChat(t, made)

	pm.stopChat(false)

	if f.wasKilled() {
		t.Fatal("a clean stopChat Kill()ed the program")
	}
	if pm.chatActive() {
		t.Fatal("chatActive() still true after stopChat")
	}
	if pm.chat != nil || pm.chatIntents != nil || pm.chatSends != nil {
		t.Fatalf("chat handles not cleared: chat=%v intents=%v sends=%v", pm.chat, pm.chatIntents, pm.chatSends)
	}
	if pm.chatDone() != nil {
		t.Fatal("chatDone() not nil after stopChat")
	}
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stopChat(false) wrote to a sink: out=%q err=%q", out.String(), errb.String())
	}
}

// --- Matrix: chat teardown with resume -------------------------------------

func TestPaneManagerStopChatResumeRepaints(t *testing.T) {
	made := stubChatProgram(t)
	pm, out, _ := newPaneManagerFor(t, statusline.PhaseFreeToChat)

	pm.launchChat(proto.Matched{Opener: "o"})
	waitChat(t, made)

	pm.stopChat(true)

	if !strings.Contains(out.String(), "free to chat") {
		t.Fatalf("stopChat(true) did not repaint the resume phase: %q", out.String())
	}
}

// TestPaneManagerResumePhaseThunkEvaluatedAtTeardown pins the one non-obvious
// reason resumePhase is a func() and not a value: loop's `desired` is a mutable
// local, so the thunk must be read at teardown time. A manager whose resumePhase
// reads a test-controlled local must repaint whatever that local holds *now*,
// not what it held at construction.
func TestPaneManagerResumePhaseThunkEvaluatedAtTeardown(t *testing.T) {
	made := stubChatProgram(t)
	out := &lockedBuffer{}
	cfg := Config{Out: out, Err: &lockedBuffer{}}
	phase := statusline.PhaseWaiting
	pm := newPaneManager(context.Background(), cfg, statusline.New(out), func() statusline.Phase { return phase })

	pm.launchChat(proto.Matched{Opener: "o"})
	waitChat(t, made)
	phase = statusline.PhaseFreeToChat
	pm.stopChat(true)
	if !strings.Contains(out.String(), "free to chat") {
		t.Fatalf("first teardown did not repaint the current thunk value: %q", out.String())
	}

	pm.launchChat(proto.Matched{Opener: "o2"})
	waitChat(t, made)
	phase = statusline.PhaseWaiting
	pm.stopChat(true)
	if !strings.Contains(out.String(), "waiting for the next quiet moment") {
		t.Fatalf("second teardown did not repaint the reassigned thunk value: %q", out.String())
	}
}

// --- Matrix: showPhase gate ------------------------------------------------

func TestPaneManagerShowPhaseWritesWithNoPaneUp(t *testing.T) {
	pm, out, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.showPhase(statusline.PhaseWaiting)

	if !strings.Contains(out.String(), "waiting for the next quiet moment") {
		t.Fatalf("showPhase wrote nothing with no pane up: %q", out.String())
	}
}

func TestPaneManagerShowPhaseSuppressedWhileChatOwnsPane(t *testing.T) {
	made := stubChatProgram(t)
	pm, out, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	waitChat(t, made)
	t.Cleanup(func() { pm.stopChat(false) })

	before := out.String()
	pm.showPhase(statusline.PhaseFreeToChat)
	if out.String() != before {
		t.Fatalf("showPhase wrote while the chat surface owned the pane: %q -> %q", before, out.String())
	}
}

func TestPaneManagerShowPhaseSuppressedWhileSearchOwnsPane(t *testing.T) {
	made := stubSearchProgram(t)
	pm, out, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchSearch()
	waitSearch(t, made)
	t.Cleanup(func() { pm.stopSearch(false) })

	before := out.String()
	pm.showPhase(statusline.PhaseFreeToChat)
	if out.String() != before {
		t.Fatalf("showPhase wrote while the spinner owned the pane: %q -> %q", before, out.String())
	}
}

// --- Matrix: operator breadcrumb -----------------------------------------

func TestPaneManagerLogRoutesThroughSurfaceWhenUp(t *testing.T) {
	made := stubChatProgram(t)
	pm, _, errb := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	f := waitChat(t, made)
	t.Cleanup(func() { pm.stopChat(false) })

	pm.log("companion: a breadcrumb")

	if !strings.Contains(f.printed(), "companion: a breadcrumb") {
		t.Fatalf("log did not reach the surface as a LogLine: %q", f.printed())
	}
	if strings.Contains(errb.String(), "breadcrumb") {
		t.Fatalf("log also went to cfg.Err while a surface was up: %q", errb.String())
	}
}

func TestPaneManagerLogRoutesToErrWithNoSurface(t *testing.T) {
	pm, out, errb := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.log("companion: a breadcrumb")

	if !strings.Contains(errb.String(), "companion: a breadcrumb") {
		t.Fatalf("log did not go to cfg.Err with no surface up: %q", errb.String())
	}
	if strings.Contains(out.String(), "breadcrumb") {
		t.Fatalf("log leaked to cfg.Out: %q", out.String())
	}
}

// --- Matrix: inbound peer line -----------------------------------------

func TestPaneManagerSendPeerNoSurfaceIsNoop(t *testing.T) {
	pm, out, errb := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.sendPeer("peer-1", "hi from them") // must not panic

	if out.String() != "" || errb.String() != "" {
		t.Fatalf("sendPeer with no surface wrote to a sink: out=%q err=%q", out.String(), errb.String())
	}
}

func TestPaneManagerSendPeerForwardsToLiveSurface(t *testing.T) {
	made := stubChatProgram(t)
	pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	f := waitChat(t, made)
	t.Cleanup(func() { pm.stopChat(false) })

	pm.sendPeer("peer-1", "hi from them")

	found := false
	for _, m := range f.receivedMsgs() {
		if pmsg, ok := m.(chatui.PeerMsg); ok && pmsg.ClientMsgID == "peer-1" && pmsg.Text == "hi from them" {
			found = true
		}
	}
	if !found {
		t.Fatal("sendPeer did not forward a PeerMsg to the live chat surface")
	}
}

// --- Matrix: spinner launch / teardown --------------------------------

func TestPaneManagerSpinnerLifecycle(t *testing.T) {
	made := stubSearchProgram(t)
	pm, out, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchSearch()
	sf := waitSearch(t, made)

	if !pm.searchActive() {
		t.Fatal("searchActive() false after launchSearch")
	}
	if pm.searchDone() == nil {
		t.Fatal("searchDone() nil while the spinner is active")
	}

	pm.stopSearch(true)

	if sf.wasKilled() {
		t.Fatal("a clean stopSearch Kill()ed the spinner")
	}
	if pm.searchActive() {
		t.Fatal("searchActive() still true after stopSearch")
	}
	if pm.search != nil {
		t.Fatal("search handle not cleared after stopSearch")
	}
	if pm.searchDone() != nil {
		t.Fatal("searchDone() not nil after stopSearch")
	}
	if !strings.Contains(out.String(), "waiting for the next quiet moment") {
		t.Fatalf("stopSearch(true) did not repaint the resume phase: %q", out.String())
	}
}

// --- Matrix: terminal path (stopAll) --------------------------------

func TestPaneManagerStopAllIsANoopWhenIdle(t *testing.T) {
	pm, out, errb := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.stopAll() // must not panic or write anything

	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stopAll on an idle manager wrote to a sink: out=%q err=%q", out.String(), errb.String())
	}
}

// TestPaneManagerStopWithResumeOnIdleIsNoop covers the path
// TestPaneManagerStopAllIsANoopWhenIdle does not: stopAll passes resume=false,
// but stopChat(true) / stopSearch(true) on a manager that never launched
// anything must also be silent — pane.teardown returns false before the
// `if resume` repaint line ever runs.
func TestPaneManagerStopWithResumeOnIdleIsNoop(t *testing.T) {
	pm, out, errb := newPaneManagerFor(t, statusline.PhaseFreeToChat)

	pm.stopChat(true)   // must not panic or repaint
	pm.stopSearch(true) // must not panic or repaint

	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stop*(true) on an idle manager wrote to a sink: out=%q err=%q", out.String(), errb.String())
	}
}

func TestPaneManagerStopAllTearsDownBothSurfaces(t *testing.T) {
	madeSearch := stubSearchProgram(t)
	madeChat := stubChatProgram(t)
	pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchSearch()
	sf := waitSearch(t, madeSearch)
	pm.launchChat(proto.Matched{Opener: "o"})
	cf := waitChat(t, madeChat)

	pm.stopAll()

	if pm.anyActive() {
		t.Fatal("stopAll left a pane active")
	}
	if sf.wasKilled() || cf.wasKilled() {
		t.Fatal("stopAll Kill()ed a surface instead of a clean Quit")
	}
}

// --- Matrix: stale chat program callback -----------------------------

// TestPaneManagerStaleChatCallbackLandsOnCapturedChannel proves the notify
// closure a chat program was built with keeps writing to the channel instance it
// captured at launch, not to paneManager's current field — so a lagging program
// that fires after a relaunch cannot corrupt the live surface's stream.
func TestPaneManagerStaleChatCallbackLandsOnCapturedChannel(t *testing.T) {
	made := stubChatProgram(t)
	pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

	pm.launchChat(proto.Matched{Opener: "o"})
	stale := waitChat(t, made)
	oldIntents := pm.chatIntents

	pm.stopChat(false)

	pm.launchChat(proto.Matched{Opener: "o2"})
	waitChat(t, made)
	t.Cleanup(func() { pm.stopChat(false) })
	newIntents := pm.chatIntents

	// The stale program fires a callback after the relaunch.
	stale.notify(chatui.IntentBlock)

	if len(newIntents) != 0 {
		t.Fatalf("a stale callback landed on the current chatIntents: len=%d", len(newIntents))
	}
	if len(oldIntents) != 1 {
		t.Fatalf("the stale callback did not land on the captured old channel: len=%d", len(oldIntents))
	}
}

// --- Acceptance: chat and search share the one teardown path -----------

// TestPaneTeardownSharedByChatAndSearch runs both surfaces through the single
// pane.teardown helper (the F5 fold), on both branches: a clean Quit and a
// Quit-ignored -> grace -> Kill escalation.
func TestPaneTeardownSharedByChatAndSearch(t *testing.T) {
	cases := []struct {
		name       string
		ignoreQuit bool
		wantKill   bool
	}{
		{"clean quit", false, false},
		{"quit ignored escalates to kill", true, true},
	}

	for _, tc := range cases {
		t.Run("chat/"+tc.name, func(t *testing.T) {
			made := stubChatProgram(t, func(f *fakeChatProgram) { f.ignoreQuit = tc.ignoreQuit })
			pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

			pm.launchChat(proto.Matched{Opener: "o"})
			f := waitChat(t, made)

			pm.stopChat(false)

			if f.wasKilled() != tc.wantKill {
				t.Fatalf("chat teardown Kill()ed=%v, want %v", f.wasKilled(), tc.wantKill)
			}
			if pm.chatActive() {
				t.Fatal("chat still active after teardown")
			}
		})

		t.Run("search/"+tc.name, func(t *testing.T) {
			made := stubSearchProgram(t, func(f *fakeSearchProgram) { f.ignoreQuit = tc.ignoreQuit })
			pm, _, _ := newPaneManagerFor(t, statusline.PhaseWaiting)

			pm.launchSearch()
			f := waitSearch(t, made)

			pm.stopSearch(false)

			if f.wasKilled() != tc.wantKill {
				t.Fatalf("search teardown Kill()ed=%v, want %v", f.wasKilled(), tc.wantKill)
			}
			if pm.searchActive() {
				t.Fatal("search still active after teardown")
			}
		})
	}
}
