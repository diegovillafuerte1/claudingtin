import { test, expect } from 'claude-code/testing'
import { isCanonicalUUIDv4, loadAccountKey } from './identity'
import { isWindowsPath, joinPath, pickConfigDir } from './configdir'

// A hand-made `$` over an in-memory file system: the test kit's own `$` has
// no fs, env or process nouns, and loadAccountKey needs only those three.
// `files` by absolute path, `dirs` that exist, `unreadable` paths that exist
// but reject a read.
type World = {
  env: Record<string, string>
  files?: Record<string, string>
  dirs?: string[]
  unreadable?: string[]
  failWrite?: boolean
  failChmod?: boolean
  // Stands in for another session whose write lands last.
  racer?: string
}

function world(w: World) {
  const files = new Map(Object.entries(w.files ?? {}))
  const dirs = w.dirs ?? []
  const unreadable = w.unreadable ?? []
  const writes: string[] = []
  const runs: string[][] = []
  const logs: string[] = []
  const say = (kind: string) => (...a: unknown[]) => {
    logs.push(kind + JSON.stringify(a))
  }
  const $ = {
    env: { get: async (name: string) => w.env[name] },
    fs: {
      read: async (path: string) => {
        if (unreadable.includes(path)) throw new Error('EACCES')
        if (!files.has(path)) throw new Error('ENOENT')
        return files.get(path)
      },
      exists: async (path: string) =>
        files.has(path) || unreadable.includes(path) || dirs.some((d) => d === path || d.startsWith(path + '/')),
      write: async (path: string, text: string) => {
        if (w.failWrite) throw new Error('EROFS')
        writes.push(path)
        files.set(path, w.racer ?? text)
      },
    },
    process: {
      run: async (argv: readonly string[]) => {
        runs.push([...argv])
        if (w.failChmod) throw new Error('no chmod')
        return { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
      },
    },
    ui: { log: say('log'), status: say('status'), toast: say('toast') },
  }
  return { $: $ as any, files, writes, runs, logs }
}

const MAC = { env: { HOME: '/Users/a' }, dirs: ['/Users/a/Library/Application Support'] }
const MAC_DIR = '/Users/a/Library/Application Support/claudingtin'
const MAC_KEY = `${MAC_DIR}/account-key`
const VALID = '0f8fad5b-d9cb-469f-a165-70867728950e'


test('isCanonicalUUIDv4 matches the companion rule', async () => {
  expect(isCanonicalUUIDv4(VALID)).toBe(true)
  expect(isCanonicalUUIDv4(VALID.toUpperCase())).toBe(false)
  expect(isCanonicalUUIDv4('0f8fad5b-d9cb-169f-a165-70867728950e')).toBe(false) // v1
  expect(isCanonicalUUIDv4('0f8fad5b-d9cb-469f-c165-70867728950e')).toBe(false) // variant
  expect(isCanonicalUUIDv4(VALID + 'a')).toBe(false)
  expect(isCanonicalUUIDv4('')).toBe(false)
})

test('config dir: Windows, macOS, XDG, Linux default, none', async () => {
  expect(pickConfigDir({ appData: 'C:\\Users\\a\\AppData\\Roaming', home: '/x' })).toBe(
    'C:\\Users\\a\\AppData\\Roaming\\claudingtin',
  )
  expect(pickConfigDir({ home: '/Users/a', hasMacAppSupport: true, xdgConfigHome: '/xdg' })).toBe(MAC_DIR)
  expect(pickConfigDir({ home: '/home/a', xdgConfigHome: '/xdg' })).toBe('/xdg/claudingtin')
  expect(pickConfigDir({ home: '/home/a', xdgConfigHome: 'relative/xdg' })).toBe('/home/a/.config/claudingtin')
  expect(pickConfigDir({ home: '/home/a' })).toBe('/home/a/.config/claudingtin')
  expect(pickConfigDir({})).toBe(undefined)
  expect(isWindowsPath('C:\\a')).toBe(true)
  expect(isWindowsPath('/a')).toBe(false)
  expect(joinPath('C:\\a\\claudingtin', 'account-key')).toBe('C:\\a\\claudingtin\\account-key')
})

test('key carries over: companion key returned, file untouched, chmod only', async () => {
  const w = world({ ...MAC, files: { [MAC_KEY]: VALID + '\n' } })
  expect(await loadAccountKey(w.$)).toBe(VALID)
  expect(w.writes).toEqual([])
  expect(w.files.get(MAC_KEY)).toBe(VALID + '\n')
  expect(w.runs).toEqual([['chmod', '600', MAC_KEY]])
  expect(w.logs.join('')).not.toContain(VALID)
})

test('key carries over with surrounding whitespace (trimmed)', async () => {
  const w = world({ ...MAC, files: { [MAC_KEY]: `  ${VALID}\r\n\n` } })
  expect(await loadAccountKey(w.$)).toBe(VALID)
})

test('first run (macOS): new UUIDv4 written with newline, chmod 600, returned', async () => {
  const w = world(MAC)
  const key = await loadAccountKey(w.$)
  expect(key).toBeDefined()
  expect(isCanonicalUUIDv4(key!)).toBe(true)
  expect(w.writes).toEqual([MAC_KEY])
  expect(w.files.get(MAC_KEY)).toBe(key + '\n')
  expect(w.runs).toEqual([['chmod', '600', MAC_KEY]])
  expect(w.logs.join('')).not.toContain(key!)
})

test('first run (Linux, no Application Support): ~/.config/claudingtin', async () => {
  const w = world({ env: { HOME: '/home/a' } })
  const key = await loadAccountKey(w.$)
  expect(w.files.get('/home/a/.config/claudingtin/account-key')).toBe(key + '\n')
})

test('first run (XDG_CONFIG_HOME absolute)', async () => {
  const w = world({ env: { HOME: '/home/a', XDG_CONFIG_HOME: '/xdg' } })
  const key = await loadAccountKey(w.$)
  expect(w.files.get('/xdg/claudingtin/account-key')).toBe(key + '\n')
})

test('first run (Windows APPDATA): backslash path, no chmod', async () => {
  const w = world({ env: { APPDATA: 'C:\\Users\\a\\AppData\\Roaming', HOME: '/ignored' } })
  const key = await loadAccountKey(w.$)
  expect(w.files.get('C:\\Users\\a\\AppData\\Roaming\\claudingtin\\account-key')).toBe(key + '\n')
  expect(w.runs).toEqual([])
})

for (const [name, body] of [
  ['empty', ''],
  ['whitespace', '  \n\t\n'],
  ['uppercase', VALID.toUpperCase() + '\n'],
  ['v1 UUID', '0f8fad5b-d9cb-169f-a165-70867728950e\n'],
  ['over 512 bytes', VALID + '\n' + ' '.repeat(600)],
] as const) {
  test(`corrupt key (${name}) is regenerated`, async () => {
    const w = world({ ...MAC, files: { [MAC_KEY]: body } })
    const key = await loadAccountKey(w.$)
    expect(isCanonicalUUIDv4(key!)).toBe(true)
    expect(key).not.toBe(VALID)
    expect(w.files.get(MAC_KEY)).toBe(key + '\n')
  })
}

test('after a write the on-disk value is returned (a racing writer wins)', async () => {
  const w = world({ env: { HOME: '/home/a' }, racer: VALID + '\n' })
  expect(await loadAccountKey(w.$)).toBe(VALID)
  expect(w.files.get('/home/a/.config/claudingtin/account-key')).toBe(VALID + '\n')
})

test('re-read after the write does not parse: undefined, not the generated key', async () => {
  const w = world({ env: { HOME: '/home/a' }, racer: 'garbage\n' })
  expect(await loadAccountKey(w.$)).toBe(undefined)
  expect(w.writes.length).toBe(1)
})

test('concurrent loads on a first run share one write and one key', async () => {
  const w = world({ env: { HOME: '/home/a' } })
  const [a, b] = await Promise.all([loadAccountKey(w.$), loadAccountKey(w.$)])
  expect(w.writes.length).toBe(1)
  expect(isCanonicalUUIDv4(a!)).toBe(true)
  expect(b).toBe(a)
  // Settled: a later call loads afresh (and finds the key on disk).
  expect(await loadAccountKey(w.$)).toBe(a)
  expect(w.writes.length).toBe(1)
})

test('write fails: undefined, no throw', async () => {
  const w = world({ ...MAC, failWrite: true })
  expect(await loadAccountKey(w.$)).toBe(undefined)
})

test('existing but unreadable key is left alone: undefined, nothing written', async () => {
  const w = world({ ...MAC, unreadable: [MAC_KEY] })
  expect(await loadAccountKey(w.$)).toBe(undefined)
  expect(w.writes).toEqual([])
})

test('no home: neither APPDATA nor HOME set → undefined, no throw', async () => {
  const w = world({ env: {} })
  expect(await loadAccountKey(w.$)).toBe(undefined)
  expect(w.writes).toEqual([])
})

test('chmod failure is best effort: key still returned', async () => {
  const w = world({ env: { HOME: '/home/a' }, failChmod: true })
  const key = await loadAccountKey(w.$)
  expect(isCanonicalUUIDv4(key!)).toBe(true)
})

test('no throw escapes even when every noun rejects', async () => {
  const boom = async () => {
    throw new Error('boom')
  }
  const $ = { env: { get: boom }, fs: { read: boom, write: boom, exists: boom }, process: { run: boom } }
  expect(await loadAccountKey($ as any)).toBe(undefined)
})
