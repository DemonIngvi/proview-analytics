// Package config loads analytics configuration from the environment.
package config

import (
	"errors"
	"time"

	"github.com/DemonIngvi/proview-analytics/internal/chquery"
	"github.com/DemonIngvi/proview-analytics/internal/platform"
)

// Config is the analytics configuration.
type Config struct {
	HTTPAddr         string
	OpsAddr          string
	ClickHouse       chquery.Config
	CacheTTL         time.Duration
	AdminUser        string
	AdminPassword    string
	AuthDisabled     bool
	BannerAdminURL   string
	BannerAdminToken string
	ShutdownDelay    time.Duration
	ShutdownTimeout  time.Duration
	LogLevel         string
}

// Load reads configuration using lookup.
func Load(lookup func(string) (string, bool)) (Config, error) {
	c := Config{
		HTTPAddr: platform.Env(lookup, "HTTP_ADDR", ":8080"),
		OpsAddr:  platform.Env(lookup, "OPS_ADDR", ":9090"),
		ClickHouse: chquery.Config{
			Addrs:    platform.EnvList(lookup, "CLICKHOUSE_ADDRS", nil),
			Username: platform.Env(lookup, "CLICKHOUSE_USER", "default"),
			Password: platform.Env(lookup, "CLICKHOUSE_PASSWORD", ""),
			TLS:      platform.Env(lookup, "CLICKHOUSE_TLS", "false") == "true",
		},
		AdminUser:        platform.Env(lookup, "ADMIN_USER", "admin"),
		AdminPassword:    platform.Env(lookup, "ADMIN_PASSWORD", ""),
		AuthDisabled:     platform.Env(lookup, "AUTH_DISABLED", "false") == "true",
		BannerAdminURL:   platform.Env(lookup, "BANNER_ADMIN_URL", ""),
		BannerAdminToken: platform.Env(lookup, "BANNER_ADMIN_TOKEN", ""),
		LogLevel:         platform.Env(lookup, "LOG_LEVEL", "info"),
	}
	var errs []error
	var err error
	if c.ClickHouse.MaxExecutionTime, err = platform.EnvDuration(lookup, "QUERY_TIMEOUT", 10*time.Second); err != nil {
		errs = append(errs, err)
	}
	if c.CacheTTL, err = platform.EnvDuration(lookup, "CACHE_TTL", 15*time.Second); err != nil {
		errs = append(errs, err)
	}
	if c.ShutdownDelay, err = platform.EnvDuration(lookup, "SHUTDOWN_DELAY", 3*time.Second); err != nil {
		errs = append(errs, err)
	}
	if c.ShutdownTimeout, err = platform.EnvDuration(lookup, "SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		errs = append(errs, err)
	}
	if len(c.ClickHouse.Addrs) == 0 {
		errs = append(errs, errors.New("CLICKHOUSE_ADDRS is required"))
	}
	if c.AdminPassword == "" && !c.AuthDisabled {
		errs = append(errs, errors.New("ADMIN_PASSWORD is required (or AUTH_DISABLED=true behind an authenticating proxy)"))
	}
	if c.AuthDisabled {
		c.AdminPassword = ""
	}
	return c, errors.Join(errs...)
}
