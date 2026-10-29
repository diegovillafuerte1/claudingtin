import { test, expect, mock } from 'claude-code/testing'
import {
  applyEvents,
  capText,
  chatBand,
  clearBack,
  inert,
  markKept,
  onChatClose,
  onModelBack,
  oneLine,
  resetChat,
  submitLine,
} from './chat'

// Story 7.5: the chat surface, through the plugin's own hooks with the world
// mocked beneath it. The harness is link.test.ts's `start`, plus a pane log
// and an events feed the test answers poll by poll.

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
  send?: Router
  // Each events poll waits until the test feeds it; `events` overrides.
  events?: Router
  // What each $.ui.open answers, by call number (1-based); default placed.
  open?: (n: number) => { isPlaced: true } | { isPlaced: false; reason: string } | 'throw'
}

const welcome = (handle = HANDLE) => ({ status: 200, text: JSON.stringify({ handle, type: 'welcome', v: 1 }) })
const never = () => new Promise<never>(() => {})

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

const PANE = {
  plugin: 'claudingtin',
  surface: 'terminal',
  component: 'Pane',
  requestId: 'claudingtin-chat',
  props: { title: 'claudingtin', isFocused: true, bodyColumns: 80, placement: 'inline', scroll: { offset: 0, bodyRows: 30 } as any, view: {} as any },
} as const

async function start($: any, on: any, w: World = {}) {
  mountedPane = null
  const clock = mock.clock(on, { now: 1700000000999 })
  mock.env(on, { HOME })
  const files = new Map(Object.entries({ [ACK]: ACK_V1, [KEY_PATH]: KEY + '\n' }))
  const log: Req[] = []
  const opens: any[] = []
  const closes: any[] = []
  const said: string[] = []
  const counts: Record<string, number> = {}
  const polls: Array<(v: { status: number; text?: string }) => void> = []
  on('fs.read', async (_$: any, e: any) => {
    if (!files.has(e.path)) throw new Error('ENOENT')
    return { value: files.get(e.path) }
  })
  on('fs.exists', (_$: any, e: any) => ({ value: files.has(e.path) || e.path === `${HOME}/Library/Application Support` }))
  on('fs.write', (_$: any, e: any) => {
    files.set(e.path, e.text)
    return { value: undefined }
  })
  on('process.run', () => ({ value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }))
  on('session.surfaces', () => ({ value: ['terminal'] }))
  on('http.fetch', async (_$: any, e: any) => {
    const url: string = e.url
    const r: Req = { method: e.init?.method ?? 'GET', url, headers: { ...(e.init?.headers ?? {}) }, body: e.init?.body }
    const path = url.replace(BASE, '').replace(/\?.*$/, '')
    let route: Router
    if (path === '/status') route = () => ({ status: 200 })
    else {
      log.push(r)
      if (path === '/v1/hello') route = () => welcome()
      else if (path === '/v1/send') route = w.send ?? (() => ({ status: 204 }))
      else if (path === '/v1/events') route = w.events ?? (() => new Promise((res) => polls.push(res)))
      else route = () => ({ status: 404 })
    }
    counts[path] = (counts[path] ?? 0) + 1
    const reply = await route(r, counts[path]!)
    if (reply === 'throw') throw new Error('ECONNREFUSED')
    return { value: { status: reply.status, ok: reply.status >= 200 && reply.status < 300, headers: {}, text: reply.text ?? '' } }
  })
  on('ui.render', engineRender)
  let openN = 0
  on('ui.open', (_$: any, e: any) => {
    opens.push({ ...e })
    const a = w.open?.(++openN) ?? { isPlaced: true }
    if (a === 'throw') throw new Error('no surface')
    return { value: a }
  })
  on('ui.close', (_$: any, e: any) => {
    closes.push({ id: e.id, origin: e.origin?.kind })
    return { value: undefined }
  })
  on('ui.panes', () => ({ value: [] }))
  for (const ev of ['ui.log', 'ui.status', 'ui.toast', 'ui.notice']) {
    on(ev, (_$: any, e: any) => {
      said.push(JSON.stringify(e))
      return { value: undefined }
    })
  }
  on('command.register', (_$: any, e: any) => ({ value: { command: e.name } }))
  on('session.start', (_$: any, e: any) => e as any)
  on('turn.start', (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))
  await $.session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  await $.ui.mount(BAND as any)
  await clock.settle()

  // Answers the oldest waiting events poll with these events.
  const feed = async (events: unknown[]) => {
    const res = polls.shift()
    if (!res) throw new Error('no events poll waiting')
    res({ status: 200, text: JSON.stringify(events) })
    await clock.settle()
  }
  const failPoll = async (reply: { status: number; text?: string }) => {
    const res = polls.shift()
    if (!res) throw new Error('no events poll waiting')
    res(reply)
    await clock.settle()
  }
  return { clock, log, opens, closes, said, feed, failPoll }
}

let turnN = 0
let lastTurn = ''
async function turnStart($: any, clock: any) {
  const turnId = `t${++turnN}`
  lastTurn = turnId
  await $.turn.start({ text: 'hi', turnId })
  await clock.settle()
}
async function turnEnd($: any, clock: any) {
  await $.turn.complete({ answer: '', durationMs: 1, turnId: lastTurn, isAborted: false, reason: 'answer' })
  await clock.settle()
}

const lines = (log: Req[]) =>
  log.map((r) => {
    const path = r.url.replace(BASE, '')
    if (path.startsWith('/v1/send')) return `send ${JSON.parse(r.body!).type}`
    if (path.startsWith('/v1/hello')) return 'hello'
    return `events ${path.replace('/v1/events', '')}`
  })
const sends = (log: Req[]) => log.filter((r) => r.url.endsWith('/v1/send')).map((r) => JSON.parse(r.body!))

