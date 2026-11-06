//go:build integration

package chquery

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DemonIngvi/proview-analytics/internal/query"
)

// PROVIEW_TEST_CLICKHOUSE=localhost:9000 go test -tags integration ./internal/chquery/
//
// testdata/*.sql is a copy of the schema owned by proview-event-ingester:
// the test fails if analytics queries drift from that contract.
func TestQueriesAgainstAggregates(t *testing.T) {
	addr := os.Getenv("PROVIEW_TEST_CLICKHOUSE")
	if addr == "" {
		t.Skip("PROVIEW_TEST_CLICKHOUSE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := Open(ctx, Config{Addrs: []string{addr}, Username: "default", Password: os.Getenv("PROVIEW_TEST_CLICKHOUSE_PASSWORD")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, q := range []string{"DROP DATABASE IF EXISTS proview_agg SYNC", "DROP DATABASE IF EXISTS proview SYNC",
		"CREATE DATABASE proview", "CREATE DATABASE proview_agg"} {
		if err := s.conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"../../testdata/0001_events_raw.sql", "../../testdata/0002_aggregates.sql"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(b), ";\n") {
			if strings.TrimSpace(stripComments(stmt)) == "" {
				continue
			}
			if err := s.conn.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
		}
	}

	now := time.Now().UTC().Truncate(time.Hour)
	t0 := now.Add(-3 * time.Hour)
	insert := `INSERT INTO proview.events_raw (event_id, event_type, impression_id, banner_id, placement, event_time, received_at, duration_ms, visitor_id, country, device, page_host) VALUES`
	vals := []string{}
	add := func(id, typ, imp string, banner int, at time.Time, dur int, visitor, country, device string) {
		ts := at.Format("2006-01-02 15:04:05")
		vals = append(vals, "('"+id+"','"+typ+"','"+imp+"',"+itoa(banner)+",'home_top','"+ts+"','"+ts+"',"+itoa(dur)+",'"+visitor+"','"+country+"','"+device+"','x')")
	}
	for i := range 10 {
		imp := "i" + itoa(i)
		add(imp+":0", "impression", imp, 1+i%2, t0.Add(time.Duration(i)*10*time.Minute), 0, "v"+itoa(i%5), "RU", "desktop")
		add(imp+":1", "viewable", imp, 1+i%2, t0.Add(time.Duration(i)*10*time.Minute), 0, "v"+itoa(i%5), "RU", "desktop")
	}
	add("i0:2", "click", "i0", 1, t0, 0, "v0", "RU", "desktop")
	add("i1:2", "hover", "i1", 2, t0, 1500, "v1", "RU", "desktop")
	if err := s.conn.Exec(ctx, insert+strings.Join(vals, ",")); err != nil {
		t.Fatal(err)
	}

	q, _ := url.ParseQuery("from=" + now.Add(-6*time.Hour).Format(time.RFC3339) + "&to=" + now.Add(time.Hour).Format(time.RFC3339) + "&granularity=hour&group_by=banner")
	p, err := query.Parse(q, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	clock := now.Add(time.Hour)

	m, err := s.Metrics(ctx, p.Summary(clock, p.From, p.To))
	if err != nil {
		t.Fatal(err)
	}
	if m.Impressions != 10 || m.Viewable != 10 || m.Clicks != 1 || m.Hovers != 1 || m.HoverMS != 1500 || m.Visitors != 5 || m.CTR != 0.1 {
		t.Fatalf("summary = %+v", m)
	}

	series, err := s.Series(ctx, p.Timeseries(clock))
	if err != nil || len(series) == 0 {
		t.Fatalf("series: %v %d", err, len(series))
	}
	var total uint64
	for _, r := range series {
		total += r.M.Impressions
		if r.Key != "1" && r.Key != "2" {
			t.Fatalf("unexpected key %q", r.Key)
		}
	}
	if total != 10 {
		t.Fatalf("series impressions = %d", total)
	}

	bd, _ := p.Breakdown(clock, "banner", 10)
	rows, err := s.Keyed(ctx, bd)
	if err != nil || len(rows) != 2 || rows[0].M.Impressions != 5 {
		t.Fatalf("breakdown: %v %+v", err, rows)
	}
	heat, err := s.Heat(ctx, p.Heatmap(clock))
	if err != nil || len(heat) == 0 {
		t.Fatalf("heatmap: %v %d", err, len(heat))
	}
	dims, err := s.Dimensions(ctx, p.Dimensions(clock))
	if err != nil || len(dims.Banners) != 2 || dims.Countries[0] != "RU" {
		t.Fatalf("dimensions: %v %+v", err, dims)
	}

	// Filters travel as parameters.
	q.Set("banner", "2")
	q.Set("country", "RU")
	pf, _ := query.Parse(q, clock)
	mf, err := s.Metrics(ctx, pf.Summary(clock, pf.From, pf.To))
	if err != nil || mf.Impressions != 5 || mf.Clicks != 0 {
		t.Fatalf("filtered summary: %v %+v", err, mf)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func stripComments(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "--") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
