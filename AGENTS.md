# AGENTS.md

Guide for coding agents working on courrier, a desktop mail client in Go
built on [MyGo](https://mygo.egoist.dev)'s native UI (no web frontend).

## Read first

- [README.md](README.md): features, install, build and test commands.
- [docs/how-it-works.md](docs/how-it-works.md): the architecture, packages,
  sync, the offline action queue, the outbox and the database.
- [docs/design.md](docs/design.md): layout, typography, color tokens,
  spacing, icons. Follow it for any UI change.
- [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md):
  MyGo rules (UI lifetime, identity, threading, testing). Read it before
  touching UI code.

## Layout

- `main` (repo root): the UI. `app.go` sidebar and window, `mailview.go`
  the three panes, `compose.go`, `settings.go`, `tray.go`, `theme.go`
  (icons, palette, shared widgets).
- `mail/`: IMAP, POP3, SMTP, OAuth2, MIME, and the per-account `Syncer`.
- `store/`: all persistent state, in one SQLite database.
- `resources/`: bundled files. `icon.png` is the app icon (used by
  `go tool mygo build` by default), `tray.png` the tray icon and sidebar logo.
- `build/`, `.mygo/`, `notes/` are git-ignored.

The UI never talks to a server: it reads and writes the cache in `store/`,
and the syncers carry changes to the server.

## Commands

```sh
go tool mygo dev               # run, rebuilding on change
CGO_ENABLED=0 go test ./...    # tests (what CI runs)
go tool mygo vet .             # MyGo-aware vet (CI runs it too)
go tool mygo build             # production build in build/<os>-<arch>/
```

CI also checks `GOOS=darwin|windows|linux go build .` still compiles. Keep
the code cgo-free.

The MyGo source is not in this repo; find the pinned version with
`go list -m -f '{{.Dir}}' github.com/egoist/mygo`. Don't edit it: UI owned
by MyGo plugins (for example the updater window) can't be changed from here.

## Code style

- Match the surrounding code: short doc comments in plain sentences that
  say what a thing is or why, not how.
- Keep slow I/O off the UI thread; publish results with `a.update(...)`.
- Add or update a test next to the code you change (`*_test.go` in the
  same package).

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org):

```
type(scope): summary in the imperative, lower case, no period

Optional body: what changed and why.
```

- Types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `style`,
  `build`, `ci`, `chore`. Releases are `chore(release): vX.Y.Z`.
- Scope is the area touched, e.g. `sidebar`, `tray`, `compose`, `settings`,
  `mail`, `store`, `sync`.
- One commit per fix or change, never one big commit. When one file holds
  several changes, stage them separately (e.g. `git apply --cached` with a
  partial patch).
- No `Co-Authored-By` or other AI attribution trailers.
