# Sessions — lessons and rationale

The behavioural contract for sessions lives in
[`openspec/specs/sessions/spec.md`](../openspec/specs/sessions/spec.md) — the session
lifecycle, the session browser, the compact and reset commands, and message queueing are all
captured there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls,
and design rationale behind that flow: the "why it works this odd way" that the spec's
requirements don't dwell on.

## Why session keys are deterministic

When an agent has no messaging conversation (see the next section), the fallback session key is
deterministic for non-default agents (based on agent ID) so the same session is restored on
restart rather than spawning a fresh conversation each launch. That fallback rule is reused by
the one-shot CLI mode (`lucinate send`): it uses `MainKey` for the default agent and the literal
`"main"` for any other agent, so a scripted dispatch lands on the same conversation as "open the
picker, pick the agent, hit enter". Keeping both entry points on the same key rule is the whole
point — otherwise a scripted send and an interactive pick would drift onto different
conversations.

The `lucinate chat --session <key>` override is deliberately **one-shot** — cleared once
consumed in the `viewSelect` block so a follow-up agent pick on the same picker doesn't keep
landing on the original key. It beats the messaging-conversation preference too.

## Why opening an agent prefers its messaging conversation

When you reach an agent through an external messaging channel, the real conversation lives in a
channel-tied session the gateway minted — a key like `agent:main:telegram:direct:123456789`.
Opening the agent onto a blank `main` bucket would hide the history and context you came to see,
so `backend.PickMessagingSessionKey` looks up the agent's sessions and prefers that conversation
before falling back to the `main`/`MainKey` rule above.

Two things are deliberate about how it finds that session:

- **It reads structured fields, never the key.** The `channel`, `kind`, and
  `origin`/`deliveryContext` fields on each `sessions.list` row already carry the channel, the
  direct-vs-group kind, and the peer identity. Those are the signals we match on. The five-part
  key format is the gateway's to own — parsing it here would couple us to a shape we don't
  control, and the structured fields carry the same information without that coupling.
- **Most-recently-updated wins when there is more than one.** Nothing the client can see says
  which human is "you", so if an agent has several messaging DMs we take the most recent one. It
  is a heuristic, and we accept it: the alternative (asking the gateway which channel account is
  connected via `channels.status`) turned out not to carry the sender identity either, so it
  cannot pin down the conversation any better.

The lookup fails safe. A `sessions.list` error, an unexpected response, or simply no messaging
session all collapse to "" and fall back to the old default — opening an agent is never blocked
on this extra call. Backends with no messaging sessions (OpenAI-compatible, Hermes) always fall
back, so their behaviour is unchanged.

## Why history and stats load in parallel

On `chatModel.Init()`, `loadHistory()` and `loadStats()` run as two async commands in parallel
rather than in sequence — neither depends on the other, so serialising them would just add
latency to the first paint.

## Why transcripts are remembered across switches

Switching sessions builds a fresh `chatModel` and fetches its history. On OpenClaw that fetch can
be large: a session with an expanded CLI import comes back whole whatever `limit` is asked for
(2,874 messages and 7 MB measured on one session), so a switch showed "Loading conversation
history…" for a noticeable moment, every time, including when returning to a session left seconds
earlier.

Two things keep the switch fast:

- **`fetchHistory` trims before it renders.** It keeps the last `historyLimit` messages the
  transcript shows and renders only those. Markdown rendering is the cost (about 2.4 ms per 1,000
  characters of code-heavy Markdown at width 100: 19 ms for 8,000 characters, 1.15 s for 500,000,
  which is the size `chat.history` is now asked to allow per message), so it must be bounded by
  the limit, not by what the gateway chose to send. The download and its parse are not reduced.
- **`transcriptCache` remembers the last 10 sessions.** A revisit paints from it at once, and the
  history load that `Init` still issues replaces those rows.

The rules that make the cache safe, each of which was a defect in an earlier draft:

- **One writer.** Only `AppModel.stashChat` writes the cache, when the open chat is replaced, and
  it stores what that chat held from the gateway (rows with `gen == 0`). An earlier design stored
  history replies as they arrived; the cache could then hold a load that the chat model had
  discarded as older than a refresh, and a reply in flight could re-add a session just deleted.
- **Identify remembered rows by `gen`, never by position.** Replacing them is
  `mergeHistoryRefresh(fetched, 0)`. Tracking "the first N rows are the seed" panics after
  `/clear` empties the slice.
- **A load older than a merged refresh is discarded.** Otherwise a slow initial load overwrites
  the turn that a later refresh brought in.
- **An empty reply still replaces.** A session emptied elsewhere must not keep its old rows.
- **Copy in and out.** A re-render rewrites rows in place; a shared slice would let one chat's
  resize rewrite another's remembered transcript.
- **Not remembered:** a chat that never loaded, a cron transcript, an archived or deleted session
  (including one a `/reset` deleted and could not replace), the live rows of an unfinished turn,
  and a chat left over from a previous connection (session keys such as `main` repeat across
  gateways). A connection change clears the cache. A `/reset` drops its session exactly when the
  delete step went through (`sessionClearedMsg.deleted`).

## Why Markdown is never rendered on the UI goroutine

A resize, a theme change and a switch to a remembered session all used to re-render every
rendered row inside `Update`. At the cost above that froze the UI for as long as the transcript
was large, on every resize.

