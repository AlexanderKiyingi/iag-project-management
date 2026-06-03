# PM realtime — future work (delta / event channel)

**Status:** Planned — not implemented.  
**Current (v1):** Workspace WebSocket pushes the full document envelope on every mutation. See [FRONTEND_INTEGRATION.md](./FRONTEND_INTEGRATION.md) §5.

```json
{ "type": "workspace", "data": { …Document… }, "version": 43 }
```

The PM frontend bridges `/api/pm/ws` (or gateway `wss://…/api/v1/ws/workspace?token=`) and merges **chats/messages** (and related slices) in memory — sufficient for live chat UX without a separate backend channel.

---

## When to implement a delta or event channel

Add targeted realtime events (alongside or instead of full workspace frames) when one or more of these become true:

| Trigger | Why |
|--------|-----|
| Workspace documents routinely exceed ~500 KB–1 MB on WS | Full-document pushes hurt latency and mobile bandwidth |
| Chat-only subscribers | Clients need messages without receiving tasks/goals/requisitions on every push |
| High fan-out / many concurrent editors | Reducing payload size lowers Redis pub/sub and gateway load |
| Offline / replay | Clients need `since=eventId` or `since=version` append-only replay, not only latest snapshot |
| Cross-service consumers | Other services need `message.created` without loading the full PM document |

---

## Proposed direction (sketch)

**Option A — Hybrid (recommended if pursued)**  
Keep `type: "workspace"` for bootstrap and conflict recovery; add optional frames:

```json
{ "type": "message.created", "version": 44, "chatId": 3, "message": { … } }
{ "type": "chat.updated", "version": 44, "chat": { … } }
```

Emit from the same `Mutate` → post-persist hook as `BroadcastWorkspace`, so handlers do not maintain two divergent code paths.

**Option B — Event log table**  
Persist `pm_workspace_events` (owner, type, payload, version, time); WS replays from cursor; full workspace push only on connect or `version` gap.

**Option C — Replace workspace pushes**  
Only deltas on WS; clients hold local document and apply patches. Highest client complexity; align with offline `PUT` + `If-Match` story first.

---

## Implementation checklist (future)

- [ ] Define event catalogue (`message.created`, `chat.read`, `task.patched`, …) and JSON schema
- [ ] Document ordering guarantees (monotonic `version` per workspace; idempotency keys)
- [ ] Extend `internal/realtime` hub + Redis bridge for non-workspace frame types
- [ ] Gateway: no change if path stays `/api/v1/ws/workspace`; document frame types in OpenAPI / integration guide
- [ ] Frontend: subscribe handler per `type`; fall back to full workspace on gap (`version` jump > 1 or unknown cursor)
- [ ] Load test: message rate × document size × connected clients
- [ ] Deprecation policy for clients that only handle `type: "workspace"`

---

## Explicitly out of scope for v1

- Per-chat WebSocket URLs (`/ws/chat/:id`)
- Typing indicators / presence (unless added as small ephemeral events later)
- Server-sent events (SSE) parallel to WS — PM standard is WS only

---

## References

- Current broadcast: `internal/workspace/service.go` (`BroadcastWorkspace`)
- WS handler: `internal/handlers/workspace.go` (`ws`)
- Hub: `internal/realtime/hub.go`
- Frontend merge (v1): `iagprojects/pm/lib/pm/workspace-merge.ts`, `lib/pm/realtime.ts`
