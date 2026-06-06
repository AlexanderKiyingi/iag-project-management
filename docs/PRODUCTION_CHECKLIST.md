# Project Management — production checklist

Use this before enabling PM in staging/production.

## Required

| Item | Env / setting | Verify |
|------|----------------|--------|
| Database | `DATABASE_URL` → `iag_pm` schema migrated | `GET /ready` returns `database: true` |
| Auth | `JWT_ISSUER`, `JWKS_URL`, `AUDIENCE=iag.project-management` | Mutating API returns 401 without Bearer |
| Service account | `SERVICE_CLIENT_SECRET` set | Startup log: permissions registered |
| Kafka publish | `EVENT_BUS_ENABLED=true`, `KAFKA_BROKERS` | Requisition creates `pm.requisition.submitted` on `iag.commercial` |
| **Kafka consumer** | `CONSUMER_ENABLED=true` | Procurement approval updates requisition status; contract/auth/users events apply |
| DLQ | `CONSUMER_DLQ_TOPIC=iag.dlq.project-management` | Failed events land in DLQ topic (monitor lag) |
| Finance AP | `FINANCE_API_URL` + `SERVICE_CLIENT_SECRET` | Approved requisition sets `financeApRef` and books `PM-REQ-{id}` in finance |

## Recommended

| Item | Notes |
|------|--------|
| Redis | `REDIS_URL` for cross-replica WebSocket fan-out |
| `PUBLIC_API_URL` | Gateway origin for status metadata |
| `USERS_API_URL` | Org proxy (`/v1/orgs`) |
| Reminders | `NOTIFY_DEFAULT_RECIPIENT` + `cmd/jobs --reminders` or in-process loop |
| Uploads volume | `PM_UPLOAD_DIR` writable (not compatible with `readOnlyRootFilesystem` unless mounted) |

## Kubernetes

Manifests: [`deploy/kubernetes/project-management/`](../../../../deploy/kubernetes/project-management/)

1. Copy `secret.example.yaml` → sealed secret / external secrets operator.
2. Apply configmap + deployment + service.
3. Point gateway `UPSTREAM_PROJECT_MANAGEMENT` at `iag-project-management:4102`.

## Smoke test (post-deploy)

```bash
# Health
curl -s https://api.example.com/api/v1/project-management/ready

# Authenticated workspace (platform JWT)
curl -s -H "Authorization: Bearer $TOKEN" \
  https://api.example.com/api/v1/project-management/api/v1/workspace

# Requisition round-trip: create → approve in procurement → PM status approved + financeApRef
```

## Contract ↔ project linking

Contract events may carry:

- `pmProjectId` — PM project id (preferred)
- `workspaceOwnerUserId` — workspace owner
- `zone` — fallback matched to project `id` or `code`

Linked contracts appear on `project.linkedContracts[]`.
