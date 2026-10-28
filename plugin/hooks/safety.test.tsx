import { test, expect, mock } from 'claude-code/testing'
import {
  SAFETY_COPY,
  SAFETY_PANE_ID,
  accept,
  decline,
  isAccepted,
  onSafetyClose,
  openSafety,
  parseAckVersion,
  recordAccepted,
  requireGate,
} from './safety'

const HOME = '/Users/a'
const DIR = `${HOME}/Library/Application Support/claudingtin`
const ACK = `${DIR}/safety-ack`
const KEY = `${DIR}/account-key`
const VALID = '0f8fad5b-d9cb-469f-a165-70867728950e'
const ACK_V1 = 'version 1\naccepted_at 1700000000000\n'

type Files = Record<string, string>

// ---------------------------------------------------------------------------
// Unit: a hand-made `$` (the test kit's own `$` has no fs, env, process or
// state nouns). State is an in-memory versioned map, as $.state keeps it.

type Fake = {
  files?: Files
  env?: Record<string, string>
  failWrite?: boolean
  unreadable?: string[]
  openPanes?: string[]
  notPlaced?: boolean
  now?: number
}

function fake(f: Fake = {}) {
  const files = new Map(Object.entries(f.files ?? {}))
  const env = f.env ?? { HOME }
  const state = new Map<string, { value: unknown; version: number }>()
  const opens: any[] = []
  const closes: string[] = []
  const writes: string[] = []
  const panes = new Set(f.openPanes ?? [])
  const addr = (r: any) => `${r.plugin}.${r.key}`
  const $ = {
    env: { get: async (n: string) => env[n] },
    fs: {
      read: async (p: string) => {
        if ((f.unreadable ?? []).includes(p) || !files.has(p)) throw new Error('ENOENT')
        return files.get(p)
      },
      exists: async (p: string) => files.has(p) || p === `${HOME}/Library/Application Support`,
      write: async (p: string, t: string) => {
        if (f.failWrite) throw new Error('EROFS')
        writes.push(p)
        files.set(p, t)
      },
    },
    process: { run: async () => ({ exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false }) },
    clock: { now: async () => f.now ?? 1700000000123 },
    state: {
      get: async (r: any) => state.get(addr(r)) ?? { value: undefined, version: 0 },
      set: async (r: any, value: unknown, o?: { ifVersion?: number }) => {
        const cur = state.get(addr(r)) ?? { value: undefined, version: 0 }
        if (o?.ifVersion !== undefined && o.ifVersion !== cur.version) return { isSet: false, version: cur.version }
        state.set(addr(r), { value, version: cur.version + 1 })
        return { isSet: true, version: cur.version + 1 }
      },
    },
    ui: {
      open: async (a: any) => {
        opens.push(a)
        panes.add(a.id)
        return f.notPlaced ? { isPlaced: false, reason: 'narrow' } : { isPlaced: true }
      },
      close: async (a: any) => {
        closes.push(a.id)
        panes.delete(a.id)
      },
      panes: async () => [...panes].map((id) => ({ id, title: id, isShown: true, isFocused: true, isPlaced: true })),
    },
  }
  return { $: $ as any, files, opens, closes, writes }
}

test('parseAckVersion reads the companion body', async () => {
  expect(parseAckVersion(ACK_V1)).toBe(1)
  expect(parseAckVersion('accepted_at 1\nversion 2\n')).toBe(2)
  expect(parseAckVersion('version x\n')).toBe(undefined)
  expect(parseAckVersion('')).toBe(undefined)
  expect(parseAckVersion('version 1 extra\n')).toBe(undefined)
})

test('isAccepted: only version 1 counts', async () => {
  expect(await isAccepted(fake({ files: { [ACK]: ACK_V1 } }).$)).toBe(true)
  expect(await isAccepted(fake().$)).toBe(false)
  expect(await isAccepted(fake({ files: { [ACK]: 'version 0\naccepted_at 1\n' } }).$)).toBe(false)
  expect(await isAccepted(fake({ files: { [ACK]: 'garbled' } }).$)).toBe(false)
  expect(await isAccepted(fake({ files: { [ACK]: ACK_V1 + ' '.repeat(600) } }).$)).toBe(false)
  expect(await isAccepted(fake({ files: { [ACK]: ACK_V1 }, unreadable: [ACK] }).$)).toBe(false)
  expect(await isAccepted(fake({ files: { [ACK]: ACK_V1 }, env: {} }).$)).toBe(false)
})