async function bandText($: any): Promise<string | undefined> {
  const ui = await $.ui.mount(BAND as any)
  return (await ui.find({ type: 'Text' }))?.text
}
// One Pane instance per test: mounted once, then drawn again with these props.
let mountedPane: any = null
async function pane($: any, bodyRows = 30) {
  const props = { ...PANE.props, scroll: { offset: 0, bodyRows } }
  if (mountedPane) await mountedPane.redraw(props)
  else mountedPane = await $.ui.mount({ ...PANE, props } as any)
  return mountedPane
}
const texts = async (ui: any) => (await ui.findAll({ type: 'Text' })).map((t: any) => t.text)

const runCommand = ($: any, args: string) =>
  $.command.run({ command: 'claudingtin', args, origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 80 } })

const MATCHED = { seq: 2, type: 'matched', v: 1, session_id: 's-1', pseudonym: 'Ana', blurb: 'likes owls', opener: 'what are you building?' }

// Into a burst, queued, then matched (flourish played out).
async function matched($: any, on: any, w: World = {}) {
  const s = await start($, on, w)
  await turnStart($, s.clock)
  await s.feed([{ seq: 1, type: 'queued', v: 1 }])
  await s.feed([MATCHED])
  await s.clock.advance(600)
  return s
}

const X = { options: { backendUrl: BASE } }
const OFF = { options: { backendUrl: BASE, persist: false } }

// ---------------------------------------------------------------------------
// Text safety.

test('inert: keeps \\n and ZWJ; drops C0, DEL, C1, format, separators; lone surrogate -> U+FFFD', async () => {
  expect(inert('a\nb')).toBe('a\nb')
  expect(inert('\u{1f468}\u200d\u{1f469}')).toBe('\u{1f468}\u200d\u{1f469}')
  expect(inert('a\x1b[31mb\x07c\x7fd\x85e\x00f\rg\th')).toBe('a[31mbcdefgh')
  expect(inert('x\u202ey\u200bz\u2028w\u2029v\ue000u\ufeff')).toBe('xyzwvu')
  expect(inert('a\ud800b')).toBe('a\ufffdb')
  expect(inert('a\udc00')).toBe('a\ufffd')
  expect(inert('nb\u00a0sp \u00e9 \u{1f600}')).toBe('nb\u00a0sp \u00e9 \u{1f600}')
  expect(inert('\x1b]8;;http://x\x07[a](b)<i>')).toBe(']8;;http://x[a](b)<i>')
  expect(inert(42)).toBe('')
  expect(oneLine(' Ana\nthe\r\nowl ')).toBe('Ana the owl')
})

test('capText: 2000 code points', async () => {
  expect(capText('a'.repeat(2500)).length).toBe(2000)
  expect(Array.from(capText('\u{1f600}'.repeat(2100))).length).toBe(2000)
  expect(capText('hi')).toBe('hi')
})

// ---------------------------------------------------------------------------
// The matrix.

test('queued: band shows the animated spinner, no position, ETA or count', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  expect(await bandText($)).toBe('claudingtin · connected')
  await feed([{ seq: 1, type: 'queued', v: 1 }])
  const a = await bandText($)
  expect(a).toMatch(/^. looking for someone$/)
  await clock.advance(100)
  const b = await bandText($)
  expect(b).toMatch(/^. looking for someone$/)
  expect(b).not.toBe(a)
  expect(b).not.toMatch(/\d/)
})

test('matched: flourish in the band, then the Pane opens focused with header, blurb, opener, buttons', X, async ($, on) => {
  const { clock, feed, opens } = await start($, on)
  await turnStart($, clock)
  await feed([{ seq: 1, type: 'queued', v: 1 }])
  await feed([MATCHED])
  expect(opens).toContainEqual({ id: 'claudingtin-chat', title: 'claudingtin', focus: true, rows: 16 })
  expect(await bandText($)).toBe('◐  here we go')
  await clock.advance(90)
  expect(await bandText($)).toBe('◓  here we go')
  await clock.advance(450)
  // Six frames at 90 ms, then the status line (placed: no hint).
  expect(await bandText($)).toBe('claudingtin · connected')
  const ui = await pane($)
  const t = await texts(ui)
  expect(t).toContain("you're chatting with Ana")
  expect(t).toContain('likes owls')
  expect(t).toContain('what are you building?')
  for (const key of ['leave', 'block', 'report']) expect(await ui.find({ type: 'Button', key })).toBeTruthy()
  expect((await ui.find({ key: 'leave' }))?.props).toMatchObject({ label: "my claude's back" })
  expect((await ui.find({ type: 'Input' }))?.props).toMatchObject({ placeholder: 'say something' })
})

test('matched without a pseudonym: header says someone new', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([{ ...MATCHED, pseudonym: '\x1b ', blurb: '' }])
  const t = await texts(await pane($))
  expect(t).toContain("you're chatting with someone new")
})

for (const [name, open] of [
  ['not placed', () => ({ isPlaced: false, reason: 'narrow' })],
  ['open throws', () => 'throw'],
] as const) {
  test(`narrow (${name}): band hints /claudingtin chat, which opens it`, X, async ($, on) => {
    let n = 0
    const { clock, opens } = await matched($, on, { open: () => (++n === 1 ? (open() as any) : { isPlaced: true }) })
    expect(await bandText($)).toBe('claudingtin · chatting with Ana — /claudingtin chat')
    const out = await runCommand($, 'chat')
    expect(out?.text).toBe(undefined)
    expect(opens.length).toBe(2)
    expect(opens[1]).toMatchObject({ id: 'claudingtin-chat', focus: true })
    await clock.settle()
    expect(await bandText($)).toBe('claudingtin · connected')
  })
}

test('/claudingtin chat with no chat: a calm line', X, async ($, on) => {
  await start($, on)
  expect((await runCommand($, 'chat'))?.text).toBe('no chat going right now.')
})

test('peer line: appended while chatting; one before the match is dropped; unknown types ignored', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([{ seq: 1, type: 'chat_msg', v: 1, client_msg_id: 'x', text: 'too early' }, { seq: 2, type: 'blocklist', v: 1 }])
  await feed([{ ...MATCHED, seq: 3 }])
  await feed([{ seq: 4, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'hello there' }])
  const t = await texts(await pane($))
  expect(t).toContain('them  hello there')
  expect(t.join('\n')).not.toContain('too early')
})

