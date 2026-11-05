# Design

How courrier looks and behaves, and the values behind it. The source of
truth is `app/theme.go` (tokens, themes, shared components); change values there
and keep this page in step.

## Principles

- **Never wait on the server.** Every action shows at once from the local
  cache; the network catches up behind it (see [how it works](how-it-works.md)).
- **Keyboard first, mouse friendly.** Every list action has a single-key
  shortcut (rebindable in Settings) and a visible button.
- **Calm and roomy.** One sans typeface, Figtree, few colors, color only where it
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
│ unified;     │                    │                              │
│ Settings     │                    │                              │
└──────────────┴────────────────────┴──────────────────────────────┘
```

| Spec | Value |
|---|---|
| Window | 960 × 680 default, 560 × 420 minimum; size and position remembered |
| Title bar | hidden; the app draws its own (min 32 high), draggable, clear of the system's window controls. The main window's holds the command palette's button (320 wide at most) |
| Command palette (⌘K) | 640 wide, radius 14, near the window's top at a fixed height: a borderless 17 search field, then Actions, Mailboxes and Settings, each row an icon on soft accent (26, radius 7), its name, a muted detail and its keys as keycaps; a footer with Run ↩ and Close esc. Every word typed narrows it; ↑ ↓ choose, Return runs |
| Sidebar / list widths | 230 / 340 DIP default, resizable split views, remembered |
| List views (`views`) | Comfortable: avatar, sender, subject, snippet. Compact: sender and subject. One line: a row a line across the window, messages open over the list |
| Sidebar view | a menu under Compose: All accounts, Unified (its mailboxes only), Unread (folders with unread mail, and the open one), or one account's folders. It widens by itself when the palette or a notification opens a mailbox outside it |
| Reply | under the message, in a resizable split (45 % message); pop out to a window. Its From shows with several addresses, its Cc once used |
| Narrow (`narrowContent`) | under 600 DIP beside the sidebar: one pane, list *or* message, with a back button |
| Settings | the Settings button, under the folders and above the sync status, opens it beside the sidebar: a menu 240 wide (190 when the window leaves less than 1000 beside the sidebar) (General, Accounts, Groups, Rules, Servers, Storage), then the chosen page |
| Pages (settings, accounts) | centered, max 880 wide; padding 32, 18 when narrow; 40 at the bottom |
| Forms | labels beside fields; above them under 520 DIP |

## Typography

**Figtree** throughout (Regular, Medium, SemiBold, Bold, embedded, and in the
conversation's web view too). If it fails to load, the system font stands in.

| Role | Size | Weight / tracking |
|---|---|---|
| Base text | 13 (Settings → Font size, 10–20) | Regular |
| Eyebrow (kicker above a title) | 11, upper case | Medium, +1.2 |
| Secondary: dates, counts, hints, title bar | 11–12 | Regular, muted color |
| App name in sidebar | 15 | Bold, −0.3 |
| Mailbox title above the list | 17 | Bold, −0.4 |
| Empty-state title | 20 | Bold, −0.4 |
| Page title | 24 | Bold, −0.5 |

## Color

Each theme fills the same slots. "System" follows the OS's light or dark
appearance with Light / Dark.

| Theme | bg | surface | text | muted | accent | success | danger |
|---|---|---|---|---|---|---|---|
| Light | `#ffffff` | `#f0f0f3` | `#17171c` | `#73737f` | `#6e56cf` | `#12a26b` | `#d93d42` |
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
| Chosen message rows | accent at 20 %, square corners, text in its usual colors |
| Chosen sidebar item | accent at 20 %, icon and label in the accent, while the sidebar has the focus; surface-pressed without it |
| Sender avatars | initial in one of `avatarColors`, on that color at 16 % |
| Soft accent (icon tiles of empty states, account and server cards) | accent at 14 % |
| Notice background | its color at 12 %, with a 6 px dot of the full color |

To add a theme: add its name to `appearances` and its colors to `looks`.

## Spacing and shape

| Spec | Value |
|---|---|
| Spacing unit | 4 |
| Default radius | 10 |
| Card | radius 12, padding 18 × 20, 1 px border, gap 14, muted title |
| Icon button | 30 × 30, radius 8, 16 px icon, hover fills with surface-hover |
| Pill (Compose, search, reply buttons) | radius 16–18; Compose on soft accent, hover accent at 22 %, icon and label in the accent |
| Segmented (`segmented`) | pill: surface track, radius 18, padding 3; segments 6 × 16, radius 15; chosen one raised on the background with a soft shadow, others muted |
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
- **Motion**: brief and quiet. Hover fills fade in 120 ms (`hoverFade`);
  an opened message's header rises 6 px into place (`openFade`). With the
  system's reduce motion on, only the fades remain.
- **Background**: with "Show in menu bar" on (the default), closing the
  window hides it and sync continues; Quit from the menu bar item.

## Writing in the UI

Short, plain sentences, in the second person ("What courrier keeps of your
mail on this computer."). Sentence case for titles and buttons. Server
errors are shown in the server's own words, capitalized, never hidden.
