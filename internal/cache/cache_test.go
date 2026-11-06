package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheHitsAndExpiry(t *testing.T) {
	c := New(time.Minute, 10)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }
	var calls int
	fn := func() (any, error) { calls++; return calls, nil }
	if v, _ := c.Do("k", fn); v != 1 {
		t.Fatal(v)
	}
	if v, _ := c.Do("k", fn); v != 1 || calls != 1 {
		t.Fatal("expected cache hit")
	}
	clock = clock.Add(2 * time.Minute)
	if v, _ := c.Do("k", fn); v != 2 {
		t.Fatal("expected recompute after TTL")
	}
}

func TestErrorsNotCached(t *testing.T) {
	c := New(time.Minute, 10)
	if _, err := c.Do("k", func() (any, error) { return nil, errors.New("x") }); err == nil {
		t.Fatal("error lost")
	}
	if v, err := c.Do("k", func() (any, error) { return 5, nil }); err != nil || v != 5 {
		t.Fatal("error was cached")
	}
}

func TestCoalescing(t *testing.T) {
	c := New(time.Minute, 10)
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Do("k", func() (any, error) { calls.Add(1); <-release; return 1, nil })
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fn called %d times, want 1", calls.Load())
	}
}

func TestEvictionBound(t *testing.T) {
	c := New(time.Minute, 3)
	for i := range 10 {
		_, _ = c.Do(string(rune('a'+i)), func() (any, error) { return i, nil })
	}
	if c.Len() > 3 {
		t.Fatalf("len = %d", c.Len())
	}
	if v, _ := New(0, 1).Do("k", func() (any, error) { return 9, nil }); v != 9 {
		t.Fatal("ttl=0 must bypass cache")
	}
}
