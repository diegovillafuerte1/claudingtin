import { atom, read, update } from 'claude-code'
import type { EngineInterface, PluginState } from 'claude-code'
import type { Presence } from '../types'
import { loadAccountKey } from './identity'
import { requireGate } from './safety'

// The /v1 client (Story 7.4). Main-loop turns set `want` (ready while Claude
// thinks, busy when it is done); one background worker brings the backend in
// line: a lazy hello, then send ready/busy, and while ready one events
// long-poll that holds the lease and acks by seq. Every failure is quiet:
// nothing here is ever shown, logged or thrown. The account key goes only in
// the hello body; the handle only in the handle header.

export const HANDLE_HEADER = 'X-Claudingtin-Handle'
const PROTOCOL_VERSION = 1
const SEND_TIMEOUT_MS = 5000
// Above the server's ~20 s hold, so a held poll is not abandoned early.
const EVENTS_TIMEOUT_MS = 25000
const BACKOFF_MIN_MS = 1000
const BACKOFF_MAX_MS = 15000
// A poll answered at once with nothing (a proxy that does not hold) is paced,
// so the loop never spins.
const EMPTY_PACE_MS = 500

type Link = PluginState['claudingtin']['link']

const INITIAL: Link = { handle: null, after: 0, want: null, sent: null, quiet: false, outdated: false, gen: 0 }

export const link = atom({ plugin: 'claudingtin', key: 'link' } as const, INITIAL)
/** Set once the AbovePrompt band hook is asked to render: the mod's UI draws here. */
export const drawn = atom({ plugin: 'claudingtin', key: 'drawn' } as const, false)

// Drawing is pure: a render hook may not write state. So the band hook only
// notes it here (this environment) and leaves the state write to a timer,
// which runs outside the render dispatch; the state copy survives a reload.
let drawnHere = false

/** The AbovePrompt band hook was asked to render. Synchronous; never throws. */
export function markDrawn($: EngineInterface): void {
  if (drawnHere) return
  drawnHere = true
  try {
    $.clock.after(0, () => {
      void update($, drawn, () => true).catch(() => {})
    })
  } catch {
    // This environment still knows; only a reload forgets until the next draw.
  }
}

function setLink($: EngineInterface, fn: (l: Link) => Link): Promise<unknown> {
  return update($, link, (l) => fn({ ...INITIAL, ...l }))
}

async function getLink($: EngineInterface): Promise<Link> {
  return { ...INITIAL, ...(await read($, link)) }
}

// ---------------------------------------------------------------------------
// Entry points: each sets `want` (state only, awaited so turn order holds),
// then starts the worker without waiting on it. Never throw.

/** A main-loop turn started: ask for the queue, and lift a supersede's quiet. */
export async function onTurnStart($: EngineInterface, base: string): Promise<void> {
  try {
    await setLink($, (l) => ({ ...l, want: 'ready', quiet: false }))
  } catch {
    return
  }
  void work($, base)
}

/** A main-loop turn completed (any reason): leave the queue. */
export async function onTurnEnd($: EngineInterface, base: string): Promise<void> {
  try {
    await setLink($, (l) => ({ ...l, want: 'busy' }))
  } catch {
    return
  }
  void work($, base)
}

/**
 * session.start (also after a hot reload): abandon any earlier poll loop,
 * then reconcile and, when still ready on a handle, resume polling on it.
 */
export async function resume($: EngineInterface, base: string): Promise<void> {
  try {
    await setLink($, (l) => ({ ...l, gen: l.gen + 1 }))
  } catch {
    return
  }
  void work($, base)
  void ensurePoll($, base)
}

// ---------------------------------------------------------------------------
// The worker: one reconcile loop at a time per environment (as loadAccountKey's
// inFlight). A call while it runs asks it to go round once more.

let working = false
let again = false

export async function work($: EngineInterface, base: string): Promise<void> {
  if (working) {
    again = true
    return
  }
  working = true
  try {
    do {
      again = false
      try {
        await reconcile($, base, (await getLink($)).gen)
      } catch {
        // Quiet: the next turn tries again.
      }
    } while (again)
  } finally {
    working = false
  }
}

// Abandoned past SEND_TIMEOUT_MS (a key read that never settles), so the
// worker is never held: a timeout is not eligible.
async function eligible($: EngineInterface): Promise<string | undefined> {
  return abandonAfter($, checkEligible($), SEND_TIMEOUT_MS)
}

async function checkEligible($: EngineInterface): Promise<string | undefined> {
  try {
    if (!drawnHere && !(await read($, drawn))) return undefined
    let surfaces: readonly unknown[] = []
    try {
      surfaces = await $.session.surfaces()
    } catch {
      surfaces = []
    }
    if (!Array.isArray(surfaces) || surfaces.length === 0) return undefined
    if (!(await requireGate($))) return undefined
    return await loadAccountKey($)
  } catch {
    return undefined
  }
}

