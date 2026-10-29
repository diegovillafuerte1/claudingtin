import { atom, read, update } from 'claude-code'
import type { EngineInterface, RenderInput, RenderSurface } from 'claude-code'
import type { Chat, ChatLine, ChatMatch, Keepsake } from '../types'
import { getLink, releaseHold, sendNow, sendSeqNow } from './link'

// The chat surface (Story 7.5): the events the /v1 poll hands over drive a
// small state in $.state (memory only, never disk), and the band and a Pane
// draw from it. Peer-controlled text (lines, opener, pseudonym, blurb) is only
// ever drawn as `Text`, after `inert`. Sends go out through link's sendNow,
// never awaited; every failure is quiet.

export const CHAT_PANE_ID = 'claudingtin-chat'
export const INPUT_KEY = 'claudingtin-input'
export const MAX_LINES = 200
export const MAX_INPUT = 2000
const MAX_NAME = 80
const MAX_OPENER = 500
const REPORT_LAST_N = 5

export const COPY = {
  searching: 'looking for someone',
  flourish: '  here we go',
  header: "you're chatting with ",
  someone: 'someone new',
  placeholder: 'say something',
  ended: "looks like their claude's back — catch you later",
  leave: "my claude's back",
  block: 'block',
  report: 'report',
  noChat: 'no chat going right now.',
  back: "your claude's back",
  heldBand: "claudingtin · your claude's back — chat's still open",
  persistOn: "persist is on — your next chat stays open when your claude's back",
  persistOff: "persist is off — chats end when your claude's back",
  persistFailed: "couldn't change persist here.",
  keepNote: "this chat's id — the only way to find them again",
  keepCopy: 'copy id',
  keepDone: 'done',
  keepCopied: 'copied',
  keepFailed: "no clipboard here — the id's above",
  keepBand: "claudingtin · keep this chat's id — /claudingtin chat",
} as const

const SPINNER = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'] as const
export const FLOURISH = ['◐', '◓', '◑', '◒', '◐', '◓'] as const
const SPIN_MS = 100
const FLOURISH_MS = 90

const IDLE: Chat = { phase: 'idle', match: null, lines: [], ended: false, placed: false, left: false, persist: false, back: false, kept: false, keepsake: null }

export const chat = atom({ plugin: 'claudingtin', key: 'chat' } as const, IDLE)

// The persist setting (Story 3.6), from userConfig. A module variable: a
// change to the options reloads the plugin and register sets it again. Each
// chat snapshots it at `matched`, so a toggle changes only the next chat.
let persistSetting = true

/** register(on, options): the userConfig `persist`; anything but a boolean reads as on. */
export function setPersistDefault(v: unknown): void {
  persistSetting = typeof v === 'boolean' ? v : true
}

export function getPersistSetting(): boolean {
  return persistSetting
}

// ---------------------------------------------------------------------------
// Text safety: a port of companion/internal/chatui/inert.go (deleted in Story
// 7.6; see commit 9398045).

const UNSAFE = /^[\p{C}\p{Zl}\p{Zp}]$/u

/**
 * Terminal-safe literal text: keeps `\n` and U+200D; drops C0 (bar `\n`), DEL,
 * C1 and every other control, format, separator or unassigned code point;
 * a lone surrogate becomes U+FFFD. No markup is interpreted.
 */
export function inert(s: unknown): string {
  if (typeof s !== 'string') return ''
  let out = ''
  for (const ch of s) {
    const cp = ch.codePointAt(0)!
    if (ch === '\n' || cp === 0x200d) out += ch
    else if (cp >= 0xd800 && cp <= 0xdfff) out += '�'
    else if (!UNSAFE.test(ch)) out += ch
  }
  return out
}

/** `inert`, collapsed to one trimmed line (pseudonym, blurb). */
export function oneLine(s: unknown): string {
  return inert(s).replace(/\n/g, ' ').trim()
}

