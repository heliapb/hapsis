package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/grafana/tempo/v3/pkg/api"
)

// forwarded lists the client request headers copied to every backend.
var forwarded = []string{"X-Scope-OrgID", "Authorization", "Traceparent", "Tracestate"}

type result struct {
	backend *backend
	resp    *http.Response
	err     error
}

// fanOut sends GET path?query to every backend in parallel and returns one result per backend, in configuration order.
func (p *Proxy) fanOut(ctx context.Context, in *http.Request, path string) []result {
	results := make([]result, len(p.backends))
	var wg sync.WaitGroup
	for i, b := range p.backends {
		wg.Go(func() { results[i] = p.call(ctx, b, in, path) })
	}
	wg.Wait()
	return results
}

// call performs one backend request.
func (p *Proxy) call(ctx context.Context, b *backend, in *http.Request, path string) result {
	req, err := buildRequest(ctx, b, in, path)
	if err != nil {
		return result{backend: b, err: err}
	}
	resp, err := b.client.Do(req)
	return result{backend: b, resp: resp, err: err}
}

// buildRequest derives the upstream request from the client request
func buildRequest(ctx context.Context, b *backend, in *http.Request, path string) (*http.Request, error) {
	u := *b.url
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = in.URL.RawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for _, h := range forwarded {
		if v := in.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set(api.HeaderAccept, api.HeaderAcceptProtobuf)
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	return req, nil
}
