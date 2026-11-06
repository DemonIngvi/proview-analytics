// Package query validates analytics request parameters and builds
// parameterised ClickHouse SQL over the aggregator database (proview_agg).
//
// Only whitelisted identifiers are ever interpolated into SQL; every
// user-supplied value travels as a server-side query parameter.
package query

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Granularities.
const (
	Minute = "minute"
	Hour   = "hour"
	Day    = "day"
)

// Dimensions available for grouping and filtering.
var dimensions = map[string]string{
	"none":      "''",
	"banner":    "toString(banner_id)",
	"placement": "placement",
	"country":   "country",
	"device":    "device",
}

// MaxRange per granularity keeps result sizes bounded.
var MaxRange = map[string]time.Duration{
	Minute: 48 * time.Hour,
	Hour:   93 * 24 * time.Hour,
	Day:    730 * 24 * time.Hour,
}

// minuteTableRetention is slightly below the 14-day TTL of banner_stats_1m.
const minuteTableRetention = 13 * 24 * time.Hour

var (
	placementRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	countryRe   = regexp.MustCompile(`^[A-Z]{2}$`)
	devices     = []string{"desktop", "mobile", "tablet", "bot"}
)

// Filter restricts the slice.
type Filter struct {
	Banners    []uint64
	Placements []string
	Countries  []string
	Devices    []string
}

// Params is a validated analytics request.
type Params struct {
	From, To    time.Time
	Location    *time.Location
	Granularity string
	GroupBy     string
	Top         int
	Filter      Filter
}

// Parse validates query-string parameters. now anchors relative defaults.
func Parse(q url.Values, now time.Time) (Params, error) {
	p := Params{Granularity: q.Get("granularity"), GroupBy: q.Get("group_by"), Top: 10}
	var errs []error

	tz := q.Get("tz")
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		errs = append(errs, fmt.Errorf("tz: unknown time zone %q", tz))
		loc = time.UTC
	}
	p.Location = loc

	p.To = now
	if v := q.Get("to"); v != "" {
		if p.To, err = parseTime(v, loc); err != nil {
			errs = append(errs, fmt.Errorf("to: %w", err))
		}
	}
	p.From = p.To.Add(-24 * time.Hour)
	if v := q.Get("from"); v != "" {
		if p.From, err = parseTime(v, loc); err != nil {
			errs = append(errs, fmt.Errorf("from: %w", err))
		}
	}
	if !p.From.Before(p.To) {
		errs = append(errs, errors.New("from must be before to"))
	}

	if p.Granularity == "" {
		p.Granularity = autoGranularity(p.To.Sub(p.From))
	}
	maxRange, ok := MaxRange[p.Granularity]
	switch {
	case !ok:
		errs = append(errs, errors.New("granularity must be minute, hour or day"))
	case p.To.Sub(p.From) > maxRange:
		errs = append(errs, fmt.Errorf("range too large for %s granularity (max %s)", p.Granularity, maxRange))
	case p.Granularity == Minute && now.Sub(p.From) > minuteTableRetention:
		errs = append(errs, errors.New("minute granularity is only available for the last 13 days"))
	}

	if p.GroupBy == "" {
		p.GroupBy = "none"
	}
	if _, ok := dimensions[p.GroupBy]; !ok {
		errs = append(errs, errors.New("group_by must be none, banner, placement, country or device"))
	}
	if v := q.Get("top"); v != "" {
		if p.Top, err = strconv.Atoi(v); err != nil || p.Top < 1 || p.Top > 50 {
			errs = append(errs, errors.New("top must be within 1..50"))
		}
	}

	for _, s := range splitList(q.Get("banner")) {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil || id == 0 {
			errs = append(errs, fmt.Errorf("banner: invalid id %q", s))
			continue
		}
		p.Filter.Banners = append(p.Filter.Banners, id)
	}
	p.Filter.Placements, errs = validList(q.Get("placement"), "placement", func(s string) bool { return placementRe.MatchString(s) }, errs)
	p.Filter.Countries, errs = validList(strings.ToUpper(q.Get("country")), "country", func(s string) bool { return countryRe.MatchString(s) }, errs)
	p.Filter.Devices, errs = validList(q.Get("device"), "device", func(s string) bool { return slices.Contains(devices, s) }, errs)
	return p, errors.Join(errs...)
}