test('events apply in ascending seq order (a batch out of order)', X, async ($, on) => {
  const { clock, feed, log } = await start($, on)
  await turnStart($, clock)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'after the match' }, MATCHED])
  expect(await texts(await pane($))).toContain('them  after the match')
  expect(lines(log).at(-1)).toBe('events ?after=3')
})

test('send: trimmed self line at once, then POST /v1/send chat_msg', X, async ($, on) => {
  const { log } = await matched($, on)
  const ui = await pane($)
  await ui.input({ key: 'claudingtin-input', text: '  hi ' })
  expect(await texts(await pane($))).toContain('you  hi')
  const s = sends(log).filter((m) => m.type === 'chat_msg')
  expect(s).toEqual([{ type: 'chat_msg', v: 1, client_msg_id: expect.any(String), text: 'hi' }])
  expect(s[0].client_msg_id).toMatch(/^[0-9a-f-]{36}$/)
  const sent = log.filter((r) => r.url.endsWith('/v1/send')).at(-1)!
  expect(sent.headers['X-Claudingtin-Handle']).toBe(HANDLE)
  // Each line its own id.
  await ui.input({ key: 'claudingtin-input', text: 'again' })
  const ids = sends(log).filter((m) => m.type === 'chat_msg').map((m) => m.client_msg_id)
  expect(new Set(ids).size).toBe(2)
})

test('send: empty after trim sends nothing', X, async ($, on) => {
  const { log } = await matched($, on)
  const before = log.length
  await (await pane($)).input({ key: 'claudingtin-input', text: '   \n ' })
  expect(log.length).toBe(before)
})

test('send: capped at 2000 code points on change and on submit', X, async ($, on) => {
  const { log } = await matched($, on)
  const ui = await pane($)
  const r = await ui.input({ key: 'claudingtin-input', text: 'a'.repeat(2500), kind: 'change' })
  expect(r?.value.length).toBe(2000)
  await ui.input({ key: 'claudingtin-input', text: '\u{1f600}'.repeat(2100) })
  const m = sends(log).find((x) => x.type === 'chat_msg')!
  expect(Array.from(m.text as string).length).toBe(2000)
})

test('send fails: the line stays, nothing shown', X, async ($, on) => {
  const { log, said } = await matched($, on, { send: (r) => (JSON.parse(r.body!).type === 'chat_msg' ? 'throw' : { status: 204 }) })
  await (await pane($)).input({ key: 'claudingtin-input', text: 'still here' })
  expect(await texts(await pane($))).toContain('you  still here')
  expect(sends(log).some((m) => m.type === 'chat_msg')).toBe(true)
  expect(said).toEqual([])
})

test('peer ended mid-burst: ended line only in the Pane, band back to searching', X, async ($, on) => {
  const { feed, closes } = await matched($, on)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'bye' }])
  await feed([{ seq: 4, type: 'session_ended', v: 1 }])
  const ui = await pane($)
  expect(await texts(ui)).toEqual(["looks like their claude's back — catch you later"])
  expect(await ui.findAll({ type: 'Button' })).toEqual([])
  expect(await bandText($)).toMatch(/^. looking for someone$/)
  expect(closes.filter((c) => c.id === 'claudingtin-chat')).toEqual([])
  // Nothing to leave now.
  expect((await runCommand($, 'leave'))?.text).toBe('no chat going right now.')
})

test('session_ended while not chatting is ignored', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([{ seq: 1, type: 'queued', v: 1 }, { seq: 2, type: 'session_ended', v: 1 }])
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toMatch(/looking for someone/)
})

test('leave (button): exactly one leave, chat cleared, Pane closed, searching while the burst lasts', X, async ($, on) => {
  const { log, closes, clock } = await matched($, on)
  const ui = await pane($)
  await ui.press({ key: 'leave' })
  await clock.settle()
  expect(sends(log).filter((m) => m.type === 'leave')).toEqual([{ type: 'leave', v: 1 }])
  expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toMatch(/^. looking for someone$/)
  // Again: nothing more is sent.
  expect((await runCommand($, 'leave'))?.text).toBe('no chat going right now.')
  expect(sends(log).filter((m) => m.type === 'leave').length).toBe(1)
})

test('leave (command) while chatting: one leave, no line', X, async ($, on) => {
  const { log } = await matched($, on)
  expect((await runCommand($, 'leave'))?.text).toBe(undefined)
  expect(sends(log).filter((m) => m.type === 'leave').length).toBe(1)
})

test('leave with no chat: a calm line, nothing sent', X, async ($, on) => {
  const { log } = await start($, on)
  expect((await runCommand($, 'leave'))?.text).toBe('no chat going right now.')
  expect((await runCommand($, 'block'))?.text).toBe('no chat going right now.')
  expect((await runCommand($, 'report'))?.text).toBe('no chat going right now.')
  expect(log).toEqual([])
})

for (const [kind, via] of [
  ['block', 'button'],
  ['block', 'command'],
  ['report', 'button'],
  ['report', 'command'],
] as const) {
  test(`${kind} (${via}): ${kind} once, then exactly one leave`, X, async ($, on) => {
    const { log, closes, clock } = await matched($, on)
    if (via === 'button') await (await pane($)).press({ key: kind })
    else await runCommand($, kind)
    await clock.settle()
    const s = sends(log).filter((m) => m.type !== 'ready')
    expect(s).toEqual([kind === 'block' ? { type: 'block', v: 1 } : { type: 'report', v: 1, last_n: 5 }, { type: 'leave', v: 1 }])
    expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  })
}

test('own Claude back, persist off: leave then busy, chat reset, Pane closed', OFF, async ($, on) => {
  const { clock, log, closes } = await matched($, on)
  await turnEnd($, clock)
  expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  expect(lines(log).slice(-2)).toEqual(['send leave', 'send busy'])
  expect(sends(log).filter((m) => m.type === 'busy').length).toBe(1)
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toBe('claudingtin · connected')
})

