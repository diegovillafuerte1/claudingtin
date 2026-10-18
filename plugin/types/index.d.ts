/** Last /status poll result: true = 2xx, false = error/unreachable/timed out, null = no result yet. */
export type Online = boolean | null

/** The poll guard: `gen` names the newest poll; `inFlight` is true while it is unresolved. */
export type Poller = { gen: number; inFlight: boolean }

/**
 * The first-run gate for this session: `accepted` once safety-ack is version 1
 * (or the person accepted and the write failed); `declined` once they said not
 * now or closed the pane, so it is not reopened unasked.
 */
export type Gate = { accepted: boolean; declined: boolean }

declare module 'claude-code' {
  interface PluginState {
    claudingtin: {
      online: Online
      /** At most one /status poll in flight; results from an older `gen` are ignored. */
      poller: Poller
      /** The first-run safety gate. Never holds the account key. */
      gate: Gate
    }
  }
}
