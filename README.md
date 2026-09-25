# celadon

A command line and a full-screen terminal interface for the
[Aether Plug-In API](http://144.31.187.78:8080/docs): the archive, the vault,
the shortcuts and the settings of the account an application key opens.
Every call goes through [teal](https://github.com/x-chunk/teal), the Go client
for the API.

```text
$ celadon search invoice --where suser=ann
ID     CHAT            FROM  SENT              TEXT
90210  -1001234567890  @ann  2026-09-01 09:14  the invoice is attached (edited ×1)
page 1 of 4 · 37 matches
cost $0.0005 · balance $4.1995 · credits
```

Run `celadon` with no arguments on a terminal to open the interface.

## Install

From a release: download the archive for your platform from the
[releases](https://github.com/x-chunk/celadon/releases), put `celadon` on
your `PATH`, and copy the man pages and completion scripts from `man/` and
`completions/` wherever your system keeps them.

From source (Go 1.27 or later):

```sh
go install github.com/x-chunk/celadon/cmd/celadon@latest
```

or, in a clone, `make install`.

## Log in

The bot shows an application key once, when an application is created or its
key is regenerated. Give it to celadon:

```sh
celadon auth login                        # asks for the base URL and the key
celadon auth login --profile work \
  --base-url https://aether.example.com   # a second deployment or application
celadon auth login --with-token < key.txt # from standard input, for scripts
```

The key is checked against `GET /v1/app` (free on every billing mode) before
it is stored. It is read from a hidden prompt or from standard input and
never from an argument, where every user of the machine could read it through
`ps` and the shell would write it into its history.

Keys live in `~/.celadon/keys/<profile>`, readable by you alone; the rest of
the profile is in `~/.celadon/config.toml`. See
[docs/configuration.md](docs/configuration.md) for the layout, the
environment variables and how a profile is chosen.

```sh
celadon auth status          # every profile, and whether its key still works
celadon auth switch work     # make another profile the default
celadon auth logout          # delete a profile and its key
celadon auth token           # print the key, for another program
```

## Commands

| Area       | Commands |
|------------|----------|
| Archive    | `search`, `count`, `export`, `chats`, `message view`, `message versions`, `fields`, `insights`, `portrait` |
| Account    | `app view`, `app usage`, `app prices`, `account view`, `account quotas` |
| Vault      | `vault store`, `reveal`, `rename`, `codes`, `recover`, `delete` |
| Shortcuts  | `actions list`, `view`, `create`, `edit`, `delete`, `placeholders` |
| Settings   | `settings`, `settings retention`, `vault`, `actions`, `language` |
| Other      | `auth …`, `tui`, `completion`, `version` |

`celadon <command> --help` documents each one, and `man celadon-<command>`
does too once the man pages are installed.

### Searching

Words search the text. Filters narrow it further and are combined left to
right, in the order they are typed:

```sh
celadon search refund                                  # text contains "refund"
celadon search --where suser=ann --where created=2026-09-01
celadon search --where media=photo --or media=video    # OR joins to the filter before it
celadon search refund --chat -1001234567890 --page 2
celadon count --where media=photo                      # the number only; spends no daily search
celadon fields                                         # what a filter may name
```

`field=value` matches the whole value and `field~value` a part of it. Every
search, and every page of one, is billed as one query.

`export` writes every match as one JSON document — to `--file`, to standard
output when it is a pipe, or to `aether-export-<time>.json`. The file is
written beside its destination and moved into place when complete; one cut
short at the server's 50 MB cap is kept and reported.

### The vault

```sh
celadon vault store                          # passphrase twice, then the secret
celadon vault store --secret-file id_ed25519
celadon vault reveal > id_ed25519            # byte for byte into a file
celadon vault recover --rekey                # a recovery code, then a new passphrase
printf '%s\n' "$PASS" | celadon vault reveal --passphrase-stdin
```

Passphrases and codes come from a hidden prompt or from standard input
(`--passphrase-stdin`, `--code-stdin`), never from arguments. Recovery codes go
to standard output and the warning that they will not be shown again to
standard error, so a pipe captures the codes alone.

### Shortcuts and settings

```sh
celadon actions create hi --body 'Hi [[YOU_FIRST]]!'
celadon actions edit 12                       # opens the body in $EDITOR
celadon actions placeholders                  # which placeholders the plan opens
celadon settings retention --mode rotate --ttl 90d
celadon settings vault --reveal-ttl 5m
celadon settings language --auto
```

A settings command without flags reads; with flags it writes only what was
given — `--ttl off` switches the window off, leaving `--ttl` out leaves it
alone.

## The interface

`celadon` (or `celadon tui`) opens six tabs: **Overview** (the application,
the plan, quotas and spending), **Archive** (chats, search, message cards and
edit history), **Actions**, **Vault**, **Insights** (At a Glance and chat
portraits) and **Settings**. Press `?` for the keys; `1`–`6` or `tab` move
between tabs, `q` quits. The header shows the live balance and the status line
what the last call cost. See [docs/tui.md](docs/tui.md).

## Scripting

- `-o json` prints the API's payload as teal decodes it, with the API's own
  field names.
- Results go to standard output; notes, costs and warnings go to standard
  error, and `-q` silences them.
- Exit codes follow the GitHub CLI: `0` success, `1` failure, `2` usage error,
  `4` authentication problem, `130` interrupted.
- `CELADON_API_KEY` and `CELADON_BASE_URL` configure a run without any file
  on disk, which is what a CI job wants.
- `--timeout` bounds a command (default one minute; `export` is unbounded
  unless it is set), and `--retries` sets how often a rate refusal or a failed
  read is retried. Writes are never retried after a server failure.

```sh
celadon search --where media=photo -o json -q | jq '.messages[].id'
```

## Development

```sh
make check      # gofmt, go mod tidy -diff, go vet, go test -race — what CI runs
make build      # bin/celadon with the version stamped in
make docs       # man pages and completion scripts into generated/
make snapshot   # a local release with GoReleaser
```

The layout is described in [docs/architecture.md](docs/architecture.md).
Releases are built by GoReleaser when a `v*` tag is pushed.
