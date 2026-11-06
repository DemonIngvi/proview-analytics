// Package names resolves banner ids to human-readable names using the
// banner-service management API. Analytics keeps working without it: unknown
// ids are labelled "#<id>".
package names

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Resolver keeps an id -> name map refreshed in the background.
type Resolver struct {
	url      string
	token    string
	client   *http.Client
	interval time.Duration
	log      *slog.Logger

	mu    sync.RWMutex
	names map[uint64]string
}

// New returns a Resolver; url may be empty to disable lookups.
func New(url, token string, interval time.Duration, log *slog.Logger) *Resolver {
	return &Resolver{url: url, token: token, interval: interval, log: log,
		client: &http.Client{Timeout: 5 * time.Second}, names: map[uint64]string{}}
}

// Run refreshes until ctx is done.
func (r *Resolver) Run(ctx context.Context) {
	if r.url == "" {
		return
	}
	for {
		if err := r.Refresh(ctx); err != nil {
			r.log.Warn("banner names refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.interval):
		}
	}
}

// Refresh fetches the banner list once.
func (r *Resolver) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url+"/admin/v1/banners", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("banner-service returned %s", resp.Status)
	}
	var body struct {
		Items []struct {
			ID   uint64 `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	m := make(map[uint64]string, len(body.Items))
	for _, it := range body.Items {
		m[it.ID] = it.Name
	}
	r.mu.Lock()
	r.names = m
	r.mu.Unlock()
	return nil
}

// Label returns "#id name" or "#id" when the name is unknown.
func (r *Resolver) Label(id uint64) string {
	r.mu.RLock()
	name := r.names[id]
	r.mu.RUnlock()
	if name == "" {
		return "#" + strconv.FormatUint(id, 10)
	}
	return "#" + strconv.FormatUint(id, 10) + " " + name
}