test('handle lost (events 410): chat reset, Pane closed', X, async ($, on) => {
  const { failPoll, closes } = await matched($, on)
  await failPoll({ status: 410 })
  expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toBe('claudingtin · connected')
})

test('poll gap: an error and its backoff change nothing visible', X, async ($, on) => {
  const { failPoll, clock, closes } = await matched($, on)
  const before = await texts(await pane($))
  const band = await bandText($)
  await failPoll({ status: 503 })
  await clock.advance(500)
  expect(await texts(await pane($))).toEqual(before)
  expect(await bandText($)).toBe(band)
  expect(closes).toEqual([])
})

test('hot reload while searching: the spinner ticks again', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([{ seq: 1, type: 'queued', v: 1 }])
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  const a = await bandText($)
  expect(a).toMatch(/looking for someone/)
  await clock.advance(100)
  const b = await bandText($)
  expect(b).toMatch(/looking for someone/)
  expect(b).not.toBe(a)
})

test('hot reload while chatting: the Pane redraws from state', X, async ($, on) => {
  const { feed, clock } = await matched($, on)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'still here?' }])
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  const t = await texts(await pane($))
  expect(t).toContain("you're chatting with Ana")
  expect(t).toContain('them  still here?')
})

test('hostile text: drawn as Text with ESC/BEL removed, markup literal; no Markdown, Link or Code', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([
    { ...MATCHED, pseudonym: '\x1b[31mEve\x07\n**x**', blurb: '<b>hi</b>\n\x9bsecond', opener: '[o](http://o)' },
  ])
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: '\x1b]8;;http://x\x07[a](b)<i>' }])
  const ui = await pane($)
  const t = await texts(ui)
  expect(t).toContain('them  ]8;;http://x[a](b)<i>')
  expect(t).toContain("you're chatting with [31mEve **x**")
  expect(t).toContain('<b>hi</b> second')
  expect(t).toContain('[o](http://o)')
  for (const type of ['Markdown', 'Link', 'Code']) expect(await ui.findAll({ type })).toEqual([])
  expect(JSON.stringify(await ui.drawn())).not.toMatch(/\\u001b|\\u0007/)
})

test('buttons in every chatting render; history keeps the newest 200 lines', X, async ($, on) => {
  const { feed } = await matched($, on)
  const buttons = async (ui: any) => (await ui.findAll({ type: 'Button' })).map((b: any) => b.key)
  expect(await buttons(await pane($))).toEqual(['leave', 'block', 'report'])
  const batch = Array.from({ length: 205 }, (_, i) => ({ seq: 3 + i, type: 'chat_msg', v: 1, client_msg_id: `p${i}`, text: `m${i}` }))
  await feed(batch)
  let ui = await pane($, 1000)
  expect(await buttons(ui)).toEqual(['leave', 'block', 'report'])
  const peer = (await texts(ui)).filter((x: string) => x.startsWith('them  '))
  expect(peer.length).toBe(200)
  expect(peer[0]).toBe('them  m5')
  expect(peer.at(-1)).toBe('them  m204')
  await ui.input({ key: 'claudingtin-input', text: 'mine' })
  // A short pane draws only the newest lines that fit; header and buttons stay.
  ui = await pane($, 8)
  expect(await buttons(ui)).toEqual(['leave', 'block', 'report'])
  const t = await texts(ui)
  expect(t).toContain("you're chatting with Ana")
  expect(t.at(-1)).toBe('you  mine')
  expect(t.length).toBeLessThan(10)
})

test('hung backend: submit and leave resolve without waiting on the fetch', X, async ($, on) => {
  const { log, clock } = await matched($, on, { send: (r) => (JSON.parse(r.body!).type === 'ready' ? { status: 204 } : never()) })
  const ui = await pane($)
  await ui.input({ key: 'claudingtin-input', text: 'anyone?' })
  expect(await texts(await pane($))).toContain('you  anyone?')
  await ui.press({ key: 'leave' })
  expect(sends(log).map((m) => m.type)).toEqual(['ready', 'chat_msg', 'leave'])
  await clock.advance(60000)
  expect(sends(log).filter((m) => m.type === 'leave').length).toBe(1)
})

test('chat text travels only in a chat_msg body; peer text never leaves', X, async ($, on) => {
  const { log, feed, said, clock } = await matched($, on)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'zebra-peer' }])
  await (await pane($)).input({ key: 'claudingtin-input', text: 'zebra-self' })
  await runCommand($, 'report')
  await turnEnd($, clock)
  for (const r of log) {
    const isChat = r.body !== undefined && JSON.parse(r.body).type === 'chat_msg'
    const all = JSON.stringify(r)
    expect(all).not.toContain('zebra-peer')
    if (!isChat) expect(all).not.toContain('zebra-self')
  }
  expect(said.join('')).not.toMatch(/zebra|owls|Ana/)
})

// ---------------------------------------------------------------------------
// Review patches.

test('matched: an open that never settles does not stall polling; the next chat_msg applies', X, async ($, on) => {
  const { clock, feed, opens } = await start($, on, { open: () => never() as any })
  await turnStart($, clock)
  await feed([MATCHED])
  expect(opens.length).toBe(1)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'are you there' }])
  expect(await texts(await pane($))).toContain('them  are you there')
  // Never placed: the band offers the chat once the flourish is done.
  await clock.advance(600)
  expect(await bandText($)).toBe('claudingtin · chatting with Ana — /claudingtin chat')
})

test('peer ids never key the drawing: a repeated client_msg_id, one equal to a self id', X, async ($, on) => {
  const { feed, log } = await matched($, on)
  await (await pane($)).input({ key: 'claudingtin-input', text: 'mine' })
  const selfId = sends(log).find((m) => m.type === 'chat_msg')!.client_msg_id
  await feed([
    { seq: 3, type: 'chat_msg', v: 1, client_msg_id: selfId, text: 'one' },
    { seq: 4, type: 'chat_msg', v: 1, client_msg_id: selfId, text: 'two' },
  ])
  const ui = await pane($)
  const t = await texts(ui)
  for (const x of ['you  mine', 'them  one', 'them  two']) expect(t).toContain(x)
  expect(t.filter((x: string) => /^(you|them)  /.test(x)).length).toBe(3)
})

