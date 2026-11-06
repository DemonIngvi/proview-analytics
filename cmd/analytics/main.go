// Command analytics serves the ProView admin dashboard and its JSON API over
// the ClickHouse aggregator database.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones for the tz parameter in distroless images

	"github.com/DemonIngvi/proview-analytics/internal/cache"
	"github.com/DemonIngvi/proview-analytics/internal/chquery"
	"github.com/DemonIngvi/proview-analytics/internal/config"
	"github.com/DemonIngvi/proview-analytics/internal/httpapi"
	"github.com/DemonIngvi/proview-analytics/internal/names"
	"github.com/DemonIngvi/proview-analytics/internal/platform"
	"github.com/DemonIngvi/proview-analytics/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	log := platform.NewLogger(cfg.LogLevel, "analytics").With("version", version)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := chquery.Open(ctx, cfg.ClickHouse)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	resolver := names.New(cfg.BannerAdminURL, cfg.BannerAdminToken, time.Minute, log)
	go resolver.Run(ctx)
	if cfg.AdminPassword == "" {
		log.Warn("authentication disabled: run only behind an authenticating proxy")
	}

	reg := platform.NewRegistry()
	metrics := platform.NewHTTPMetrics(reg, "proview_analytics")
	api := &httpapi.API{
		Store: st, Names: resolver, Cache: cache.New(cfg.CacheTTL, 2000), Log: log,
		User: cfg.AdminUser, Password: cfg.AdminPassword, UI: web.FS(),
	}

	ready := &platform.Readiness{}
	ready.AddCheck(func() error {
		pctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return st.Ping(pctx)
	})

	return platform.Serve(ctx, log, ready, cfg.ShutdownDelay, cfg.ShutdownTimeout,
		platform.NewServer(cfg.HTTPAddr, platform.Recover(log, metrics.Wrap("analytics", api.Handler()))),
		platform.NewServer(cfg.OpsAddr, platform.OpsHandler(reg, ready)),
	)
}
