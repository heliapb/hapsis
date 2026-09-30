package proxy

import (
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/mux"
	"github.com/grafana/tempo/v3/pkg/api"
	"github.com/grafana/tempo/v3/pkg/tempopb"
	v1 "github.com/grafana/tempo/v3/pkg/tempopb/trace/v1"

	"github.com/heliapb/hapsis/pkg/config"
)

const (
	traceShared = "0000000000000000000000000000abcd"
	traceOnlyA  = "00000000000000000000000000001111"
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func trace(traceID, spanID, name string) *tempopb.Trace {
	return &tempopb.Trace{ResourceSpans: []*v1.ResourceSpans{{ScopeSpans: []*v1.ScopeSpans{{
		Spans: []*v1.Span{{TraceId: mustHex(traceID), SpanId: mustHex(spanID), Name: name}},
	}}}}}
}

// fakeTempo serves /api/v2/traces/{id} in protobuf for a fixed set of traces.
func fakeTempo(t *testing.T, traces map[string]*tempopb.Trace) *httptest.Server {
	t.Helper()
	r := mux.NewRouter()
	r.HandleFunc("/api/v2/traces/{traceID}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.HeaderAccept) != api.HeaderAcceptProtobuf {
			t.Errorf("expected protobuf Accept, got %q", r.Header.Get(api.HeaderAccept))
		}
		tr, ok := traces[mux.Vars(r)["traceID"]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := proto.Marshal(&tempopb.TraceByIDResponse{Trace: tr, Metrics: &tempopb.TraceByIDMetrics{InspectedBytes: 1}})
		w.Header().Set(api.HeaderContentType, api.HeaderAcceptProtobuf)
		_, _ = w.Write(b)
	})
	return httptest.NewServer(r)
}

func spanNames(tr *tempopb.Trace) []string {
	var names []string
	for _, rs := range tr.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				names = append(names, s.Name)
			}
		}
	}
	return names
}

func TestTraceByID(t *testing.T) {
	a := fakeTempo(t, map[string]*tempopb.Trace{
		traceShared: trace(traceShared, "0000000000000001", "from-a"),
		traceOnlyA:  trace(traceOnlyA, "0000000000000002", "only-a"),
	})
	defer a.Close()
	b := fakeTempo(t, map[string]*tempopb.Trace{
		traceShared: trace(traceShared, "0000000000000003", "from-b"),
	})
	defer b.Close()

	cfg, err := config.Parse([]byte("backends:\n  - {name: a, url: " + a.URL + "}\n  - {name: b, url: " + b.URL + "}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := p.Handler()

	get := func(path string) (*httptest.ResponseRecorder, *tempopb.TraceByIDResponse) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(api.HeaderAccept, api.HeaderAcceptProtobuf)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		resp := &tempopb.TraceByIDResponse{}
		if rec.Code == http.StatusOK {
			if err := proto.Unmarshal(rec.Body.Bytes(), resp); err != nil {
				t.Fatal(err)
			}
		}
		return rec, resp
	}

	t.Run("trace on both backends is merged", func(t *testing.T) {
		rec, resp := get("/api/v2/traces/" + traceShared)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		if names := spanNames(resp.Trace); len(names) != 2 {
			t.Fatalf("want 2 spans, got %v", names)
		}
		if resp.Metrics.InspectedBytes != 2 {
			t.Errorf("metrics not summed: %+v", resp.Metrics)
		}
	})

	t.Run("trace on one backend", func(t *testing.T) {
		rec, resp := get("/api/v2/traces/" + traceOnlyA)
		if rec.Code != http.StatusOK || len(spanNames(resp.Trace)) != 1 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("unknown trace is 404", func(t *testing.T) {
		if rec, _ := get("/api/v2/traces/00000000000000000000000000009999"); rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("bad trace id is 400", func(t *testing.T) {
		if rec, _ := get("/api/v2/traces/zzz"); rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d", rec.Code)
		}
	})
}