// Re-reads the link after every await, so a burst that ended meanwhile is
// never sent a late ready, busy can never overtake ready, and a supersede the
// poll loop saw meanwhile is honoured. `gen` is the link's at entry: once a
// resume (or a hot reload) moves it on, this round gives way to the new one.
//
// `sent` is what the backend last knew for sure: `busy` for a fresh handle (a
// new hello is not queued) or a busy sent, `ready` after a 2xx ready, and null
// while unknown (a ready in flight or one that failed, which the server may
// still have applied), so a later busy is never skipped.
async function reconcile($: EngineInterface, base: string, gen: number): Promise<void> {
  let stale = false // one re-hello per reconcile after a 410 on send ready
  for (;;) {
    const l = await getLink($)
    if (l.gen !== gen) return
    if (l.want === null || l.want === l.sent) return

    if (l.want === 'busy') {
      if (l.handle === null) {
        // Nothing can be queued without a handle (a fast first burst): nothing to say.
        await setLink($, (s) => (s.want === 'busy' && s.handle === null ? { ...s, sent: 'busy' } : s))
        continue
      }
      // Mark it first: the poll loop stops on its next round.
      const handle = l.handle
      await setLink($, (s) => (s.want === 'busy' && s.handle === handle ? { ...s, sent: 'busy' } : s))
      const res = await send($, base, handle, 'busy')
      if (res?.status === 410) await setLink($, (s) => (s.handle === handle ? { ...s, handle: null, after: 0 } : s))
      // Any other failure is dropped silently.
      continue
    }

    // want === 'ready'
    if (l.quiet || l.outdated) return
    const key = await eligible($)
    if (key === undefined) return
    const e = await getLink($)
    if (e.gen !== gen || e.quiet || e.outdated) return
    if (e.want !== 'ready') continue

    let handle = e.handle
    if (handle === null) {
      const r = await hello($, base, key)
      if (r.kind === 'outdated') {
        await setLink($, (s) => ({ ...s, outdated: true }))
        return
      }
      if (r.kind === 'failed') return // retried at the next turn.start
      let stored = false
      await setLink($, (s) => {
        stored = s.handle === null && !s.quiet && !s.outdated && s.want === 'ready'
        return stored ? { ...s, handle: r.handle, after: 0, sent: 'busy' } : s
      })
      // Not stored: the burst ended, or a handle or a quiet came meanwhile. Drop it.
      if (!stored) continue
      handle = r.handle
      continue // re-read: the burst may have ended while the hello was out
    }

    const h = handle
    let marked = false
    await setLink($, (s) => {
      marked = s.gen === gen && s.handle === h && s.want === 'ready' && !s.quiet && !s.outdated
      return marked ? { ...s, sent: null } : s
    })
    if (!marked) continue
    const res = await send($, base, h, 'ready')
    if (res !== undefined && res.status >= 200 && res.status < 300) {
      await setLink($, (s) => (s.handle === h && s.sent === null ? { ...s, sent: 'ready' } : s))
      void ensurePoll($, base)
      continue
    }
    if (res?.status === 410) {
      await setLink($, (s) => (s.handle === h ? { ...s, handle: null, after: 0 } : s))
      if (stale) {
        await setLink($, (s) => ({ ...s, quiet: true }))
        return
      }
      stale = true
      continue
    }
    return // network error, timeout, other status: retried at the next turn.start
  }
}

// ---------------------------------------------------------------------------
// The events poll: one loop per `gen` and handle, at most one fetch in flight
// on a live handle (a dropped handle's poll is abandoned to its fate). It runs
// only while the burst lasts (want and sent both ready, on a handle).

let pollingKey: string | null = null
const pollKey = (l: Link) => `${l.gen}:${l.handle}`

const polling = (l: Link) => l.handle !== null && l.want === 'ready' && l.sent === 'ready' && !l.quiet && !l.outdated

export async function ensurePoll($: EngineInterface, base: string): Promise<void> {
  let gen: number
  let handle: string
  let key: string
  try {
    const l = await getLink($)
    if (!polling(l) || pollingKey === pollKey(l)) return
    gen = l.gen
    handle = l.handle!
    key = pollKey(l)
  } catch {
    return
  }
  pollingKey = key
  try {
    await pollLoop($, base, gen, handle)
  } catch {
    // Quiet.
  } finally {
    if (pollingKey === key) pollingKey = null
  }
  // A ready that landed while this loop was winding down starts a new one.
  try {
    const l = await getLink($)
    if (l.gen === gen && polling(l) && pollingKey !== pollKey(l)) void ensurePoll($, base)
  } catch {
    // Quiet.
  }
}

