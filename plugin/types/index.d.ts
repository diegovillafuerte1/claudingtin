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

/** What the mod wants the backend to know, or last told it: in the queue (`ready`) or not (`busy`). */
export type Presence = 'ready' | 'busy'

/**
 * The /v1 link. `handle` is the opaque welcome handle (never logged or shown);
 * `after` the highest event `seq` seen on it; `want` what the turns ask for and
 * `sent` what the backend was last told; `quiet` after a supersede (an events
 * poll's 410) or a second 410 on send ready, until the next main-loop
 * turn.start; `outdated` after please_update (this session);
 * `gen` names the newest events poll loop; `held` while a persisted chat stays
 * open past the burst (busy is withheld and the events poll keeps running).
 * Never holds the account key.
 */
export type Link = {
  handle: string | null
  after: number
  want: Presence | null
  sent: Presence | null
  quiet: boolean
  outdated: boolean
  gen: number
  held: boolean
}

/** Where the chat surface is: nothing, waiting in the queue, or in a match. */
export type ChatPhase = 'idle' | 'searching' | 'chatting'

/** The current match as `matched` named it. Peer-controlled text: render only through `inert`. */
export type ChatMatch = { sessionId: string; pseudonym: string; blurb: string; opener: string }

/** One line of the current chat: the peer's, or one this person sent (optimistic). */
export type ChatLine = { id: string; from: 'peer' | 'self'; text: string }

/**
 * The chat surface (Story 7.5), in memory only: never written to disk.
 * `lines` holds the current chat (at most 200); `ended` after the peer's
 * session_ended (the Pane shows the calm line); `placed` whether the chat Pane
 * is drawn; `left` after this person left, blocked or reported.
 */
export type Chat = {
  phase: ChatPhase
  match: ChatMatch | null
  lines: ChatLine[]
  ended: boolean
  placed: boolean
  left: boolean
  /** The persist setting as it was at `matched` (Story 3.6): a toggle changes only the next chat. */
  persist: boolean
  /** Held past the burst: the person's Claude is back and the chat stays open. */
  back: boolean
  /** Held past the burst at least once (Story 3.7): its end offers the keepsake. */
  kept: boolean
  /** The session-ID offer after a kept chat ended; null when none is pending. */
  keepsake: Keepsake | null
}

export type Keepsake = { id: string; copy: 'none' | 'copied' | 'failed' }

declare module 'claude-code' {
  interface PluginState {
    claudingtin: {
      online: Online
      /** At most one /status poll in flight; results from an older `gen` are ignored. */
      poller: Poller
      /** The first-run safety gate. Never holds the account key. */
      gate: Gate
      /** The /v1 link state; kept here so a hot reload resumes on the same handle. */
      link: Link
      /** True once the AbovePrompt band hook was asked to render this session. */
      drawn: boolean
      /** The chat surface; in memory only, so it survives a hot reload but never a restart. */
      chat: Chat
    }
  }
}
