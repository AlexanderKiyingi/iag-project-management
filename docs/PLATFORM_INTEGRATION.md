# IAG Project Management — platform integration

Go/Gin service behind the **API gateway**, using **iag-authentication** for IAM and **Postgres** for per-user workspace documents.

## Services

| Service | Integration |
|---------|-------------|
| **iag-authentication** | Gateway JWT; registers `pm.*` permissions at startup |
| **iag-users** | Org membership + billing identity; PM validates org links and proxies org APIs |
| **iag-api-gateway** | Public ingress at `/api/v1/project-management/api/v1/...` |
| **Redis** | WebSocket fan-out across replicas (`REDIS_URL`) |
| **iag-notifications** | Subscribes to `iag.commercial` for `pm.alert.raised`, `pm.task.assigned`, and `pm.mention.created` |

## Environment

| Variable | Purpose |
|----------|---------|
| `ADDR` | Listen address (default `:4102`) |
| `DATABASE_URL` | Postgres (`iag_pm`) |
| `REDIS_URL` | Optional WS pub/sub |
| `JWT_ISSUER` / `JWKS_URL` | Auth service URLs for Bearer verification |
| `AUDIENCE` | Required aud claim on inbound tokens (default `iag.project-management`) |
| `SERVICE_CLIENT_ID` / `SERVICE_CLIENT_SECRET` / `AUTH_TOKEN_URL` | Service-account credentials for outbound calls + permission registration |
| `PUBLIC_API_URL` | Gateway origin for status |
| `USERS_API_URL` | iag-users gateway base (default `{PUBLIC_API_URL}/api/v1/users`; client calls `/v1/...` → `/api/v1/users/v1/...`) |
| `GATEWAY_API_PREFIX` | `/api/v1/project-management` |
| `NOTIFY_DEFAULT_RECIPIENT` | Optional inbox for task/mention/reminder emails via notifications |
| `TASK_DUE_REMINDER_DAYS` | Jobs: tasks due within N days (default `7`) |

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/workspace` | Load workspace JSON + `version` |
| PUT | `/api/v1/workspace` | Save document (`If-Match` / body `version`) |
| GET | `/api/v1/ws/workspace?token=` | WebSocket pushes `{"type":"workspace","data":...,"version":N}` |
| * | `/api/v1/tasks`, `/goals`, `/sprints`, `/chats`, … | Per-entity REST (see `internal/handlers/entities.go`) |
| POST | `/api/v1/requisitions` | Creates requisition + publishes `pm.requisition.submitted` (consumed by **iag-procurement**) |
| POST | `/api/v1/workspace/members` | Share workspace (`pm.admin`) |
| PATCH | `/api/v1/workspace/org` | Set `org_id` (validated against iag-users) |
| GET | `/api/v1/workspace/org` | Linked org id + metadata |
| GET | `/api/v1/orgs` | List caller's platform orgs (iag-users proxy) |
| GET | `/api/v1/orgs/:orgId/members` | Org members (iag-users proxy) |

Send `X-Workspace-User` (initials) on mutating requests for audit attribution.

## Events (Kafka)

| Topic | Type | Notes |
|-------|------|-------|
| `iag.commercial` | `pm.requisition.submitted` | Consumed by **iag-procurement** |
| `iag.commercial` | `pm.task.assigned` | Emitted on create and assignee patch; notifications when `NOTIFY_DEFAULT_RECIPIENT` set |
| `iag.commercial` | `pm.mention.created` | Emitted from task comments and chat messages with `@mentions` |
| `iag.commercial` | `pm.alert.raised` | Requisition submit + scheduled reminder jobs (`pm.requisition.submitted`, `pm.message.reminder`, `pm.task.due_soon`) |
| `iag.commercial` | `auth.user.deactivated` / `auth.user.reactivated` / `auth.user.updated` | PM consumer syncs workspace members |
| `iag.commercial` | `users.org.member_added` / `users.org.member_removed` | PM consumer syncs org-linked workspace teams |

## Scheduled jobs

```bash
DATABASE_URL=... EVENT_BUS_ENABLED=true KAFKA_BROKERS=localhost:19092 \
  NOTIFY_DEFAULT_RECIPIENT=ops@iag.local \
  go run ./cmd/jobs --reminders
```

Docker Compose runs this as the `project-management-jobs` one-shot service after the API is healthy.

## Local development

```bash
# Platform stack
pnpm infra:up

# PM API via gateway
curl http://localhost:8080/api/v1/project-management/ready

# Direct (jwt mode)
cd services/commercial/project-management
cp .env.example .env
pnpm dev:pm   # from repo root
```

Frontend: set `NEXT_PUBLIC_PM_API_URL=http://localhost:8080/api/v1/project-management` and use a platform JWT from authentication login.