test('line ids (the drawing keys) stay unique whatever client_msg_id a peer sends', async () => {
  // The drawn tree does not carry Text keys, so this reads the ids they come from.
  const { $, chatState } = fakeChat$()
  await applyEvents($, [MATCHED as any])
  await submitLine($, BASE, 'mine')
  const selfId: string = chatState().lines[0].id
  await applyEvents($, [
    { seq: 3, type: 'chat_msg', client_msg_id: selfId, text: 'one' },
    { seq: 4, type: 'chat_msg', client_msg_id: selfId, text: 'two' },
    { seq: 5, type: 'chat_msg', client_msg_id: selfId.slice(2), text: 'three' },
  ])
  await applyEvents($, [{ seq: 4, type: 'chat_msg', client_msg_id: 'x', text: 'two again' }])
  const ids = chatState().lines.map((l: any) => l.id)
  expect(chatState().lines.map((l: any) => l.text)).toEqual(['mine', 'one', 'two', 'three'])
  expect(new Set(ids).size).toBe(ids.length)
  expect(selfId).toMatch(/^s-[0-9a-f-]{36}$/)
})

test('duplicate seq in one batch: applied once', X, async ($, on) => {
  const { feed } = await matched($, on)
  const m = { seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'hi' }
  await feed([m, m])
  expect((await texts(await pane($))).filter((x: string) => x === 'them  hi').length).toBe(1)
})

