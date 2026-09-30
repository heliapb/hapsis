package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/mux"
	"github.com/grafana/tempo/v3/modules/frontend/combiner"
	"github.com/grafana/tempo/v3/pkg/api"
	"github.com/grafana/tempo/v3/pkg/util"

	"github.com/heliapb/hapsis/pkg/config"
)

type backend struct {
	name    string
	url     *url.URL
	headers map[string]string
	client  *http.Client
}

type Proxy struct {
	logger   *slog.Logger
	backends []*backend
}

func New(cfg *config.Config, logger *slog.Logger) (*Proxy, error) {
	p := &Proxy{logger: logger}
	for _, b := range cfg.Backends {
		u, err := url.Parse(b.URL)
		if err != nil {
			return nil, err
		}
		p.backends = append(p.backends, &backend{
			name:    b.Name,
			url:     u,
			headers: b.Headers,
			client:  &http.Client{Timeout: b.Timeout},
		})
	}
	return p, nil
}

func (p *Proxy) Handler() http.Handler {
	r := mux.NewRouter()
	r.HandleFunc(api.PathTracesV2, p.handleTraceByID).Methods(http.MethodGet)
	r.HandleFunc(api.PathEcho, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("echo"))
	}).Methods(http.MethodGet)
	return r
}

type result struct {
	backend *backend
	resp    *http.Response
	err     error
}

func (p *Proxy) fanOut(ctx context.Context, in *http.Request, path string) []result {
	results := make([]result, len(p.backends))
	var wg sync.WaitGroup
	for i, b := range p.backends {
		wg.Add(1)
		go func(i int, b *backend) {
			defer wg.Done()
			u := *b.url
			u.Path = strings.TrimSuffix(u.Path, "/") + path
			u.RawQuery = in.URL.RawQuery
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				results[i] = result{backend: b, err: err}
				return
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
			resp, err := b.client.Do(req)
			results[i] = result{backend: b, resp: resp, err: err}
		}(i, b)
	}
	wg.Wait()
	return results
}

var forwarded = []string{"X-Scope-OrgID", "Authorization", "Traceparent", "Tracestate"}

// handleTraceByID serves /api/v2/traces/{id}.
// Backends that answer 404 are skipped
// a trace found on several backends is merged and de-duplicated by Tempo's own combiner.
func (p *Proxy) handleTraceByID(w http.ResponseWriter, r *http.Request) {
	traceID := mux.Vars(r)["traceID"]
	if _, err := util.HexStringToTraceID(traceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := p.fanOut(r.Context(), r, "/api/v2/traces/"+traceID)
	defer func() {
		for _, res := range results {
			if res.resp != nil {
				_ = res.resp.Body.Close()
			}
		}
	}()

	c := combiner.NewTypedTraceByIDV2(0, api.MarshallingFormatJSON, nil, combiner.TraceByIDV2Options{})
	found := 0
	for i, res := range results {
		switch {
		case res.err != nil:
			http.Error(w, fmt.Sprintf("backend %s: %v", res.backend.name, res.err), http.StatusBadGateway)
			return
		case res.resp.StatusCode == http.StatusNotFound:
			continue
		case res.resp.StatusCode != http.StatusOK:
			body, _ := io.ReadAll(io.LimitReader(res.resp.Body, 4096))
			http.Error(w, fmt.Sprintf("backend %s: %s", res.backend.name, body), res.resp.StatusCode)
			return
		}

		if err := c.AddResponse(pipelineResponse{resp: res.resp, idx: i}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		found++
	}
	if found == 0 {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}

	final, err := c.GRPCFinal()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeMessage(w, r, final)
}

type pipelineResponse struct {
	resp *http.Response
	idx  int
}

func (p pipelineResponse) HTTPResponse() *http.Response { return p.resp }
func (p pipelineResponse) RequestData() any             { return p.idx }
func (p pipelineResponse) IsMetadata() bool             { return false }

func writeMessage(w http.ResponseWriter, r *http.Request, msg proto.Message) {
	if api.MarshalingFormatFromAcceptHeader(r.Header) == api.MarshallingFormatProtobuf {
		b, err := proto.Marshal(msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set(api.HeaderContentType, api.HeaderAcceptProtobuf)
		_, _ = w.Write(b)
		return
	}
	var buf bytes.Buffer
	if err := new(jsonpb.Marshaler).Marshal(&buf, msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set(api.HeaderContentType, api.HeaderAcceptJSON)
	_, _ = w.Write(buf.Bytes())
}
