import { test, expect, mock } from 'claude-code/testing'
import { normalizeBaseUrl } from './register'

const BAND = {
  plugin: 'claudingtin',
  surface: 'terminal',
  component: 'AbovePrompt',
  props: { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 80, scroll: { top: 0, bodyRows: 9 } as any, view: {} as any },
} as const

const LABEL = { type: 'Text', text: /claudingtin/ } as const

// Stands in for the engine's own band: what the mod yields to. The test kit
// rejects `null` from the terminal render hook, so it draws a marker Text.
const engineRender = ($: any, e: any) => {
  const { Text } = $.ui.resolve(e)
  return <Text key="engine">engine</Text>
}

type FetchFn = (url: string) => any

// Wires the terminal hooks a test needs, starts the session and lets the first poll land.
async function start($: any, on: any, fetch: FetchFn) {
  const clock = mock.clock(on)
  const seen: string[] = []
  on('http.fetch', (_$: any, e: any) => {
    seen.push(e.url)
    return fetch(e.url)
  })
  on('ui.render', engineRender)
  on('session.start', (_$: any, e: any) => e as any)
  await $.session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  return { clock, seen }
}

const status = (code: number) => ({ value: { status: code, ok: code >= 200 && code < 300, headers: {}, text: '' } })
const never = () => new Promise(() => {})

async function bandText($: any, props: { hasSurvey?: boolean; maxRows?: number } = {}) {
  const ui = await $.ui.mount({ ...BAND, props: { ...BAND.props, ...props } } as any)
  const band = await ui.find(LABEL)
  if (band) return band.text
  // Yielded: the engine's element must be what is drawn.
  expect((await ui.find({ type: 'Text', text: /^engine$/ }))?.text).toBe('engine')
  return undefined
}

const X = { options: { backendUrl: 'http://x.test' } }

test('backend up: 200 reads connected and polls every 5 s', X, async ($, on) => {
  const { clock, seen } = await start($, on, () => status(200))
  expect(await bandText($)).toBe('claudingtin · connected')
  expect(seen[0]).toBe('http://x.test/status')
  await clock.advance(5000)
  expect(seen.length).toBe(2)
})

test('backend error: 503 reads offline', X, async ($, on) => {
  await start($, on, () => status(503))
  expect(await bandText($)).toBe('claudingtin · offline')
})

test('unreachable: rejected fetch reads offline', X, async ($, on) => {
  await start($, on, () => {
    throw new Error('ECONNREFUSED')
  })
  expect(await bandText($)).toBe('claudingtin · offline')
})

test('flips from connected to offline when the backend stops', X, async ($, on) => {
  let up = true
  const { clock } = await start($, on, () => status(up ? 200 : 503))
  expect(await bandText($)).toBe('claudingtin · connected')
  up = false
  await clock.advance(5000)
  expect(await bandText($)).toBe('claudingtin · offline')
})

test('flips from offline to connected when the backend comes up', X, async ($, on) => {
  let up = false
  const { clock } = await start($, on, () => status(up ? 200 : 503))
  expect(await bandText($)).toBe('claudingtin · offline')
  up = true
  await clock.advance(5000)
  expect(await bandText($)).toBe('claudingtin · connected')
})

test('a failed fetch releases the guard: the next tick polls again', X, async ($, on) => {
  let calls = 0
  const { clock, seen } = await start($, on, () => {
    calls++
    if (calls === 1) throw new Error('ECONNREFUSED')
    return status(200)
  })
  expect(await bandText($)).toBe('claudingtin · offline')
  await clock.advance(5000)
  expect(seen.length).toBe(2)
  expect(await bandText($)).toBe('claudingtin · connected')
})

test('before first result: a session with a pending fetch draws nothing', X, async ($, on) => {
  const { seen } = await start($, on, never)
  expect(seen.length).toBe(1)
  expect(await bandText($)).toBe(undefined)
})

