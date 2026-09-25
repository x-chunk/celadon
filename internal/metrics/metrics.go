// Package metrics is a Go client for Aether's metrics listener — the API the
// embedded dashboard is drawn from — written to be called the way teal calls
// the Plug-In API:
//
//	c, err := metrics.New(metrics.WithBaseURL("http://127.0.0.1:9090"), metrics.WithToken(token))
//	if err != nil {
//		return err
//	}
//	snap, meta, err := c.Series.Live(ctx)
//
// Every endpoint hangs off a service on the client — Health, Series and
// Prometheus — and every method returns its payload, a *Meta and an error.
//
// The listener is not the Plug-In API, and four things differ underneath:
//
//   - It is its own port (:9090), published on the loopback address only; a
//     remote deployment is reached through an SSH tunnel.
//   - Its METRICS_TOKEN is optional. A deployment without one serves every
//     endpoint to anybody who can reach the port, so the client sends a token
//     only when it has one. /healthz and /readyz are never guarded.
//   - It answers in plain JSON, not an envelope; a refusal is
//     {"ok":false,"message":…} with the outcome in the status line, and *Error
//     derives its code from the status, as the admin client does.
//   - Being not ready is an answer, not a failure: Health.Ready returns the
//     readiness the server reported, 503 and all, with a nil error.
package metrics

// DefaultBaseURL is where the metrics listener is on the machine it runs on,
// and where an SSH tunnel (ssh -N -L 9090:127.0.0.1:9090 host) puts it.
const DefaultBaseURL = "http://127.0.0.1:9090"