async function pollLoop($: EngineInterface, base: string, gen: number, handle: string): Promise<void> {
  let backoff = 0
  for (;;) {
    const l = await getLink($)
    if (l.gen !== gen || l.handle !== handle || !polling(l)) return
    const res = await request($, `${base}/v1/events?after=${l.after}`, { method: 'GET', headers: { [HANDLE_HEADER]: handle } }, EVENTS_TIMEOUT_MS)
    const now = await getLink($)
    if (now.gen !== gen || now.handle !== handle) return // a newer loop owns the link

    if (res?.status === 410) {
      // Superseded (another session took the key): quiet until the next turn.start.
      await setLink($, (s) => (s.handle === handle ? { ...s, handle: null, after: 0, sent: null, quiet: true } : s))
      return
    }
    if (res?.status === 400) {
      // `after` beyond this handle's last event: start its count afresh.
      await setLink($, (s) => (s.handle === handle && s.gen === gen ? { ...s, after: 0 } : s))
    }
    const seqs = res !== undefined && res.status === 200 ? parseSeqs(res.text) : undefined
    if (seqs === undefined) {
      backoff = backoff === 0 ? BACKOFF_MIN_MS : Math.min(backoff * 2, BACKOFF_MAX_MS)
      await $.clock.sleep(backoff)
      continue
    }
    backoff = 0
    const fresh = seqs.filter((n) => n > now.after)
    if (fresh.length > 0) {
      const max = fresh.reduce((m, n) => (n > m ? n : m), now.after)
      await setLink($, (s) => (s.handle === handle && s.gen === gen && max > s.after ? { ...s, after: max } : s))
      // 7.5 renders these; 7.4 only acks them.
    } else {
      // Nothing new (empty, or only seqs already seen): pace, never spin.
      await $.clock.sleep(EMPTY_PACE_MS)
    }
  }
}

/** The `seq` of each event in a bare JSON array; `undefined` when the body is not one. */
export function parseSeqs(text: string): number[] | undefined {
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return undefined
  }
  if (!Array.isArray(v)) return undefined
  const out: number[] = []
  for (const e of v) {
    if (e === null || typeof e !== 'object') continue
    const seq = (e as { seq?: unknown }).seq
    if (typeof seq === 'number' && Number.isSafeInteger(seq) && seq > 0) out.push(seq)
  }
  return out
}

// ---------------------------------------------------------------------------
// Requests. A fetch cannot be cancelled (HttpInit has no abort or timeout),
// only abandoned: past its time the caller moves on and its late result is
// dropped. Never rejects; `undefined` is a network error or timeout.

type Res = { status: number; text: string }

/** `p`, or `undefined` once `ms` pass first; `p`'s late result is dropped. Never rejects. */
async function abandonAfter<T>($: EngineInterface, p: Promise<T | undefined>, ms: number): Promise<T | undefined> {
  let timer: { cancel: () => void } | undefined
  try {
    const timeout = new Promise<undefined>((resolve) => {
      timer = $.clock.after(ms, () => resolve(undefined))
    })
    return await Promise.race([p.catch(() => undefined), timeout])
  } catch {
    return undefined
  } finally {
    try {
      timer?.cancel()
    } catch {
      // The timer may already have fired.
    }
  }
}

async function request(
  $: EngineInterface,
  url: string,
  init: { method: string; headers?: Record<string, string>; body?: string },
  ms: number,
): Promise<Res | undefined> {
  let call: Promise<Res | undefined>
  try {
    call = $.http.fetch(url, init).then(
      (r): Res => ({ status: typeof r?.status === 'number' ? r.status : 0, text: typeof r?.text === 'string' ? r.text : '' }),
      () => undefined,
    )
  } catch {
    return undefined
  }
  return abandonAfter($, call, ms)
}

type Hello = { kind: 'welcome'; handle: string } | { kind: 'outdated' } | { kind: 'failed' }

async function hello($: EngineInterface, base: string, key: string): Promise<Hello> {
  const body = JSON.stringify({ type: 'hello', v: 1, protocol_version: PROTOCOL_VERSION, account_key: key })
  const res = await request($, `${base}/v1/hello`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body }, SEND_TIMEOUT_MS)
  if (res === undefined || res.status !== 200) return { kind: 'failed' }
  let msg: unknown
  try {
    msg = JSON.parse(res.text)
  } catch {
    return { kind: 'failed' }
  }
  if (msg === null || typeof msg !== 'object') return { kind: 'failed' }
  const m = msg as { type?: unknown; handle?: unknown }
  if (m.type === 'please_update') return { kind: 'outdated' }
  if (m.type === 'welcome' && typeof m.handle === 'string' && m.handle !== '') return { kind: 'welcome', handle: m.handle }
  return { kind: 'failed' }
}

function send($: EngineInterface, base: string, handle: string, type: Presence): Promise<Res | undefined> {
  return request(
    $,
    `${base}/v1/send`,
    { method: 'POST', headers: { 'Content-Type': 'application/json', [HANDLE_HEADER]: handle }, body: JSON.stringify({ type, v: 1 }) },
    SEND_TIMEOUT_MS,
  )
}