func autoGranularity(d time.Duration) string {
	switch {
	case d <= 6*time.Hour:
		return Minute
	case d <= 14*24*time.Hour:
		return Hour
	default:
		return Day
	}
}

func parseTime(v string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02"} {
		var t time.Time
		var err error
		if layout == time.RFC3339 {
			t, err = time.Parse(layout, v)
		} else {
			t, err = time.ParseInLocation(layout, v, loc)
		}
		if err == nil {
			return t, nil
		}
	}
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(sec, 0), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q (use RFC3339, YYYY-MM-DD[THH:MM] or unix seconds)", v)
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func validList(v, name string, ok func(string) bool, errs []error) ([]string, []error) {
	var out []string
	for _, s := range splitList(v) {
		if !ok(s) {
			errs = append(errs, fmt.Errorf("%s: invalid value %q", name, s))
			continue
		}
		out = append(out, s)
	}
	return out, errs
}

// Statement is SQL plus its server-side parameters.
type Statement struct {
	SQL    string
	Params map[string]string
}

// MetricsSQL selects the base counters; derived metrics are computed in Go.
const MetricsSQL = `sumIf(events, event_type = 'impression') AS impressions,
	sumIf(events, event_type = 'viewable') AS viewable,
	sumIf(events, event_type = 'click') AS clicks,
	sumIf(events, event_type = 'hover') AS hovers,
	sumIf(duration_ms, event_type = 'hover') AS hover_ms,
	sumIf(duration_ms, event_type = 'active_time') AS active_ms,
	uniqMergeIf(visitors, event_type = 'impression') AS visitors`

// table picks the minute table for short, recent ranges and the hourly one
// otherwise.
func (p Params) table(now time.Time) string { return p.tableFor(now, p.From, p.To) }

func (p Params) tableFor(now, from, to time.Time) string {
	if now.Sub(from) < minuteTableRetention && (p.Granularity == Minute || to.Sub(from) <= 48*time.Hour) {
		return "proview_agg.banner_stats_1m"
	}
	return "proview_agg.banner_stats_1h"
}

// where builds the filter. from is floored to the bucket size of table so
// that a partially covered first bucket is included rather than dropped.
func (p Params) where(params map[string]string, table string, from, to time.Time) string {
	step := time.Minute
	if strings.HasSuffix(table, "_1h") {
		step = time.Hour
	}
	params["from"] = strconv.FormatInt(from.Truncate(step).Unix(), 10)
	params["to"] = strconv.FormatInt(to.Unix(), 10)
	conds := []string{"bucket >= toDateTime({from:UInt32}, 'UTC')", "bucket < toDateTime({to:UInt32}, 'UTC')"}
	if len(p.Filter.Banners) > 0 {
		ids := make([]string, len(p.Filter.Banners))
		for i, id := range p.Filter.Banners {
			ids[i] = strconv.FormatUint(id, 10)
		}
		params["banners"] = "[" + strings.Join(ids, ",") + "]"
		conds = append(conds, "banner_id IN {banners:Array(UInt64)}")
	}
	addStrings := func(key, col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		// Values are validated against strict patterns above, so quoting is safe.
		params[key] = "['" + strings.Join(vals, "','") + "']"
		conds = append(conds, col+" IN {"+key+":Array(String)}")
	}
	addStrings("placements", "placement", p.Filter.Placements)
	addStrings("countries", "country", p.Filter.Countries)
	addStrings("devices", "device", p.Filter.Devices)
	return strings.Join(conds, " AND ")
}

// bucketExpr returns the SQL that truncates bucket to the granularity in tz.
func (p Params) bucketExpr() string {
	switch p.Granularity {
	case Minute:
		return "toUnixTimestamp(bucket)"
	case Hour:
		return "toUnixTimestamp(toStartOfHour(toTimeZone(bucket, {tz:String})))"
	default:
		return "toUnixTimestamp(toStartOfDay(toTimeZone(bucket, {tz:String})))"
	}
}

// Summary builds totals for [from, to).
func (p Params) Summary(now time.Time, from, to time.Time) Statement {
	params := map[string]string{}
	sql := "SELECT " + MetricsSQL + " FROM " + p.tableFor(now, from, to) + " WHERE " + p.where(params, p.tableFor(now, from, to), from, to)
	return Statement{SQL: sql, Params: params}
}

