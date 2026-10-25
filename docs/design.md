# Design

How courrier looks and behaves, and the values behind it. The source of
truth is `app/theme.go` (tokens, themes, shared components); change values there
and keep this page in step.

## Principles

- **Never wait on the server.** Every action shows at once from the local
  cache; the network catches up behind it (see [how it works](how-it-works.md)).
- **Keyboard first, mouse friendly.** Every list action has a single-key
  shortcut (rebindable in Settings) and a visible button.
- **Calm and dense.** One monospace typeface, few colors, color only where it
  means something: the accent marks selection and action, green success, red danger.
- **Native, not a web page.** MyGo's native UI draws everything except the body
  of HTML mail, which gets a locked-down system web view.

## Layout

```
┌──────────────┬────────────────────┬──────────────────────────────┐
│ Sidebar      │ Message list       │ Message                      │
│ 230          │ 340                │ the rest                     │
│ accounts,    │ 50 rows a page,    │ header, actions, body,       │
│ folders,     │ more as you scroll │ attachments                  │
│ unified,     │                    │                              │
│ settings     │                    │                              │
└──────────────┴────────────────────┴──────────────────────────────┘
```

| Spec | Value |
|---|---|
| Window | 960 × 680 default, 560 × 420 minimum; size and position remembered |
| Title bar | hidden; the app draws its own (min 32 high), draggable, clear of the system's window controls |
| Sidebar / list widths | 230 / 340 DIP default, resizable split views, remembered |
| Narrow (`narrowContent`) | under 600 DIP beside the sidebar: one pane, list *or* message, with a back button |
| Pages (settings, accounts) | centered, max 720 wide; padding 32, 18 when narrow; 40 at the bottom |
| Forms | labels beside fields; above them under 520 DIP |

## Typography

One family: **JetBrains Mono NL** (Regular, Medium, Bold, embedded). The
"NL" cut has no ligatures, so `->` or `!=` in mail stays as typed. If a font
fails to load, the system font stands in.

| Role | Size | Weight / tracking |
|---|---|---|
| Base text | 13 (Settings → Font size, 10–20) | Regular |
| Eyebrow (kicker above a title) | 11, upper case | Medium, +1.2 |
| Secondary: dates, counts, hints, title bar | 11–12 | Regular, muted color |
| App name in sidebar | 15 | Bold, −0.3 |
| Empty-state title | 20 | Bold, −0.4 |
| Page title | 24 | Bold, −0.5 |

## Color

Each theme fills the same slots. "System" follows the OS's light or dark
appearance with Light / Dark.

| Theme | bg | surface | text | muted | accent | success | danger |
|---|---|---|---|---|---|---|---|
| Light | `#fbfbfc` | `#f0f0f3` | `#17171c` | `#73737f` | `#6e56cf` | `#12a26b` | `#d93d42` |
| Dark | `#111113` | `#1f1f23` | `#ececf1` | `#8b8b96` | `#8e7cf6` | `#3ddc97` | `#ff6b6b` |
| Solarized Light | `#fdf6e3` | `#eee8d5` | `#073642` | `#657b83` | `#268bd2` | `#859900` | `#dc322f` |
| Nord | `#2e3440` | `#3b4252` | `#eceff4` | `#9aa5b8` | `#88c0d0` | `#a3be8c` | `#bf616a` |
| Dracula | `#282a36` | `#343746` | `#f8f8f2` | `#9099c0` | `#bd93f9` | `#50fa7b` | `#ff5555` |
| Gruvbox | `#282828` | `#3c3836` | `#ebdbb2` | `#a89984` | `#fe8019` | `#b8bb26` | `#fb4934` |

Every theme also sets hover, pressed, border, accent hover / pressed / text,
sidebar and card colors (see `looks` in `app/theme.go`). Derived from the accent
in all themes:

| Token | Value |
|---|---|
| Selection | accent at 28 % |
| Focus ring | accent at 55 % |
| Soft accent (icon tiles of empty states, account and server cards) | accent at 14 % |
| Notice background | its color at 12 %, with a 6 px dot of the full color |

To add a theme: add its name to `appearances` and its colors to `looks`.

## Spacing and shape

| Spec | Value |
|---|---|
| Spacing unit | 4 |
| Default radius | 8 |
| Card | radius 12, padding 18 × 20, 1 px border, gap 14, muted title |
| Icon button | 28 × 28, radius 7, 16 px icon, hover fills with surface-hover |
| Page header | eyebrow, title and subtitle, gap 6 |
| Notice | padding 10 × 14, radius 8 |

## Icons

[Lucide](https://lucide.dev) shapes (ISC license), inlined in `app/theme.go` as
24-unit SVGs with a 2-unit stroke, drawn in the text color at 16 px. Special
folders map to icons in `specialIcons` (Inbox, Sent, Drafts, Archive, Junk,
Trash, Snoozed). The Google and Microsoft sign-in logos keep their own
colors, per their brand guidelines; Google's "G" sits on a white tile in
dark themes too.

## Interaction specs

- **Optimistic actions** update the list at once; failures show as a
  notice and stay queued for retry. A move or delete shows an Undo toast;
  it can be undone for 10 minutes.
- **Selection**: click, ⇧-click for a range, ⌘/Ctrl-click to add; actions
  and shortcuts apply to every selected message.
- **Drafts** save as you type; nothing typed is ever lost to a crash.
- **HTML mail**: no scripts; remote images blocked unless allowed in
  Settings or for that message.
- **Background**: with "Show in menu bar" on (the default), closing the
  window hides it and sync continues; Quit from the menu bar item.

## Writing in the UI

Short, plain sentences, in the second person ("What courrier keeps of your
mail on this computer."). Sentence case for titles and buttons. Server
errors are shown in the server's own words, capitalized, never hidden.
