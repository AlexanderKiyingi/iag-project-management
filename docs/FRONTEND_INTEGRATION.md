# Project-Management Frontend Integration Guide

Comprehensive guide for connecting a frontend (Next.js, SvelteKit, plain SPA)
to the project-management backend. Covers auth, the workspace-document
model, every HTTP route, the realtime WebSocket envelope, file uploads,
and the optimistic-locking version protocol.

PM has a deliberately different shape from Fleet or Contract-Management:
**state lives in a single JSONB document per user**, not in per-entity
tables. The frontend loads the document once, holds it in client state,
keeps it live via WebSocket, and mutates either via fine-grained entity
endpoints (cheap) or via a full document PUT with `If-Match` (offline
sync). Read §2 carefully before §4.

For deployment-side env config see
[PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md). For sibling services
see Fleet's
[FRONTEND_INTEGRATION.md](../../../operations/fleet/docs/FRONTEND_INTEGRATION.md)
and Contract-Management's
[FRONTEND_INTEGRATION.md](../../contract-management/docs/FRONTEND_INTEGRATION.md).

---

## 1. Authentication

PM runs in **platform Bearer+aud mode** (hard cutover complete). Every
request — except the three public probes (§4.1) — requires:

```
Authorization: Bearer <jwt>
```

The JWT must carry `aud=iag.project-management`. Signatures are verified
locally against the auth service's JWKS; the verifier refreshes every 5
minutes. There is no callback to auth on the request hot path.

### WebSocket auth — query param token

Browsers can't set `Authorization` headers on the native `WebSocket`
constructor, so the `/api/v1/ws/workspace` endpoint accepts the token as a
**query parameter** instead:

```
ws://localhost:8080/api/v1/project-management/api/v1/ws/workspace?token=<jwt>
```

Same token, same audience requirement. Use a short-lived access token
(the 15-minute default is fine) — query-param tokens may end up in proxy
logs, so don't pass refresh tokens this way.

### Token refresh flow

```
┌─────────┐  POST /api/v1/authentication/oauth/token   ┌──────────┐
│ Browser │ ───────── grant_type=password ────────────▶│   Auth   │
│         │◀─── access_token, refresh_token ──────────│  Service │
└─────────┘                                            └──────────┘
     │  Authorization: Bearer …  (REST)
     │  ?token=…                  (WebSocket)
     ▼
┌──────────────────────┐
│  project-management  │  (verifies JWT locally via cached JWKS)
└──────────────────────┘
```

**Frontend responsibilities:**
- Hold `access_token` in memory; refresh ~1 minute before its 15-minute TTL.
- On any 401 from PM, refresh; on 401 from refresh, redirect to login.
- On 403, the user lacks the route's permission (§3) — hide the UI control.
- Re-issue the WebSocket connection with a fresh token after refresh.

---

## 2. The Workspace Document Model

This is the single most important concept for the PM frontend.

PM persists a **single JSONB document per workspace owner**. There's no
per-entity table — tasks, goals, sprints, chats, messages, members,
files-metadata all live as nested arrays inside one `Document` object.

```ts
type Document = {
  tasks:               Task[];
  projects:            Record<string, Project>;
  members:             Member[];
  goals:               Goal[];
  sprints:             Sprint[];
  requisitions:        Requisition[];
  chats:               Chat[];
  messages:            Message[];
  notifications:       WorkspaceNotification[];
  savedViews:          SavedView[];
  audit:               AuditEntry[];
  files:               WorkspaceFile[];
  taskComments:        TaskComment[];
  taskCustomFieldDefs: TaskCustomFieldDef[];
  taskListColumns:     Record<string, boolean>;
  sidebarCollapsed:    boolean;
  sidebarProjectsOpen: boolean;
  sidebarSavedViewsOpen: boolean;
  desktopNotificationsEnabled: boolean;
  theme:               string;
  orgId?:              string;
};
```

### Optimistic locking via `version`

Every workspace carries a monotonically-increasing `version` (int64). To
update, send the version you last saw via `If-Match`; if it doesn't match
the server's current version, you get `409 Conflict`. Re-fetch, merge,
retry.

