# Work Log

## 1. Current Understanding

<current_mode>
active
</current_mode>

<active_task>
none
</active_task>

<parked_tasks>
- PR preview builds via pkg-pr-new (workflow is in place, untested)
- Per-project auth on server
- Add deprecation notice to plugins/reader-vite in gsd-lite repo pointing to this monorepo
- Set up Hetzner to deploy from GitHub monorepo instead of local copy
</parked_tasks>

<vision>
Central distribution layer for GSD-Lite artifacts. Any remote session can push context to a readable dashboard with a single command, regardless of client. Index UI makes 30+ projects navigable.
</vision>

<decisions>
- Migrated reader from gsd-lite/plugins/reader-vite/ to standalone repo for independent npm publishing
- Use npm OIDC trusted publishing (no secrets in CI)
- Server credentials externalized via .env (was hardcoded)
- Node.js autoSelectFamily disabled to fix fetch on dual-stack DNS with unreachable IPv6
- Index UI is server-rendered Go template with client-side JS (not a separate SPA build)
- Origin base_path extracted from uploaded index.html for server/mount context in path display
- Path truncation: first 2 segments (server ID like /home/ken) + last 2 (parent/project), ellipsis between
- GSD-Lite stamped on Hetzner deployment at /root/dev/gsd-reader/gsd-lite/
</decisions>

<blockers>
None
</blockers>

<next_action>
Set up Hetzner to deploy from GitHub monorepo instead of local copy
</next_action>

---

## 2. Key Events

| Date | Event | Impact |
|------|-------|--------|
| 2026-03-24 | Diagnosed fetch failure as Node.js v22 autoSelectFamily bug | Unblocked all dump uploads from pi + personal hosts |
| 2026-03-24 | Migrated reader-vite to standalone gsd-reader repo | Independent npm publishing, cleaner separation |
| 2026-03-24 | Set up npm OIDC publish workflow | Zero-secret CI, provenance attestation |
| 2026-03-24 | Added Go server to monorepo | Client + server in one place, credentials externalized |
| 2026-03-24 | Published v0.2.27 to npm | First release from new repo with fetch fix |
| 2026-04-06 | Index UI overhaul: pagination, search, metadata, bulk delete | 37 projects now navigable with keyword search and server-context paths |
| 2026-04-06 | Path display: origin base_path extraction + smart truncation | Users can map projects to servers at a glance (/home/ken = Thinkpad, /root/dev = Hetzner) |
| 2026-04-06 | Synced latest server/main.go to monorepo (de93b94) | Monorepo now has all UI improvements — 809 insertions |
| 2026-04-06 | GSD-Lite stamped on Hetzner deployment | /root/dev/gsd-reader/gsd-lite/ now discoverable via list_gsd_lite_dirs |

---

## 3. Atomic Session Log

### [LOG-001] - [BUG] [DISCOVERY] - Node.js v22 fetch fails on Cloudflare Tunnel domains - Task: fetch-fix
**Timestamp:** 2026-03-24
**Depends On:** None (initial investigation)

---

#### Symptom

`npx @luutuankiet/gsd-reader dump` fails with `fetch failed` on both pi server and personal host (thinkpad). The identical upload works fine from the Python fs-mcp auto-dump (`urllib.request`).

```
[dump] Uploading 46KB markdown -> https://gsd.kenluu.org/upload-markdown/pi-gsd/gsd-lite
[dump] ❌ Upload failed: fetch failed
```

#### Investigation

Systematic isolation of the failure:

| Test | Result | What it proves |
|------|--------|----------------|
| `curl https://gsd.kenluu.org/` | ✅ HTTP 401 in 1.3s | Server reachable, not a network/DNS issue |
| Node.js `fetch()` (default) | ❌ ETIMEDOUT on all 4 addresses | Node.js-specific connection failure |
| Node.js `https` module (default) | ❌ ETIMEDOUT | Not fetch/undici-specific — affects all Node.js |
| DNS resolution from Node.js | ✅ Both A + AAAA records resolve | DNS is fine |
| Node.js `https.get({family: 4})` | ✅ HTTP 401 | IPv4-only works — IPv6 interference |
| Raw TCP to `104.21.56.184:443` | ✅ Connected | TCP layer is fine |
| `net.setDefaultAutoSelectFamily(false)` | ✅ fetch works | **Root cause confirmed** |
| `dns.setDefaultResultOrder('ipv4first')` | ❌ Still broken | DNS order doesn't fix it |
| `NODE_OPTIONS='--dns-result-order=ipv4first'` | ❌ Still broken | CLI flag doesn't fix it either |
| Python `urllib.request` (fs-mcp) | ✅ Always worked | Python uses OS sockets, no Happy Eyeballs |

#### Root Cause

Node.js v20+ enables `autoSelectFamily` (Happy Eyeballs / RFC 8305) by default with a 250ms attempt timeout. The domain `gsd.kenluu.org` is behind a Cloudflare Tunnel — Cloudflare's anycast DNS always returns both AAAA and A records.