/** At most `max` code points (MAX_INPUT by default); '' when not a string. */
export function capText(s: unknown, max: number = MAX_INPUT): string {
  if (typeof s !== 'string') return ''
  if (s.length <= max) return s
  const cps = Array.from(s)
  return cps.length <= max ? s : cps.slice(0, max).join('')
}

const peerName = (m: ChatMatch | null) => oneLine(m?.pseudonym) || COPY.someone

// ---------------------------------------------------------------------------
// The ticker: one module-level interval while searching (spinner) or while the
// flourish plays. Module state resets on hot reload, which cancels the old
// environment's timers; resumeChat starts it again from $.state.

let ticker: { cancel: () => void } | null = null
let tickerMs = 0
let frame = 0
let flourish: number | null = null
let phaseNow: Chat['phase'] = 'idle'

function syncTicker($: EngineInterface, c?: Chat): void {
  if (c !== undefined) phaseNow = c.phase
  const want = flourish !== null ? FLOURISH_MS : phaseNow === 'searching' ? SPIN_MS : 0
  if (want === tickerMs) return
  try {
    ticker?.cancel()
  } catch {
    // Already gone.
  }
  ticker = null
  tickerMs = 0
  if (want === 0) return
  try {
    ticker = $.clock.every(want, () => tick($))
    tickerMs = want
  } catch {
    // No animation: the band still draws its first frame, and a flourish
    // that cannot play is dropped rather than stuck on its first frame.
    flourish = null
  }
}

function tick($: EngineInterface): void {
  frame++
  if (flourish !== null) {
    flourish++
    if (flourish >= FLOURISH.length) {
      flourish = null
      syncTicker($)
    }
  }
  invalidate($)
}

function invalidate($: EngineInterface): void {
  try {
    $.ui.invalidate('ui.render')
  } catch {
    // Quiet.
  }
}

async function setChat($: EngineInterface, fn: (c: Chat) => Chat): Promise<void> {
  let next: Chat | undefined
  await update($, chat, (c) => (next = fn({ ...IDLE, ...c })))
  // Only a value the update actually produced moves the ticker.
  if (next !== undefined) syncTicker($, next)
}

async function getChat($: EngineInterface): Promise<Chat> {
  return { ...IDLE, ...(await read($, chat)) }
}

/** The offer a kept chat leaves when it ends, or null (not kept, no chat, no id). */
function keepsakeOf(c: Chat): Keepsake | null {
  if (!c.kept || c.phase !== 'chatting' || c.match === null) return null
  const id = oneLine(c.match.sessionId)
  return id === '' ? null : { id, copy: 'none' }
}

async function burstActive($: EngineInterface): Promise<boolean> {
  try {
    return (await getLink($)).want === 'ready'
  } catch {
    return false
  }
}

function closePane($: EngineInterface): void {
  try {
    void $.ui.close({ id: CHAT_PANE_ID }).catch(() => {})
  } catch {
    // Quiet.
  }
}

// ---------------------------------------------------------------------------
// Events from the poll.

export type ChatEvent = { seq: number; type: string; [k: string]: unknown }

const str = (v: unknown) => (typeof v === 'string' ? v : '')

/**
 * Applies fresh events in ascending seq order. Never throws. After a held
 * chat's session_ended the rest of the batch is dropped: Claude is back, so a
 * re-enqueue the hub raced in (queued, matched) is never shown.
 */
export async function applyEvents($: EngineInterface, events: readonly ChatEvent[], base?: string): Promise<void> {
  const sorted = [...events].sort((a, b) => a.seq - b.seq)
  for (const ev of sorted) {
    try {
      if ((await applyEvent($, ev, base)) === 'stop') return
    } catch {
      // Quiet: the next event still applies.
    }
  }
}