test('inbound caps: text 2000, pseudonym and blurb 80, opener 500; a blank peer line is skipped', X, async ($, on) => {
  const { clock, feed } = await start($, on)
  await turnStart($, clock)
  await feed([{ ...MATCHED, pseudonym: 'n'.repeat(200), blurb: 'b'.repeat(200), opener: 'o'.repeat(900) }])
  await feed([
    { seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p1', text: 'x'.repeat(3000) },
    { seq: 4, type: 'chat_msg', v: 1, client_msg_id: 'p2', text: ' \n\t ' },
  ])
  const t = await texts(await pane($, 1000))
  expect(t).toContain("you're chatting with " + 'n'.repeat(80))
  expect(t).toContain('b'.repeat(80))
  expect(t).toContain('o'.repeat(500))
  expect(t).toContain('them  ' + 'x'.repeat(2000))
  expect(t.filter((x: string) => x.startsWith('them  ')).length).toBe(1)
})

test('peer ended while not placed: band back to searching, no ended line lingers', X, async ($, on) => {
  const { feed } = await matched($, on, { open: () => ({ isPlaced: false, reason: 'narrow' }) })
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  expect(await bandText($)).toMatch(/^. looking for someone$/)
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
})

test('block / report settle before the leave goes out', X, async ($, on) => {
  let release!: () => void
  const held = new Promise<{ status: number }>((r) => (release = () => r({ status: 204 })))
  const { log, clock } = await matched($, on, { send: (r) => (JSON.parse(r.body!).type === 'block' ? held : { status: 204 }) })
  await runCommand($, 'block')
  await clock.settle()
  expect(sends(log).map((m) => m.type)).toEqual(['ready', 'block'])
  release()
  await clock.settle()
  expect(sends(log).map((m) => m.type)).toEqual(['ready', 'block', 'leave'])
})

// The engine's test `$` cannot raise a person's close, so this drives the
// module with a hand-made `$` (state in memory), as safety.test.tsx does.
// Lets the background open (patch 2) land: a few microtask turns.
const flush = async () => {
  for (let i = 0; i < 50; i++) await Promise.resolve()
}

function fakeChat$() {
  const state = new Map<string, { value: unknown; version: number }>()
  const addr = (r: any) => `${r.plugin}.${r.key}`
  const $ = {
    state: {
      get: async (r: any) => state.get(addr(r)) ?? { value: undefined, version: 0 },
      set: async (r: any, value: unknown, o?: { ifVersion?: number }) => {
        const cur = state.get(addr(r)) ?? { value: undefined, version: 0 }
        if (o?.ifVersion !== undefined && o.ifVersion !== cur.version) return { isSet: false, version: cur.version }
        state.set(addr(r), { value, version: cur.version + 1 })
        return { isSet: true, version: cur.version + 1 }
      },
    },
    // No timers here: the flourish must drop rather than stick (patch 7).
    clock: {
      every: () => {
        throw new Error('no timers')
      },
      after: () => ({ cancel() {} }),
    },
    ui: { open: async () => ({ isPlaced: true }), close: async () => {}, invalidate: () => {} },
  }
  return { $: $ as any, chatState: () => state.get('claudingtin.chat')?.value as any }
}

test('person close: band offers the chat again; after an ended line it clears it; a plugin close changes nothing', async () => {
  const { $, chatState } = fakeChat$()
  await applyEvents($, [MATCHED as any])
  await flush()
  expect(chatState()).toMatchObject({ phase: 'chatting', placed: true })
  // A timer that cannot start drops the flourish: the band is not stuck on it.
  expect(await chatBand($)).toBe(null)
  await onChatClose($, { kind: 'plugin' })
  expect(chatState()).toMatchObject({ phase: 'chatting', placed: true })
  await onChatClose($, { kind: 'person' })
  expect(await chatBand($)).toBe('claudingtin · chatting with Ana — /claudingtin chat')

  const b = fakeChat$()
  await applyEvents(b.$, [MATCHED as any])
  await flush()
  await applyEvents(b.$, [{ seq: 3, type: 'session_ended' }])
  expect(b.chatState()).toMatchObject({ ended: true, match: null })
  await onChatClose(b.$, { kind: 'plugin' })
  expect(b.chatState().ended).toBe(true)
  await onChatClose(b.$, { kind: 'person' })
  expect(b.chatState()).toMatchObject({ ended: false, placed: false })
})

test('stale poll: a matched that lands after turn.complete opens nothing, stays idle', X, async ($, on) => {
  const { clock, feed, opens, log } = await start($, on)
  await turnStart($, clock)
  await turnEnd($, clock)
  await feed([MATCHED])
  expect(opens).toEqual([])
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toBe('claudingtin · connected')
  // Not acked either: no further poll carries after=2.
  expect(lines(log).some((l) => l === 'events ?after=2')).toBe(false)
})

// ---------------------------------------------------------------------------
// Story 3.6: persist.

const busies = (log: Req[]) => sends(log).filter((m) => m.type === 'busy').length
const leaves = (log: Req[]) => sends(log).filter((m) => m.type === 'leave').length
const COPY_ENDED = "looks like their claude's back — catch you later"
const BACK = "your claude's back"
const HELD_BAND = "claudingtin · your claude's back — chat's still open"
const KEEP_NOTE = "this chat's id — the only way to find them again"

// Matched with persist on (the default: X sets no persist), then the person's Claude came back.
async function held($: any, on: any, w: World = {}) {
  const s = await matched($, on, w)
  await turnEnd($, s.clock)
  return s
}

test('persist on by default: Claude back holds the chat open with the quiet note, nothing sent', X, async ($, on) => {
  const { log, closes } = await held($, on)
  expect(closes).toEqual([])
  expect(busies(log)).toBe(0)
  expect(leaves(log)).toBe(0)
  expect(await texts(await pane($))).toContain(BACK)
  expect(await bandText($)).toBe(HELD_BAND)
})

test('held: the events poll keeps running, so peer lines still land', X, async ($, on) => {
  const { feed } = await held($, on)
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p', text: 'still here?' }])
  expect(await texts(await pane($))).toContain('them  still here?')
})

test('held, peer ends: ended line, one busy, and a raced rematch in the batch is ignored', X, async ($, on) => {
  const { feed, log, opens } = await held($, on)
  const opened = opens.length
  await feed([
    { seq: 3, type: 'session_ended', v: 1 },
    { seq: 4, type: 'queued', v: 1 },
    { ...MATCHED, seq: 5, session_id: 's-2' },
  ])
  expect(await texts(await pane($))).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
  expect(busies(log)).toBe(1)
  expect(opens.length).toBe(opened)
  expect(await bandText($)).toBe('claudingtin · connected')
})

test('held, own leave: exactly one leave, then the withheld busy, Pane kept for the keepsake', X, async ($, on) => {
  const { clock, log, closes } = await held($, on)
  await runCommand($, 'leave')
  await clock.settle()
  expect(lines(log).filter((l) => l.startsWith('send')).slice(-2)).toEqual(['send leave', 'send busy'])
  expect(leaves(log)).toBe(1)
  expect(busies(log)).toBe(1)
  expect(closes).toEqual([])
  expect(await texts(await pane($))).toEqual([KEEP_NOTE, 's-1'])
})

for (const verb of ['block', 'report'] as const) {
  test(`held, ${verb}: ${verb}, leave, then busy, in that order`, X, async ($, on) => {
    const { clock, log } = await held($, on)
    await runCommand($, verb)
    await clock.settle()
    expect(lines(log).filter((l) => l.startsWith('send')).slice(-3)).toEqual([`send ${verb}`, 'send leave', 'send busy'])
  })
}

test('held, handle lost (events 410): hold dropped, chat reset to the keepsake, nothing more sent', X, async ($, on) => {
  const { failPoll, log, closes } = await held($, on)
  const before = sends(log).length
  await failPoll({ status: 410 })
  expect(closes).toEqual([])
  expect(await texts(await pane($))).toEqual([KEEP_NOTE, 's-1'])
  expect(sends(log).length).toBe(before)
})

test('held, new burst: the note goes, the chat carries on, nothing sent', X, async ($, on) => {
  const { clock, log } = await held($, on)
  const before = sends(log).length
  await turnStart($, clock)
  expect(await texts(await pane($))).not.toContain(BACK)
  expect(sends(log).length).toBe(before)
  // And the next return holds it again.
  await turnEnd($, clock)
  expect(await texts(await pane($))).toContain(BACK)
  expect(busies(log)).toBe(0)
})

test('a toggle mid-chat changes only the next chat', X, async ($, on) => {
  on('config.set', (_$: any, e: any) => ({ value: e.value }))
  const { clock, log, feed } = await matched($, on)
  expect((await runCommand($, 'persist')).text).toBe("persist is off — chats end when your claude's back")
  await clock.settle()
  // This chat was matched with persist on: it is held.
  await turnEnd($, clock)
  expect(leaves(log)).toBe(0)
  expect(await texts(await pane($))).toContain(BACK)
  // Peer ends it; the next chat snapshots persist off.
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await turnStart($, clock)
  await feed([{ ...MATCHED, seq: 4, session_id: 's-2' }])
  await clock.advance(600)
  await turnEnd($, clock)
  expect(lines(log).slice(-2)).toEqual(['send leave', 'send busy'])
})

test('/claudingtin persist twice flips it back on', OFF, async ($, on) => {
  const sets: any[] = []
  on('config.set', (_$: any, e: any) => {
    sets.push(e.value)
    return { value: e.value }
  })
  await start($, on)
  expect((await runCommand($, 'persist')).text).toBe("persist is on — your next chat stays open when your claude's back")
  expect((await runCommand($, 'persist')).text).toBe("persist is off — chats end when your claude's back")
  expect(sets).toEqual([true, false])
})

test('hung backend: Claude back resolves without waiting on a fetch (persist off)', OFF, async ($, on) => {
  // Only the leave and busy hang: ready went out fine.
  const { clock } = await matched($, on, { send: (r) => (/"(leave|busy)"/.test(r.body ?? '') ? never() : { status: 204 }) })
  let done = false
  void $.turn.complete({ answer: '', durationMs: 1, turnId: lastTurn, isAborted: false, reason: 'answer' }).then(() => (done = true))
  await clock.settle()
  expect(done).toBe(true)
})

test('hung backend: Claude back resolves without waiting on a fetch (persist on)', X, async ($, on) => {
  const { clock } = await matched($, on, { send: (r) => (/"(leave|busy)"/.test(r.body ?? '') ? never() : { status: 204 }) })
  let done = false
  void $.turn.complete({ answer: '', durationMs: 1, turnId: lastTurn, isAborted: false, reason: 'answer' }).then(() => (done = true))
  await clock.settle()
  expect(done).toBe(true)
})

test('hot reload while held: note and polling resume', X, async ($, on) => {
  const { clock, feed } = await held($, on)
  await $.session.start({ cwd: '/tmp', source: 'resume' })
  await clock.settle()
  expect(await texts(await pane($))).toContain(BACK)
  await feed([]) // the pre-reload poll: its loop has given way to the new one
  await feed([{ seq: 3, type: 'chat_msg', v: 1, client_msg_id: 'p', text: 'after reload' }])
  expect(await texts(await pane($))).toContain('them  after reload')
})

test('/claudingtin persist flips the userConfig field and says what the next chat does', X, async ($, on) => {
  const sets: any[] = []
  on('config.set', (_$: any, e: any) => {
    sets.push({ key: e.key, value: e.value })
    return { value: e.value }
  })
  await start($, on)
  expect((await runCommand($, 'persist')).text).toBe("persist is off — chats end when your claude's back")
  expect(sets).toEqual([{ key: 'claudingtin.persist', value: false }])
})

test('/claudingtin persist when the setting cannot change: a calm line', OFF, async ($, on) => {
  on('config.set', () => {
    throw new Error('locked')
  })
  await start($, on)
  expect((await runCommand($, 'persist')).text).toBe("couldn't change persist here.")
})

// ---------------------------------------------------------------------------
// Story 3.7: the session-ID keepsake.

const KEEP_BAND = "claudingtin · keep this chat's id — /claudingtin chat"
const buttons = async (ui: any) => (await ui.findAll({ type: 'Button' })).map((b: any) => b.props.label)

test('kept, peer ends: ended line, note, the id as text, copy id and done', X, async ($, on) => {
  const { feed } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  const ui = await pane($)
  expect(await texts(ui)).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
  expect(await buttons(ui)).toEqual(['copy id', 'done'])
})

for (const verb of ['block', 'report'] as const) {
  test(`kept, ${verb}: no keepsake, Pane closed`, X, async ($, on) => {
    const { clock, closes } = await held($, on)
    await runCommand($, verb)
    await clock.settle()
    expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
    expect(await texts(await pane($))).toEqual(['no chat going right now.'])
    expect(await bandText($)).not.toBe(KEEP_BAND)
  })
}

test('kept, then a new burst, then the peer ends: keepsake on the Pane, spinner in the band', X, async ($, on) => {
  const { clock, feed } = await held($, on)
  await turnStart($, clock)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  expect(await texts(await pane($))).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
  expect(await bandText($)).toMatch(/^. looking for someone$/)
})

// The engine harness cannot make a live poll unholdable on cue, so this drives
// the module: kept once, then a return that cannot hold ends it via resetChat.
test('kept, then Claude back again with no poll to hold on: reset, keepsake offered', async () => {
  const { $, chatState } = fakeChat$()
  await applyEvents($, [MATCHED as any])
  await flush()
  expect(await onModelBack($, true)).toBe('hold')
  await markKept($)
  await clearBack($)
  expect(await onModelBack($, false)).toBe('leave')
  await resetChat($)
  expect(chatState()).toMatchObject({ phase: 'idle', match: null, keepsake: { id: 's-1', copy: 'none' } })
})

test('not kept: a persist-on chat the peer ends mid-burst offers no id', X, async ($, on) => {
  const { feed } = await matched($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1, session_id: 's-1' }])
  const ui = await pane($)
  expect(await texts(ui)).toEqual([COPY_ENDED])
  expect(await buttons(ui)).toEqual([])
})

