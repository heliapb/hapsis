package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"
	"github.com/grafana/tempo/v3/pkg/api"

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

// New builds a Proxy from a validated configuration
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
			client:  &http.Client{Transport: newTransport(), Timeout: b.Timeout},
		})
	}
	return p, nil
}

// newTransport returns a transport tuned for many concurrent requests to a single host
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     5 * time.Minute,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

func (p *Proxy) Handler() http.Handler {
	req := mux.NewRouter()
	req.HandleFunc(api.PathTraces, p.handleTraceByID(false)).Methods(http.MethodGet)
	req.HandleFunc(api.PathTracesV2, p.handleTraceByID(true)).Methods(http.MethodGet)
	req.HandleFunc(api.PathEcho, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("echo")) }).Methods(http.MethodGet)
	return req
}