- **Every rendered row carries a `renderStamp`** (wrap width and theme). A row whose stamp is not
  the chat's (`chatModel.stamp()`, computed from its width and preferences, never stored) is
  stale. There is no per-chat "rendered at" value: one was tried in design and could not
  represent a history reply rendered before a resize landing after it.
- **`AppModel.Update` is the only place a re-render is asked for.** After every message it calls
  `chatModel.rerenderCmd()`, which returns a command when a row is stale and no re-render is in
  flight. Nothing else has to remember to ask, and `setSize`, `seedHistory` and the message
  handlers render nothing.
- **One re-render at a time per chat.** A drag resize is a stream of sizes; a command per size
  would render the whole transcript once for each, in parallel. The one in flight cannot be
  cancelled, so its result arrives out of date, changes nothing, and one more command is issued
  for the size by then. Only a result at the stamp in flight ends it, so a late result from a
  chat this one replaced on the same key (two cron transcripts) does not. The one case left is
  such a result at the very same stamp: each one can let one extra command start beside the one
  running. That needs a transcript replaced while its command runs and the pane back at the same
  size; it never sticks and corrects itself, and no test pins it. There is no cap on the bytes one command renders: it runs off the UI
  goroutine.
- **The result is matched by source text, never by index.** A result for a stamp the chat has left
  changes nothing; rows it did not cover stay stale and the next `Update` asks again. A source the
  renderer rejects is stamped so it is not asked for twice.
- **Every command builds its own renderer.** `chatModel.newRenderer` is a factory, called inside
  the command's goroutine. glamour v2.0.1 documents no concurrency guarantee for `TermRenderer`,
  and one instance used from two goroutines was measured to crash (nil dereference in
  `RenderBytes`) and to race (`ansi.BlockStack`). The history fetch and the post-turn refresh used
  to share `chatModel.renderer`; that renderer now stays on the UI goroutine.
- **One exception:** a cron transcript renders its run summaries synchronously when it opens.
  They are short, and it stamps them at the pane's width so nothing follows.

A chat that was never sized wraps at the minimum (20 columns). One opened while parked behind a
cron transcript is therefore rendered narrow, and re-rendered when it is restored.

## Where the outcome of a session command lands

`/new`, `/archive`, `/delete` and `/rename` answer after a gateway round trip, and the operator
may have moved by then. `AppModel.sessionChat(key)` finds the chat on a session — the open one, or
the one parked behind a cron transcript — and every outcome is applied there:

- The outcome is written to that chat, or dropped if no chat is on the session any more.
- A follow-up that replaces the chat (the neighbour after a removal, the session `/new` created)
  carries `inPlaceOf` and replaces that chat where it is. It never changes the view or the focus.
  If the chat is gone, the new session is listed in the sidebar and nothing is opened.
- A chat opened in place while it cannot receive unkeyed replies (parked, another view showing, or
  the sidebar focused) loads its history at once and holds the rest of its startup
  (`initPending`) until it has focus; issued early, those replies would be written into whatever
  the operator was looking at.

Session-scoped replies (`historyLoadedMsg`, `historyRefreshMsg`, `sessionClearedMsg`,
`transcriptRerenderedMsg`) are routed by `AppModel.deliverToChat` ahead of the view-state switch. The view-state routing drops them when
the operator is in another view or has the sidebar focused, which used to leave a chat on
"loading" for good; with a cache it would leave stale rows with nothing to show they were stale.
Each carries the session it belongs to, and a chat ignores one for another session.

## Compact: server-side vs local streaming

The distinction that catches people out: on OpenClaw the gateway runs the compaction pass
server-side, but on the OpenAI-compatible backend the pass runs **locally** (a streaming
`POST /v1/chat/completions` against the agent's configured model). Same `/compact` command, two
very different execution paths — worth remembering when a compaction behaves differently between
backends.

## Reset is delete-then-recreate

`/reset` doesn't clear a session in place; it calls `SessionDelete()` to permanently remove the
session and then immediately creates a replacement via `CreateSession()`. The new session key
comes back as `sessionClearedMsg{sessionKey, newSessionKey}` — the chat model reinitialises
against a fresh key rather than reusing the old one. `sessionKey` names the session that was
reset: a chat that has since moved to another session ignores the reply, where it used to be
renamed and emptied by it.

## Queueing gotcha: exec results also drain the queue

While a response is in flight, new input is appended to `m.pendingMessages` rather than sent, so
fast typing doesn't drop messages. The non-obvious part is that local (`!`) and remote (`!!`)
exec results **also** trigger `drainQueue()` — not just chat responses. If you change the exec
flow, remember it shares the same drain path; miss that and queued messages silently stall after
an exec.

`lucinate chat <message>` pre-seeds the same queue and drains it from the `historyLoadedMsg`
handler *after* the scrollback has rendered, so the launch message appears after the loaded
history — matching what a human typing the same message would see, rather than jumping ahead of
it.

## Scheduled sessions sort separately

Sessions whose key contains `:cron:` are split into their own **Scheduled** list in the session
browser, separate from regular **Conversations**. The `:cron:` marker in the key is what drives
the grouping — there's no other flag distinguishing an automated session from a hand-started one.
