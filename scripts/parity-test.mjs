/**
 * PM frontend ↔ backend route parity checker.
 * Run: node services/commercial/project-management/scripts/parity-test.mjs
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const pmClient = readFileSync(join(root, "pm/lib/pm-client.ts"), "utf8");
const pmApi = readFileSync(join(root, "pm/lib/pm-api.ts"), "utf8");
const entitiesGo = readFileSync(join(root, "internal/handlers/entities.go"), "utf8");
const workspaceGo = readFileSync(join(root, "internal/handlers/workspace.go"), "utf8");

/** @type {{ method: string, path: string, source: string }[]} */
const feRoutes = [];

function addFe(method, path, source) {
  const norm = path.replace(/\$\{[^}]+\}/g, ":param").replace(/encodeURIComponent\([^)]+\)/g, ":param");
  feRoutes.push({ method: method.toUpperCase(), path: norm, source });
}

// pm-api.ts
for (const m of pmApi.matchAll(/fetch\(pmApiPath\("([^"]+)"\)[^)]*method:\s*"(\w+)"/g)) {
  addFe(m[2], m[1], "pm-api.ts");
}
for (const m of pmApi.matchAll(/fetch\(pmApiPath\("([^"]+)"\)/g)) {
  if (!feRoutes.some((r) => r.path === m[1] && r.method === "GET")) {
    addFe("GET", m[1], "pm-api.ts");
  }
}
addFe("GET", "/ws/workspace", "pm-api.ts (WebSocket)");

// pm-client.ts restFireWithActor (backtick or quoted paths)
for (const m of pmClient.matchAll(
  /restFireWithActor\([^,]+,\s*"(\w+)",\s*(?:`([^`]+)`|"([^"]+)")/g,
)) {
  addFe(m[1], m[2] ?? m[3], "pm-client.ts");
}
for (const m of pmClient.matchAll(/fetch\(pmApiPath\("([^"]+)"\)[\s\S]*?method:\s*"(\w+)"/g)) {
  addFe(m[2], m[1], "pm-client.ts (await)");
}

/** @type {{ method: string, path: string }[]} */
const beRoutes = [];
const routeRe = /rg\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"/g;
for (const src of [entitiesGo, workspaceGo]) {
  let m;
  while ((m = routeRe.exec(src))) {
    const p = m[2].startsWith("/") ? m[2] : `/${m[2]}`;
    beRoutes.push({ method: m[1], path: p });
  }
}

function matchPath(fePath, bePath) {
  const feParts = fePath.split("/").filter(Boolean);
  const beParts = bePath.split("/").filter(Boolean);
  if (feParts.length !== beParts.length) return false;
  return feParts.every((p, i) => p === beParts[i] || p === ":param" || beParts[i].startsWith(":"));
}

const matched = [];
const feOnly = [];
const beOnly = [];

for (const fe of feRoutes) {
  const hit = beRoutes.find((be) => be.method === fe.method && matchPath(fe.path, be.path));
  if (hit) matched.push({ fe, be: hit });
  else feOnly.push(fe);
}

for (const be of beRoutes) {
  if (!feRoutes.some((fe) => fe.method === be.method && matchPath(fe.path, be.path))) {
    beOnly.push(be);
  }
}

// Documented README expectations (legacy pmapi)
const readmeGaps = [
  { item: "POST /api/v1/auth/login", note: "README still references pmapi login; backend uses platform JWT only" },
  { item: "components/login-page.tsx", note: "Referenced in README but not present (login redirects to home)" },
];

// Behavioral / payload gaps (static analysis)
const behaviorGaps = [
  { severity: "high", area: "Auth", gap: "No PM-hosted login; FE uses localStorage demo or external platform token" },
  { severity: "high", area: "RBAC", gap: "FE pmCan* / pmAuthCan always true; server enforces pm.* permissions when gateway sends them" },
  { severity: "medium", area: "Members", gap: "POST /workspace/members updates SQL only, not document.members[] shown in Team UI" },
  { severity: "info", area: "Files", gap: "GET /files/:id serves blob storage; FE uses pmFileDataUrl() for attachments" },
  { severity: "info", area: "Messages", gap: "replyTo/edited/deleted bound on POST /messages when sent" },
  { severity: "info", area: "Task comments", gap: "Backend parses @mentions (mentions package)" },
  { severity: "info", area: "Saved views", gap: "Persisted via PATCH /settings savedViews when entity REST enabled" },
  { severity: "low", area: "Notifications", gap: "No entity REST; inbox notifications only via full workspace document" },
  { severity: "low", area: "REST errors", gap: "pm-client fire-and-forget ignores non-2xx; UI can diverge from server" },
  { severity: "low", area: "WebSocket", gap: "Gateway does not proxy WS; requires NEXT_PUBLIC_PM_WS_URL to :4102" },
  { severity: "info", area: "Task create", gap: "Backend sets minimal task defaults (no subtasks/activity); FE merges locally until refresh" },
  { severity: "info", area: "fireRest", gap: "Exported from pm-client but unused" },
];

console.log("═".repeat(60));
console.log("PM Frontend ↔ Backend Parity Report");
console.log("═".repeat(60));
console.log(`\nFrontend routes scanned: ${feRoutes.length}`);
console.log(`Backend routes scanned:  ${beRoutes.length}`);
console.log(`Matched:               ${matched.length}`);
console.log(`Frontend-only:         ${feOnly.length}`);
console.log(`Backend-only:          ${beOnly.length}`);

if (feOnly.length) {
  console.log("\n── Frontend calls without backend route ──");
  for (const fe of feOnly) {
    console.log(`  ${fe.method.padEnd(6)} ${fe.path}  (${fe.source})`);
  }
}

if (beOnly.length) {
  console.log("\n── Backend routes without frontend client ──");
  for (const be of beOnly) {
    console.log(`  ${be.method.padEnd(6)} ${be.path}`);
  }
}

console.log("\n── Documentation / legacy gaps ──");
for (const g of readmeGaps) console.log(`  • ${g.item}: ${g.note}`);

console.log("\n── Behavioral / data model gaps ──");
for (const g of behaviorGaps) {
  console.log(`  [${g.severity}] ${g.area}: ${g.gap}`);
}

console.log("\n── Matched routes (sample) ──");
for (const { fe, be } of matched.slice(0, 8)) {
  console.log(`  ✓ ${fe.method} ${fe.path}`);
}
if (matched.length > 8) console.log(`  … and ${matched.length - 8} more`);

const exitCode = feOnly.length > 0 ? 1 : 0;
process.exit(exitCode);
