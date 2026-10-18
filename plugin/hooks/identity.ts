import type { EngineInterface } from 'claude-code'
import { chmod600, configDir, joinPath } from './configdir'

// The account key, byte-compatible with the companion's
// (companion/internal/identity): one canonical lowercase UUIDv4 and a newline
// in <configDir>/account-key, mode 0600. A missing, empty, oversized or
// non-canonical file is regenerated; a file that exists but cannot be read is
// left alone. The key is never logged, shown, kept in $.state or $.store, or
// put in an error.

const KEY_FILE = 'account-key'
const MAX_KEY_FILE_SIZE = 512

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

/** A canonical version-4 UUID: lowercase hex, version `4`, variant 8/9/a/b. */
export function isCanonicalUUIDv4(s: string): boolean {
  return UUID_V4.test(s)
}

type Inspected = { state: 'valid'; key: string } | { state: 'missing' | 'corrupt' | 'unreadable' }

async function inspect($: EngineInterface, path: string): Promise<Inspected> {
  let raw: unknown
  try {
    raw = await $.fs.read(path)
  } catch {
    // fs.read rejects both when missing and when unreadable; only a missing
    // file may be created, never one that is there but locked.
    let exists = true
    try {
      exists = await $.fs.exists(path)
    } catch {
      exists = true
    }
    return { state: exists ? 'unreadable' : 'missing' }
  }
  if (typeof raw !== 'string') return { state: 'corrupt' }
  if (new TextEncoder().encode(raw).length > MAX_KEY_FILE_SIZE) return { state: 'corrupt' }
  const key = raw.trim()
  return isCanonicalUUIDv4(key) ? { state: 'valid', key } : { state: 'corrupt' }
}

// One load at a time per process: concurrent callers (a double press, an
// overlapping warm-up) share it, so a first run cannot write two keys.
let inFlight: Promise<string | undefined> | undefined

/**
 * The account key, created on first run and regenerated when corrupt; after a
 * write the on-disk value is re-read and returned. `undefined` when no config
 * directory resolves, the file is unreadable, or the write fails. Never throws.
 */
export function loadAccountKey($: EngineInterface): Promise<string | undefined> {
  if (inFlight) return inFlight
  const p = load($).finally(() => {
    if (inFlight === p) inFlight = undefined
  })
  inFlight = p
  return p
}

async function load($: EngineInterface): Promise<string | undefined> {
  try {
    const dir = await configDir($)
    if (dir === undefined) return undefined
    const path = joinPath(dir, KEY_FILE)

    const found = await inspect($, path)
    if (found.state === 'valid') {
      await chmod600($, path)
      return found.key
    }
    if (found.state === 'unreadable') return undefined

    const fresh = crypto.randomUUID().toLowerCase()
    try {
      await $.fs.write(path, fresh + '\n')
    } catch {
      return undefined
    }
    await chmod600($, path)
    // Racing first runs converge on whichever write landed last; a re-read
    // that does not parse yields no key rather than one disk disagrees with.
    const back = await inspect($, path)
    return back.state === 'valid' ? back.key : undefined
  } catch {
    return undefined
  }
}
