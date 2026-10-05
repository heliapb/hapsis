package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cfg, err := Parse([]byte(`
backends:
  - name: a
    url: http://a:3200
  - name: b
    url: https://b
    timeout: 5s
    headers:
      X-Scope-OrgID: t
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends) != 2 {
		t.Fatalf("want 2 backends, got %d", len(cfg.Backends))
	}
	if cfg.Backends[0].Timeout != 30*time.Second {
		t.Errorf("default timeout not applied: %v", cfg.Backends[0].Timeout)
	}
	if cfg.Backends[1].Timeout != 5*time.Second || cfg.Backends[1].Headers["X-Scope-OrgID"] != "t" {
		t.Errorf("explicit values lost: %+v", cfg.Backends[1])
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"invalid yaml": "backends: [",
		"no backends":  "backends: []",
		"missing name": "backends:\n  - url: http://a",
		"relative url": "backends:\n  - name: a\n    url: a:3200",
		"no scheme":    "backends:\n  - name: a\n    url: //a:3200",
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("backends:\n  - {name: a, url: http://a}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || len(cfg.Backends) != 1 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected not-found error, got %v", err)
	}
}