test('recordAccepted writes the companion format', async () => {
  const w = fake()
  expect(await recordAccepted(w.$)).toBe(true)
  expect(w.files.get(ACK)).toBe('version 1\naccepted_at 1700000000123\n')
  expect(await isAccepted(w.$)).toBe(true)
})

test('recordAccepted: a non-finite clock falls back to Date.now()', async () => {
  const w = fake({ now: NaN })
  expect(await recordAccepted(w.$)).toBe(true)
  const m = /^version 1\naccepted_at (\d+)\n$/.exec(w.files.get(ACK)!)
  expect(m).toBeTruthy()
  expect(Number(m![1])).toBeGreaterThan(1600000000000)
})

test('openSafety: true when placed or already open, false when not placed or declined', async () => {
  expect(await openSafety(fake().$, true)).toBe(true)
  expect(await openSafety(fake({ notPlaced: true }).$, true)).toBe(false)
  expect(await openSafety(fake({ openPanes: [SAFETY_PANE_ID] }).$, false)).toBe(true)
  const d = fake()
  await decline(d.$, false)
  expect(await openSafety(d.$, false)).toBe(false)
  expect(d.opens).toEqual([])
  const boom = async () => {
    throw new Error('boom')
  }
  expect(await openSafety({ state: { get: boom, set: boom } } as any, true)).toBe(false)
})

test('requireGate: already accepted on disk → true, no pane', async () => {
  const w = fake({ files: { [ACK]: ACK_V1 } })
  expect(await requireGate(w.$)).toBe(true)
  expect(w.opens).toEqual([])
})

test('requireGate: not accepted → false and offers the pane once', async () => {
  const w = fake()
  expect(await requireGate(w.$)).toBe(false)
  expect(await requireGate(w.$)).toBe(false)
  expect(w.opens).toEqual([expect.objectContaining({ id: SAFETY_PANE_ID, focus: true })])
})

test('accept: ack written, pane closed, requireGate true', async () => {
  const w = fake()
  await openSafety(w.$, false)
  await accept(w.$)
  expect(w.files.get(ACK)).toBe('version 1\naccepted_at 1700000000123\n')
  expect(w.closes).toEqual([SAFETY_PANE_ID])
  expect(await requireGate(w.$)).toBe(true)
})

test('accept with a failed write: accepted for this session only', async () => {
  const w = fake({ failWrite: true })
  await accept(w.$)
  expect(w.files.has(ACK)).toBe(false)
  expect(await requireGate(w.$)).toBe(true)
})

test('accept with no home: accepted for this session, nothing written', async () => {
  const w = fake({ env: {} })
  await accept(w.$)
  expect(w.writes).toEqual([])
  expect(await requireGate(w.$)).toBe(true)
})

test('decline: nothing written; requireGate false without reopening', async () => {
  const w = fake()
  await openSafety(w.$, false)
  await decline(w.$, true)
  expect(w.closes).toEqual([SAFETY_PANE_ID])
  expect(await requireGate(w.$)).toBe(false)
  expect(await requireGate(w.$)).toBe(false)
  expect(w.opens.length).toBe(1)
  expect(w.writes).toEqual([])
  // An unasked open stays quiet too.
  await openSafety(w.$, false)
  expect(w.opens.length).toBe(1)
})

test('onSafetyClose(person): declined, nothing written, requireGate false without reopening', async () => {
  const w = fake()
  await openSafety(w.$, false)
  await w.$.ui.close({ id: SAFETY_PANE_ID })
  await onSafetyClose(w.$, { kind: 'person' })
  expect(await requireGate(w.$)).toBe(false)
  expect(w.opens.length).toBe(1)
  expect(w.writes).toEqual([])
})

test('onSafetyClose(plugin / unload): gate untouched', async () => {
  for (const kind of ['plugin', 'unload'] as const) {
    const w = fake()
    await openSafety(w.$, false)
    await w.$.ui.close({ id: SAFETY_PANE_ID })
    await onSafetyClose(w.$, { kind })
    // Not declined: requireGate offers the pane again.
    expect(await requireGate(w.$)).toBe(false)
    expect(w.opens.length).toBe(2)
  }
  // After an accept, a plugin close keeps it accepted.
  const a = fake()
  await accept(a.$)
  await onSafetyClose(a.$, { kind: 'plugin' })
  expect(await requireGate(a.$)).toBe(true)
})

test('re-open (asked) clears the decline', async () => {
  const w = fake()
  await decline(w.$, false)
  await openSafety(w.$, true)
  expect(w.opens.length).toBe(1)
  await w.$.ui.close({ id: SAFETY_PANE_ID })
  // Declined flag cleared: an unasked offer is allowed again.
  expect(await requireGate(w.$)).toBe(false)
  expect(w.opens.length).toBe(2)
})

