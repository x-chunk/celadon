package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.xchunk.org/anvil/v2/result"
	"go.xchunk.org/teal"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/metrics"
)

// reply is what every teal method answers with: the payload and the Meta it
// came with, or the refusal.
type reply[T any] = result.Result[teal.Response[T]]

// requestTimeout bounds every call the interface makes. Nothing here streams,
// so a minute is generous.
const requestTimeout = 30 * time.Second

// backend is the client and the context every call runs under. The context
// ends when the interface does, so a call in flight at quit is abandoned
// rather than waited for.
type backend struct {
	ctx     context.Context
	client  *teal.Client
	admin   *admin.Client
	metrics *metrics.Client
	timeout time.Duration
}

// done is the answer to one call. op names the call, so that a tab making
// two calls with the same payload type can tell them apart; seq is the
// tab's own counter, so that an answer overtaken by a newer request can be
// dropped rather than drawn over it.
type done[T any] struct {
	op   string
	seq  int
	val  T
	meta *teal.Meta
	err  error
}

// outcome is what the root reads off every answer, whatever its payload:
// what it cost and whether it failed.
type outcome interface {
	metaOf() *teal.Meta
	errOf() error
}

func (d done[T]) metaOf() *teal.Meta { return d.meta }
func (d done[T]) errOf() error       { return d.err }

// call runs fn off the event loop and delivers its answer as a done[T]. A
// refusal is delivered with the Meta it carries: the API refunds before it
// answers, so the balance in it is the balance actually left.
func call[T any](b *backend, op string, seq int, fn func(context.Context, *teal.Client) reply[T]) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()
		res, err := fn(ctx, b.client).Value()
		if err != nil {
			var meta *teal.Meta
			if e, ok := teal.AsError(err); ok {
				meta = e.Meta
			}
			return done[T]{op: op, seq: seq, meta: meta, err: err}
		}
		return done[T]{op: op, seq: seq, val: res.Data, meta: res.Meta}
	}
}

// ack is the payload of a call that answers with nothing but its cost.
type ack = struct{}

// callAdmin runs a call to the admin API off the event loop and delivers its
// answer as a done[T], exactly as call does. The admin API bills nothing, so
// the answer carries no teal Meta.
func callAdmin[T any](b *backend, op string, seq int, fn func(context.Context, *admin.Client) (T, *admin.Meta, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()
		v, _, err := fn(ctx, b.admin)
		return done[T]{op: op, seq: seq, val: v, err: err}
	}
}

// callAdminAck runs an admin call that answers with nothing.
func callAdminAck(b *backend, op string, seq int, fn func(context.Context, *admin.Client) (*admin.Meta, error)) tea.Cmd {
	return callAdmin(b, op, seq, func(ctx context.Context, c *admin.Client) (ack, *admin.Meta, error) {
		meta, err := fn(ctx, c)
		return ack{}, meta, err
	})
}

// callMetrics runs a call to the metrics listener off the event loop and
// delivers its answer as a done[T], as call does.
func callMetrics[T any](b *backend, op string, seq int, fn func(context.Context, *metrics.Client) (T, *metrics.Meta, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()
		v, _, err := fn(ctx, b.metrics)
		return done[T]{op: op, seq: seq, val: v, err: err}
	}
}