async function applyEvent($: EngineInterface, ev: ChatEvent, base: string | undefined): Promise<'stop' | void> {
  switch (ev.type) {
    case 'queued':
      await setChat($, (c) => (c.phase === 'chatting' ? c : { ...c, phase: 'searching' }))
      return
    case 'matched': {
      const match: ChatMatch = {
        sessionId: str(ev.session_id),
        pseudonym: capText(ev.pseudonym, MAX_NAME),
        blurb: capText(ev.blurb, MAX_NAME),
        opener: capText(ev.opener, MAX_OPENER),
      }
      flourish = 0
      const persist = persistSetting
      // A pending keepsake is replaced: the new chat takes the Pane.
      await setChat($, () => ({ ...IDLE, phase: 'chatting', match, persist }))
      // Not awaited: an open that never settles must not stall the poll loop.
      void openForMatch($, match.sessionId)
      return
    }
    case 'chat_msg': {
      const text = capText(ev.text)
      if (text.trim() === '') return
      // Never the peer's client_msg_id: it is peer-controlled and would key the drawing.
      const id = `p-${ev.seq}`
      await setChat($, (c) => {
        if (c.phase !== 'chatting' || c.left || c.match === null) return c
        if (c.lines.some((l) => l.id === id)) return c
        const line: ChatLine = { id, from: 'peer', text }
        return { ...c, lines: [...c.lines, line].slice(-MAX_LINES) }
      })
      return
    }
    case 'session_ended': {
      const searching = await burstActive($)
      await setChat($, (c) =>
        c.phase !== 'chatting'
          ? c
          : // Unplaced (narrow), the ended line would never be seen, so it is
            // dropped; a keepsake stays for the band to hint at.
            {
              ...IDLE,
              phase: searching ? 'searching' : 'idle',
              ended: c.placed,
              placed: c.placed,
              persist: c.persist,
              keepsake: keepsakeOf(c),
            },
      )
      const held = (await getLink($)).held
      if (held) {
        await releaseHold($, base)
        return 'stop'
      }
      return
    }
    default:
      return
  }
}

/** The plugin-initiated open on `matched`, in the background; `placed` lands for the same match only. */
async function openForMatch($: EngineInterface, sessionId: string): Promise<void> {
  let placed = false
  try {
    const r = await $.ui.open({ id: CHAT_PANE_ID, title: 'claudingtin', focus: true, rows: 16 })
    placed = r?.isPlaced === true
  } catch {
    placed = false
  }
  try {
    await setChat($, (c) => (c.phase === 'chatting' && c.match?.sessionId === sessionId ? { ...c, placed } : c))
  } catch {
    // Quiet.
  }
}

// ---------------------------------------------------------------------------
// The person's actions. Each awaits state only; sends are voided.

/** An Input submit: trimmed, capped, appended at once, then sent. Never throws. */
export async function submitLine($: EngineInterface, base: string, raw: unknown): Promise<void> {
  try {
    const text = capText(typeof raw === 'string' ? raw.trim() : '').trim()
    if (text === '') return
    const clientMsgId = crypto.randomUUID()
    const id = `s-${clientMsgId}`
    let ok = false
    await setChat($, (c) => {
      ok = c.phase === 'chatting' && !c.left && c.match !== null
      return ok ? { ...c, lines: [...c.lines, { id, from: 'self' as const, text }].slice(-MAX_LINES) } : c
    })
    if (ok) void sendNow($, base, { type: 'chat_msg', v: 1, client_msg_id: clientMsgId, text }).catch(() => {})
  } catch {
    // Quiet.
  }
}

export type LeaveKind = 'leave' | 'block' | 'report'

/**
 * Leave (or block / report, then leave) the current chat: exactly one leave
 * sent, the chat cleared, the Pane closed. False when no chat is going.
 * Never throws.
 */