test('not kept: persist off, Claude back offers no id', OFF, async ($, on) => {
  const { clock } = await matched($, on)
  await turnEnd($, clock)
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).not.toBe(KEEP_BAND)
})

test('not kept: an own leave mid-burst offers no id and closes the Pane', X, async ($, on) => {
  const { clock, closes } = await matched($, on)
  await runCommand($, 'leave')
  await clock.settle()
  expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
})

test('copy id: onto the pressing surface\'s clipboard, then copied', X, async ($, on) => {
  const copies: any[] = []
  on('ui.copy', (_$: any, e: any) => {
    copies.push({ ...e })
    return { value: { isCopied: true } }
  })
  const { clock, feed } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  const ui = await pane($)
  await ui.press({ key: 'keep-copy' })
  await clock.settle()
  expect(copies.length).toBe(1)
  expect(copies[0].text).toBe('s-1')
  expect(copies[0].surface).toBe('terminal')
  const after = await pane($)
  expect(await texts(after)).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
  expect(await buttons(after)).toEqual(['copied', 'done'])
})

for (const [name, answer] of [
  ['no clipboard', () => ({ value: { isCopied: false, reason: 'no-clipboard' } })],
  ['copy throws', () => {
    throw new Error('nope')
  }],
] as const) {
  test(`copy id (${name}): a calm line, the id still shown`, X, async ($, on) => {
    on('ui.copy', answer as any)
    const { clock, feed } = await held($, on)
    await feed([{ seq: 3, type: 'session_ended', v: 1 }])
    await (await pane($)).press({ key: 'keep-copy' })
    await clock.settle()
    expect(await texts(await pane($))).toEqual([COPY_ENDED, KEEP_NOTE, 's-1', "no clipboard here — the id's above"])
  })
}

test('done: the keepsake goes, Pane closed', X, async ($, on) => {
  const { clock, feed, closes } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await (await pane($)).press({ key: 'keep-done' })
  await clock.settle()
  expect(closes).toContainEqual({ id: 'claudingtin-chat', origin: 'plugin' })
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
})

