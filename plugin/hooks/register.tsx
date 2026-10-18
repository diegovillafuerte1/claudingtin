import { atom, read, update } from 'claude-code'
import type { Register } from 'claude-code'

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
    return next(e)
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey || e.props.maxRows < 1) return next(e)
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
