# Project

*Initialized: 2026-03-24*

## What This Is

The distribution and rendering layer for GSD-Lite artifacts. A monorepo holding the CLI client (`@luutuankiet/gsd-reader` npm package), the Go upload server, and the Vite-powered reader app — everything needed to publish, serve, and consume `WORK.md` / `PROJECT.md` / `ARCHITECTURE.md` from any client, on any machine.

## Core Value

Any remote session (pi, thinkpad, cloud VM, CI) can push its GSD-Lite context to a central readable dashboard with a single command.

## Success Criteria

Project succeeds when:
- [x] `npx gsd-reader dump` uploads from any host to the remote server
- [x] Server renders uploaded markdown into a navigable reader app
- [x] fs-mcp auto-dump fires on every artifact write without manual intervention
- [ ] PR preview builds via `pkg-pr-new` for contributor testing
- [ ] Server supports multiple concurrent projects with per-project auth

## Context

GSD-Lite is a pair programming protocol for AI agents. The protocol itself (`@luutuankiet/gsd-lite`) scaffolds structured markdown artifacts that capture decisions, context, and session state. This repo is the **consumer side** — the tooling that makes those artifacts accessible outside the terminal where they were created.

Prior to this repo, the reader lived inside `gsd-lite/plugins/reader-vite/` in the main gsd-lite monorepo. It was migrated out on 2026-03-24 to enable independent npm publishing via GitHub OIDC and cleaner separation of concerns.

## Constraints

- **Zero-dep CLI**: `cli.cjs` must work via `npx` without pre-installation — CommonJS, minimal dependencies
- **Node.js ≥18**: uses native `fetch` (with `autoSelectFamily` workaround for v20+)
- **Server behind Cloudflare Tunnel**: no direct IP access, HTTPS only via cloudflared
- **npm OIDC**: publishing uses trusted publishing, no secrets in CI
