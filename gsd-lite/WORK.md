# Work Log

## 1. Current Understanding

<current_mode>
execution
</current_mode>

<active_task>
Repo migration complete. npm v0.2.27 published with fetch fix.
</active_task>

<parked_tasks>
- PR preview builds via pkg-pr-new (workflow is in place, untested)
- Per-project auth on server
- Clean up old plugins/reader-vite/ in gsd-lite monorepo
</parked_tasks>

<vision>
Central distribution layer for GSD-Lite artifacts. Any remote session can push context to a readable dashboard with a single command, regardless of client.
</vision>

<decisions>
- Migrated reader from gsd-lite/plugins/reader-vite/ to standalone repo for independent npm publishing
- Use npm OIDC trusted publishing (no secrets in CI)
- Server credentials externalized via .env (was hardcoded)
- Node.js autoSelectFamily disabled to fix fetch on dual-stack DNS with unreachable IPv6
</decisions>

<blockers>
None
</blockers>

<next_action>
Verify npm v0.2.27 works end-to-end from pi server: npx -y @luutuankiet/gsd-reader@latest dump
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
