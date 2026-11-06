package query

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 6, 12, 30, 0, 0, time.UTC)

func parse(t *testing.T, qs string) Params {
	t.Helper()
	q, _ := url.ParseQuery(qs)
	p, err := Parse(q, now)
	if err != nil {
		t.Fatalf("Parse(%q): %v", qs, err)
	}
	return p
}

func TestParseDefaults(t *testing.T) {
	p := parse(t, "")
	if !p.To.Equal(now) || p.To.Sub(p.From) != 24*time.Hour || p.Granularity != Hour || p.GroupBy != "none" || p.Location != time.UTC {
		t.Fatalf("defaults: %+v", p)
	}
	if g := parse(t, "from=2026-11-06T10:00:00Z").Granularity; g != Minute {
		t.Fatalf("auto granularity for 2.5h = %s", g)
	}
	if g := parse(t, "from=2026-09-01").Granularity; g != Day {
		t.Fatalf("auto granularity for 2 months = %s", g)
	}
}

func TestParseFiltersAndTZ(t *testing.T) {
	p := parse(t, "tz=Europe/Moscow&from=2026-11-01&to=2026-11-03&banner=1,2&placement=home_top&country=ru,de&device=mobile&group_by=country&top=5")
	if p.From.Hour() != 0 || p.From.Location().String() != "Europe/Moscow" {
		t.Fatalf("from parsed in tz: %v", p.From)
	}
	if len(p.Filter.Banners) != 2 || p.Filter.Countries[1] != "DE" || p.Filter.Devices[0] != "mobile" || p.Top != 5 {
		t.Fatalf("filters: %+v", p.Filter)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"tz=Mars/Base",
		"from=yesterday",
		"from=2026-11-06&to=2026-11-05",
		"granularity=week",
		"granularity=minute&from=2026-11-01",
		"granularity=hour&from=2026-01-01",
		"group_by=page",
		"top=0",
		"banner=abc",
		"placement=DROP TABLE",
		"country=RUS",
		"device=tv",
		"granularity=minute&from=2026-10-20T00:00:00Z&to=2026-10-21T00:00:00Z",
	}
	for _, qs := range bad {
		q, _ := url.ParseQuery(qs)
		if _, err := Parse(q, now); err == nil {
			t.Errorf("%q accepted", qs)
		}
	}
}

func TestStatementsUseParametersOnly(t *testing.T) {
	p := parse(t, "from=2026-11-06T00:00:00Z&banner=7&placement=home_top&country=RU&device=desktop&group_by=banner")
	ts := p.Timeseries(now)
	for _, want := range []string{"banner_stats_1m", "banner_id IN {banners:Array(UInt64)}", "placement IN {placements:Array(String)}",
		"LIMIT {top:UInt32}", "toString(banner_id) AS key", "uniqMergeIf(visitors"} {
		if !strings.Contains(ts.SQL, want) {
			t.Errorf("timeseries SQL missing %q:\n%s", want, ts.SQL)
		}
	}
	if strings.Contains(ts.SQL, "home_top") || strings.Contains(ts.SQL, "'RU'") {
		t.Error("user values interpolated into SQL")
	}
	if ts.Params["banners"] != "[7]" || ts.Params["placements"] != "['home_top']" || ts.Params["top"] != "10" || ts.Params["tz"] != "UTC" {
		t.Errorf("params = %v", ts.Params)
	}

	long := parse(t, "from=2026-10-01&to=2026-11-01")
	if s := long.Summary(now, long.From, long.To); !strings.Contains(s.SQL, "banner_stats_1h") {
		t.Errorf("long range must use the hourly table: %s", s.SQL)
	}
	if _, err := p.Breakdown(now, "none", 10); err == nil {
		t.Error("breakdown by none accepted")
	}
	if b, err := p.Breakdown(now, "device", 10); err != nil || !strings.Contains(b.SQL, "device AS key") {
		t.Errorf("breakdown: %v %s", err, b.SQL)
	}
	if h := p.Heatmap(now); !strings.Contains(h.SQL, "toDayOfWeek") {
		t.Error("heatmap SQL")
	}
}

func TestBucketsAndPrevious(t *testing.T) {
	p := parse(t, "from=2026-11-06T10:00:30Z&to=2026-11-06T10:05:00Z&granularity=minute")
	if b := p.Buckets(); len(b) != 5 || b[0] != time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("minute buckets = %v", b)
	}
	d := parse(t, "tz=Europe/Moscow&from=2026-11-01&to=2026-11-04&granularity=day")
	if b := d.Buckets(); len(b) != 3 || time.Unix(b[0], 0).In(d.Location).Hour() != 0 {
		t.Fatalf("day buckets = %v", b)
	}
	from, to := d.Previous()
	if !to.Equal(d.From) || d.From.Sub(from) != d.To.Sub(d.From) {
		t.Fatal("previous period")
	}
	if k1, k2 := d.CacheKey("a"), d.CacheKey("a"); k1 != k2 || k1 == d.CacheKey("b") {
		t.Fatal("cache key")
	}
}

func TestDerive(t *testing.T) {
	m := Metrics{Impressions: 200, Viewable: 150, Clicks: 4, Hovers: 20, HoverMS: 10000, ActiveMS: 400000}
	m.Derive()
	if m.CTR != 0.02 || m.Viewability != 0.75 || m.AvgHoverMS != 500 || m.AvgActiveMS != 2000 || m.HoverRate != 0.1 {
		t.Fatalf("derived = %+v", m)
	}
	var z Metrics
	z.Derive()
	if z.CTR != 0 {
		t.Fatal("division by zero")
	}
	z.Add(m)
	if z.Impressions != 200 {
		t.Fatal("Add")
	}
}

func TestFromIsFlooredToBucket(t *testing.T) {
	p := parse(t, "from=2026-11-06T10:00:40Z&to=2026-11-06T11:00:00Z")
	st := p.Summary(now, p.From, p.To)
	if st.Params["from"] != "1793959200" { // 10:00:00
		t.Fatalf("minute table: from = %s", st.Params["from"])
	}
	long := parse(t, "from=2026-10-01T10:30:00Z&to=2026-11-01T00:00:00Z")
	if got := long.Summary(now, long.From, long.To).Params["from"]; got != "1790848800" { // 2026-10-01 10:00
		t.Fatalf("hour table: from = %s", got)
	}
}
