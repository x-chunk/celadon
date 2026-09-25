package admin

import (
	"context"
	"net/http"
	"net/url"
)

// base is embedded in every service and folds one call down to one line:
//
//	func (s *PromoService) Reference(ctx context.Context) (Reference, *Meta, error) {
//		return s.get[Reference](ctx, "api/admin/promo/reference", nil)
//	}
//
// The type is always written out, because T is in the result and Go infers
// only from arguments.
type base struct{ c *Client }

func (b base) get[T any](ctx context.Context, path string, q url.Values) (T, *Meta, error) {
	return b.c.Do[T](ctx, Request{Method: http.MethodGet, Path: path, Query: q, Idempotent: true})
}

func (b base) post[T any](ctx context.Context, path string, body any) (T, *Meta, error) {
	return b.c.Do[T](ctx, Request{Method: http.MethodPost, Path: path, Body: body})
}

func (b base) patch[T any](ctx context.Context, path string, body any) (T, *Meta, error) {
	return b.c.Do[T](ctx, Request{Method: http.MethodPatch, Path: path, Body: body})
}

// del is spelled short because delete is a builtin. Deleting twice leaves
// the same nothing behind, so it is retried like a read.
func (b base) del[T any](ctx context.Context, path string) (T, *Meta, error) {
	return b.c.Do[T](ctx, Request{Method: http.MethodDelete, Path: path, Idempotent: true})
}

// none is the payload of an endpoint that answers with nothing worth reading.
type none = struct{}
