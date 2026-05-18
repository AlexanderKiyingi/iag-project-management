# IAG Project Management — platform integration

Go/Gin service behind the **API gateway**, using **iag-authentication** for IAM and **Postgres** for per-user workspace documents.

## Services

| Service | Integration |
|---------|-------------|
| **iag-authentication** | Gateway JWT → `X-IAG-*` headers; optional `AUTH_MODE=jwt` for local dev |
| **iag-api-gateway** | Public ingress at `/api/v1/project-management/api/v1/...` |
| **Redis** | WebSocket fan-out across replicas (`REDIS_URL`) |
| **iag-notifications** | (Phase 2) Kafka `notification.requested` |

## Environment

| Variable | Purpose |
|----------|---------|
| `ADDR` | Listen address (default `:4102`) |
| `DATABASE_URL` | Postgres (`iag_pm`) |
| `REDIS_URL` | Optional WS pub/sub |
| `AUTH_MODE` | `gateway` or `jwt` |
| `GATEWAY_INTERNAL_SECRET` | Shared with api-gateway |
| `PUBLIC_API_URL` | Gateway origin for status |
| `GATEWAY_API_PREFIX` | `/api/v1/project-management` |

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/workspace` | Load workspace JSON + `version` |
| PUT | `/api/v1/workspace` | Save document (`If-Match` / body `version`) |
| GET | `/api/v1/ws/workspace?token=` | WebSocket pushes `{"type":"workspace","data":...,"version":N}` |
| * | `/api/v1/tasks`, `/goals`, `/sprints`, `/chats`, … | Per-entity REST (see `internal/handlers/entities.go`) |
| POST | `/api/v1/requisitions` | Creates requisition + publishes `pm.requisition.submitted` |
| POST | `/api/v1/workspace/members` | Share workspace (`pm.admin`) |
| PATCH | `/api/v1/workspace/org` | Set `org_id` for tenancy metadata |

Send `X-Workspace-User` (initials) on mutating requests for audit attribution.

## Events (Kafka)

| Topic | Type |
|-------|------|
| `iag.commercial` | `pm.requisition.submitted`, `pm.task.assigned`, `pm.mention.created` |
| `iag.notifications` | `notification.requested` (requisition emails) |

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
