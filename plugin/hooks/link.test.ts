import { test, expect, mock } from 'claude-code/testing'
import { parseEvents } from './link'

// Story 7.4: think-time from turn events, through the plugin's own hooks with
// the world mocked beneath it (clock, env, fs, process, http, surfaces, UI).

const HOME = '/Users/a'
const DIR = `${HOME}/Library/Application Support/claudingtin`
const ACK = `${DIR}/safety-ack`
const KEY_PATH = `${DIR}/account-key`
const KEY = '0f8fad5b-d9cb-469f-a165-70867728950e'
const ACK_V1 = 'version 1\naccepted_at 1700000000000\n'
const BASE = 'http://x.test'
const HANDLE = 'h-3k9QmZ2pXv7Lw0aB'

type Req = { method: string; url: string; headers: Record<string, string>; body?: string }
type Reply = { status: number; text?: string } | Promise<{ status: number; text?: string }> | 'throw'
type Router = (r: Req, n: number) => Reply

type World = {
  files?: Record<string, string>
  env?: Record<string, string>
  unreadable?: string[]
  surfaces?: string[]
  hello?: Router
  send?: Router
  events?: Router
  status?: Router
  // Holds every key-file read until released.
  holdKey?: boolean
}

const welcome = (handle = HANDLE) => ({ status: 200, text: JSON.stringify({ handle, type: 'welcome', v: 1 }) })
const never = () => new Promise<never>(() => {})
const deferred = <T>() => {
  let resolve!: (v: T) => void
  const p = new Promise<T>((r) => (resolve = r))
  return { p, resolve }
}

const engineRender = ($: any, e: any) => {
  const { Text } = $.ui.resolve(e)
  return h(Text, { key: 'engine' }, 'engine')
}
declare const h: any

const BAND = {
  plugin: 'claudingtin',
  surface: 'terminal',
  component: 'AbovePrompt',
  props: { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 80, scroll: { top: 0, bodyRows: 9 } as any, view: {} as any },
} as const

async function start($: any, on: any, w: World = {}, opts: { mount?: boolean } = {}) {
  const clock = mock.clock(on, { now: 1700000000999 })
  mock.env(on, w.env ?? { HOME })
  const files = new Map(Object.entries(w.files ?? { [ACK]: ACK_V1, [KEY_PATH]: KEY + '\n' }))
  const unreadable = w.unreadable ?? []
  const log: Req[] = []
  const opens: any[] = []
  const counts: Record<string, number> = {}
  const keyGate = deferred<void>()
  on('fs.read', async (_$: any, e: any) => {
    if (w.holdKey && e.path === KEY_PATH) await keyGate.p
    if (unreadable.includes(e.path) || !files.has(e.path)) throw new Error('ENOENT')
    return { value: files.get(e.path) }
  })
  on('fs.exists', (_$: any, e: any) => ({
    value: files.has(e.path) || unreadable.includes(e.path) || e.path === `${HOME}/Library/Application Support`,
  }))
  on('fs.write', (_$: any, e: any) => {
    files.set(e.path, e.text)
    return { value: undefined }
  })
  on('process.run', () => ({ value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }))
  on('session.surfaces', () => ({ value: w.surfaces ?? ['terminal'] }))
  on('http.fetch', async (_$: any, e: any) => {
    const url: string = e.url
    const r: Req = { method: e.init?.method ?? 'GET', url, headers: { ...(e.init?.headers ?? {}) }, body: e.init?.body }
    const path = url.replace(BASE, '').replace(/\?.*$/, '')
    let route: Router
    if (path === '/status') route = w.status ?? (() => ({ status: 200 }))
    else {
      log.push(r)
      if (path === '/v1/hello') route = w.hello ?? (() => welcome())
      else if (path === '/v1/send') route = w.send ?? (() => ({ status: 204 }))
      else if (path === '/v1/events') route = w.events ?? (() => never())
      else route = () => ({ status: 404 })
    }
    counts[path] = (counts[path] ?? 0) + 1
    const reply = await route(r, counts[path]!)
    if (reply === 'throw') throw new Error('ECONNREFUSED')
    return { value: { status: reply.status, ok: reply.status >= 200 && reply.status < 300, headers: {}, text: reply.text ?? '' } }
  })
  on('ui.render', engineRender)
  on('ui.open', (_$: any, e: any) => {
    opens.push({ ...e })
    return { value: { isPlaced: true } }
  })
  on('ui.close', () => ({ value: undefined }))
  on('ui.panes', () => ({ value: [] }))
  on('command.register', (_$: any, e: any) => ({ value: { command: e.name } }))
  on('session.start', (_$: any, e: any) => e as any)
  on('turn.start', (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))
  await $.session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  if (opts.mount !== false) {
    await $.ui.mount(BAND as any)
    await clock.settle()
  }
  return { clock, log, opens, files, releaseKey: () => keyGate.resolve() }
}