// topFilter limits grouped queries to the top-N keys by impressions.
func (p Params) topFilter(now time.Time, params map[string]string, dim string) string {
	if p.GroupBy == "none" {
		return ""
	}
	params["top"] = strconv.Itoa(p.Top)
	return " AND " + dim + " IN (SELECT " + dim + " FROM " + p.table(now) + " WHERE " + p.where(params, p.table(now), p.From, p.To) +
		" GROUP BY " + dim + " ORDER BY sumIf(events, event_type = 'impression') DESC LIMIT {top:UInt32})"
}

// Timeseries builds per-bucket metrics, optionally grouped.
func (p Params) Timeseries(now time.Time) Statement {
	params := map[string]string{"tz": p.Location.String()}
	dim := dimensions[p.GroupBy]
	sql := "SELECT " + p.bucketExpr() + " AS t, " + dim + " AS key, " + MetricsSQL +
		" FROM " + p.table(now) + " WHERE " + p.where(params, p.table(now), p.From, p.To) + p.topFilter(now, params, dim) +
		" GROUP BY t, key ORDER BY t, key"
	return Statement{SQL: sql, Params: params}
}

// Breakdown builds metrics grouped by a dimension, sorted by impressions.
func (p Params) Breakdown(now time.Time, dimension string, limit int) (Statement, error) {
	dim, ok := dimensions[dimension]
	if !ok || dimension == "none" {
		return Statement{}, errors.New("dimension must be banner, placement, country or device")
	}
	params := map[string]string{"row_limit": strconv.Itoa(limit)}
	sql := "SELECT " + dim + " AS key, " + MetricsSQL + " FROM " + p.table(now) + " WHERE " + p.where(params, p.table(now), p.From, p.To) +
		" GROUP BY key ORDER BY impressions DESC, key LIMIT {row_limit:UInt32}"
	return Statement{SQL: sql, Params: params}, nil
}

// Heatmap builds metrics by weekday (1=Mon) and hour of day in tz.
func (p Params) Heatmap(now time.Time) Statement {
	params := map[string]string{"tz": p.Location.String()}
	sql := "SELECT toDayOfWeek(toTimeZone(bucket, {tz:String})) AS dow, toHour(toTimeZone(bucket, {tz:String})) AS hour, " + MetricsSQL +
		" FROM " + p.table(now) + " WHERE " + p.where(params, p.table(now), p.From, p.To) + " GROUP BY dow, hour ORDER BY dow, hour"
	return Statement{SQL: sql, Params: params}
}

// Dimensions lists distinct filter values present in the range.
func (p Params) Dimensions(now time.Time) Statement {
	params := map[string]string{}
	sql := "SELECT groupUniqArray(toString(banner_id)), groupUniqArray(placement), groupUniqArray(country), groupUniqArray(device) FROM " +
		p.table(now) + " WHERE " + p.where(params, p.table(now), p.From, p.To)
	return Statement{SQL: sql, Params: params}
}

// Buckets returns every bucket start in [From, To) so gaps render as zeros.
func (p Params) Buckets() []int64 {
	var out []int64
	var t time.Time
	switch p.Granularity {
	case Minute:
		t = p.From.UTC().Truncate(time.Minute)
	case Hour:
		f := p.From.In(p.Location)
		t = time.Date(f.Year(), f.Month(), f.Day(), f.Hour(), 0, 0, 0, p.Location)
	default:
		f := p.From.In(p.Location)
		t = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, p.Location)
	}
	for ; t.Before(p.To); t = next(t, p.Granularity) {
		out = append(out, t.Unix())
	}
	return out
}

func next(t time.Time, g string) time.Time {
	switch g {
	case Minute:
		return t.Add(time.Minute)
	case Hour:
		return t.Add(time.Hour)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// Previous returns the equally long period immediately before [From, To).
func (p Params) Previous() (time.Time, time.Time) {
	d := p.To.Sub(p.From)
	return p.From.Add(-d), p.From
}

// CacheKey is a canonical representation used for response caching.
func (p Params) CacheKey(kind string, extra ...string) string {
	return fmt.Sprintf("%s|%d|%d|%s|%s|%s|%d|%v|%v|%v|%v|%v", kind, p.From.Unix(), p.To.Unix(), p.Location, p.Granularity,
		p.GroupBy, p.Top, p.Filter.Banners, p.Filter.Placements, p.Filter.Countries, p.Filter.Devices, extra)
}
