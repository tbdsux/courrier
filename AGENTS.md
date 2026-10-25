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
- [.agents/skills/golang-patterns/SKILL.md](.agents/skills/golang-patterns/SKILL.md):
  idiomatic Go (errors, concurrency, interfaces). Its project layout
  section doesn't apply: keep this repo's packages as they are.

## Layout

- `app/` (package `main`, set as `main` in `mygo.json`): the UI. `app.go`
  sidebar and window, `mailview.go` the three panes, `compose.go`,
  `settings.go`, `tray.go`, `theme.go` (icons, palette, shared widgets).
  `app/assets/` and `app/fonts/` are embedded: `tray.png` is the tray icon
  and sidebar logo.
- `mail/`: IMAP, POP3, SMTP, OAuth2, MIME, and the per-account `Syncer`.
- `store/`: all persistent state, in one SQLite database.
- `resources/`: files `go tool mygo build` ships next to the executable.
  `icon.png` is the app icon.
- The repo root holds only config (`go.mod`, `mygo.json`) and docs.
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

CI also checks `GOOS=darwin|windows|linux go build ./app` still compiles. Keep
the code cgo-free.

The MyGo source is not in this repo; find the pinned version with
`go list -m -f '{{.Dir}}' github.com/egoist/mygo`. Don't edit it: UI owned
by MyGo plugins (for example the updater window) can't be changed from here.

## Code style

- Match the surrounding code: short doc comments in plain sentences that
  say what a thing is or why, not how.
- Separate the steps of a function with blank lines: before and after
  `if`, `for`, `switch` and `select` blocks and any statement of three
  lines or more, and before a function's final `return` when its body
  has three statements or more. Keep an assignment next to the statement
  that uses it (`x, err := f()` then `if err != nil`), and a short
  block's `return` or `break` next to what it follows.
- `len` of a nil slice or map is 0: test `len(x) == 0`, not
  `x == nil || len(x) == 0`.
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