let turnN = 0
let lastTurn = ''
async function turnStart($: any, clock: any, agentId?: string) {
  const turnId = `t${++turnN}`
  if (!agentId) lastTurn = turnId
  await $.turn.start({ text: 'hi', turnId, ...(agentId ? { agentId } : {}) })
  await clock.settle()
  return turnId
}
// Ends `turnId`, by default the last main-loop turn started.
async function turnEnd($: any, clock: any, turnId = lastTurn, agentId?: string, aborted = false) {
  const end = aborted ? { isAborted: true, reason: 'aborted' } : { isAborted: false, reason: 'answer' }
  await $.turn.complete({ answer: '', durationMs: 1, turnId, ...end, ...(agentId ? { agentId } : {}) })
  await clock.settle()
}

// What the /v1 log reads as: one short line per request.
const lines = (log: Req[]) =>
  log.map((r) => {
    const path = r.url.replace(BASE, '')
    if (path.startsWith('/v1/send')) return `send ${JSON.parse(r.body!).type}`
    if (path.startsWith('/v1/hello')) return 'hello'
    return `events ${path.replace('/v1/events', '')}`
  })

const X = { options: { backendUrl: BASE } }

// ---------------------------------------------------------------------------

test('eligible burst: hello, ready, events polls with the handle, busy; nothing before or after', X, async ($, on) => {
  const polls: Array<(v: any) => void> = []
  const { clock, log } = await start($, on, {
    events: () => new Promise((r) => polls.push(r)),
  })
  expect(log).toEqual([])
  const t = await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
  // The server holds ~20 s then answers [].
  polls[0]!({ status: 200, text: '[]' })
  await clock.advance(500)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=0'])
  await turnEnd($, clock, t)
  polls[1]!({ status: 200, text: '[]' })
  await clock.advance(60000)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=0', 'send busy'])

  // Wire format, from the /proto fixtures.
  expect(log[0]!.method).toBe('POST')
  expect(log[0]!.url).toBe(`${BASE}/v1/hello`)
  expect(JSON.parse(log[0]!.body!)).toEqual({ type: 'hello', v: 1, protocol_version: 1, account_key: KEY })
  expect(log[0]!.headers['X-Claudingtin-Handle']).toBe(undefined)
  expect(log[1]!.method).toBe('POST')
  expect(log[1]!.url).toBe(`${BASE}/v1/send`)
  expect(JSON.parse(log[1]!.body!)).toEqual({ type: 'ready', v: 1 })
  for (const r of log.slice(1)) expect(r.headers['X-Claudingtin-Handle']).toBe(HANDLE)
  expect(log[2]!.method).toBe('GET')
  expect(JSON.parse(log[4]!.body!)).toEqual({ type: 'busy', v: 1 })

  // The key rides the hello body only; the handle only its header.
  const rest = JSON.stringify([{ ...log[0]!, body: undefined }, ...log.slice(1)])
  expect(rest).not.toContain(KEY)
  for (const r of log) {
    expect(r.url).not.toContain(HANDLE)
    expect(r.body ?? '').not.toContain(HANDLE)
  }
})

