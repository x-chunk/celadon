# Watching the process

`celadon metrics` reads Aether's metrics listener — the API its built-in
dashboard is drawn from: health and readiness, the catalog of series, their
live values and the stream of them, range queries over the history, and the
Prometheus text.

## Reaching the listener

The listener is a port of its own (`:9090`), published on the deployment's
**loopback address** only. celadon looks for it at `http://127.0.0.1:9090`,
which is where it is on the machine itself and where an SSH tunnel puts a
remote one:

```sh
ssh -N -L 9090:127.0.0.1:9090 user@app-host
```

`--metrics-url`, `$CELADON_METRICS_URL` or the profile's `metrics_url` point
elsewhere — another local port for a second tunnel, say. The Plug-In API's
base URL is never used for it. When the listener cannot be reached, celadon
says how to open the tunnel.

Its `METRICS_TOKEN` is optional: a deployment without one serves the listener
to anybody who reaches the port.

```sh
celadon metrics login                                   # hidden prompt
celadon metrics login --metrics-url http://127.0.0.1:19090 --with-token < token.txt
celadon metrics login --no-token                        # the listener has none
celadon metrics status                                  # address, token, and whether both work
celadon metrics logout                                  # forget the token; the address stays
```

The token is checked against `GET /api/meta` and kept in
`~/.celadon/metrics/<profile>` (mode 0600). `$CELADON_METRICS_TOKEN` bypasses
the file. `/healthz` and `/readyz` never need it.

## Commands

| Command | Reads | What it shows |
|---|---|---|
| `metrics health [--wait 2m]` | `/healthz`, `/readyz` | liveness, readiness and every dependency; exits 1 until ready |
| `metrics overview [--trend 15m]` | `/api/meta`, `/api/live`, `/api/query` | the dashboard's headline numbers with sparklines |
| `metrics list [pattern…] [--kind]` | `/api/meta`, `/api/live` | the catalog, with every series' value now |
| `metrics get <pattern…> [--raw]` | `/api/live` | values now |
| `metrics query <pattern…>` | `/api/query` | min, avg, max and last over a window, with a sparkline, or `--points` |
| `metrics watch [pattern…]` | `/api/stream` | live readings, redrawn in place |
| `metrics prom [pattern…]` | `/metrics` | the Prometheus exposition, raw or filtered |
| `metrics tui` | all of them | the full-screen dashboard |

A series is named as the catalog names it: the metric and its labels,
`db_queries_total{op="select"}`. A pattern may be a whole key, a metric name
(every label combination of it) or a glob over either — `'db_*'`, `'*_p99'` —
and one that matches nothing is refused rather than answered with nothing.

A **counter** is published as its rate per second, a **gauge** as its value, a
**histogram** as `_avg`, `_p50` and `_p99` series. The Prometheus text is the
exception: there counters are totals. A metric that has never moved publishes
no series until it first does.

```sh
celadon metrics health --wait 2m && echo deployed
celadon metrics get go_goroutines --raw
celadon metrics query process_cpu_percent host_cpu_percent --range 6h
celadon metrics query 'db_query_seconds_p99*' --from 2026-09-24 --to 2026-09-25 --step 10m --points
celadon metrics watch 'http_requests_total' -o json | jq .values
```

A window is `--range` ending now (`15m`, `6h`, `7d`) or `--from`/`--to`. The
listener answers at the finest resolution that still covers it — seconds for
the last 15 minutes, minutes for two days, hours beyond — unless `--step` asks
for another.

## The dashboard

`celadon metrics tui` refreshes the tab in front every two seconds:

- **Overview** — the headline numbers as cards: the value now, colored as a
  percentage nears full, over a sparkline of its recent trend; the readiness
  of the database and each NATS connection; uptime, series and history.
- **Dashboard** — the listener's own layout, a section at a time (`←`/`→`), its
  panels drawn as line charts in braille with a legend of the latest values.
  `-`/`+` choose the window from 5m to 30d; a chart of a long window is re-read
  less often than one of a short window.
- **Series** — the whole catalog, filtered with `/` by any words of a series'
  key, help, kind or unit, and the selected one charted with its lowest,
  average, highest and last values.
- **Health** — liveness and its latency, readiness dependency by dependency,
  and how long the listener keeps its seconds, minutes and hours.

The header shows whether the process is ready, whichever tab is open.

## From Go

Inside celadon the client is `internal/metrics`, shaped like teal:

```go
c, err := metrics.New(metrics.WithBaseURL("http://127.0.0.1:9090"), metrics.WithToken(token))
ready, _, err := c.Health.Ready(ctx)          // a 503 is the readiness, not an error
info, _, err := c.Series.Info(ctx)            // catalog and layout
ans, _, err := c.Series.Query(ctx, metrics.QueryRequest{Keys: []string{"go_goroutines"}, Range: time.Hour})
stream, _, err := c.Series.Stream(ctx)        // server-sent events
for { snap, err := stream.Next(); … }
exp, _, err := c.Prometheus.Scrape(ctx)       // parsed samples, and the raw text
```
