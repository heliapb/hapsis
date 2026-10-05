package proxy

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
// For an unknown trace it answers like Tempo 3.1: 200 with an empty trace.
// With notFoundAs404 it answers 404 instead, as older Tempo versions do.
func fakeTempo(t *testing.T, traces map[string]*tempopb.Trace, notFoundAs404 bool) *httptest.Server {
	t.Helper()
	r := mux.NewRouter()
	r.HandleFunc("/api/v2/traces/{traceID}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.HeaderAccept) != api.HeaderAcceptProtobuf {
			t.Errorf("expected protobuf Accept, got %q", r.Header.Get(api.HeaderAccept))
		}
		tr, ok := traces[mux.Vars(r)["traceID"]]
		if !ok && notFoundAs404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !ok {
			tr = &tempopb.Trace{}
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
	for _, notFoundAs404 := range []bool{false, true} {
		t.Run(fmt.Sprintf("backends404=%v", notFoundAs404), func(t *testing.T) {
			testTraceByID(t, notFoundAs404)
		})
	}
}

func testTraceByID(t *testing.T, notFoundAs404 bool) {
	a := fakeTempo(t, map[string]*tempopb.Trace{
		traceShared: trace(traceShared, "0000000000000001", "from-a"),
		traceOnlyA:  trace(traceOnlyA, "0000000000000002", "only-a"),
	}, notFoundAs404)
	defer a.Close()
	b := fakeTempo(t, map[string]*tempopb.Trace{
		traceShared: trace(traceShared, "0000000000000003", "from-b"),
	}, notFoundAs404)
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

	t.Run("v1 returns a bare trace in legacy json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/traces/"+traceShared, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		// v1 is the trace itself, not wrapped in {"trace": ...}.
		if strings.Contains(body, `"trace"`) || !strings.Contains(body, `"batches"`) {
			t.Fatalf("expected legacy v1 layout with top-level batches, got %s", body)
		}
		if !strings.Contains(body, "from-a") || !strings.Contains(body, "from-b") {
			t.Fatalf("spans from both backends missing: %s", body)
		}
	})

	t.Run("v1 protobuf is a bare Trace", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/traces/"+traceOnlyA, nil)
		req.Header.Set(api.HeaderAccept, api.HeaderAcceptProtobuf)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		tr := &tempopb.Trace{}
		if err := proto.Unmarshal(rec.Body.Bytes(), tr); err != nil {
			t.Fatal(err)
		}
		if names := spanNames(tr); len(names) != 1 || names[0] != "only-a" {
			t.Fatalf("got %v", names)
		}
	})

	t.Run("v1 unknown trace is 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/traces/00000000000000000000000000009999", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("v2 unknown trace is 200 with an empty trace, like Tempo", func(t *testing.T) {
		rec, resp := get("/api/v2/traces/00000000000000000000000000009999")
		if rec.Code != http.StatusOK || len(spanNames(resp.Trace)) != 0 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("bad trace id is 400", func(t *testing.T) {
		if rec, _ := get("/api/v2/traces/zzz"); rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestBuildRequest(t *testing.T) {
	base, _ := url.Parse("https://tempo.example.com/prefix/")
	b := &backend{name: "eu", url: base, headers: map[string]string{"X-Scope-OrgID": "eu-tenant"}}

	in := httptest.NewRequest(http.MethodGet, "/api/v2/traces/abc?start=1&end=2", nil)
	in.Header.Set("X-Scope-OrgID", "client-tenant")
	in.Header.Set("Authorization", "Bearer tok")
	in.Header.Set("Cookie", "secret=1")

	req, err := buildRequest(context.Background(), b, in, "/api/v2/traces/abc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := req.URL.String(), "https://tempo.example.com/prefix/api/v2/traces/abc?start=1&end=2"; got != want {
		t.Errorf("url: got %s want %s", got, want)
	}
	if got := req.Header.Get(api.HeaderAccept); got != api.HeaderAcceptProtobuf {
		t.Errorf("accept: %q", got)
	}
	if got := req.Header.Get("X-Scope-OrgID"); got != "eu-tenant" {
		t.Errorf("backend header must override client header, got %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("forwarded header lost: %q", got)
	}
	if got := req.Header.Get("Cookie"); got != "" {
		t.Errorf("unlisted header must not be forwarded, got %q", got)
	}
}

func newProxyFor(t *testing.T, urls ...string) http.Handler {
	t.Helper()
	doc := "backends:\n"
	for i, u := range urls {
		doc += fmt.Sprintf("  - {name: b%d, url: %s}\n", i, u)
	}
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return p.Handler()
}

func do(h http.Handler, path, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		req.Header.Set(api.HeaderAccept, accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBackendErrors(t *testing.T) {
	good := fakeTempo(t, map[string]*tempopb.Trace{traceOnlyA: trace(traceOnlyA, "0000000000000002", "only-a")}, false)
	defer good.Close()
	status := func(code int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, body, code)
		}))
	}
	internal := status(http.StatusInternalServerError, "boom")
	defer internal.Close()
	badRequest := status(http.StatusBadRequest, "bad query")
	defer badRequest.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close() // connection refused from here on

	cases := []struct {
		name     string
		backends []string
		want     int
		body     string
	}{
		{"5xx from a backend is relayed", []string{good.URL, internal.URL}, http.StatusInternalServerError, "backend b1: boom"},
		{"4xx from a backend is relayed", []string{good.URL, badRequest.URL}, http.StatusBadRequest, "backend b1: bad query"},
		{"unreachable backend is 502", []string{good.URL, dead.URL}, http.StatusBadGateway, "backend b1:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(newProxyFor(t, c.backends...), "/api/v2/traces/"+traceOnlyA, "")
			if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.body) {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestV2JSON(t *testing.T) {
	a := fakeTempo(t, map[string]*tempopb.Trace{traceOnlyA: trace(traceOnlyA, "0000000000000002", "only-a")}, false)
	defer a.Close()
	rec := do(newProxyFor(t, a.URL), "/api/v2/traces/"+traceOnlyA, "")
	if rec.Code != http.StatusOK || rec.Header().Get(api.HeaderContentType) != api.HeaderAcceptJSON {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get(api.HeaderContentType))
	}
	// jsonpb layout: trace wrapped, resourceSpans rather than batches.
	body := rec.Body.String()
	if !strings.Contains(body, `"trace"`) || !strings.Contains(body, `"resourceSpans"`) || !strings.Contains(body, "only-a") {
		t.Fatalf("unexpected v2 json: %s", body)
	}
}

func TestEcho(t *testing.T) {
	a := fakeTempo(t, nil, false)
	defer a.Close()
	if rec := do(newProxyFor(t, a.URL), "/api/echo", ""); rec.Code != http.StatusOK || rec.Body.String() != "echo" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	cfg := &config.Config{Backends: []config.Backend{{Name: "a", URL: "http://[::1"}}}
	if _, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("expected error for unparsable url")
	}
}
