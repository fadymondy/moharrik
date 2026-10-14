<div align="center">

<img src="docs/assets/logo.png" alt="Moharrik" width="640" />

# Moharrik — محرّك

**The open-source AI engine behind a business: connect your channels and accounts, and Moharrik watches (notifications, trends, research), acts (automation), talks to customers (chat, email, WhatsApp) and remembers them (CRM). Every capability is an MCP tool, so your AI agents can do it too.**

[![CI](https://github.com/fadymondy/moharrik/actions/workflows/ci.yml/badge.svg)](https://github.com/fadymondy/moharrik/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)
[![Discord](https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/BMERkavdVq)
[![GitHub stars](https://img.shields.io/github/stars/fadymondy/moharrik?style=social)](https://github.com/fadymondy/moharrik/stargazers)

[Website](https://moharrik.com) · [Architecture](docs/architecture.md) · [Discord](https://discord.gg/BMERkavdVq) · [Discussions](https://github.com/fadymondy/moharrik/discussions) · [Issues](https://github.com/fadymondy/moharrik/issues)

</div>

## Features

- **Unified inbox**: website chat widget, email mailboxes and WhatsApp conversations in one place,
  with labels, assignment, snoozing and canned replies. Alerts can go to Slack, Telegram and Discord.
- **CRM**: contacts, companies, deals on a pipeline, activities and timelines. Includes vCard and CSV
  import/export and Google Contacts sync.
- **Leads**: embeddable inquiry forms that capture UTM and click IDs. One click converts a lead into
  a contact, a company and a deal.
- **Email marketing**: lists with per-list double opt-in, campaigns, templates and per-workspace
  SMTP. Includes RFC 8058 one-click unsubscribe.
- **Automation**: rules and workflows, plus a browser extension that records and replays steps from
  a library of preset automations. Steps that publish or send are always marked.
- **Watch the world**: a notification center, trend sources, scheduled research and prospect
  discovery, plus Google Search Console, GA4 and YouTube analytics.
- **AI agents over MCP**: a workspace MCP server with explicit scopes (no wildcards). Tokens are
  narrowed to the user's current role on every call, so Claude, ChatGPT and friends work under the
  same permissions as a person. OAuth connectors are included.
- **Multi-tenant and secure by default**: workspaces with owner/admin/manager/member/client roles,
  default-deny routes, and Postgres row-level security on every tenant table.
- **Bilingual**: English and Arabic (RTL), light and dark.

## Quick start

### Hosted

To try Moharrik without installing anything, use the hosted version at
**[moharrik.com](https://moharrik.com)**. Sign up, create a workspace and connect a channel.

### Self-host with Docker Compose

Requirements: Docker with Compose v2.

```bash
git clone https://github.com/fadymondy/moharrik.git && cd moharrik
cp .env.selfhost.example .env.selfhost
# fill in POSTGRES_PASSWORD, MOHARRIK_APP_DB_PASSWORD and AUTH_SECRET
# (generate each with: openssl rand -hex 32)
docker compose -f docker-compose.selfhost.yml --env-file .env.selfhost up -d --build
```

Open http://localhost:3000, register and create a workspace. The stack runs Postgres 17 and a
one-shot migration job. A single app container serves the Go API and the Next.js web on one port.
For a public deployment:

- put a TLS reverse proxy in front;
- set `APP_URL` to the public origin;
- set `COOKIE_SECURE=1`.

Every other setting is documented in [`.env.example`](.env.example): mail, AI providers, research,
Google, push notifications and the CircleXO app store.

To make yourself a platform admin (for the admin MCP server):

```bash
docker compose -f docker-compose.selfhost.yml --env-file .env.selfhost exec app /app/bin/moharrik-admin promote you@example.com
```

### Local development

See [CONTRIBUTING.md](CONTRIBUTING.md#local-setup) and the "Run it" section of
[docs/architecture.md](docs/architecture.md#run-it).

## Architecture

```
   browser · chat widget · browser extension · MCP clients
                        │  one origin: /api, /graphql, /api/mcp, pages
          ┌─────────────▼──────────────────────────────────────────────┐
          │ Go API (ToGO: chi + Huma REST + gqlgen + sqlc)               │
          │  default-deny guard → tenant scope → authz → handlers       │
          │  inbox · CRM · leads · marketing · automation · research    │
          │  MCP (user + admin) · background workers                    │
          │  proxies every other path to Next.js (same container)       │
          └──────────────┬─────────────────────────────────────────────┘
                         │ owner pool + RLS app pool (moharrik_app)
                  ┌──────▼───────┐
                  │  PostgreSQL  │  FORCE row-level security per workspace
                  └──────────────┘
```

| Path | What |
|---|---|
| `cmd/api`, `cmd/migrate`, `cmd/moharrik-admin` | API (and worker), migrator, platform admin CLI |
| `internal/server`, `internal/tenant`, `internal/authz` | Default-deny guard, workspace scope and RLS pool, permissions |
| `internal/inbox`, `internal/channels`, `internal/whatsapp` | Conversations and channels |
| `internal/crm`, `internal/contacts`, `internal/leads` | CRM and leads |
| `internal/marketing` | Lists, campaigns, double opt-in, SMTP |
| `internal/automation`, `internal/rules`, `extension/` | Automation, rules and the browser extension |
| `internal/research`, `internal/trends`, `internal/notifications` | Research, trends, notification center |
| `internal/mcp` | User and admin MCP servers, OAuth |
| `internal/db/schema`, `internal/db/post` | Schema and post-migration SQL (RLS, roles, constraints) |
| `web/` | Next.js app (en/ar) |

Moharrik is built on [ToGO](https://github.com/togo-framework), an API-first Go + React framework.
The engineering notebook in **[docs/architecture.md](docs/architecture.md)** covers tenancy and RLS,
MCP, the modules and framework notes.

## Documentation

- [Architecture and engineering notes](docs/architecture.md)
- [MCP agent provider](docs/mcp/agent-provider.md) · [CircleXO integration](docs/circlexo.md)
- API reference: run the API and open `/docs` (OpenAPI)

## Contributing

Bug reports, docs and code contributions are all welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md), which covers setup, conventions and the DCO sign-off. Then look
for a [`good first issue`](https://github.com/fadymondy/moharrik/labels/good%20first%20issue). AI
coding agents should start with [AGENTS.md](AGENTS.md).

## Community

- [Discord](https://discord.gg/BMERkavdVq): chat with the team and other users
- [GitHub Discussions](https://github.com/fadymondy/moharrik/discussions): questions, ideas, show and tell
- [Issues](https://github.com/fadymondy/moharrik/issues/new/choose): bugs and feature requests
- Security reports: see [SECURITY.md](SECURITY.md)

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Sponsor

Moharrik is free and open source, built and maintained by [Fady Mondy](https://github.com/fadymondy).
If it saves you time or powers your business, please consider supporting its development:

<p>
  <a href="https://github.com/sponsors/fadymondy"><img src="https://img.shields.io/badge/GitHub%20Sponsors-fadymondy-ea4aaa?logo=githubsponsors&logoColor=white" alt="GitHub Sponsors" /></a>
  <a href="https://buymeacoffee.com/fadymondy"><img src="https://img.shields.io/badge/Buy%20Me%20a%20Coffee-fadymondy-ffdd00?logo=buymeacoffee&logoColor=black" alt="Buy Me a Coffee" /></a>
</p>

Every sponsorship goes into maintenance, docs, translations and new features.

## License

Copyright (C) 2026 Fady Mondy. Moharrik is free software, licensed under the
[GNU Affero General Public License v3.0](LICENSE). If you run a modified version as a network
service, you must offer its source to your users. Third-party components keep their own licenses.

**Commercial licensing available:** for a license without the AGPL obligations, contact
[info@3x1.io](mailto:info@3x1.io).