test('hung backend: turn.start and turn.complete hooks settle without any fetch landing', X, async ($, on) => {
  const { clock, log } = await start($, on, { hello: never, send: never, events: never, status: never })
  const t = await turnStart($, clock)
  await turnEnd($, clock, t)
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello'])
  // Abandoned at 5 s. The turn.start that came meanwhile asks once more (a
  // second hello, itself abandoned); never a ready, the hello never landed.
  await clock.advance(5000)
  expect(lines(log)).toEqual(['hello', 'hello'])
  await clock.advance(60000)
  expect(lines(log)).toEqual(['hello', 'hello'])
})

test('second burst reuses the handle: no hello', X, async ($, on) => {
  const { clock, log } = await start($, on)
  const t = await turnStart($, clock)
  await turnEnd($, clock, t)
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy', 'send ready'])
})

test('fast burst: complete before the hello goes out sends nothing', X, async ($, on) => {
  const { clock, log, releaseKey } = await start($, on, { holdKey: true })
  const t = await turnStart($, clock)
  await turnEnd($, clock, t)
  releaseKey()
  await clock.advance(60000)
  expect(log).toEqual([])
})

test('subagent turns and a second main turn.start send nothing more', X, async ($, on) => {
  const { clock, log } = await start($, on)
  await turnStart($, clock, 'agent-1')
  expect(log).toEqual([])
  await turnEnd($, clock, 'ta', 'agent-1')
  expect(log).toEqual([])
  await turnStart($, clock)
  await turnStart($, clock, 'agent-2')
  await turnEnd($, clock, 'tb', 'agent-2')
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
})

for (const [name, w, mount] of [
  ['gate not accepted', { files: { [KEY_PATH]: KEY + '\n' } }, true],
  ['no key (unreadable)', { unreadable: [KEY_PATH] }, true],
  ['no surfaces (-p, VS Code)', { surfaces: [] }, true],
  ['band never rendered', {}, false],
] as const) {
  test(`not eligible (${name}): no hello, no ready, no poll`, X, async ($, on) => {
    const { clock, log } = await start($, on, w as World, { mount })
    const t = await turnStart($, clock)
    await clock.advance(30000)
    await turnEnd($, clock, t)
    await turnStart($, clock)
    await clock.advance(30000)
    expect(log).toEqual([])
  })
}

test('hot reload: session.start with a handle and sent=ready resumes the poll, no hello', X, async ($, on) => {
  const { clock, log } = await start($, on)
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=0'])
  expect(log[3]!.headers['X-Claudingtin-Handle']).toBe(HANDLE)
})

test('events: after becomes the max seq; seq ≤ after is ignored', X, async ($, on) => {
  const replies = [
    { status: 200, text: JSON.stringify([{ seq: 1, type: 'queued', v: 1 }, { seq: 2, type: 'queued', v: 1 }]) },
    { status: 200, text: JSON.stringify([{ seq: 3, type: 'queued', v: 1 }, { seq: 2, type: 'queued', v: 1 }]) },
    { status: 200, text: JSON.stringify([{ seq: 1, type: 'queued', v: 1 }]) },
  ]
  const { clock, log } = await start($, on, { events: (_r, n) => replies[n - 1] ?? never() })
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=2', 'events ?after=3'])
  // The third answer held only seq 1 (≤ after): after stays 3, and it is paced.
  await clock.advance(500)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=2', 'events ?after=3', 'events ?after=3'])
})

test('events: 400 (after beyond the last event) resets after to 0, then backs off', X, async ($, on) => {
  const { clock, log } = await start($, on, {
    events: (_r, n) => (n === 1 ? { status: 200, text: '[{"seq":2,"type":"queued","v":1}]' } : n === 2 ? { status: 400 } : never()),
  })
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=2'])
  await clock.advance(999)
  expect(lines(log).length).toBe(4)
  await clock.advance(1)
  expect(lines(log).at(-1)).toBe('events ?after=0')
})

