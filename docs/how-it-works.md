# How courrier works

courrier is one Go program in three layers:

```
app/ (UI)           app.go, mailview.go, compose.go, settings.go, ...
        │  reads and writes
        ▼
store/              SQLite: accounts, sealed passwords, the mail cache,
        ▲           drafts, outbox, queued actions, rules, snoozes
        │  writes what it fetches (mail.Cache)
mail/               IMAP, POP3, SMTP, OAuth2, MIME parsing, one Syncer per account
```

The UI never talks to a server. It reads the cache, and every change it
makes goes into the cache first; the syncers carry it to the server.

## Packages

| Package | What it does |
|---|---|
| `app/` (package `main`) | The window and its views, built with MyGo's native UI (`ui.View`). `main.go` wires up the app: single-instance lock, `mailto:` links, notification clicks, dialogs, the updater. `mailview.go` is the three panes, `compose.go` the compose window, `settings.go` / `servers.go` / `form.go` the account and settings screens, `rules.go` / `later.go` rules, snooze and send later, `notify.go` / `tray.go` notifications, badge and menu bar. |
| `mail/` | Protocols. `imap.go` (go-imap v2, IDLE, CONDSTORE/QRESYNC), `pop3.go`, `smtp.go`, `oauth.go` (PKCE + loopback redirect, XOAUTH2), `parse.go` (go-message), `compose.go` (building MIME), `markdown.go`, `mbox.go` (export), `search.go` (server search query parsing), `check.go` (the "Test connection" check), and `sync.go`, the `Syncer`. |
| `store/` | All persistent state, in one SQLite database (MyGo's sqlite plugin). |

## Sync

Each account has one `mail.Syncer`, running in its own goroutine:

- **IMAP**: IDLE on the Inbox for push; the Inbox is polled every 5 minutes
  without IDLE, other folders every 15. With CONDSTORE/QRESYNC only changed
  flags and vanished UIDs are fetched; without them it compares UIDs.
  A first sync fetches headers newest first, in pages, then fills in older
  mail between syncs (not a folder of all mail, like Gmail's All Mail).
- What the user waits for (opening a message, its header or source, a
  page of older mail) goes over a second IMAP connection, opened on demand,
  so it doesn't wait behind a sync. Opening fetches only the text and
  inline images; attachments are fetched when saved. Between requests,
  that connection fetches the bodies of the newest unread Inbox mail ahead.
- **POP3**: polled every 5 minutes; mail is kept locally, optionally left on
  the server. POP3 mail lives only in the cache, so the cache sweep never drops it.
- Errors back off from 5 seconds to 5 minutes. When the network comes back,
  queued work is retried.
- After any change the syncer calls `OnChange`; the UI then reloads from the
  cache on the UI thread (`Window.Update`).

Changing one account restarts only that account's syncer.

## Offline-safe actions

Mark read, flag, archive, delete and move are applied to the cache at once
and written to the `actions` table. The syncer replays them on the server
and removes them once done; a failed action keeps its attempt count and the
server's error. A sync never drops a change still waiting in `actions`.
Moves the server finished stay undoable for 10 minutes, using the UIDs the
server reported (UIDPLUS).

## Sending

Compose saves the draft to the database as you type (it survives a crash),
and syncs it to the server's Drafts folder. Send moves it to the outbox;
the syncer sends it over SMTP when online, or at its `send_after` time for
send later, then stores a copy in Sent unless the server does that itself.

## Rules and snooze

Rules run in the app, on mail a sync reports as new, so only while courrier
runs. They match header fields and the snippet, and their effects are
ordinary queued actions. Snooze moves a message to a "Snoozed" folder on the
server and keeps the wake time locally; when it's due, the message moves
back to where it was, unread.

## Storage and secrets

- The database is `courrier.db` in the app's user-data folder
  (`mygo.PathUserData`, e.g. `~/Library/Application Support/…` on macOS).
- Passwords and OAuth refresh tokens are stored in the `secrets` table,
  sealed with AES-GCM. The key is the app's single item in the system
  credential store (`courrier:key`), so the system asks at most once per run.
- Message bodies older than the limit in Settings → Storage are swept daily;
  headers stay, and an opened message keeps its body for 30 days.

## Database

One SQLite file, `courrier.db`; each `store/*.go` file creates its own tables.

| Tables | What they hold |
|---|---|
| `servers`, `accounts`, `groups`, `identities` | server profiles, accounts, sidebar groups, send-as addresses |
| `secrets` | sealed passwords and refresh tokens, one row per account |
| `folders`, `messages`, `attachments` | the mail cache |
| `messages_fts` | full-text index (FTS5) for local search |
| `thread_refs` | Message-ID / References links that group conversations |
| `actions` | changes waiting for the server |
| `drafts`, `draft_files`, `outbox` | mail being written and waiting to send |
| `contacts` | addresses for suggestions in To / Cc |
| `rules`, `snoozes` | mail rules and snoozed messages' wake times |
| `pop3_seen` | UIDLs already downloaded, per POP3 account |
| `prefs` | settings and window layout |

## Showing mail

Plain-text mail is drawn natively. HTML mail is shown in the system web view
under a strict Content-Security-Policy: no scripts at all, and images only
from the message itself (`cid:`/`data:`) unless you choose to load remote
images.

## Building and releasing

`go tool mygo build` compiles, bundles and packages the app as `mygo.json`
says (name, bundle identifier, version, the `mailto` URL scheme). The
GitHub Actions workflow in `.github/workflows/release.yml` builds every
platform for each `v*` tag and uploads to a draft release, which installed
copies update from.

For the look and feel (layout, themes, typography) see [design.md](design.md).
