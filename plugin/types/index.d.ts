/** Last /status poll result: true = 2xx, false = error/unreachable/timed out, null = no result yet. */
export type Online = boolean | null

/** The poll guard: `gen` names the newest poll; `inFlight` is true while it is unresolved. */
export type Poller = { gen: number; inFlight: boolean }

declare module 'claude-code' {
  interface PluginState {
    claudingtin: {
      online: Online
      /** At most one /status poll in flight; results from an older `gen` are ignored. */
      poller: Poller
    }
  }
}