test('no home: requireGate false, no throw', async () => {
  const w = fake({ env: {} })
  expect(await requireGate(w.$)).toBe(false)
})

test('requireGate never throws', async () => {
  const boom = async () => {
    throw new Error('boom')
  }
  expect(await requireGate({ state: { get: boom, set: boom } } as any)).toBe(false)
})

test('copy carries the four points and never asks to type', async () => {
  const all = SAFETY_COPY.join('\n')
  expect(all).toContain('stranger')
  expect(all).toContain('keep the private stuff private')
  expect(all).toContain('screenshotted')
  expect(all).toContain('block')
  expect(all).toContain('report')
  expect(all).toContain('notice')
  expect(all).toContain('18 or older')
  expect(all).not.toMatch(/type\s+yes/i)
  expect(all).toContain('"i am 18 or older" (or y)')
})

// ---------------------------------------------------------------------------
// Integration: the plugin's own hooks, with the world mocked beneath it.

type World = { files?: Files; env?: Record<string, string>; failWrite?: boolean; notPlaced?: boolean }

async function start($: any, on: any, w: World = {}) {
  const clock = mock.clock(on, { now: 1700000000999 })
  mock.env(on, w.env ?? { HOME })
  const files = new Map(Object.entries(w.files ?? {}))
  const log = { fetches: [] as string[], opens: [] as any[], closes: [] as any[], writes: [] as string[], runs: [] as string[][], said: [] as string[], commands: [] as any[] }
  const panes = new Set<string>()
  on('fs.read', (_$: any, e: any) => {
    if (!files.has(e.path)) throw new Error('ENOENT')
    return { value: files.get(e.path) }
  })
  on('fs.exists', (_$: any, e: any) => ({ value: files.has(e.path) || e.path === `${HOME}/Library/Application Support` }))
  on('fs.write', (_$: any, e: any) => {
    if (w.failWrite) throw new Error('EROFS')
    log.writes.push(e.path)
    files.set(e.path, e.text)
    return { value: undefined }
  })
  on('process.run', (_$: any, e: any) => {
    log.runs.push([...e.argv])
    return { value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('http.fetch', (_$: any, e: any) => {
    log.fetches.push(`${e.init?.method ?? 'GET'} ${e.url}`)
    return { value: { status: 200, ok: true, headers: {}, text: '' } }
  })
  on('ui.open', (_$: any, e: any) => {
    log.opens.push({ ...e })
    panes.add(e.id)
    return { value: w.notPlaced ? { isPlaced: false, reason: 'narrow' } : { isPlaced: true } }
  })
  on('ui.close', (_$: any, e: any) => {
    log.closes.push({ id: e.id, origin: e.origin?.kind })
    panes.delete(e.id)
    return { value: undefined }
  })
  on('ui.panes', () => ({ value: [...panes].map((id) => ({ id, title: id, isShown: true, isFocused: true, isPlaced: true })) }))
  on('command.register', (_$: any, e: any) => {
    log.commands.push({ ...e })
    return { value: { command: e.name } }
  })
  for (const ev of ['ui.log', 'ui.status', 'ui.toast', 'ui.notice']) {
    on(ev, (_$: any, e: any) => {
      log.said.push(JSON.stringify(e))
      return { value: undefined }
    })
  }
  on('session.start', (_$: any, e: any) => e as any)
  await $.session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  return { clock, files, log, panes }
}

const PANE = {
  plugin: 'claudingtin',
  surface: 'terminal',
  component: 'Pane',
  requestId: 'claudingtin-safety',
  props: { title: 'claudingtin', isFocused: true, bodyColumns: 80, placement: 'inline', scroll: { top: 0, bodyRows: 30 } as any, view: {} as any },
} as const

const runCommand = ($: any, args: string) =>
  $.command.run({ command: 'claudingtin', args, origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 80 } })

test('companion files carry over: no pane, key untouched, only /status fetched', async ($, on) => {
  const { files, log } = await start($, on, { files: { [ACK]: ACK_V1, [KEY]: VALID + '\n' } })
  expect(log.opens).toEqual([])
  expect(log.writes).toEqual([])
  expect(files.get(KEY)).toBe(VALID + '\n')
  expect(log.runs).toContainEqual(['chmod', '600', KEY])
  expect(log.fetches).toEqual(['GET http://127.0.0.1:8080/status'])
  expect(log.said.join('')).not.toContain(VALID)
  expect(log.commands).toEqual([expect.objectContaining({ name: 'claudingtin', argumentHint: 'chat|leave|block|report|persist|safety' })])
})

test('first run (no ack, no key): ack absent → key file not created before acceptance', async ($, on) => {
  const { log } = await start($, on)
  expect(log.writes).toEqual([])
})

for (const [name, body] of [
  ['missing', undefined],
  ['older version', 'version 0\naccepted_at 1\n'],
  ['garbled', '\u0000\u0001nope'],
] as const) {
  test(`not accepted (${name}): focused pane opens; only GET /status fetched`, async ($, on) => {
    const { log, clock } = await start($, on, body === undefined ? {} : { files: { [ACK]: body } })
    expect(log.opens).toEqual([expect.objectContaining({ id: 'claudingtin-safety', focus: true })])
    await clock.advance(15000)
    expect(log.fetches.length).toBeGreaterThan(1)
    for (const f of log.fetches) expect(f).toBe('GET http://127.0.0.1:8080/status')
  })
}

test('pane draws the copy and two buttons', async ($, on) => {
  await start($, on)
  const ui = await $.ui.mount(PANE as any)
  expect((await ui.find({ type: 'Text', text: /here's the deal/ }))?.text).toBe(SAFETY_COPY[0])
  const accept = await ui.find({ key: 'accept' })
  const no = await ui.find({ key: 'decline' })
  expect(accept?.props).toMatchObject({ label: 'i am 18 or older', hotkey: 'y' })
  expect(no?.props).toMatchObject({ label: 'not now', hotkey: 'n' })
})

test('accept: ack written (companion format), chmod 600, pane closed, key warmed', async ($, on) => {
  const { files, log, clock } = await start($, on)
  const ui = await $.ui.mount(PANE as any)
  await ui.press({ key: 'accept' })
  await clock.settle()
  expect(files.get(ACK)).toMatch(/^version 1\naccepted_at \d+\n$/)
  expect(log.runs).toContainEqual(['chmod', '600', ACK])
  expect(log.closes).toContainEqual({ id: 'claudingtin-safety', origin: 'plugin' })
  expect(files.get(KEY)).toMatch(/^[0-9a-f-]{36}\n$/)
  expect(log.said.join('')).not.toContain(files.get(KEY)!.trim())
  // A reload's session.start now finds it accepted: no pane.
  const before = log.opens.length
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  expect(log.opens.length).toBe(before)
})

test('accept with a failed write: no file, pane closed, not reopened this session', async ($, on) => {
  const { files, log, clock } = await start($, on, { failWrite: true })
  const ui = await $.ui.mount(PANE as any)
  await ui.press({ key: 'accept' })
  await clock.settle()
  expect(files.has(ACK)).toBe(false)
  const before = log.opens.length
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  expect(log.opens.length).toBe(before)
})

test('decline: nothing written, pane closed, not reopened this session', async ($, on) => {
  const { log, clock } = await start($, on)
  const ui = await $.ui.mount(PANE as any)
  await ui.press({ key: 'decline' })
  await clock.settle()
  expect(log.writes).toEqual([])
  expect(log.closes).toContainEqual({ id: 'claudingtin-safety', origin: 'plugin' })
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  expect(log.opens.length).toBe(1)
})

test('/claudingtin safety re-opens the pane after a decline', async ($, on) => {
  const { log, clock } = await start($, on)
  const ui = await $.ui.mount(PANE as any)
  await ui.press({ key: 'decline' })
  await clock.settle()
  const out = await runCommand($, 'safety')
  expect(out?.text).toBe('the safety note is up.')
  expect(log.opens.length).toBe(2)
  expect(log.opens[1]).toMatchObject({ id: 'claudingtin-safety', focus: true })
})

test('/claudingtin safety when the pane cannot be placed: a calm line', async ($, on) => {
  await start($, on, { notPlaced: true })
  expect((await runCommand($, 'safety'))?.text).toBe("couldn't show the safety note here.")
})

test('/claudingtin foo and bare /claudingtin print usage naming safety', async ($, on) => {
  const { log } = await start($, on)
  expect((await runCommand($, 'foo'))?.text).toContain('safety')
  expect((await runCommand($, ''))?.text).toContain('safety')
  expect(log.opens.length).toBe(1)
})

test('no home: pane still offered, nothing written, no throw', async ($, on) => {
  const { log } = await start($, on, { env: {} })
  expect(log.writes).toEqual([])
  expect(log.opens.length).toBe(1)
})