```
GET /api/v1/workspace
→ 200 { "data": { …Document… }, "version": 42 }

PUT /api/v1/workspace
If-Match: 42
{ "data": { …mutated… }, "version": 42 }
→ 200 { "data": { …new… }, "version": 43 }

# If someone else updated in between:
→ 409 Conflict { "error": "version conflict" }
```

The `version` field in the JSON body is accepted as a fallback for
clients that can't set custom headers.

### Two mutation paths

1. **Fine-grained entity endpoints** (§4.4–§4.8) — e.g. `PATCH /tasks/:id`.
   Server loads the document, mutates one slice, saves with bumped
   version, broadcasts to the WebSocket. **Use this for normal UI
   interactions** — small payloads, server-side retry on contention.
2. **Full document PUT** (§4.2) — replaces the entire document. Useful
   for offline-edit-then-sync, or bulk migrations. Requires `If-Match`.

The frontend usually does (1) for everything except an initial
import/restore flow.

### Realtime broadcast contract

Every successful mutation — whether via entity endpoint or full PUT — is
broadcast to **every connected WebSocket** belonging to that workspace's
audience (the owner plus any shared members). The envelope is:

```json
{ "type": "workspace", "data": { …Document… }, "version": 43 }
```

So a single client doesn't need to maintain a hand-rolled delta protocol
— the server always pushes the whole document. (This is fine because
documents are typically <500 KB and traffic is bursty.) If you have
multiple tabs open, each gets the same push.

---

## 3. Permission Model

PM intentionally ships a **tiny permission catalog** — three keys, all
namespaced under `iag.project-management`:

| Key | Grants |
|---|---|
| `pm.view_workspace` | Read the workspace document, get files, open the WebSocket |
| `pm.mutate_workspace` | Create/update/delete tasks, goals, sprints, chats, messages, etc. |
| `pm.admin` | Add/remove members, change org metadata |

Catalog defined in
[internal/models/permissions.go](../internal/models/permissions.go). The
catalog is registered with the auth service at startup so admins can
grant the keys via the auth admin UI.

**Route gating** is on the handler middleware — `auth.RequireWorkspaceRead()`,
`auth.RequireWorkspaceWrite()`, `auth.RequirePerm("pm.admin")`. There's
no `module.action` matrix like contract-management; PM is the "whole
workspace or nothing" model because the data structure is one document.

