// Package httpapi serves the analytics JSON API and the admin web UI.
package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/DemonIngvi/proview-analytics/internal/cache"
	"github.com/DemonIngvi/proview-analytics/internal/chquery"
	"github.com/DemonIngvi/proview-analytics/internal/query"
)

// Store is the read side (implemented by chquery.Store).
type Store interface {
	Metrics(ctx context.Context, st query.Statement) (query.Metrics, error)
	Series(ctx context.Context, st query.Statement) ([]chquery.SeriesRow, error)
	Keyed(ctx context.Context, st query.Statement) ([]chquery.KeyedRow, error)
	Heat(ctx context.Context, st query.Statement) ([]chquery.HeatRow, error)
	Dimensions(ctx context.Context, st query.Statement) (chquery.Dims, error)
}

// Labeler resolves banner ids to display names.
type Labeler interface {
	Label(id uint64) string
}

// API wires handlers together.
type API struct {
	Store    Store
	Names    Labeler
	Cache    *cache.Cache
	Log      *slog.Logger
	Now      func() time.Time
	User     string
	Password string // empty disables auth (only when behind an authenticating proxy)
	UI       fs.FS
}

// Handler returns the routed API and UI.
func (a *API) Handler() http.Handler {
	if a.Now == nil {
		a.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/summary", a.summary)
	mux.HandleFunc("GET /api/v1/timeseries", a.timeseries)
	mux.HandleFunc("GET /api/v1/breakdown", a.breakdown)
	mux.HandleFunc("GET /api/v1/heatmap", a.heatmap)
	mux.HandleFunc("GET /api/v1/dimensions", a.dimensions)
	if a.UI != nil {
		mux.Handle("GET /", http.FileServerFS(a.UI))
	}
	return securityHeaders(a.auth(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (a *API) auth(next http.Handler) http.Handler {
	if a.Password == "" {
		return next
	}
	wantUser := sha256.Sum256([]byte(a.User))
	wantPass := sha256.Sum256([]byte(a.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotUser, gotPass := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
		if !ok || subtle.ConstantTimeCompare(gotUser[:], wantUser[:])&subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="ProView analytics", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) params(w http.ResponseWriter, r *http.Request) (query.Params, bool) {
	p, err := query.Parse(r.URL.Query(), a.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return p, false
	}
	return p, true
}

func (a *API) respond(w http.ResponseWriter, r *http.Request, key string, fn func(ctx context.Context) (any, error)) {
	v, err := a.Cache.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()
		return fn(ctx)
	})
	if err != nil {
		a.Log.Error("analytics query failed", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "analytics storage unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=5")
	writeJSON(w, http.StatusOK, v)
}

// SummaryResponse compares the period with the previous one of equal length.
type SummaryResponse struct {
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Current  query.Metrics `json:"current"`
	Previous query.Metrics `json:"previous"`
}

func (a *API) summary(w http.ResponseWriter, r *http.Request) {
	p, ok := a.params(w, r)
	if !ok {
		return
	}
	now := a.Now()
	a.respond(w, r, p.CacheKey("summary"), func(ctx context.Context) (any, error) {
		cur, err := a.Store.Metrics(ctx, p.Summary(now, p.From, p.To))
		if err != nil {
			return nil, err
		}
		pf, pt := p.Previous()
		prev, err := a.Store.Metrics(ctx, p.Summary(now, pf, pt))
		if err != nil {
			return nil, err
		}
		return SummaryResponse{From: p.From, To: p.To, Current: cur, Previous: prev}, nil
	})
}

// Series is one line on the chart; Points align with TimeseriesResponse.Buckets.
type Series struct {
	Key    string          `json:"key"`
	Label  string          `json:"label"`
	Points []query.Metrics `json:"points"`
	Total  query.Metrics   `json:"total"`
}

// TimeseriesResponse holds gap-filled series.
type TimeseriesResponse struct {
	Granularity string   `json:"granularity"`
	GroupBy     string   `json:"group_by"`
	Buckets     []int64  `json:"buckets"`
	Series      []Series `json:"series"`
}

func (a *API) timeseries(w http.ResponseWriter, r *http.Request) {
	p, ok := a.params(w, r)
	if !ok {
		return
	}
	now := a.Now()
	a.respond(w, r, p.CacheKey("timeseries"), func(ctx context.Context) (any, error) {
		rows, err := a.Store.Series(ctx, p.Timeseries(now))
		if err != nil {
			return nil, err
		}
		return buildSeries(p, rows, a.label(p.GroupBy)), nil
	})
}

func buildSeries(p query.Params, rows []chquery.SeriesRow, label func(string) string) TimeseriesResponse {
	buckets := p.Buckets()
	index := make(map[int64]int, len(buckets))
	for i, b := range buckets {
		index[b] = i
	}
	byKey := map[string]*Series{}
	var order []string
	for _, row := range rows {
		s, ok := byKey[row.Key]
		if !ok {
			s = &Series{Key: row.Key, Label: label(row.Key), Points: make([]query.Metrics, len(buckets))}
			byKey[row.Key] = s
			order = append(order, row.Key)
		}
		if i, ok := index[row.T]; ok {
			s.Points[i] = row.M
		}
		s.Total.Add(row.M)
	}
	resp := TimeseriesResponse{Granularity: p.Granularity, GroupBy: p.GroupBy, Buckets: buckets, Series: make([]Series, 0, len(order))}
	for _, k := range order {
		s := byKey[k]
		s.Total.Visitors = 0 // uniques are not additive across buckets; use /summary for totals
		s.Total.Derive()
		resp.Series = append(resp.Series, *s)
	}
	return resp
}

func (a *API) label(dim string) func(string) string {
	return func(key string) string {
		switch dim {
		case "none":
			return "Все баннеры"
		case "banner":
			id, err := strconv.ParseUint(key, 10, 64)
			if err != nil {
				return key
			}
			return a.Names.Label(id)
		default:
			if key == "" {
				return "не определено"
			}
			return key
		}
	}
}

// KeyedMetrics is a labelled breakdown row.
type KeyedMetrics struct {
	Key     string        `json:"key"`
	Label   string        `json:"label"`
	Metrics query.Metrics `json:"metrics"`
}

func (a *API) breakdown(w http.ResponseWriter, r *http.Request) {
	p, ok := a.params(w, r)
	if !ok {
		return
	}
	dim := r.URL.Query().Get("dimension")
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 100 {
		limit = 20
	}
	st, err := p.Breakdown(a.Now(), dim, limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	a.respond(w, r, p.CacheKey("breakdown", dim, strconv.Itoa(limit)), func(ctx context.Context) (any, error) {
		rows, err := a.Store.Keyed(ctx, st)
		if err != nil {
			return nil, err
		}
		lbl := a.label(dim)
		out := make([]KeyedMetrics, len(rows))
		for i, row := range rows {
			out[i] = KeyedMetrics{Key: row.Key, Label: lbl(row.Key), Metrics: row.M}
		}
		return map[string]any{"dimension": dim, "rows": out}, nil
	})
}

// HeatCell is one weekday/hour cell (dow: 1=Monday ... 7=Sunday).
type HeatCell struct {
	Dow     uint8         `json:"dow"`
	Hour    uint8         `json:"hour"`
	Metrics query.Metrics `json:"metrics"`
}

func (a *API) heatmap(w http.ResponseWriter, r *http.Request) {
	p, ok := a.params(w, r)
	if !ok {
		return
	}
	now := a.Now()
	a.respond(w, r, p.CacheKey("heatmap"), func(ctx context.Context) (any, error) {
		rows, err := a.Store.Heat(ctx, p.Heatmap(now))
		if err != nil {
			return nil, err
		}
		cells := make([]HeatCell, len(rows))
		for i, row := range rows {
			cells[i] = HeatCell{Dow: row.Dow, Hour: row.Hour, Metrics: row.M}
		}
		return map[string]any{"tz": p.Location.String(), "cells": cells}, nil
	})
}

// Option is a filter choice.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func (a *API) dimensions(w http.ResponseWriter, r *http.Request) {
	p, ok := a.params(w, r)
	if !ok {
		return
	}
	now := a.Now()
	a.respond(w, r, p.CacheKey("dimensions"), func(ctx context.Context) (any, error) {
		d, err := a.Store.Dimensions(ctx, p.Dimensions(now))
		if err != nil {
			return nil, err
		}
		opts := func(vals []string, dim string) []Option {
			lbl := a.label(dim)
			out := make([]Option, 0, len(vals))
			for _, v := range vals {
				if v != "" {
					out = append(out, Option{Value: v, Label: lbl(v)})
				}
			}
			sortOptions(out)
			return out
		}
		return map[string]any{
			"banner": opts(d.Banners, "banner"), "placement": opts(d.Placements, "placement"),
			"country": opts(d.Countries, "country"), "device": opts(d.Devices, "device"),
		}, nil
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
