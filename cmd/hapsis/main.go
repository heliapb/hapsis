package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/heliapb/hapsis/pkg/config"
	"github.com/heliapb/hapsis/pkg/proxy"
)

var opts struct {
	BindAddr   string
	ConfigPath string
}

func init() {
	flag.StringVar(&opts.BindAddr, "bind-addr", ":3200", "address to listen on")
	flag.StringVar(&opts.ConfigPath, "config", "config.yaml", "path to the configuration file")
}

func main() {
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	p, err := proxy.New(cfg, logger)
	if err != nil {
		logger.Error("failed to build proxy", "err", err)
		os.Exit(1)
	}

	logger.Info("starting hapsis", "addr", opts.BindAddr, "backends", len(cfg.Backends))
	if err := http.ListenAndServe(opts.BindAddr, p.Handler()); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