test('events: an empty [] answered at once is paced at 500 ms', X, async ($, on) => {
  const { clock, log } = await start($, on, { events: () => ({ status: 200, text: '[]' }) })
  await turnStart($, clock)
  const polls = () => lines(log).filter((l) => l.startsWith('events')).length
  expect(polls()).toBe(1)
  await clock.advance(499)
  expect(polls()).toBe(1)
  await clock.advance(1)
  expect(polls()).toBe(2)
  await turnEnd($, clock)
  await clock.advance(5000)
  expect(polls()).toBe(2)
})

test('events: only already-seen seqs, answered at once, are paced at 500 ms', X, async ($, on) => {
  const { clock, log } = await start($, on, {
    events: (_r, n) => ({ status: 200, text: n === 1 ? '[{"seq":2,"type":"queued","v":1}]' : '[{"seq":1,"type":"queued","v":1}]' }),
  })
  await turnStart($, clock)
  const polls = () => lines(log).filter((l) => l.startsWith('events')).length
  // The first answer is new (seq 2): polled again at once; the second is stale.
  expect(polls()).toBe(2)
  await clock.advance(499)
  expect(polls()).toBe(2)
  await clock.advance(1)
  expect(polls()).toBe(3)
  await clock.advance(500)
  expect(polls()).toBe(4)
  expect(lines(log).at(-1)).toBe('events ?after=2')
  await turnEnd($, clock)
})

test('events: bad JSON backs off and retries', X, async ($, on) => {
  const { clock, log } = await start($, on, { events: (_r, n) => (n === 1 ? { status: 200, text: '{nope' } : never()) })
  await turnStart($, clock)
  expect(lines(log).length).toBe(3)
  await clock.advance(999)
  expect(lines(log).length).toBe(3)
  await clock.advance(1)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=0'])
})

test('stale handle: ready → 410 drops it, one hello, ready again', X, async ($, on) => {
  let h = 0
  const { clock, log } = await start($, on, {
    hello: () => welcome(`h-${++h}`),
    send: (r, n) => (n === 1 ? { status: 410 } : { status: 204 }),
  })
  // One burst: the hello's h-1 is stale at once (410), so one more hello mints h-2.
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'hello', 'send ready', 'events ?after=0'])
  expect(log[1]!.headers['X-Claudingtin-Handle']).toBe('h-1')
  expect(log[3]!.headers['X-Claudingtin-Handle']).toBe('h-2')
  expect(log[4]!.headers['X-Claudingtin-Handle']).toBe('h-2')
})

test('stale handle twice: second 410 goes quiet until the next turn.start', X, async ($, on) => {
  const { clock, log } = await start($, on, { send: (r) => (JSON.parse(r.body!).type === 'ready' ? { status: 410 } : { status: 204 }) })
  const t = await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'hello', 'send ready'])
  await clock.advance(30000)
  await turnEnd($, clock, t)
  expect(lines(log).length).toBe(4)
  await turnStart($, clock)
  expect(lines(log).slice(4)).toEqual(['hello', 'send ready', 'hello', 'send ready'])
})

test('superseded: events → 410 goes quiet; no hello until the next main turn.start', X, async ($, on) => {
  let h = 0
  const { clock, log } = await start($, on, {
    hello: () => welcome(`h-${++h}`),
    events: (_r, n) => (n === 1 ? { status: 410 } : never()),
  })
  const t = await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
  await clock.advance(60000)
  await turnStart($, clock, 'agent-1')
  await turnEnd($, clock, t)
  expect(lines(log).length).toBe(3)
  await turnStart($, clock)
  expect(lines(log).slice(3)).toEqual(['hello', 'send ready', 'events ?after=0'])
  expect(log[5]!.headers['X-Claudingtin-Handle']).toBe('h-2')
})