**UI gating pattern:** at login call `GET /api/users/me` on auth (or read
the JWT's `permissions` claim directly) — if `pm.mutate_workspace` is
absent, render the workspace read-only; if `pm.admin` is absent, hide
the Members and Org Settings panels.

---

## 4. Endpoint Catalog (core surface)

All routes prefixed with the base URL (§7). Routes are gated by the
permission listed in the third column.

> **Scope note:** this catalog covers the core workspace surface. The
> Phase 4–6 feature routes are **not** all listed here — they exist in
> [`internal/handlers/entities.go`](../internal/handlers/entities.go)
> `Register()` and follow the same `pm.view_workspace` /
> `pm.mutate_workspace` gating: templates (`/templates`), automation
> rules (`/rules`), time tracking (`/tasks/:id/time/*`, `/users/:id/time`),
> portfolios (`/portfolios`), intake forms (`/forms`), reports
> (`/reports/{workload,throughput,status-rollup,burndown/:id}`), webhooks
> (`/webhooks`), subtasks (`/tasks/:id/subtasks`), task approvals
> (`/tasks/:id/{approve,reject,approvers}`), message reactions
> (`/messages/:id/reactions`), project sections (`/projects/:id/sections`),
> project status (`/projects/:id/status`), custom-field defs
> (`/custom-fields/:id`), and entity comments
> (`/{projects,goals,sprints}/:id/comments`). Treat the router source as
> the authoritative list until an OpenAPI spec ships (§10).

### 4.1 Public probes (no auth)

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Liveness — `{status:"ok"}` |
| GET | `/health` | Alias |
| GET | `/ready` | Readiness (DB ping) |

### 4.2 Workspace document & realtime

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/workspace` | `pm.view_workspace` | Load full document → `{data, version}` |
| PUT | `/api/v1/workspace` | `pm.mutate_workspace` | Replace document. Body `{data, version?}` or `If-Match: <version>` |
| GET | `/api/v1/ws/workspace?token=<jwt>` | `pm.view_workspace` | **WebSocket** — push `{type, data, version}` on every mutation |
| GET | `/api/v1/platform/status` | staff+ | Dependency health (auth, redis, kafka) |

### 4.3 Settings & audit

| Method | Path | Permission | Description |
|---|---|---|---|
| PATCH | `/api/v1/settings` | `pm.mutate_workspace` | Partial-update workspace settings (theme, sidebar prefs, desktopNotifications) |
| DELETE | `/api/v1/audit` | `pm.admin` | Clear the workspace audit log |

### 4.4 Tasks

| Method | Path | Permission | Description |
|---|---|---|---|
| POST | `/api/v1/tasks` | `pm.mutate_workspace` | Create task — body is a Task |
| PATCH | `/api/v1/tasks/:id` | `pm.mutate_workspace` | Partial-update — JSON-merge |
| POST | `/api/v1/tasks/bulk` | `pm.mutate_workspace` | Body `{tasks: [...]}` — create many |
| POST | `/api/v1/tasks/bulk-patch` | `pm.mutate_workspace` | Bulk partial-update many |
| POST | `/api/v1/tasks/delete-batch` | `pm.mutate_workspace` | Body `{ids: [...]}` — delete many |
| POST | `/api/v1/tasks/:id/tags` | `pm.mutate_workspace` | Body `{tag}` — add tag |
| DELETE | `/api/v1/tasks/:id/tags/:tag` | `pm.mutate_workspace` | Remove tag |
| PATCH | `/api/v1/tasks/:id/custom/:field` | `pm.mutate_workspace` | Body `{value}` — set custom-field value |
| POST | `/api/v1/tasks/:id/deps` | `pm.mutate_workspace` | Body `{dependsOnId}` — add dependency |
| DELETE | `/api/v1/tasks/:id/deps/:depId` | `pm.mutate_workspace` | Remove dependency |
| POST | `/api/v1/tasks/:id/comments` | `pm.mutate_workspace` | Body `{text}` — adds TaskComment; `@mentions` are parsed and emit `pm.mention.created` |
| DELETE | `/api/v1/comments/:id` | `pm.mutate_workspace` | Delete task comment |

> **Task assignment notifications** — Creating or changing a task's
> `assignee` publishes `pm.task.assigned` once per (taskID, assignee,
> day) thanks to server-side dedupe.

### 4.5 Goals

| Method | Path | Permission |
|---|---|---|
| POST | `/api/v1/goals` | `pm.mutate_workspace` |
| PATCH | `/api/v1/goals/:id` | `pm.mutate_workspace` |
| DELETE | `/api/v1/goals/:id` | `pm.mutate_workspace` |
| POST | `/api/v1/goals/:id/progress` | `pm.mutate_workspace` |
| POST | `/api/v1/goals/:id/key-results` | `pm.mutate_workspace` |
| PATCH | `/api/v1/goals/:id/key-results/:krId` | `pm.mutate_workspace` |
| DELETE | `/api/v1/goals/:id/key-results/:krId` | `pm.mutate_workspace` |

### 4.6 Sprints

| Method | Path | Permission |
|---|---|---|
| POST | `/api/v1/sprints` | `pm.mutate_workspace` |
| PATCH | `/api/v1/sprints/:id` | `pm.mutate_workspace` |
| DELETE | `/api/v1/sprints/:id` | `pm.mutate_workspace` |

### 4.7 Chats & messages

| Method | Path | Permission | Description |
|---|---|---|---|
| POST | `/api/v1/chats` | `pm.mutate_workspace` | Create chat |
| POST | `/api/v1/chats/:id/read` | `pm.mutate_workspace` | Mark all read |
| POST | `/api/v1/chats/:id/mute` | `pm.mutate_workspace` | Body `{muted: bool}` |
| POST | `/api/v1/messages` | `pm.mutate_workspace` | Post message; `@mentions` emit `pm.mention.created` |

### 4.8 Projects, requisitions, files, members

| Method | Path | Permission | Description |
|---|---|---|---|
| PUT | `/api/v1/projects/:id` | `pm.mutate_workspace` | Upsert project |
| POST | `/api/v1/requisitions` | `pm.mutate_workspace` | Create + publishes `pm.requisition.submitted` to procurement; procurement echoes back `procurement.requisition.approved`/`.rejected` and PM updates the requisition status automatically |
| POST | `/api/v1/files` | `pm.mutate_workspace` | See §6 |
| GET | `/api/v1/files/:id` | `pm.view_workspace` | See §6 |
| POST | `/api/v1/workspace/members` | `pm.admin` | Body `{userId, role, member?}` — adds to membership table + (if `member` populated) inserts into the document's Members array. The handler auto-anchors `member.userId` from `userId`. |
| PATCH | `/api/v1/workspace/org` | `pm.admin` | Body `{orgId}` — set workspace org |

---

## 5. The WebSocket Channel

**Endpoint:** `GET /api/v1/ws/workspace?token=<jwt>` (upgrade to WS).

**Flow:**
1. Frontend opens the WS with `?token=<access_token>`.
2. Server validates the token (same audience requirement), upgrades, and
   immediately pushes one frame containing the full document.
3. Every subsequent server-side mutation — by this client, by another
   client, or by an upstream consumer like
   `procurement.requisition.approved` — pushes another full frame.
4. The frame envelope is **always** `{type:"workspace", data, version}`.

> **Future:** A lighter delta/event channel (`message.created`, etc.) is
> documented but **not implemented** — see [REALTIME_FUTURE.md](./REALTIME_FUTURE.md).
> v1 clients should merge chat/message slices from workspace pushes (see
> iagprojects `pm` `lib/pm/workspace-merge.ts`).

```ts
const ws = new WebSocket(
  `${WS_BASE}/api/v1/ws/workspace?token=${encodeURIComponent(accessToken)}`,
);

ws.onmessage = ev => {
  const frame = JSON.parse(ev.data);
  if (frame.type === "workspace") {
    store.replaceWorkspace(frame.data, frame.version);
  }
};

ws.onclose = () => scheduleReconnect();  // backoff 1s → 30s
```

### Fan-out across replicas

PM uses Redis Pub/Sub when `REDIS_URL` is set (
[main.go](../main.go) wires `realtime.NewRedisBridge`). A mutation
handled on replica A is published to a per-user Redis channel; replicas
B/C pick it up and push to any WebSocket they own for that user. So
clients reconnect to any replica and still receive their owner's
mutations.

### Reconnect

PM does not implement `Last-Event-ID` or replay. On reconnect, the
server sends the current full document — so the client always
re-syncs in one frame. Implement exponential backoff client-side
(1 s → 2 s → 4 s, cap 30 s).

### Notifications & desktop notifications

The same WS channel carries every state change. To surface a desktop
notification when `notifications` grows, diff the inbound document's
`notifications` array against the previous version and trigger
`Notification.requestPermission()` + `new Notification(...)` for any
new entry marked `mention: true` and not `read`. Workspace flag
`desktopNotificationsEnabled` lets the user opt out.

---

## 6. File Uploads

PM's file API is **JSON-only** — no `multipart/form-data`. The
frontend base64-encodes the file (or reads it as a data URL) and POSTs
it on the `data` field. The server detects payloads > 256 bytes,
persists them to the blob store, and rewrites the document's
`WorkspaceFile.data` to `blob:<uuid>`:

```ts
POST /api/v1/files
Content-Type: application/json

{
  "n": "spec.pdf",
  "s": "1.2 MB",
  "t": "PDF",
  "i": "file-pdf",
  "d": "2026-05-28",
  "project": "iag",
  "data": "data:application/pdf;base64,JVBERi0xLjQK…"
}
→ 200 { "file": { "n": "spec.pdf", "data": "blob:5fa3…", ... }, "version": 124 }
```

To retrieve the bytes:

```ts
GET /api/v1/files/5fa3…
Authorization: Bearer …
→ 200 application/pdf
   Content-Disposition: inline; filename="spec.pdf"
   <binary>
```

The `:id` path param accepts either the bare UUID or the `blob:<uuid>`
form. Anyone with `pm.view_workspace` for the workspace can fetch the
file; the handler verifies blob → workspace ownership.

**Practical sizing:** PM itself enforces **no** request-body limit — the
effective cap is whatever the API gateway imposes. The bundled gateway
nginx config sets `client_max_body_size 20m`, so plan for ~20 MB and keep
uploads comfortably under it (confirm against your own gateway deploy).
For larger files, upload to your own storage and store the URL as a string
in `data` (skip blob persistence by keeping `data` under 256 bytes).

---

## 7. Base URLs

| Environment | REST base | WebSocket base |
|---|---|---|
| Local direct | `http://localhost:4102/api/v1` | `ws://localhost:4102/api/v1` |
| Local via gateway | `http://localhost:8080/api/v1/project-management/api/v1` | `ws://localhost:8080/api/v1/project-management/api/v1` |
| Production | `https://iag-api-gateway-production.up.railway.app/api/v1/project-management/api/v1` | `wss://iag-api-gateway-production.up.railway.app/api/v1/project-management/api/v1` |

> **Public entry point is the gateway only.** In production the frontend
> talks exclusively to `https://iag-api-gateway-production.up.railway.app`.
> Individual services run on Railway's private network
> (`iag-project-management.railway.internal`) and are **not** meant to be
> called via their own `*.up.railway.app` host — that bypasses the gateway's
> rate limiting, CORS, and request-ID handling.

> The `/api/v1` repeats because the gateway prefix is
> `/api/v1/project-management` and the service itself mounts everything
> under `/api/v1`. Plan to factor the gateway prefix out via a config
> rewrite if it bothers you.

### Required frontend env vars

```env
# Local (via gateway)
NEXT_PUBLIC_PM_API_URL=http://localhost:8080/api/v1/project-management/api/v1
NEXT_PUBLIC_PM_WS_URL=ws://localhost:8080/api/v1/project-management/api/v1
NEXT_PUBLIC_AUTH_API_URL=http://localhost:8080/api/v1/authentication
```

```env
# Production (Railway, via gateway)
NEXT_PUBLIC_PM_API_URL=https://iag-api-gateway-production.up.railway.app/api/v1/project-management/api/v1
NEXT_PUBLIC_PM_WS_URL=wss://iag-api-gateway-production.up.railway.app/api/v1/project-management/api/v1
NEXT_PUBLIC_AUTH_API_URL=https://iag-api-gateway-production.up.railway.app/api/v1/authentication
```

### CORS

Origins are configured via `CORS_ORIGIN` (comma-separated, default
`http://localhost:3000`). Allowed methods: GET/POST/PUT/PATCH/DELETE/OPTIONS.
Allowed headers: `Content-Type, Authorization, If-Match, X-Workspace-User`.
Exposed: `ETag`. Credentials flag is on (legacy), but PM uses the
Authorization header — no cookies.

Request bodies are capped at the **gateway** (the bundled nginx config
uses `client_max_body_size 20m`); PM imposes no body limit of its own.

---

## 8. Error Conventions

Bodies follow `{"error":"message"}`. Status codes drive frontend
branching:

| Status | Meaning | Frontend action |
|---|---|---|
| 400 | Bad request body / validation | Show inline field error |
| 401 | Missing / invalid / expired token | Refresh; on second 401, re-login |
| 403 | Permission denied | Hide the UI control |
| 404 | Resource not found | Soft state — re-fetch document |
| 409 | Version conflict (workspace PUT or entity mutation) | Re-fetch `/workspace`, merge, retry |
| 413 | Request body over the gateway cap (~20 MB nginx default) | Trim or upload to external storage |
| 500 | Server error | Generic toast + retry |
| 503 | DB / Redis unavailable | Show maintenance banner |

The optimistic-lock `409` is the one to handle explicitly. The fine-grained
entity endpoints do an internal retry once on conflict (
[workspace/service.go](../internal/workspace/service.go) `Mutate`) so
you rarely see this from `PATCH /tasks/:id`; you'll mainly see it from
full document PUTs.

---

## 9. Event Bus (Server-side context)

PM both **publishes and consumes** on Kafka topic `iag.commercial`. The
frontend never connects to Kafka — but knowing what flows is useful for
debugging why state changed when no UI action occurred.

### Published by PM

| Event type | When | Payload keys |
|---|---|---|
| `pm.alert.raised` | Background `RunReminders` job (every 15 m by default) — overdue tasks, message reminders | `channel, recipient, templateId, variables` |
| `pm.requisition.submitted` | POST `/requisitions` | `requisitionId, workspaceOwnerUserId, title, amount, currency, status, requestedBy, forDept, urgency, payee, justification` |
| `pm.task.assigned` | Task created/patched with non-empty assignee (24h dedupe) | `taskId, taskName, assignee, actor, actorEmail?, recipient?` |
| `pm.mention.created` | `@mentions` parsed from task comments + chat messages | `mentionee, author, text, context, contextId` |

These flow to **notifications** (which fan out as email/SMS) and to
**procurement** (which imports the requisition).

### Consumed by PM (and reflected in the document)

These cause server-side mutations that you'll see arrive on the
WebSocket:

| Event type | What happens to the document |
|---|---|
| `procurement.requisition.approved` | The matching `Requisition.status` flips to `"approved"`, `ApprovedBy` + `ApprovedAt` set; audit entry appended |
| `procurement.requisition.rejected` | Same, status `"rejected"`, `RejectedAt` set |
| `contracts.contract.created` | Audit entry only (on every workspace) |
| `contracts.contract.updated` / `.deleted` | Audit entry only |
| `contracts.contract.status_changed` | Audit + WorkspaceNotification on workspaces whose Members include the contract `cs` (owner) |
| `contracts.milestone.due_soon` | WorkspaceNotification on workspaces whose Members include the milestone `owner` |
| `contracts.assistance.requested` | Mention-style WorkspaceNotification on workspaces whose Members include `from` |
| `auth.user.deactivated` | `Member.active` flipped to `false`; open task `assignee` cleared; audit entry |
| `auth.user.reactivated` | `Member.active` flipped to `true`; audit entry |

So when the WebSocket pushes a new frame and no local user did anything,
look for an upstream event of this kind. The audit log is the canonical
trail.

---

## 10. What's Missing (Not Shipped Today)

If you hit any of these and need them, file an issue against the PM
repo:

- **No OpenAPI spec.** Routes are hand-registered in
  [`internal/router/router.go`](../internal/router/router.go) and
  [`internal/handlers/entities.go`](../internal/handlers/entities.go).
- **No multipart upload.** Use the JSON `data` field with base64 / data
  URL.
- **No `Last-Event-ID` on the WebSocket.** Reconnect → full document
  resync.
- **No declarative entity queries** — the GET is the full document.
  Filter and paginate client-side. Document size is typically < 500 KB.
- **Limited batch endpoints.** Tasks support `POST /tasks/bulk` (create),
  `POST /tasks/bulk-patch` (update), and `POST /tasks/delete-batch`
  (delete). Other entities are one-per-request.
- **No shared TS client package** (unlike `@iag/fleet-client`). Use
  `fetch` + the route table here.

---

## 11. Quickstart Checklist

For a new PM frontend project:

- [ ] Set `NEXT_PUBLIC_PM_API_URL`, `NEXT_PUBLIC_PM_WS_URL`,
      `NEXT_PUBLIC_AUTH_API_URL` (§7).
- [ ] Implement OAuth password-grant login against auth.
- [ ] Hold access token in memory; silent refresh ~1 min before TTL (§1).
- [ ] On app load: `GET /api/v1/workspace` → hydrate client store with
      `{data, version}` (§4.2).
- [ ] Open `WebSocket` to `/ws/workspace?token=<jwt>`; on every
      `type:"workspace"` frame, replace the store atomically (§5).
- [ ] For each user action, call the matching entity endpoint (§4.4–§4.8);
      do NOT call `PUT /workspace` per-mutation — that's the offline-sync
      path only.
- [ ] On 409 from `PUT /workspace`, re-fetch and retry the merge.
- [ ] Gate UI on permissions: `pm.mutate_workspace` (read-only fallback),
      `pm.admin` (Members/Org panels) (§3).
- [ ] For file attachments < 6 MB, base64 → JSON `data` field; for
      larger, upload externally and pass URL (§6).
- [ ] Implement WebSocket reconnect with exponential backoff (§5).
- [ ] Diff inbound `document.notifications` against previous version
      for desktop notifications when `desktopNotificationsEnabled` is
      true (§5).

---

## See Also

- [PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md) — backend
  deployment + env config.
- [docs/PERMISSIONS.md](./PERMISSIONS.md) — server-side permission
  registration flow (if/when added).
- Sibling guides:
  [Fleet](../../../operations/fleet/docs/FRONTEND_INTEGRATION.md),
  [Contract-Management](../../contract-management/docs/FRONTEND_INTEGRATION.md).
- Auth `/oauth/token` —
  [shared/services/authentication](../../../../shared/services/authentication).
