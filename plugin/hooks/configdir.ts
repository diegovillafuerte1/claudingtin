import type { EngineInterface } from 'claude-code'

// The companion's config directory, so keys and acknowledgements it wrote carry
// over: Go's os.UserConfigDir() joined with "claudingtin". The API exposes no
// platform, so Windows is told by APPDATA and macOS by probing
// ~/Library/Application Support.

const SUBDIR = 'claudingtin'

/** A Windows path: a drive letter (`C:\`, `C:/`) or a UNC share (`\\host`). */
export function isWindowsPath(p: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(p) || p.startsWith('\\\\')
}

/** Joins `name` onto `dir` with the separator the directory itself uses. */
export function joinPath(dir: string, name: string): string {
  const sep = isWindowsPath(dir) ? '\\' : '/'
  return dir.endsWith('/') || dir.endsWith('\\') ? dir + name : dir + sep + name
}

/**
 * The pure half of `configDir`: the directory from the three variables and
 * whether `$HOME/Library/Application Support` exists. `undefined` when no base
 * resolves (Go's UserConfigDir error).
 */
export function pickConfigDir(env: {
  appData?: string
  home?: string
  xdgConfigHome?: string
  hasMacAppSupport?: boolean
}): string | undefined {
  if (env.appData) return joinPath(env.appData, SUBDIR)
  if (env.home && env.hasMacAppSupport) return joinPath(`${env.home}/Library/Application Support`, SUBDIR)
  if (env.xdgConfigHome && env.xdgConfigHome.startsWith('/')) return joinPath(env.xdgConfigHome, SUBDIR)
  if (env.home) return joinPath(`${env.home}/.config`, SUBDIR)
  return undefined
}

/** The claudingtin config directory, or `undefined` when none resolves. Never throws. */
export async function configDir($: EngineInterface): Promise<string | undefined> {
  try {
    const appData = await $.env.get('APPDATA')
    if (appData) return pickConfigDir({ appData })
    const home = await $.env.get('HOME')
    let hasMacAppSupport = false
    if (home) {
      try {
        hasMacAppSupport = await $.fs.exists(`${home}/Library/Application Support`)
      } catch {
        hasMacAppSupport = false
      }
    }
    const xdgConfigHome = await $.env.get('XDG_CONFIG_HOME')
    return pickConfigDir({ home, xdgConfigHome, hasMacAppSupport })
  } catch {
    return undefined
  }
}

/**
 * Best-effort `chmod 600` ($.fs has no mode). Skipped on Windows paths, where
 * permissions are advisory. Never throws.
 */
export async function chmod600($: EngineInterface, path: string): Promise<void> {
  if (isWindowsPath(path)) return
  try {
    await $.process.run(['chmod', '600', path], { timeoutMs: 5000 })
  } catch {
    // Best effort: the file is still usable.
  }
}
