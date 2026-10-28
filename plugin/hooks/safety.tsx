import { atom, read, update } from 'claude-code'
import type { EngineInterface, PaneCloseOrigin, RenderInput } from 'claude-code'
import { chmod600, configDir, joinPath } from './configdir'
import { loadAccountKey } from './identity'

// The first-run gate, byte-compatible with the companion's
// (companion/internal/safety, deleted in Story 7.6): <configDir>/safety-ack
// holds `version 1\naccepted_at <ms>\n`, and only version 1 counts. Acceptance is
// local only: nothing about it is ever sent anywhere.

export const SAFETY_PANE_ID = 'claudingtin-safety'
const ACK_FILE = 'safety-ack'
const ACK_VERSION = 1
const MAX_ACK_FILE_SIZE = 512

/** The first-run screen, one paragraph per entry (the companion's copy, with its last lines rewritten for buttons). */
export const SAFETY_COPY: readonly string[] = [
  "here's the deal, before anything connects.",
  "while claude is off thinking, this puts you in a room with one other person who's also waiting around — a stranger, picked at random. no names, no profiles, no history. you won't know who they are and they won't know you.",
  'so keep the private stuff private: no real name, no address or city, nothing about where you work or your money, and no photos of yourself. anything you type can be screenshotted or quoted somewhere else — write like it might not stay in the room.',
  'if the other person makes it unpleasant, you can leave whenever you want. block keeps you from being paired with them again. report hands the last few messages to a human to look at. neither one sends the other person a notice.',
  'you also have to be 18 or older.',
  'if that\'s you and you want to go ahead, press "i am 18 or older" (or y). "not now" (or n) keeps this quiet: no one is connected, and you\'ll see this again next session, or sooner with /claudingtin safety.',
]

/**
 * `accepted`: this session may connect (from disk, or a press whose write
 * failed). `declined`: the person said not now or closed the pane, so it is not
 * opened again unasked this session.
 */
export const gate = atom({ plugin: 'claudingtin', key: 'gate' } as const, { accepted: false, declined: false })

/** The `version` in an ack body, as the companion's parseAckVersion reads it; `undefined` when absent or unparseable. */
export function parseAckVersion(body: string): number | undefined {
  for (const ln of body.split('\n')) {
    const fields = ln.trim().split(/\s+/)
    if (fields.length === 2 && fields[0] === 'version') {
      return /^[+-]?\d+$/.test(fields[1]!) ? Number(fields[1]) : undefined
    }
  }
  return undefined
}

/** Whether safety-ack records version 1. Missing, unreadable, oversized or garbled is false. Never throws. */
export async function isAccepted($: EngineInterface): Promise<boolean> {
  try {
    const dir = await configDir($)
    if (dir === undefined) return false
    const raw = await $.fs.read(joinPath(dir, ACK_FILE))
    if (typeof raw !== 'string') return false
    if (new TextEncoder().encode(raw).length > MAX_ACK_FILE_SIZE) return false
    return parseAckVersion(raw) === ACK_VERSION
  } catch {
    return false
  }
}

/** Writes safety-ack (then chmod 600); true when the write landed. Never throws. */
export async function recordAccepted($: EngineInterface): Promise<boolean> {
  try {
    const dir = await configDir($)
    if (dir === undefined) return false
    let now: number
    try {
      now = await $.clock.now()
    } catch {
      now = Date.now()
    }
    if (!Number.isFinite(now)) now = Date.now()
    const path = joinPath(dir, ACK_FILE)
    await $.fs.write(path, `version ${ACK_VERSION}\naccepted_at ${Math.floor(now)}\n`)
    await chmod600($, path)
    return true
  } catch {
    return false
  }
}

/**
 * Opens the safety pane, focused. `asked` (the person's `/claudingtin safety`)
 * clears an earlier decline; unasked, a declined session or an already open
 * pane is left alone. True when the pane is drawn (or was already open); false
 * when declined, not placed, or on any failure. Never throws.
 */
export async function openSafety($: EngineInterface, asked: boolean): Promise<boolean> {
  try {
    if (asked) {
      await update($, gate, (g) => ({ ...g, declined: false }))
    } else {
      if ((await read($, gate)).declined) return false
      const panes = await $.ui.panes()
      if (panes.some((p) => p.id === SAFETY_PANE_ID)) return true
    }
    const opened = await $.ui.open({ id: SAFETY_PANE_ID, title: 'claudingtin', focus: true, closeOnEscape: true, rows: 20 })
    return opened?.isPlaced === true
  } catch {
    // Fail open: the mod stays inert.
    return false
  }
}

/**
 * Whether this session may connect. Not accepted and not declined, it also
 * offers the pane (unasked). Never throws; any failure is false.
 */
export async function requireGate($: EngineInterface): Promise<boolean> {
  try {
    const g = await read($, gate)
    if (g.accepted) return true
    if (await isAccepted($)) {
      await update($, gate, (s) => ({ ...s, accepted: true }))
      return true
    }
    if (!g.declined) await openSafety($, false)
    return false
  } catch {
    return false
  }
}

/** The person pressed "i am 18 or older". A failed write still accepts for this session. */
export async function accept($: EngineInterface): Promise<void> {
  try {
    await update($, gate, () => ({ accepted: true, declined: false }))
  } catch {
    // Best effort.
  }
  await recordAccepted($)
  try {
    await $.ui.close({ id: SAFETY_PANE_ID })
  } catch {
    // Best effort.
  }
  void loadAccountKey($).catch(() => {})
}

/** The person said not now, or closed the pane: nothing written, not reopened unasked this session. */
export async function decline($: EngineInterface, close: boolean): Promise<void> {
  try {
    await update($, gate, (g) => ({ ...g, declined: true }))
  } catch {
    // Best effort.
  }
  if (!close) return
  try {
    await $.ui.close({ id: SAFETY_PANE_ID })
  } catch {
    // Best effort.
  }
}

/**
 * The pane closed: the person's close (mark, ctrl+x x, Escape) is a decline;
 * a plugin close (after a press) or an unload leaves the gate as it is.
 */
export async function onSafetyClose($: EngineInterface, origin: PaneCloseOrigin): Promise<void> {
  if (origin?.kind === 'person') await decline($, false)
}

/** The pane's body: the copy, then the two buttons. */
export function renderSafety($: EngineInterface, e: RenderInput<'Pane'>) {
  const { Box, Text, Button } = $.ui.resolve(e)
  return (
    <Box key="claudingtin-safety" flexDirection="column" gap={1}>
      {SAFETY_COPY.map((p, i) => (
        <Text key={`copy-${i}`} wrap="wrap">
          {p}
        </Text>
      ))}
      <Box key="choices" flexDirection="row" gap={2}>
        <Button
          key="accept"
          label="i am 18 or older"
          hotkey="y"
          variant="primary"
          onPress={() => accept($)}
        />
        <Button key="decline" label="not now" hotkey="n" onPress={() => decline($, true)} />
      </Box>
    </Box>
  )
}