test('trailing slash and whitespace are stripped', { options: { backendUrl: '  http://h:1//  ' } }, async ($, on) => {
  const { seen } = await start($, on, () => status(200))
  expect(seen[0]).toBe('http://h:1/status')
})

test('single trailing slash is stripped', { options: { backendUrl: 'http://h:1/' } }, async ($, on) => {
  const { seen } = await start($, on, () => status(200))
  expect(seen[0]).toBe('http://h:1/status')
})

test('unset config: polls the manifest default (127.0.0.1:8080)', async ($, on) => {
  const { seen } = await start($, on, () => status(200))
  expect(seen[0]).toBe('http://127.0.0.1:8080/status')
})

test('blank backendUrl falls back to the code default', { options: { backendUrl: '   ' } }, async ($, on) => {
  const { seen } = await start($, on, () => status(200))
  expect(seen[0]).toBe('http://127.0.0.1:8080/status')
})

test('normalizeBaseUrl: code default for missing or non-string input', async () => {
  expect(normalizeBaseUrl(undefined)).toBe('http://127.0.0.1:8080')
  expect(normalizeBaseUrl(42)).toBe('http://127.0.0.1:8080')
  expect(normalizeBaseUrl('http://h:1///')).toBe('http://h:1')
})

test('survey showing: band yields to the engine', X, async ($, on) => {
  await start($, on, () => status(200))
  expect(await bandText($, { hasSurvey: true })).toBe(undefined)
})

test('maxRows 0: band yields to the engine', X, async ($, on) => {
  await start($, on, () => status(200))
  expect(await bandText($, { maxRows: 0 })).toBe(undefined)
})

test('slow poll: offline after 4 s, polls again at 5 s, late result ignored', X, async ($, on) => {
  let calls = 0
  let resolveFirst: (v: any) => void = () => {}
  const { clock, seen } = await start($, on, () => {
    calls++
    if (calls === 1) return new Promise((r) => { resolveFirst = r })
    return never()
  })
  expect(seen.length).toBe(1)
  expect(await bandText($)).toBe(undefined)
  await clock.advance(4001)
  expect(await bandText($)).toBe('claudingtin · offline')
  await clock.advance(999)
  expect(seen.length).toBe(2)
  // The abandoned first fetch now lands with 200: it must not write connected.
  resolveFirst(status(200))
  await clock.settle()
  expect(await bandText($)).toBe('claudingtin · offline')
})

test('fetch settling under 4 s cancels the timeout; no overlapping fetch', X, async ($, on) => {
  let calls = 0
  let resolveFirst: (v: any) => void = () => {}
  const { clock, seen } = await start($, on, () => {
    calls++
    if (calls === 1) return new Promise((r) => { resolveFirst = r })
    return status(503)
  })
  await clock.advance(3000)
  expect(seen.length).toBe(1)
  resolveFirst(status(200))
  await clock.settle()
  expect(await bandText($)).toBe('claudingtin · connected')
  // Past 4 s: a live timeout would have flipped the band to offline.
  await clock.advance(1500)
  expect(seen.length).toBe(1)
  expect(await bandText($)).toBe('claudingtin · connected')
  await clock.advance(500)
  expect(seen.length).toBe(2)
  expect(await bandText($)).toBe('claudingtin · offline')
})

test('session.start again (reload) abandons the in-flight poll and polls at once', X, async ($, on) => {
  let calls = 0
  let resolveFirst: (v: any) => void = () => {}
  const { clock, seen } = await start($, on, () => {
    calls++
    if (calls === 1) return new Promise((r) => { resolveFirst = r })
    return status(503)
  })
  expect(seen.length).toBe(1)
  await ($ as any).session.start({ cwd: '/tmp', source: 'startup' })
  await clock.settle()
  expect(seen.length).toBe(2)
  expect(await bandText($)).toBe('claudingtin · offline')
  // The abandoned first fetch lands with 200: it must not change the band.
  resolveFirst(status(200))
  await clock.settle()
  expect(await bandText($)).toBe('claudingtin · offline')
})
