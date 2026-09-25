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
├── admin           a teal-shaped client for the private admin API (/admin/promo)
├── metrics         a teal-shaped client for the metrics listener (health through /api)
├── config          ~/.celadon: profiles, key and token files, resolution order
├── iostreams       stdin/stdout/stderr, TTY detection, hidden prompts
├── output          tables, JSON, sanitizing, formatting money, time and readings; sparklines
├── api             the teal, admin and metrics clients for a profile; errors explained
├── query           filters, one-line queries, durations, promotions and moments, parsed
├── cli             one cobra command per API operation
└── tui             one tab per area of the API
```

Dependencies point one way: `cli` and `tui` use `api`, `config`, `output` and
`query`; `cli` does not import `tui` — `main` hands it `RunTUI`, which keeps
the command line testable without a terminal.

## The admin client

`internal/admin` is to Aether's private admin API what teal is to the Plug-In
API, and is written to be called the same way: a `Client` built from a token
and options, services hanging off it (`Promo`), one request type per call with
pointers for optional fields, generic `get`/`post`/`patch`/`del` helpers over
`Do[T]`, and every method returning `(payload, *Meta, error)`.

What differs is the API underneath. It is opened by the deployment's
`ADMIN_TOKEN`, bills nothing, and answers in `{ok, message, data}` with the
outcome in the status line — so `*Error` derives its code from the status, and a
404 that is not the envelope becomes `CodeNotServed`: a deployment without an
admin token registers no admin routes at all. Reads and deletes are retried
after a server failure or a failed connection; a write never is, since it may
have arrived before the failure.

## The metrics client

`internal/metrics` is the same shape again for the metrics listener: `Health`
(`Live`, `Ready`), `Series` (`Info`, `Live`, `Query`, `Stream`) and `Prometheus`
(`Scrape`) over a generic `Do[T]`. The listener is another port, usually
reached through a tunnel, with an optional token, and answers in plain JSON:
refusals are `{ok:false, message}` with a code derived from the status, and a
503 from `/readyz` is returned as the readiness it carries. Points arrive as
compact `[ms, avg, min, max, last]` arrays with `null` for a gap, decoded to
`NaN`; the stream is read as server-sent events; every endpoint is a read, so
every failure is retried. `api.Unreachable` tells a refused connection to the
listener apart from the Plug-In API being down, and says how to open the
tunnel.

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

`celadon metrics tui` is the same `Model` again, built by `tui.NewMetrics`,
with a ticker that refreshes only the tab in front and a header showing
readiness. Its charts are drawn by `chart.go` in braille — two dots across and
four down per cell — with a value axis, the window along the bottom, stacking
for stacked panels and a legend of the latest values; its sparklines are
`output.Sparkline`, which the command line uses too.

`celadon admin tui` is the same `Model` built by `tui.NewAdmin` with the admin
tabs (campaigns, codes, reference) and a header naming the deployment; its
calls go through `callAdmin`, the admin twin of `call`, and arrive as the same
`done[T]`. Editing uses a small `form` widget shared by the admin tabs.

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
