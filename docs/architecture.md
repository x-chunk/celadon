# Architecture

One binary, `cmd/celadon`, with two faces over one client: a cobra command
line and a Bubble Tea interface. Neither talks HTTP itself — every call is a
[teal](https://github.com/x-chunk/teal) method, and teal owns the wire format,
retries and error decoding.

```text
cmd/celadon         main: signals, streams, wiring the TUI into the CLI
cmd/gendocs         man pages and completion scripts, for packaging
internal/
├── version         the build's version, from -ldflags or the module info
├── config          ~/.celadon: profiles, key files, resolution order
├── iostreams       stdin/stdout/stderr, TTY detection, hidden prompts
├── output          tables, JSON, sanitizing, formatting money and time
├── api             the teal client for a profile; errors explained
├── query           filters, one-line queries and durations, parsed
├── cli             one cobra command per API operation
└── tui             one tab per area of the API
```

Dependencies point one way: `cli` and `tui` use `api`, `config`, `output` and
`query`; `cli` does not import `tui` — `main` hands it `RunTUI`, which keeps
the command line testable without a terminal.

## Command line

`cli.Env` is what every command shares: the streams, the store, the global
flags and the seams a test replaces (config directory, HTTP client, clock).
Every command follows the same shape: validate the arguments before anything
is sent (a malformed filter is never billed), build the client, call teal
under `Env.Context` (the `--timeout`), and hand the result to
`Printer.Result`, which writes JSON or calls the command's text renderer.

Errors are classified once, in `cli.report`: usage errors (bad flags,
arguments, values) exit 2, authentication problems exit 4, everything else
exits 1, and `api.Explain` turns a teal `*Error` into a title and a hint by its
error code — never by its message, as teal asks.

## Terminal interface

`tui.Model` is the root: the header, the tab bar, the status line and the
keys. Each tab implements a small interface (`init`, `update`, `view`,
`capturing`, `keys`); tabs are pointers and change in place.

Calls run as `tea.Cmd`s through `call[T]`, which delivers a `done[T]` carrying
the payload, the `*teal.Meta` and the error. Every tab stamps its calls with a
sequence number and drops an answer that is not the latest. The root sees
every `done` through the `outcome` interface to keep the balance and the status
line current, then broadcasts it to all tabs — a tab left while its call was in
flight still gets its answer.

Tabs are found by title, not position, so their order is decided only in
`tui.New`. A tab with a focused text field reports `capturing`, and the root
then leaves every key but `ctrl+c` to it.

## Untrusted text

Everything from the archive — messages, names, chat titles — is text somebody
typed into Telegram. `output.Sanitize` replaces control characters, escape
sequences and bidirectional overrides before anything is drawn, in both faces.
A revealed vault secret written to a pipe is the one exception: it is written
byte for byte, because it is data going to a file, not text going to a screen.

## Testing

`internal/cli` runs whole command lines against an `httptest` server standing
in for Aether and checks the request bodies, the output and the exit codes.
`internal/tui` drives the model the way Bubble Tea would — key messages in,
commands run synchronously, messages fed back — against the same kind of
server, and asserts on the rendered screen.
