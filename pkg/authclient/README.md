# iag-authclient (vendored)

JWKS/JWT verification for platform gateway auth. Canonical source: `shared/go/authclient` in the IAG monorepo.

When updating JWT verification behavior, sync from monorepo `shared/go/authclient` before releasing project-management.

This copy exists so **standalone** builds (Railway, project-management repo only) do not depend on `../../shared/go/authclient`.