test('superseded mid-burst: a session.start (reload) stays quiet, no hello', X, async ($, on) => {
  const { clock, log } = await start($, on, { events: (_r, n) => (n === 1 ? { status: 410 } : never()) })
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.advance(60000)
  expect(lines(log).length).toBe(3)
})

test('outdated: please_update stops hellos for the session', X, async ($, on) => {
  const { clock, log } = await start($, on, { hello: () => ({ status: 200, text: '{"type":"please_update","v":1}' }) })
  const t = await turnStart($, clock)
  await turnEnd($, clock, t)
  await turnStart($, clock)
  await clock.advance(30000)
  expect(lines(log)).toEqual(['hello'])
})

test('hello fails: nothing more; retried at the next turn.start', X, async ($, on) => {
  let fail = true
  const { clock, log } = await start($, on, { hello: () => (fail ? 'throw' : welcome()) })
  const t = await turnStart($, clock)
  await clock.advance(30000)
  await turnEnd($, clock, t)
  expect(lines(log)).toEqual(['hello'])
  fail = false
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'hello', 'send ready', 'events ?after=0'])
})

test('hello timeout: abandoned at 5 s, nothing sent', X, async ($, on) => {
  const { clock, log } = await start($, on, { hello: never })
  await turnStart($, clock)
  await clock.advance(4999)
  expect(lines(log)).toEqual(['hello'])
  await clock.advance(1)
  // Abandoned: no ready follows, and nothing is retried until the next turn.start.
  await clock.advance(30000)
  expect(lines(log)).toEqual(['hello'])
  await turnEnd($, clock)
  expect(lines(log)).toEqual(['hello'])
})

test('want re-checked after hello: a burst that ended while the hello was out sends no ready, no busy', X, async ($, on) => {
  const d = deferred<{ status: number; text?: string }>()
  const { clock, log } = await start($, on, { hello: () => d.p })
  await turnStart($, clock)
  await turnEnd($, clock)
  d.resolve(welcome())
  await clock.settle()
  await clock.advance(60000)
  expect(lines(log)).toEqual(['hello'])
})

test('busy → 410 drops the handle: the next burst says hello again, on the new handle', X, async ($, on) => {
  let h = 0
  const { clock, log } = await start($, on, {
    hello: () => welcome(`h-${++h}`),
    send: (r) => (JSON.parse(r.body!).type === 'busy' ? { status: 410 } : { status: 204 }),
  })
  await turnStart($, clock)
  await turnEnd($, clock)
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy', 'hello', 'send ready', 'events ?after=0'])
  expect(log[5]!.headers['X-Claudingtin-Handle']).toBe('h-2')
})

test('ready timed out: the turn end still sends busy (the server may have queued it)', X, async ($, on) => {
  const { clock, log } = await start($, on, { send: (r) => (JSON.parse(r.body!).type === 'ready' ? never() : { status: 204 }) })
  await turnStart($, clock)
  await clock.advance(5000)
  expect(lines(log)).toEqual(['hello', 'send ready'])
  await turnEnd($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'send busy'])
  expect(log[2]!.headers['X-Claudingtin-Handle']).toBe(HANDLE)
})

test('an aborted turn still sends busy', X, async ($, on) => {
  const { clock, log } = await start($, on)
  await turnStart($, clock)
  await turnEnd($, clock, undefined, undefined, true)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy'])
})

