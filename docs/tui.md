# The terminal interface

`celadon` with no arguments on a terminal, or `celadon tui`, opens a
full-screen interface on the profile the command line would use (`--profile`
and `--base-url` apply). It needs at least 60×16 characters.

```text
celadon · Reports · credits · work                                  balance $4.20
 1 Overview  2 Archive  3 Actions  4 Vault  5 Insights  6 Settings
─────────────────────────────────────────────────────────────────────────────────
 …the active tab…
─────────────────────────────────────────────────────────────────────────────────
cost $0.0005 · balance $4.1995 · credits
/ search · enter open · [ ] page (billed) · c all chats · ← chats · ? help · q quit
```

The header shows the application, its billing mode, the profile and the
balance, kept up to date from every response. The status line shows what the
last call cost, or why it failed and what to do about it. The last line lists
the keys that apply right now.

## Everywhere

| Key               | Does |
|-------------------|------|
| `1`–`6`           | open a tab |
| `tab`, `shift+tab`| next, previous tab |
| `?`               | all the keys |
| `q`               | quit (`ctrl+c` works even while typing) |

While a text field has the keyboard, every key but `ctrl+c` goes to it, and
`esc` gives the keyboard back.

A tab loads the first time it is opened, so a tab you never open costs no
requests. Requests run in the background with a 30-second limit each; an answer
that arrives after a newer request was made is dropped rather than drawn over
it.

## Overview

The application (billing mode, balance, what was funded and spent, the key's
prefix), the plan behind the account, every quota as a bar that turns yellow
at 80% and red when spent, and 30 days of spending by operation. `r`
refreshes; the arrows scroll. Everything here is free.

## Archive

Chats on the left, matches on the right.

| Key          | Does |
|--------------|------|
| `/`          | type a query; `enter` runs it |
| `enter`      | on a chat: read it; on a match: open its card |
| `←`/`→`, `h`/`l` | move between the two lists |
| `[`, `]`     | previous, next page of the focused list |
| `c`          | drop the chat the search is narrowed to |
| `p`          | the portrait of the selected chat, in Insights |
| `v`          | on a card: the edit history |
| `esc`        | close the card |

A query is typed on one line: free words search the text, `field=value` and
`field~part` are AND filters, `|field=value` is an OR filter, and double quotes
keep spaces together — `invoice suser=ann |suser=bob "created~2026-09"`.
Every search and every page of one is billed as a query, so one only runs when
a key is pressed.

## Actions

`n` new, `e` or `enter` edit, `d` delete (then `y`), `p` the placeholders and
the plan each needs, `r` refresh. In the form, `tab` moves between the name
and the body, `ctrl+s` saves and `esc` cancels. An edit sends only what
changed.

## Vault

Pick an operation and press `enter`; every field is masked. `enter` moves to
the next field and runs the operation from the last one. A new passphrase is
typed twice. Destroying an entry asks for `y`.

What the vault hands back — a plaintext, recovery codes — stays on screen until
`x`, and is forgotten the moment you leave the tab; an answer still in flight
then is never shown. The form is wiped before its request is sent. A secret of
more than one line is stored with `celadon vault store --secret-file`.

## Insights

At a Glance, and a chat's portrait: `/` and a chat id, or `p` on a chat in the
Archive tab. A portrait still being built is asked for again when the API says
to (at most every 30 seconds), and nothing is charged for the wait.

## Settings

Move with the arrows. `enter` switches the archive's mode and the deletion in
the chat, and opens a field for the retention window and the reveal timer
(`7d`, `12h`, `90s`, `off`). The shortcut prefix and the language cycle with
`←`/`→` through the values the API accepts. A setting the plan does not open is
marked, and the API refuses a change to it.
