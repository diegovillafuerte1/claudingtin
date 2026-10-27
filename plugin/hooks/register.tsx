import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'
import { CHAT_PANE_ID, COPY, INPUT_KEY, capText, chatBand, leaveChat, onChatClose, openChat, renderChat, resumeChat } from './chat'
import { loadAccountKey } from './identity'
import { markDrawn, onTurnEnd, onTurnStart, resume } from './link'
import { onSafetyClose, openSafety, renderSafety, requireGate } from './safety'

export { requireGate } from './safety'

const DEFAULT_BACKEND_URL = 'http://127.0.0.1:8080'
const POLL_MS = 5000
const POLL_TIMEOUT_MS = 4000

// null until the first poll lands, so the band never flashes "offline" at startup.
const online = atom({ plugin: 'claudingtin', key: 'online' } as const, null)
// The in-flight guard and generation token live in $.state too: module
// variables reset on hot reload.
const poller = atom({ plugin: 'claudingtin', key: 'poller' } as const, { gen: 0, inFlight: false })

export function normalizeBaseUrl(raw: unknown): string {
  const s = typeof raw === 'string' ? raw.trim().replace(/\/+$/, '') : ''
  return s === '' ? DEFAULT_BACKEND_URL : s
}

const USAGE =
  'usage: /claudingtin chat | leave | block | report | safety  (show the chat, end it, block or report them, or read the safety note again)'

// On session start: the same check 7.4 makes (requireGate), which offers the
// pane unless accepted or declined; accepted → warm the key file. Background
// work; never throws.
async function startGate($: EngineInterface): Promise<void> {
  try {
    if (await requireGate($)) void loadAccountKey($).catch(() => {})
  } catch {
    // Fail open: the mod stays inert.
  }
}

export const register: Register = (on, options) => {
  const base = normalizeBaseUrl(options.backendUrl)

  on('session.start', async ($, e, next) => {
    // Settles poll `gen` once: only while it is still the newest poll and in
    // flight does it release the guard and write `online`. A timed-out fetch's
    // late result, or a poll from an earlier session.start, matches neither.
    // `online` is written before the guard is released, so no newer poll can
    // start (and land) in between and then be overwritten by this older one.
    // A timer-vs-fetch tie on the same gen may write twice; the last write wins.
    const release = (gen: number) =>
      update($, poller, (p) => (p.gen === gen && p.inFlight ? { gen: p.gen, inFlight: false } : p))
    const finish = async (gen: number, ok: boolean) => {
      const p = await read($, poller)
      if (p.gen !== gen || !p.inFlight) return
      await update($, online, () => ok)
      await release(gen)
    }

    const poll = async () => {
      let myGen = -1
      try {
        let gen = -1
        await update($, poller, (p) => {
          if (p.inFlight) {
            gen = -1
            return p
          }
          gen = p.gen + 1
          return { gen, inFlight: true }
        })
        if (gen < 0) return
        myGen = gen
        // A hung fetch cannot be cancelled, only abandoned: after the timeout
        // the band fails open to offline and the guard is released.
        const timer = $.clock.after(POLL_TIMEOUT_MS, () => {
          void finish(myGen, false).catch(() => {})
        })
        let ok = false
        try {
          ok = (await $.http.fetch(`${base}/status`)).ok === true
        } catch {
          ok = false
        }
        try {
          timer.cancel()
        } catch {
          // The timer may already have fired; finish() ignores the loser.
        }
        await finish(myGen, ok)
      } catch {
        // Fail open: never let a poll error escape a timer. If this poll holds
        // the guard, release it (for its own gen only) so polling never freezes.
        if (myGen >= 0) {
          try {
            await release(myGen)
          } catch {
            // Best effort.
          }
        }
      }
    }

    try {
      // A new session.start (e.g. after hot reload) abandons any earlier poll.
      await update($, poller, (p) => ({ gen: p.gen + 1, inFlight: false }))
      void poll()
      $.clock.every(POLL_MS, () => {
        void poll()
      })
    } catch {
      // Fail open: the band simply never draws.
    }
    try {
      await $.command.register({
        name: 'claudingtin',
        description: 'claudingtin: show the chat, leave, block or report, or read the safety note again',
        argumentHint: 'chat|leave|block|report|safety',
      })
    } catch {
      // Fail open: no command this session.
    }
    void startGate($)
    void resumeChat($).catch(() => {})
    void resume($, base).catch(() => {})
    return next(e)
  })

  // Think-time: only main-loop turns count (agentId absent). The hooks only
  // record what they want (state, never the network) and return.
  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) await onTurnStart($, base).catch(() => {})
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId === undefined) await onTurnEnd($, base).catch(() => {})
    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: 'claudingtin-safety' }, async ($, e, next) => {
    try {
      return renderSafety($, e)
    } catch {
      return next(e)
    }
  })

  on('ui.close', { id: 'claudingtin-safety' }, async ($, e, next) => {
    await onSafetyClose($, e.origin).catch(() => {})
    return next(e)
  }).catch((_$, e, next) => next(e)) // A failed decline never keeps the pane open.

  on('ui.render', { component: 'Pane', requestId: CHAT_PANE_ID }, async ($, e, next) => {
    try {
      return await renderChat($, e, base)
    } catch {
      return next(e)
    }
  })

  on('ui.close', { id: CHAT_PANE_ID }, async ($, e, next) => {
    await onChatClose($, e.origin).catch(() => {})
    return next(e)
  }).catch((_$, e, next) => next(e))

  // The outbound cap: every change and submit reaches the Input's handler at
  // MAX_INPUT code points at most (InputProps has no maxLength).
  on('ui.input', { plugin: 'claudingtin', element: INPUT_KEY }, async ($, e, next) => {
    let capped: typeof e = e
    try {
      if (typeof e.value === 'string') capped = { ...e, value: capText(e.value) }
    } catch {
      capped = e // The typing as it came.
    }
    // One next either way, so the Input's handler never runs twice.
    return next(capped)
  })

  on('command.run', { command: 'claudingtin' }, async ($, e) => {
    try {
      const verb = e.args.trim().toLowerCase()
      if (verb === 'safety') {
        return (await openSafety($, true))
          ? { text: 'the safety note is up.' }
          : { text: "couldn't show the safety note here." }
      }
      if (verb === 'leave' || verb === 'block' || verb === 'report') {
        return (await leaveChat($, base, verb)) ? {} : { text: COPY.noChat }
      }
      if (verb === 'chat') {
        const r = await openChat($)
        return r === 'open' ? {} : r === 'none' ? { text: COPY.noChat } : { text: "couldn't show the chat here." }
      }
    } catch {
      // Fall through to the usage line.
    }
    return { text: USAGE }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    // Asked to draw: the mod's UI reaches this session (half of "no surface, no queue").
    markDrawn($)
    if (e.props.hasSurvey || e.props.maxRows < 1) return next(e)
    let line: string | null = null
    try {
      line = await chatBand($)
    } catch {
      line = null
    }
    if (line !== null) {
      const { Text } = $.ui.resolve(e)
      return (
        <Text key="claudingtin-band" dimColor wrap="truncate-end">
          {line}
        </Text>
      )
    }
    let v: boolean | null
    try {
      v = await read($, online)
    } catch {
      return next(e)
    }
    if (v === null) return next(e)
    const { Text } = $.ui.resolve(e)
    return (
      <Text key="claudingtin-band" dimColor wrap="truncate-end">
        {v ? 'claudingtin · connected' : 'claudingtin · offline'}
      </Text>
    )
  })
}
