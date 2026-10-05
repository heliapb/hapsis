package proxy

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/grafana/tempo/v3/modules/frontend/combiner"
	"github.com/grafana/tempo/v3/pkg/api"
	"github.com/grafana/tempo/v3/pkg/tempopb"
	"github.com/grafana/tempo/v3/pkg/util"
)

// handleTraceByID serves /api/v2/traces/{id}.
// Backends that answer 404 are skipped
// a trace found on several backends is merged and de-duplicated by Tempo's own combiner.
func (p *Proxy) handleTraceByID(v2 bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traceID := mux.Vars(r)["traceID"]
		if _, err := util.HexStringToTraceID(traceID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		final, status, err := p.traceByID(r, traceID)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		// Tempo's two API differ on "not found"
		// v2 answers 200 with an empty trace
		// v1 answers 404. Mirror each.
		if v2 {
			writeMessage(w, r, final)
			return
		}
		if spanCount(final.Trace) == 0 {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		writeTraceV1(w, r, final.Trace)
	}
}

func spanCount(tr *tempopb.Trace) int {
	if tr == nil {
		return 0
	}
	n := 0
	for _, rs := range tr.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			n += len(ss.Spans)
		}
	}
	return n
}

// traceByID fans the lookup out and merges the results.
// On error it returns the HTTP status the client should see.
func (p *Proxy) traceByID(r *http.Request, traceID string) (*tempopb.TraceByIDResponse, int, error) {
	results := p.fanOut(r.Context(), r, "/api/v2/traces/"+hi)
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
			return nil, http.StatusBadGateway, fmt.Errorf("backend %s: %w", res.backend.name, res.err)
		case res.resp.StatusCode == http.StatusNotFound:
			continue
		case res.resp.StatusCode != http.StatusOK:
			body, _ := io.ReadAll(io.LimitReader(res.resp.Body, 4096))
			return nil, res.resp.StatusCode, fmt.Errorf("backend %s: %s", res.backend.name, body)
		}

		if err := c.AddResponse(pipelineResponse{resp: res.resp, idx: i}); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		found++
	}
	if found == 0 {
		return &tempopb.TraceByIDResponse{Trace: &tempopb.Trace{}, Metrics: &tempopb.TraceByIDMetrics{}}, http.StatusOK, nil
	}

	final, err := c.GRPCFinal()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return final, http.StatusOK, nil
}

// writeTraceV1 writes a bare trace in the v1 wire shape, protobuf when asked for,
// otherwise Tempo's legacy JSON layout (MarshalToJSONV1)
func writeTraceV1(w http.ResponseWriter, r *http.Request, tr *tempopb.Trace) {
	if tr == nil {
		tr = &tempopb.Trace{}
	}
	if api.MarshalingFormatFromAcceptHeader(r.Header) == api.MarshallingFormatProtobuf {
		writeMessage(w, r, tr)
		return
	}
	b, err := tempopb.MarshalToJSONV1(tr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set(api.HeaderContentType, api.HeaderAcceptJSON)
	_, _ = w.Write(b)
}
