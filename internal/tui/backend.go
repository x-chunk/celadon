package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/x-chunk/teal"
)

// requestTimeout bounds every call the interface makes. Nothing here streams,
// so a minute is generous.
const requestTimeout = 30 * time.Second

// backend is the client and the context every call runs under. The context
// ends when the interface does, so a call in flight at quit is abandoned
// rather than waited for.
type backend struct {
	ctx     context.Context
	client  *teal.Client
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

// call runs fn off the event loop and delivers its answer as a done[T].
func call[T any](b *backend, op string, seq int, fn func(context.Context, *teal.Client) (T, *teal.Meta, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()
		v, meta, err := fn(ctx, b.client)
		return done[T]{op: op, seq: seq, val: v, meta: meta, err: err}
	}
}

// ack is the payload of a call that answers with nothing but its cost.
type ack struct{}

// callAck runs a call that returns only a Meta.
func callAck(b *backend, op string, seq int, fn func(context.Context, *teal.Client) (*teal.Meta, error)) tea.Cmd {
	return call(b, op, seq, func(ctx context.Context, c *teal.Client) (ack, *teal.Meta, error) {
		meta, err := fn(ctx, c)
		return ack{}, meta, err
	})
}