export async function leaveChat($: EngineInterface, base: string, kind: LeaveKind): Promise<boolean> {
  try {
    const searching = await burstActive($)
    let left = false
    let offer: Keepsake | null = null
    await setChat($, (c) => {
      left = c.phase === 'chatting' && !c.left
      if (!left) return c
      // Leaving keeps the thread back; blocking or reporting never does.
      offer = kind === 'leave' ? keepsakeOf(c) : null
      return { ...IDLE, phase: searching ? 'searching' : 'idle', left: true, keepsake: offer, placed: offer !== null && c.placed }
    })
    if (!left) return false
    // In order on the wire (block / report settles before the leave goes), never awaited here.
    const bodies: Record<string, unknown>[] = []
    if (kind === 'block') bodies.push({ type: 'block', v: 1 })
    if (kind === 'report') bodies.push({ type: 'report', v: 1, last_n: REPORT_LAST_N })
    bodies.push({ type: 'leave', v: 1 })
    // A held chat's withheld busy follows once these settle (Claude is back).
    void sendSeqNow($, base, bodies)
      .then(() => releaseHold($, base))
      .catch(() => {})
    // A keepsake stays on the Pane (or the band, when it is not placed).
    if (offer === null) closePane($)
    return true
  } catch {
    return false
  }
}

/** Back to idle and the Pane closed: own Claude back, or the handle lost. Never throws. */
export async function resetChat($: EngineInterface): Promise<void> {
  try {
    flourish = null
    let had = false
    let offer: Keepsake | null = null
    await setChat($, (c) => {
      had = c.phase !== 'idle' || c.ended || c.match !== null || c.lines.length > 0 || c.placed
      if (!had) return c
      // A kept chat ending here offers its keepsake; one already pending stays.
      offer = keepsakeOf(c) ?? c.keepsake
      if (offer === null) return { ...IDLE }
      return { ...IDLE, keepsake: offer, placed: c.placed, ended: c.match === null && c.ended }
    })
    if (had && offer === null) closePane($)
  } catch {
    // Quiet.
  }
}

export type ReturnMode = 'hold' | 'leave' | 'none'

/**
 * The person's Claude is back (a main-loop turn.complete): a live chat that
 * was matched with persist on is held open with the quiet note ('hold'), when
 * the link can keep polling; otherwise it ends ('leave'); no chat is 'none'.
 * Never throws.
 */
export async function onModelBack($: EngineInterface, canHold: boolean): Promise<ReturnMode> {
  try {
    let mode: ReturnMode = 'none'
    await setChat($, (c) => {
      if (c.phase !== 'chatting' || c.left || c.match === null) return c
      mode = c.persist && canHold ? 'hold' : 'leave'
      return mode === 'hold' ? { ...c, back: true } : c
    })
    return mode
  } catch {
    return 'none'
  }
}

/** The hold landed: the chat outlived the burst and is kept (Story 3.7). Never throws. */
export async function markKept($: EngineInterface): Promise<void> {
  try {
    await setChat($, (c) => (c.phase === 'chatting' && c.back && !c.kept ? { ...c, kept: true } : c))
  } catch {
    // Quiet.
  }
}

/** A new burst began: the "your claude's back" note goes. Never throws. */
export async function clearBack($: EngineInterface): Promise<void> {
  try {
    await setChat($, (c) => (c.back ? { ...c, back: false } : c))
  } catch {
    // Quiet.
  }
}

/** `/claudingtin chat`: opens the Pane (person-initiated, so placed at any width). */
export async function openChat($: EngineInterface): Promise<'open' | 'none' | 'unplaced'> {
  try {
    const c = await getChat($)
    const chatting = c.phase === 'chatting' && c.match !== null
    if (!chatting && c.keepsake === null) return 'none'
    const sessionId = chatting ? c.match!.sessionId : null
    const keepId = c.keepsake?.id ?? null
    let placed = false
    try {
      const r = await $.ui.open({ id: CHAT_PANE_ID, title: 'claudingtin', focus: true, rows: 16 })
      placed = r?.isPlaced === true
    } catch {
      placed = false
    }
    await setChat($, (s) => {
      if (sessionId !== null) return s.phase === 'chatting' && s.match?.sessionId === sessionId ? { ...s, placed } : s
      return s.phase !== 'chatting' && s.keepsake?.id === keepId ? { ...s, placed } : s
    })
    return placed ? 'open' : 'unplaced'
  } catch {
    return 'unplaced'
  }
}