On hosts where IPv6 is unreachable (both pi and personal host):
1. Node.js resolves 4 addresses: 2×IPv6 + 2×IPv4
2. Happy Eyeballs tries IPv6 first → `ENETUNREACH`
3. The algorithm's connection multiplexing logic **corrupts the IPv4 fallback** → `ETIMEDOUT` on all addresses
4. `fetch` throws generic `"fetch failed"`

curl handles this correctly because libcurl has its own connection fallback logic.

#### Fix Applied

One line added to `cli.cjs` after the require block:

```javascript
// Fix Node.js v20+ Happy Eyeballs (autoSelectFamily) breaking fetch on
// dual-stack DNS with unreachable IPv6 (e.g., Cloudflare Tunnel domains)
require('net').setDefaultAutoSelectFamily(false);
```

File: `cli.cjs:28-30`

#### Verification

```
[GSD-Lite Reader] v0.2.27
[dump] Uploading 2KB markdown -> https://gsd.kenluu.org/upload-markdown/troubleshoot-gsd-reader/gsd-lite
[dump] ✅ Upload complete: Rendered troubleshoot-gsd-reader/gsd-lite
```

---

📦 STATELESS HANDOFF (for future agents reading this log)
**Dependency chain:** LOG-001 (root)
**What was decided:** Disable `autoSelectFamily` in cli.cjs to fix Node.js fetch on dual-stack DNS with unreachable IPv6
**Next action:** Monitor if Node.js upstream fixes this in future versions; the workaround is safe and has no downside
**If pivoting:** The fix is in `cli.cjs:28-30` — if reverting, remove those 3 lines and test on a host with working IPv6

---

### [LOG-002] - [EXEC] - Migrate reader-vite to standalone gsd-reader repo - Task: repo-migration
**Timestamp:** 2026-03-24
**Depends On:** LOG-001 (fetch fix included in migration)

---

#### Context

The reader lived at `gsd-lite/plugins/reader-vite/` inside the main gsd-lite monorepo. This coupled npm publishing to the monorepo's release cycle and prevented using GitHub OIDC trusted publishing (which requires the npm package to be linked to a specific GitHub repo).

#### What Was Done

| Step | Action | Verification |
|------|--------|--------------|
| 1 | Created blank repo `github.com/luutuankiet/gsd-reader` | ✅ |
| 2 | Cloned and copied all source files (no node_modules/lockfiles) | ✅ 19 files |
| 3 | Updated `package.json`: repo URL → new repo, version → 0.2.27 | ✅ |
| 4 | Added `.gitignore` | ✅ |
| 5 | Added `.github/workflows/publish.yml` — npm OIDC publish | ✅ |
| 6 | Replaced workflow with battle-tested OIDC setup (pnpm, no `registry-url`, dist-tag detection, PR preview) | ✅ |
| 7 | Added `pnpm-lock.yaml` and `packageManager` field | ✅ |
| 8 | Fixed CI: version bump step skips when package.json already matches tag | ✅ |
| 9 | Tagged `v0.2.27` and pushed → GHA published to npm | ✅ |
| 10 | Added Go server (`server/`) with externalized credentials | ✅ |
| 11 | Rewrote README with architecture docs and mermaid diagrams | ✅ |

#### CI Fix: "Version not changed" error

First GHA run failed because `pnpm version "0.2.27"` errors when `package.json` already has that version. Fixed by adding a guard:

```yaml
- name: Set version from tag
  run: |
    VERSION="${GITHUB_REF_NAME#v}"
    CURRENT=$(node -p "require('./package.json').version")
    if [ "$VERSION" != "$CURRENT" ]; then
      pnpm version "$VERSION" --no-git-tag-version
    fi
```

#### Security: Credentials externalized

The original `docker-compose.yaml` on hetzner had `AUTH_PASS=659142` hardcoded. The migrated version uses `${AUTH_PASS}` env var references with a `.env.example` template. `.env` and `server/persistent/` are gitignored.

---

📦 STATELESS HANDOFF (for future agents reading this log)
**Dependency chain:** LOG-002 ← LOG-001
**What was decided:** Reader is now a standalone repo with its own npm publish pipeline; server code co-located
**Next action:** Verify v0.2.27 works from pi server; clean up old plugins/reader-vite/ in gsd-lite monorepo
**If pivoting:** The old code still exists at `gsd-lite/plugins/reader-vite/` — it has the same fix applied locally but is no longer the publish source

---

### [LOG-003] - [EXEC] - Index UI overhaul + monorepo sync + Hetzner stamp - Task: ad-hoc
**Timestamp:** 2026-04-06
**Depends On:** LOG-002 (monorepo established, server code co-located)

---

#### What Was Built

Rewrote the Go server's index page from a bare project list into a full-featured dashboard. All changes in `server/main.go` — the entire UI is an inline Go HTML template with ~400 lines of client-side JS.

