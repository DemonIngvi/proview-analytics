// Package chquery executes analytics statements against ClickHouse.
package chquery

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/DemonIngvi/proview-analytics/internal/query"
)

// Config describes the ClickHouse connection. Use a read-only user.
type Config struct {
	Addrs            []string
	Username         string
	Password         string
	TLS              bool
	MaxExecutionTime time.Duration
}

// Store runs read-only queries.
type Store struct {
	conn    driver.Conn
	maxExec int
}

// SeriesRow is one (bucket, key) cell.
type SeriesRow struct {
	T   int64
	Key string
	M   query.Metrics
}

// KeyedRow is one breakdown row.
type KeyedRow struct {
	Key string
	M   query.Metrics
}

// HeatRow is one weekday/hour cell.
type HeatRow struct {
	Dow, Hour uint8
	M         query.Metrics
}

// Dims lists values available for filters.
type Dims struct {
	Banners, Placements, Countries, Devices []string
}

// Open connects and pings ClickHouse.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	opts := &clickhouse.Options{
		Addr:             cfg.Addrs,
		Auth:             clickhouse.Auth{Database: "default", Username: cfg.Username, Password: cfg.Password},
		DialTimeout:      5 * time.Second,
		MaxOpenConns:     16,
		MaxIdleConns:     4,
		ConnMaxLifetime:  time.Hour,
		Compression:      &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		ConnOpenStrategy: clickhouse.ConnOpenRoundRobin,
	}
	if cfg.TLS {
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Ping(pctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	maxExec := int(cfg.MaxExecutionTime.Seconds())
	if maxExec < 1 {
		maxExec = 10
	}
	return &Store{conn: conn, maxExec: maxExec}, nil
}

// Close closes the pool.
func (s *Store) Close() error { return s.conn.Close() }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.conn.Ping(ctx) }

func (s *Store) ctx(ctx context.Context, st query.Statement) context.Context {
	return clickhouse.Context(ctx,
		clickhouse.WithParameters(clickhouse.Parameters(st.Params)),
		clickhouse.WithSettings(clickhouse.Settings{"max_execution_time": s.maxExec, "max_result_rows": 200000}))
}

func scanMetrics(dst *query.Metrics) []any {
	return []any{&dst.Impressions, &dst.Viewable, &dst.Clicks, &dst.Hovers, &dst.HoverMS, &dst.ActiveMS, &dst.Visitors}
}

// Metrics returns a single totals row.
func (s *Store) Metrics(ctx context.Context, st query.Statement) (query.Metrics, error) {
	var m query.Metrics
	err := s.conn.QueryRow(s.ctx(ctx, st), st.SQL).Scan(scanMetrics(&m)...)
	m.Derive()
	return m, err
}

// Series returns bucketed rows.
func (s *Store) Series(ctx context.Context, st query.Statement) ([]SeriesRow, error) {
	rows, err := s.conn.Query(s.ctx(ctx, st), st.SQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SeriesRow
	for rows.Next() {
		var r SeriesRow
		var t uint32
		if err := rows.Scan(append([]any{&t, &r.Key}, scanMetrics(&r.M)...)...); err != nil {
			return nil, err
		}
		r.T = int64(t)
		r.M.Derive()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Keyed returns breakdown rows.
func (s *Store) Keyed(ctx context.Context, st query.Statement) ([]KeyedRow, error) {
	rows, err := s.conn.Query(s.ctx(ctx, st), st.SQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []KeyedRow
	for rows.Next() {
		var r KeyedRow
		if err := rows.Scan(append([]any{&r.Key}, scanMetrics(&r.M)...)...); err != nil {
			return nil, err
		}
		r.M.Derive()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Heat returns weekday/hour cells.
func (s *Store) Heat(ctx context.Context, st query.Statement) ([]HeatRow, error) {
	rows, err := s.conn.Query(s.ctx(ctx, st), st.SQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HeatRow
	for rows.Next() {
		var r HeatRow
		if err := rows.Scan(append([]any{&r.Dow, &r.Hour}, scanMetrics(&r.M)...)...); err != nil {
			return nil, err
		}
		r.M.Derive()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Dimensions returns distinct filter values.
func (s *Store) Dimensions(ctx context.Context, st query.Statement) (Dims, error) {
	var d Dims
	err := s.conn.QueryRow(s.ctx(ctx, st), st.SQL).Scan(&d.Banners, &d.Placements, &d.Countries, &d.Devices)
	return d, err
}
