package config

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Backend struct {
	Name    string            `yaml:"name"`
	URL     string            `yaml:"url"`
	Timeout time.Duration     `yaml:"timeout"`
	Headers map[string]string `yaml:"headers"`
}

type Config struct {
	Backends []Backend `yaml:"backends"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if len(cfg.Backends) == 0 {
		return nil, fmt.Errorf("at least one backend is required")
	}
	for i := range cfg.Backends {
		be := &cfg.Backends[i]
		if be.Name == "" {
			return nil, fmt.Errorf("backends[%d]: name is required", i)
		}
		if u, err := url.Parse(be.URL); err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("backends[%d] (%s): url must be absolute, got %q", i, be.Name, be.URL)
		}
		if be.Timeout == 0 {
			be.Timeout = 30 * time.Second
		}
	}
	return cfg, nil
}