/** The person closed the Pane: not placed (the band offers it again); an ended line is done. */
export async function onChatClose($: EngineInterface, origin: { kind?: string } | undefined): Promise<void> {
  if (origin?.kind !== 'person') return
  try {
    // Closing it dismisses a keepsake too.
    await setChat($, (c) => ({ ...c, placed: false, ended: false, keepsake: c.phase === 'chatting' ? c.keepsake : null }))
  } catch {
    // Quiet.
  }
}

/** `copy id`: the keepsake onto the pressing surface's clipboard; the result shows under it. Never throws. */
export async function copyKeepsake($: EngineInterface, surface: RenderSurface | undefined): Promise<void> {
  try {
    const k = (await getChat($)).keepsake
    if (k === null) return
    let copied = false
    try {
      const r = await $.ui.copy(surface === undefined ? { text: k.id } : { text: k.id, surface })
      copied = r?.isCopied === true
    } catch {
      copied = false
    }
    const copy: Keepsake['copy'] = copied ? 'copied' : 'failed'
    await setChat($, (c) => (c.keepsake?.id === k.id ? { ...c, keepsake: { ...c.keepsake, copy } } : c))
  } catch {
    // Quiet.
  }
}

/** `done`: the keepsake goes and, outside a chat, the Pane closes. Never throws. */
export async function dismissKeepsake($: EngineInterface): Promise<void> {
  try {
    let close = false
    await setChat($, (c) => {
      if (c.keepsake === null) return c
      close = c.phase !== 'chatting'
      return close ? { ...c, keepsake: null, ended: false, placed: false } : { ...c, keepsake: null }
    })
    if (close) closePane($)
  } catch {
    // Quiet.
  }
}

/** session.start (also after a hot reload): restart the ticker and redraw from state. */
export async function resumeChat($: EngineInterface): Promise<void> {
  try {
    syncTicker($, await getChat($))
    invalidate($)
  } catch {
    // Quiet.
  }
}

// ---------------------------------------------------------------------------
// Drawing. Pure: reads only.

/** The band line for the chat, or null to leave the band to the status line. */
export function bandLine(c: Chat): string | null {
  if (flourish !== null && c.phase === 'chatting') return `${FLOURISH[flourish] ?? FLOURISH[0]}${COPY.flourish}`
  if (c.phase === 'searching') return `${SPINNER[frame % SPINNER.length]} ${COPY.searching}`
  if (c.phase === 'chatting' && !c.placed) return `claudingtin · chatting with ${peerName(c.match)} — /claudingtin chat`
  if (c.phase === 'chatting' && c.back) return COPY.heldBand
  if (c.phase === 'idle' && c.keepsake !== null && !c.placed) return COPY.keepBand
  return null
}

/** The band line from state, for the AbovePrompt hook (the read subscribes its redraw). */
export async function chatBand($: EngineInterface): Promise<string | null> {
  return bandLine(await getChat($))
}

const rowsOf = (text: string, cols: number) =>
  text.split('\n').reduce((n, part) => n + Math.max(1, Math.ceil(Array.from(part).length / Math.max(1, cols))), 0)

