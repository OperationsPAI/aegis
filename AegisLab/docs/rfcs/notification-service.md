# RFC: Notification Service (per-user inbox + live updates)

- Status: **Draft**
- Owners: aegis-backend + aegis-ui
- Stakeholders: portal, settings, datasets, containers sub-apps
- Related: [`aegis-ui` NotificationContext](https://github.com/OperationsPAI/aegis-ui/blob/main/packages/ui/src/notifications/notificationContext.ts), `consts.NotificationStreamKey`, `module/notification/`

---

## Summary

Promote `module/notification` from a write-only Redis Stream broadcast
into a **persistent per-user inbox** with read/unread state, archive,
filtering, and a hybrid pull+push transport. This unblocks the
`NotificationBell` / `InboxPage` primitives in `aegis-ui` and lets every
sub-app emit user-visible events through one place.

## Motivation

`aegis-ui` ships a notification contract that already drives a bell, an
inbox page, and the shell header:

```ts
interface AegisNotification {
  id, title, body?, timestamp, read,
  to?, category?, severity?: 'info'|'success'|'warning'|'error', actor?
}
interface NotificationContextValue {
  items, unreadCount, loading,
  markRead?, markAllRead?, archive?, refetch?
}
```

Today the console runs a `DemoNotificationProvider` backed by
`localStorage` — there is no backend inbox to talk to. An earlier
`module/notification` exposed `GET /api/v2/notifications/stream` over a
`notifications:global` Redis Stream, but it had zero producers and zero
consumers in the monorepo (it was scaffolding from PR #40 that never
finished), and has been removed in the same change as this RFC.

### Gap matrix

| Frontend need | Backend today | Gap |
| --- | --- | --- |
| `id` | Redis Stream id | ✅ |
| `title` / `body` | `message` only | ⚠ no structured title |
| `timestamp` | server time | ✅ |
| `read`, `markRead`, `markAllRead` | — | ❌ no state |
| `archive` | — | ❌ |
| `to` (deeplink) | — | ❌ |
| `category` | `type` | rename |
| `severity` | `status` (status-of-something, not tone) | mismatch |
| `actor` | — | ❌ |
| Per-user routing | single global stream | ❌ fundamental |
| Initial fetch | last-id replay over SSE | partial |
| Filter / paginate | — | ❌ |
| Persistence after Stream trim | — | ❌ |

## Non-goals

- Outbound delivery channels (email / Slack / Feishu / push). A
  separate "channels" service can subscribe to the bus this RFC
  defines.
- Notification preferences UI. Wired later; the data model leaves room.
- Real-time presence / typing / typing-stop. Different problem.
- Workflow audit log. The existing Redis Stream stays for that —
  see "Coexistence" below.

## Proposed design

### Data model

```sql
CREATE TABLE notifications (
  id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
  user_id         INT          NOT NULL,        -- recipient
  category        VARCHAR(64)  NOT NULL,        -- e.g. injection.completed
  severity        VARCHAR(16)  NOT NULL,        -- info|success|warning|error
  title           VARCHAR(256) NOT NULL,
  body            TEXT,
  link_to         VARCHAR(512),                 -- frontend route, optional
  actor_user_id   INT,                          -- who caused it (nullable)
  entity_kind     VARCHAR(64),                  -- e.g. injection, dataset
  entity_id       VARCHAR(128),                 -- foreign id (string for flexibility)
  payload         JSON,                         -- extra structured data
  created_at      DATETIME(3) NOT NULL,
  read_at         DATETIME(3),
  archived_at     DATETIME(3),
  INDEX idx_user_unread (user_id, read_at, created_at DESC),
  INDEX idx_user_category (user_id, category, created_at DESC),
  INDEX idx_entity (entity_kind, entity_id)
);

CREATE TABLE notification_subscriptions (
  user_id     INT          NOT NULL,
  category    VARCHAR(64)  NOT NULL,
  channel     VARCHAR(32)  NOT NULL DEFAULT 'inbox', -- inbox|email|slack
  enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
  PRIMARY KEY (user_id, category, channel)
);
```

- `category` is a stable dotted key — producers own the namespace
  (`injection.completed`, `dataset.build.failed`, `apikey.expiring`,
  `system.update`, …). Frontend can map category → icon.
- `severity` is `info|success|warning|error` to match the
  `AegisNotification.severity` enum 1:1.
- `entity_kind/id` lets the inbox link out and lets producers
  de-dupe ("don't post twice for the same injection finishing").
- `payload` keeps the door open for richer cards later without a
  migration.
- Notifications are **soft-deleted** via `archived_at` so we can keep
  audit history.

### Producer surface (internal Go)

```go
package notification

type Service interface {
    Publish(ctx context.Context, n PublishReq) (*Notification, error)
    PublishFanout(ctx context.Context, n PublishReq, recipients []int) ([]Notification, error)
}

type PublishReq struct {
    UserID     int               // single-recipient form
    Category   string            // required
    Severity   Severity          // default Info
    Title      string            // required
    Body       string
    LinkTo     string
    ActorID    *int
    EntityKind string
    EntityID   string
    Payload    map[string]any
    DedupeKey  string            // optional; idempotent within 10 min window
}
```

Producers (`module/injection`, `module/dataset`, `module/auth` for
API-key expiry, etc.) depend on `notification.Service` via fx and call
`Publish` / `PublishFanout`. The service:

1. Inserts into `notifications`.
2. Emits a Redis pub/sub event on `notifications:user:<id>` (and a
   tenant-wide `notifications:tenant:<t>` for admins).
3. Optionally calls outbound channel adapters (out of scope for v1).

`DedupeKey` makes producers idempotent — they can call `Publish` from a
retry loop without spamming the inbox.

### HTTP surface

All routes are `/api/v2/notifications/*`, JWT-required, `Audience=portal`.

| Method & path | Description |
| --- | --- |
| `GET /` | List recipient's notifications. Query: `unread_only`, `category`, `severity`, `before`, `after`, `limit` (≤100), `cursor`. Returns `{ items, next_cursor, total_unread }`. |
| `GET /:id` | Fetch one (404 if not the recipient). |
| `POST /:id/read` | Mark read. Idempotent. |
| `POST /read-all` | Bulk mark all unread → read. Body: optional `category`. |
| `POST /:id/archive` | Soft-delete from inbox. |
| `GET /unread-count` | Cheap counter for the bell (uses `idx_user_unread`). |
| `GET /stream` | SSE long-poll. Emits `notification` events plus periodic `ping`. Replaces the current handler. |
| `GET /subscriptions` / `PUT /subscriptions` | Read / write per-category channel preferences (v1 ships defaults). |

Response shape mirrors `AegisNotification` exactly so the console can
adopt it without an adapter:

```json
{
  "id": "n_42",
  "title": "Injection INJ-29F1 completed",
  "body": "kafka loadgen drift on EU-WEST-01 finished, blast radius 42%.",
  "timestamp": "2026-05-12T03:04:05.123Z",
  "read": false,
  "to": "/portal/injections/INJ-29F1",
  "category": "injection.completed",
  "severity": "success",
  "actor": "Bob Liu"
}
```

`actor` is denormalised at write time (looked up from `users.full_name`)
so the inbox endpoint stays O(1) per row.

### Transport: hybrid pull + push

- **Pull** is the source of truth — `GET /` and `GET /unread-count` are
  the authoritative API. The console hydrates from these on every
  mount and on tab focus.
- **Push** is the freshness layer — `GET /stream` opens an SSE that the
  console subscribes to once authenticated. New `notification` events
  prepend to `items` and bump `unreadCount` without a refetch.
- SSE backpressure: server skips events when the writer buffer is full
  and emits a `resync` event; the client refetches.
- Heartbeat: server emits a `ping` every 25s to keep proxies open.

Why not WebSocket? Single direction, JWT-bearable, vite-proxy friendly,
matches the existing handler's transport. Upgrade later if we add
bidirectional features (typing, ack).

### Authorization

- Recipients can only read / mutate their own rows. Enforced in the
  handler via `userID := ctx.UserID(); WHERE user_id = userID`.
- Service-level "broadcast to admins" uses
  `PublishFanout(req, rbac.UsersWithRole("admin"))`.
- No cross-tenant reads; query is always `WHERE user_id = ?`.

### Retention

- Default 90 days; configurable via `[notification] retention_days`.
- Hourly background job soft-deletes rows older than retention; another
  weekly job hard-deletes archived + expired rows.

### Failure modes

- DB write fails → producer surfaces error; no Redis event emitted.
- Pub/sub fails → log and continue; pull will eventually catch up.
- SSE connection drops → client reconnects with last-seen cursor.

## Migration plan

1. **Schema + service** (this PR): add tables, `notification.Service`
   producer interface, HTTP endpoints behind a config flag.
2. **Console swap**: `apps/console/src/notifications/SsoNotificationProvider.tsx`
   replaces `DemoNotificationProvider`. localStorage path remains for
   storybook / unit tests.
3. **Producers**: convert `injection.completed`, `dataset.build.*`,
   `apikey.expiring`, `user.role.changed`, `team.invited` to publish
   through the new service. Land one module per PR.
4. **Channels**: separate RFC for email/Slack/Feishu fanout.

## Alternatives considered

- **Stay on Redis Stream + add a `read` set per user.** Cheapest, but
  every list query becomes O(stream length) + a SDIFF. No category
  index. Cursor-paginated history is awkward.
- **Use a managed product (Knock / Novu / Courier).** Faster v1 but
  introduces an external dependency, billing, and vendor lock-in
  before we know our needs.
- **Push-only, no DB.** Forces every client to be online when an
  event fires. Bad UX (inbox empties on refresh) and bad for offline
  audit.
- **One inbox per workspace, not per user.** Conflates "things I need
  to know" with "things that happened in this workspace". Use the
  audit log for the latter.

## Open questions

1. **Notification ownership across orgs.** When a user belongs to
   multiple workspaces, do they have one inbox or one per workspace?
   Proposal: one inbox, but every notification carries
   `payload.workspace_id` for client-side filtering.
2. **Bulk markRead semantics.** Mark all visible (filtered) or every
   unread? Proposal: only the filter set, returning the count.
3. **Toast vs inbox.** The library has no toast/snackbar primitive yet.
   Producers can hint `payload.transient=true` to skip the bell and go
   straight to a toast — out of scope for v1.
4. **Should `apikey.expiring` be a notification or a banner?** Both:
   a notification for history, and a banner the shell renders when
   `unread_count_by_category('apikey.expiring') > 0`.
5. **Dedup window.** 10 minutes feels right for "build finished"
   spam but is too short for "API key expiring weekly reminder".
   Move to per-category dedupe windows in v2.

## Acceptance criteria

- A user can register, log in via SSO, and see exactly the seed
  notifications they would have seen in the demo provider — but
  served from the backend.
- Two browser tabs see the same unread count, kept in sync within 2 s
  of `markRead` in either tab.
- 10k notifications per user list in <100 ms at p95.
- Producers can publish 1k events / sec without dropping DB writes.
- `pnpm check` on the console stays green after the provider swap;
  `go test ./module/notification/...` covers the read/mutate/stream
  paths.

## Out-of-scope follow-ups

- Outbound channels (email/Slack/Feishu/Teams).
- Notification preferences UI in `apps/console/src/apps/settings/`.
- Digest scheduling ("send me a daily summary").
- Rich card payloads (charts, images) in the inbox.
- Mobile push.