#### Features Added

**Pagination** — configurable page size (10/15/25/50), smart page range with ellipsis for large sets, scroll-to-top on page change.

**Keyword search** — debounced (200ms) client-side filter across project name + PROJECT.md description + origin path. Multi-term AND matching (e.g., "hetzner go" finds gsd-reader). Only searches PROJECT.md content (extracted via base64 decode), not full WORK.md logs — deliberate choice since some projects have 80+ log entries.

**Project metadata per card:**
- Project name (linked to reader app)
- Last modified date
- Origin path with smart truncation + tooltip + copy button
- PROJECT.md description (collapsible, overflow-detected "more/less" toggle)

**Path display** — extracts `window.__GSD_BASE_PATH__` from each project's `index.html`. This is the absolute path from the origin machine that uploaded the project. Truncation logic:
- ≤4 path segments → show full path
- >4 segments → first 2 (server identifier) + `…` + last 2 (project context)

Examples after truncation:

| Display Path | Server |
|---|---|
| `/root/dev/claude-docker` | Hetzner |
| `/Users/luutuankiet/dev/looker_dev_tools` | Mac |
| `/home/ubuntu/dev/fs-mcp` | Personal/Ubuntu |
| `/home/ken/…/demo_joon_agents/joon-agents` | Thinkpad |
| `/workspaces/EVERYTHING/…/worktrees/feat__lookml_dashboard` | Joon VM |

**Bulk delete** — checkbox per project, "Select All" for current page, confirmation modal listing selected projects, `POST /api/projects/delete` endpoint with `os.RemoveAll`. Toast notifications for success/error.

#### New Go Functions

| Function | Purpose |
|---|---|
| `extractProjectDescription(indexPath)` | Decodes `__PROJECT_CONTENT_B64__` from index.html, parses PROJECT.md, returns first substantial paragraph (>20 chars, skips headers/stamps) |
| `extractBasePath(indexPath)` | Decodes `__GSD_BASE_PATH__` from index.html, strips `/gsd-lite` suffix, applies smart truncation |
| `handleAPIProjects(w, r)` | `GET /api/projects` — returns JSON array of all projects with metadata |
| `handleAPIDelete(w, r)` | `POST /api/projects/delete` — accepts `{"paths": [...]}`, removes directories, returns `{"deleted": [...]}` |

#### Monorepo Consolidation

Three copies existed before this session:
1. **Hetzner** `/root/dev/gsd-reader/main.go` — live server, **no git repo**
2. **Personal** `/home/ubuntu/dev/gsd-lite/plugins/reader-vite/` — original client, in `luutuankiet/gsd-lite` repo
3. **Personal** `/home/ubuntu/dev/gsd-reader-migrate/` — monorepo at `luutuankiet/gsd-reader`

**Action:** Cross-host file transfer via base64 encoding. Hetzner's `main.go` (1232 lines) → base64 → decoded on personal host → `server/main.go`. MD5 verified: `a21c025c` on both sides. Client code was already identical (only diff: version 0.2.26→0.2.27 and repo URL).

Pushed as commit `de93b94` on `luutuankiet/gsd-reader` main branch.

#### GSD-Lite Stamp on Hetzner

Scaffolded gsd-lite at `/root/dev/gsd-reader/gsd-lite/` with full PROJECT.md, ARCHITECTURE.md, and WORK.md. Removed `.claude/` agent scaffolding (not needed for non-git deployment directory). Project now discoverable via `list_gsd_lite_dirs` on Hetzner (4th project alongside artifact-server, claude-docker, hetzner-server).

#### Iteration Detail: Path Display

The path display went through 3 iterations in this session:
1. **v1:** Stripped `gsd-lite` suffix, showed data-dir subpath → too little context (just `claude-docker`)
2. **v2:** Extracted `__GSD_BASE_PATH__`, truncated to first 1 + last 2 segments → collapsed the server-identifying segment (`/home/…/dev/fs-mcp` — could be any host)
3. **v3 (final):** First **2** segments + last 2 → preserves server identity (`/home/ubuntu/dev/fs-mcp` = clearly Personal host)

---

📦 STATELESS HANDOFF (for future agents reading this log)
**Dependency chain:** LOG-003 ← LOG-002 ← LOG-001
**What was decided:** Index UI overhauled with pagination/search/delete/metadata. Origin base_path used for path display. Monorepo synced (de93b94). Hetzner deployment stamped with gsd-lite.
**Next action:** (1) Set up Hetzner to deploy from GitHub monorepo instead of local copy. (2) Add deprecation notice to `plugins/reader-vite/` in gsd-lite repo.
**Key file:** `server/main.go` — entire server is one file, index template is inline starting ~L60
**If pivoting:** Live Hetzner deployment has identical code to monorepo's `server/main.go` as of de93b94. The Hetzner copy at `/root/dev/gsd-reader/` also has a gsd-lite stamp with its own LOG-001.