test('narrow: a pending keepsake hints in the band; /claudingtin chat opens it', X, async ($, on) => {
  const { clock, opens } = await held($, on, { open: (n) => (n === 1 ? { isPlaced: false, reason: 'narrow' } : { isPlaced: true }) })
  await runCommand($, 'leave')
  await clock.settle()
  expect(await bandText($)).toBe(KEEP_BAND)
  const r = await runCommand($, 'chat')
  await clock.settle()
  expect(r.text).toBeUndefined()
  expect(opens.length).toBe(2)
  expect(await texts(await pane($))).toEqual([KEEP_NOTE, 's-1'])
  expect(await bandText($)).not.toBe(KEEP_BAND)
})

test('a new match replaces a pending keepsake: the new chat opens normally', X, async ($, on) => {
  const { clock, feed } = await held($, on)
  await runCommand($, 'leave')
  await clock.settle()
  await turnStart($, clock)
  await feed([{ seq: 3, type: 'queued', v: 1 }])
  await feed([{ ...MATCHED, seq: 4, session_id: 's-2', pseudonym: 'Bo' }])
  await clock.advance(600)
  const t = await texts(await pane($))
  expect(t[0]).toBe("you're chatting with Bo")
  expect(t).not.toContain(KEEP_NOTE)
  expect(t).not.toContain('s-1')
})

test('an empty session_id: a kept chat offers nothing', X, async ($, on) => {
  const s = await start($, on)
  await turnStart($, s.clock)
  await s.feed([{ seq: 1, type: 'queued', v: 1 }])
  await s.feed([{ ...MATCHED, session_id: '' }])
  await s.clock.advance(600)
  await turnEnd($, s.clock)
  await s.feed([{ seq: 3, type: 'session_ended', v: 1 }])
  expect(await texts(await pane($))).toEqual([COPY_ENDED])
})

test('the keepsake id never leaves: no fetch carries it', X, async ($, on) => {
  on('ui.copy', () => ({ value: { isCopied: true } }))
  const { clock, feed, log, said } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await (await pane($)).press({ key: 'keep-copy' })
  await clock.settle()
  for (const r of log) {
    expect(r.url.includes('s-1')).toBe(false)
    expect((r.body ?? '').includes('s-1')).toBe(false)
  }
  expect(said.some((x) => x.includes('s-1'))).toBe(false)
})

test('person close dismisses a keepsake; a plugin close keeps it', async () => {
  const { $, chatState } = fakeChat$()
  await applyEvents($, [MATCHED as any])
  await flush()
  await onModelBack($, true)
  await markKept($)
  await applyEvents($, [{ seq: 3, type: 'session_ended' }])
  expect(chatState().keepsake).toEqual({ id: 's-1', copy: 'none' })
  await onChatClose($, { kind: 'plugin' })
  expect(chatState().keepsake).not.toBe(null)
  await onChatClose($, { kind: 'person' })
  expect(chatState()).toMatchObject({ keepsake: null, ended: false, placed: false })
})

test('a pending keepsake outlives the next turn: leave, new burst, Claude back again', X, async ($, on) => {
  const { clock, closes } = await held($, on)
  await runCommand($, 'leave')
  await clock.settle()
  await turnStart($, clock)
  await turnEnd($, clock)
  expect(closes).toEqual([])
  expect(await texts(await pane($))).toEqual([KEEP_NOTE, 's-1'])
})

test('kept, peer ends while searching, then the burst ends: the ended line and the offer stay', X, async ($, on) => {
  const { clock, feed, closes } = await held($, on)
  await turnStart($, clock)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await turnEnd($, clock)
  expect(closes).toEqual([])
  expect(await texts(await pane($))).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
})

test('kept, peer ends while unplaced: the band hints; /claudingtin chat shows the offer without an ended line', X, async ($, on) => {
  const { clock, feed } = await held($, on, { open: (n) => (n === 1 ? { isPlaced: false, reason: 'narrow' } : { isPlaced: true }) })
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  expect(await bandText($)).toBe(KEEP_BAND)
  await runCommand($, 'chat')
  await clock.settle()
  expect(await texts(await pane($))).toEqual([KEEP_NOTE, 's-1'])
})

test('the id comes from matched, never session_ended: a stray session_id there changes nothing', X, async ($, on) => {
  const { feed } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1, session_id: 's-x' }])
  expect(await texts(await pane($))).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
})

test('done while searching: the offer goes, the spinner carries on', X, async ($, on) => {
  const { clock, feed } = await held($, on)
  await turnStart($, clock)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await (await pane($)).press({ key: 'keep-done' })
  await clock.settle()
  expect(await texts(await pane($))).toEqual(['no chat going right now.'])
  expect(await bandText($)).toMatch(/^. looking for someone$/)
})

test('copy fails, then works: the calm line gives way to copied', X, async ($, on) => {
  let n = 0
  on('ui.copy', () => ({ value: ++n === 1 ? { isCopied: false, reason: 'no-clipboard' } : { isCopied: true } }))
  const { clock, feed } = await held($, on)
  await feed([{ seq: 3, type: 'session_ended', v: 1 }])
  await (await pane($)).press({ key: 'keep-copy' })
  await clock.settle()
  await (await pane($)).press({ key: 'keep-copy' })
  await clock.settle()
  const ui = await pane($)
  expect(await texts(ui)).toEqual([COPY_ENDED, KEEP_NOTE, 's-1'])
  expect(await buttons(ui)).toEqual(['copied', 'done'])
})

test('a hold that never landed is not kept: its end offers nothing', async () => {
  const { $, chatState } = fakeChat$()
  await applyEvents($, [MATCHED as any])
  await flush()
  expect(await onModelBack($, true)).toBe('hold')
  // link.ts: the held write failed, so clearBack runs and markKept never does.
  await clearBack($)
  await applyEvents($, [{ seq: 3, type: 'session_ended' }])
  expect(chatState().keepsake).toBe(null)
})

test('a session_id with a newline: the offer is one line', async () => {
  const { $, chatState } = fakeChat$()
  await applyEvents($, [{ ...MATCHED, session_id: 's-1\nx' } as any])
  await flush()
  await onModelBack($, true)
  await markKept($)
  await applyEvents($, [{ seq: 3, type: 'session_ended' }])
  expect(chatState().keepsake).toEqual({ id: 's-1 x', copy: 'none' })
})
