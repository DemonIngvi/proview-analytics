package names

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshAndLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/admin/v1/banners" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":1,"name":"Aurora"},{"id":2,"name":"Nimbus"}]}`))
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := New(srv.URL, "tok", time.Minute, log)
	if got := r.Label(1); got != "#1" {
		t.Fatalf("before refresh: %q", got)
	}
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Label(1) != "#1 Aurora" || r.Label(9) != "#9" {
		t.Fatalf("labels: %q %q", r.Label(1), r.Label(9))
	}

	bad := New(srv.URL, "wrong", time.Minute, log)
	if err := bad.Refresh(context.Background()); err == nil {
		t.Fatal("expected auth error")
	}
}
