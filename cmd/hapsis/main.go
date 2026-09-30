package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/heliapb/hapsis/pkg/config"
	"github.com/heliapb/hapsis/pkg/proxy"
)

func main() {
	bindAddr := flag.String("bind-addr", ":3200", "address to listen on")
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	p, err := proxy.New(cfg, logger)
	if err != nil {
		logger.Error("failed to build proxy", "err", err)
		os.Exit(1)
	}

	logger.Info("starting hapsis", "addr", *bindAddr, "backends", len(cfg.Backends))
	if err := http.ListenAndServe(*bindAddr, p.Handler()); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
