# Contributing to Moharrik

Thanks for wanting to help. This guide covers how to get a local setup running, the conventions
the codebase follows, and how a change gets merged.

## Ways to contribute

- Report a bug or request a feature through the [issue forms](https://github.com/fadymondy/moharrik/issues/new/choose).
- Pick up an issue labelled [`good first issue`](https://github.com/fadymondy/moharrik/labels/good%20first%20issue)
  or [`help wanted`](https://github.com/fadymondy/moharrik/labels/help%20wanted). Comment on it first so nobody
  duplicates the work.
- Improve the docs. Typos and unclear sentences are worth a PR.

Security problems are **not** issues. Follow [SECURITY.md](SECURITY.md) instead.

## Local setup

Requirements: Go (version in `go.mod`), Node 24, Docker, and the [togo CLI](https://github.com/togo-framework) (optional; every step has a plain `go` equivalent).

```bash
cp .env.example .env    # set the moharrik_app password in APP_DATABASE_URL and AUTH_SECRET (openssl rand -hex 32)
make db                 # Postgres on :55470

# codegen (sqlc + gqlgen); internal/db/gen and internal/graph/gen are gitignored
sqlc generate && go run github.com/99designs/gqlgen generate   # or: togo generate
make migrate            # schema, app role, constraints, RLS
make api                # API on :8120
cd web && npm install && npm run dev   # web on :3120
```

Open http://localhost:3120, register and create a workspace.

## Before you open a pull request

```bash
go vet ./...
make test                       # unit tests and guards; set TEST_DATABASE_URL for the database suites
make test-e2e                   # cross-workspace isolation sweep
cd web && npm run format:check && npx tsc --noEmit && npm run build
cd extension && npm test
```

CI runs the same, plus a full `docker-compose.selfhost.yml` boot. New routes and MCP tools must be added to the isolation sweep, and every MCP tool must declare a scope.

## Conventions

- **Small, focused PRs.** One concern per PR; refactors separate from behaviour changes.
- **Branch names:** `feat/…`, `fix/…`, `docs/…`, `chore/…` off `main`.
- **Commit messages:** [Conventional Commits](https://www.conventionalcommits.org/) style
  (`feat: …`, `fix: …`, `docs: …`), imperative mood, explain *why* in the body.
- **Tests:** a bug fix comes with a test that fails without it; a feature comes with tests for its
  main path and its permission checks.
- **No secrets** in code, tests, fixtures or screenshots. Configuration goes in `.env`
  (documented in `.env.example`).
- **AI-assisted contributions are welcome.** Agents should read [AGENTS.md](AGENTS.md). You are
  responsible for every line you submit: review it, run it, and test it as if you wrote it.

## Developer Certificate of Origin (DCO)

Every commit must be signed off, certifying the [Developer Certificate of Origin](https://developercertificate.org/):
you wrote the change, or otherwise have the right to submit it under the project's license.

```bash
git commit -s -m "fix: describe the change"
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer that matches your Git identity.
Forgot? `git commit --amend -s` for the last commit, or `git rebase --signoff main` for a branch.

## License

Moharrik is licensed under the [GNU AGPL-3.0](LICENSE). By contributing you agree your contribution
is licensed under the same terms.

## Community

- **Questions and ideas:** [GitHub Discussions](https://github.com/fadymondy/moharrik/discussions)
- **Chat:** [Discord](https://discord.gg/BMERkavdVq)
- **Bugs and feature requests:** [Issues](https://github.com/fadymondy/moharrik/issues/new/choose)

Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
