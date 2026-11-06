package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/DemonIngvi/proview-analytics/internal/cache"
	"github.com/DemonIngvi/proview-analytics/internal/chquery"
	"github.com/DemonIngvi/proview-analytics/internal/query"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	calls atomic.Int32
	err   error
	last  query.Statement
}

func (f *fakeStore) Metrics(_ context.Context, st query.Statement) (query.Metrics, error) {
	f.calls.Add(1)
	f.last = st
	m := query.Metrics{Impressions: 100, Clicks: 5}
	m.Derive()
	return m, f.err
}

func (f *fakeStore) Series(_ context.Context, st query.Statement) ([]chquery.SeriesRow, error) {
	f.calls.Add(1)
	f.last = st
	h := now.Add(-2 * time.Hour).Truncate(time.Hour).Unix()
	return []chquery.SeriesRow{
		{T: h, Key: "1", M: query.Metrics{Impressions: 10, Clicks: 1}},
		{T: h, Key: "2", M: query.Metrics{Impressions: 4}},
		{T: h + 3600, Key: "1", M: query.Metrics{Impressions: 6, Clicks: 1}},
	}, f.err
}

func (f *fakeStore) Keyed(context.Context, query.Statement) ([]chquery.KeyedRow, error) {
	f.calls.Add(1)
	return []chquery.KeyedRow{{Key: "mobile", M: query.Metrics{Impressions: 3}}, {Key: "", M: query.Metrics{Impressions: 1}}}, f.err
}

func (f *fakeStore) Heat(context.Context, query.Statement) ([]chquery.HeatRow, error) {
	f.calls.Add(1)
	return []chquery.HeatRow{{Dow: 5, Hour: 10, M: query.Metrics{Impressions: 7}}}, f.err
}

func (f *fakeStore) Dimensions(context.Context, query.Statement) (chquery.Dims, error) {
	f.calls.Add(1)
	return chquery.Dims{Banners: []string{"10", "2"}, Placements: []string{"home_top"}, Countries: []string{"RU", ""}, Devices: []string{"desktop"}}, f.err
}

type labels struct{}

func (labels) Label(id uint64) string { return map[uint64]string{1: "#1 Aurora"}[id] }

func newAPI(st Store, password string) http.Handler {
	return (&API{Store: st, Names: labels{}, Cache: cache.New(time.Minute, 100), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now }, User: "admin", Password: password,
		UI: fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}}).Handler()
}

func get(h http.Handler, path string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuth(t *testing.T) {
	h := newAPI(&fakeStore{}, "secret")
	if rec := get(h, "/api/v1/summary", false); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no auth: %d", rec.Code)
	}
	if rec := get(h, "/", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ui") {
		t.Fatalf("ui: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/", true); rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP")
	}
}

func TestSummaryAndCache(t *testing.T) {
	st := &fakeStore{}
	h := newAPI(st, "secret")
	rec := get(h, "/api/v1/summary?from=2026-11-05T12:00:00Z&to=2026-11-06T12:00:00Z", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var resp SummaryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Current.CTR != 0.05 || resp.Previous.Impressions != 100 {
		t.Fatalf("resp = %+v", resp)
	}
	get(h, "/api/v1/summary?from=2026-11-05T12:00:00Z&to=2026-11-06T12:00:00Z", true)
	if st.calls.Load() != 2 {
		t.Fatalf("store calls = %d, want 2 (current+previous, then cached)", st.calls.Load())
	}
}

func TestTimeseriesGapFillAndLabels(t *testing.T) {
	h := newAPI(&fakeStore{}, "")
	rec := get(h, "/api/v1/timeseries?from=2026-11-06T06:00:00Z&to=2026-11-06T12:00:00Z&granularity=hour&group_by=banner", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var resp TimeseriesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Buckets) != 6 || len(resp.Series) != 2 {
		t.Fatalf("buckets=%d series=%d", len(resp.Buckets), len(resp.Series))
	}
	s := resp.Series[0]
	if s.Label != "#1 Aurora" || len(s.Points) != 6 || s.Points[4].Impressions != 10 || s.Points[5].Impressions != 6 || s.Points[0].Impressions != 0 {
		t.Fatalf("series = %+v", s)
	}
	if s.Total.Impressions != 16 || s.Total.CTR != 0.125 {
		t.Fatalf("total = %+v", s.Total)
	}
}

func TestBreakdownHeatmapDimensions(t *testing.T) {
	h := newAPI(&fakeStore{}, "")
	rec := get(h, "/api/v1/breakdown?dimension=device", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"label":"не определено"`) {
		t.Fatalf("breakdown: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/v1/breakdown?dimension=none", false); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad dimension: %d", rec.Code)
	}
	if rec := get(h, "/api/v1/heatmap", false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dow":5`) {
		t.Fatalf("heatmap: %s", rec.Body)
	}
	rec = get(h, "/api/v1/dimensions", false)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Index(body, `"value":"2"`) > strings.Index(body, `"value":"10"`) || strings.Contains(body, `"value":""`) {
		t.Fatalf("dimensions: %s", body)
	}
}

func TestErrors(t *testing.T) {
	h := newAPI(&fakeStore{err: errors.New("ch down")}, "")
	if rec := get(h, "/api/v1/summary", false); rec.Code != http.StatusBadGateway {
		t.Fatalf("store error: %d", rec.Code)
	}
	if rec := get(h, "/api/v1/summary?tz=Nowhere", false); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad params: %d", rec.Code)
	}
}