test('a supersede seen while eligibility is checked: no ready on the dropped handle, no hello', X, async ($, on) => {
  const w: World = {}
  const poll = deferred<{ status: number; text?: string }>()
  w.events = (_r, n) => (n === 1 ? poll.p : never())
  const { clock, log, releaseKey } = await start($, on, w)
  await turnStart($, clock)
  await turnEnd($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy'])
  // The next burst's worker waits on the key read while the old poll answers 410.
  w.holdKey = true
  await turnStart($, clock)
  poll.resolve({ status: 410 })
  await clock.settle()
  releaseKey()
  await clock.settle()
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy'])
})

test('a key read that never settles is abandoned at 5 s: the worker is free for the turn end', X, async ($, on) => {
  // The first ready times out client-side (sent unknown); a continuation turn's
  // worker then waits on a key read that never settles. Abandoned at 5 s, the
  // worker still sends the busy the turn end asks for.
  const w: World = { send: (r) => (JSON.parse(r.body!).type === 'ready' ? never() : { status: 204 }) }
  const { clock, log } = await start($, on, w)
  await turnStart($, clock)
  await clock.advance(5000)
  expect(lines(log)).toEqual(['hello', 'send ready'])
  w.holdKey = true
  await turnStart($, clock)
  await clock.advance(5000)
  await turnEnd($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'send busy'])
})

test('busy send fails: dropped silently, nothing retried', X, async ($, on) => {
  const { clock, log } = await start($, on, { send: (r) => (JSON.parse(r.body!).type === 'busy' ? 'throw' : { status: 204 }) })
  const t = await turnStart($, clock)
  await turnEnd($, clock, t)
  await clock.advance(60000)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'send busy'])
})

for (const [name, reply] of [
  ['network error', 'throw'],
  ['503', { status: 503 }],
  ['404', { status: 404 }],
] as const) {
  test(`poll error (${name}): backoff 1s → 2s → … → 15s cap while the burst lasts`, X, async ($, on) => {
    const { clock, log } = await start($, on, { events: () => reply as Reply })
    const t = await turnStart($, clock)
    const polls = () => lines(log).filter((l) => l.startsWith('events')).length
    expect(polls()).toBe(1)
    for (const [wait, total] of [[1000, 2], [2000, 3], [4000, 4], [8000, 5], [15000, 6], [15000, 7]] as const) {
      await clock.advance(wait - 1)
      expect(polls()).toBe(total - 1)
      await clock.advance(1)
      expect(polls()).toBe(total)
    }
    await turnEnd($, clock, t)
    await clock.advance(60000)
    expect(polls()).toBe(7)
    expect(lines(log).at(-1)).toBe('send busy')
  })
}

test('poll timeout: a hung events poll is abandoned at 25 s and polled again', X, async ($, on) => {
  const { clock, log } = await start($, on)
  await turnStart($, clock)
  await clock.advance(24999)
  expect(lines(log).length).toBe(3)
  await clock.advance(1)
  // Abandoned counts as an error: the backoff's first step.
  await clock.advance(999)
  expect(lines(log).length).toBe(3)
  await clock.advance(1)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0', 'events ?after=0'])
})

test('band and gate stay green: the band still draws and /status still polls', X, async ($, on) => {
  const { clock, log } = await start($, on)
  const ui = await ($ as any).ui.mount(BAND as any)
  expect((await ui.find({ type: 'Text', text: /claudingtin/ }))?.text).toBe('claudingtin · connected')
  await turnStart($, clock)
  expect(lines(log)).toEqual(['hello', 'send ready', 'events ?after=0'])
  expect((await ui.find({ type: 'Text', text: /claudingtin/ }))?.text).toBe('claudingtin · connected')
})

test('parseEvents: bare arrays only, numeric positive seqs, raw fields kept', async () => {
  expect(parseEvents('[]')).toEqual([])
  expect(parseEvents('[{"seq":3,"type":"queued","v":1},{"seq":2},{"type":"x"},{"seq":"4"},null,[1]]')).toEqual([
    { seq: 3, type: 'queued', v: 1 },
    { seq: 2, type: '' },
  ])
  expect(parseEvents('[{"seq":1.5},{"seq":0},{"seq":-1}]')).toEqual([])
  expect(parseEvents('{"seq":1}')).toBe(undefined)
  expect(parseEvents('nope')).toBe(undefined)
})