/** The chat Pane: header, blurb, the three buttons, the history window, the Input. */
export async function renderChat($: EngineInterface, e: RenderInput<'Pane'>, base: string) {
  const c = await getChat($)
  const table = $.ui.resolve(e)
  const { Box, Text, Button } = table
  // Every surface but mobile has an Input; there the chat is read-only.
  const Input = 'Input' in table ? table.Input : null

  if (c.phase !== 'chatting' || c.match === null) {
    const k = c.keepsake
    if (k === null) {
      return (
        <Box key="claudingtin-chat" flexDirection="column">
          <Text key="ended" wrap="wrap">
            {c.ended ? COPY.ended : COPY.noChat}
          </Text>
        </Box>
      )
    }
    return (
      <Box key="claudingtin-chat" flexDirection="column">
        {c.ended ? (
          <Text key="ended" wrap="wrap">
            {COPY.ended}
          </Text>
        ) : null}
        <Text key="keep-note" wrap="wrap">
          {COPY.keepNote}
        </Text>
        <Text key="keep-id" bold wrap="wrap">
          {k.id}
        </Text>
        <Box key="keep-actions" flexDirection="row" gap={2}>
          <Button
            key="keep-copy"
            label={k.copy === 'copied' ? COPY.keepCopied : COPY.keepCopy}
            onPress={(p) => copyKeepsake($, p?.surface)}
          />
          <Button key="keep-done" label={COPY.keepDone} onPress={() => dismissKeepsake($)} />
        </Box>
        {k.copy === 'failed' ? (
          <Text key="keep-failed" dimColor wrap="wrap">
            {COPY.keepFailed}
          </Text>
        ) : null}
      </Box>
    )
  }

  const blurb = oneLine(c.match.blurb)
  const entries: Array<{ key: string; text: string; dim: boolean }> = []
  const opener = inert(c.match.opener).trim()
  if (opener !== '') entries.push({ key: 'opener', text: opener, dim: true })
  for (const l of c.lines) {
    entries.push({ key: `l-${l.id}`, text: `${l.from === 'self' ? 'you' : 'them'}  ${inert(l.text)}`, dim: false })
  }
  const cols = Number.isFinite(e.props.bodyColumns) && e.props.bodyColumns > 0 ? e.props.bodyColumns : 80
  const rowsProp = e.props.scroll?.bodyRows
  const bodyRows = typeof rowsProp === 'number' && Number.isFinite(rowsProp) ? rowsProp : 14
  // Header and buttons, the blurb when there is one, the Input where the surface has one.
  const fixed = 2 + (blurb !== '' ? 1 : 0) + (c.back ? 1 : 0) + (Input !== null ? 1 : 0)
  let room = Math.max(1, bodyRows - fixed)
  let first = entries.length
  while (first > 0) {
    const r = rowsOf(entries[first - 1]!.text, cols)
    if (r > room && first < entries.length) break
    room -= r
    first--
    if (room <= 0) break
  }
  const shown = entries.slice(first)

  return (
    <Box key="claudingtin-chat" flexDirection="column">
      <Text key="header" bold wrap="truncate-end">
        {COPY.header + peerName(c.match)}
      </Text>
      {blurb !== '' ? (
        <Text key="blurb" dimColor wrap="truncate-end">
          {blurb}
        </Text>
      ) : null}
      {c.back ? (
        <Text key="back" dimColor wrap="truncate-end">
          {COPY.back}
        </Text>
      ) : null}
      <Box key="actions" flexDirection="row" gap={2}>
        <Button key="leave" label={COPY.leave} onPress={() => leaveChat($, base, 'leave')} />
        <Button key="block" label={COPY.block} onPress={() => leaveChat($, base, 'block')} />
        <Button key="report" label={COPY.report} onPress={() => leaveChat($, base, 'report')} />
      </Box>
      <Box key="history" flexDirection="column">
        {shown.map((s) => (
          <Text key={s.key} dimColor={s.dim || undefined} wrap="wrap">
            {s.text}
          </Text>
        ))}
      </Box>
      {Input !== null ? (
        <Input
          key={INPUT_KEY}
          placeholder={COPY.placeholder}
          value=""
          submitLabel="send"
          autoFocus
          onSubmit={(v: string) => submitLine($, base, v)}
        />
      ) : null}
    </Box>
  )
}
