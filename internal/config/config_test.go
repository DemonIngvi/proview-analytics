package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoad(t *testing.T) {
	c, err := Load(env(map[string]string{"CLICKHOUSE_ADDRS": "ch:9000", "ADMIN_PASSWORD": "p"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminUser != "admin" || c.CacheTTL.Seconds() != 15 || c.ClickHouse.MaxExecutionTime.Seconds() != 10 {
		t.Fatalf("defaults: %+v", c)
	}
	c, err = Load(env(map[string]string{"CLICKHOUSE_ADDRS": "ch:9000", "AUTH_DISABLED": "true", "ADMIN_PASSWORD": "p"}))
	if err != nil || c.AdminPassword != "" {
		t.Fatalf("auth disabled: %v %+v", err, c)
	}
}

func TestLoadErrors(t *testing.T) {
	_, err := Load(env(map[string]string{"CACHE_TTL": "x"}))
	for _, want := range []string{"CLICKHOUSE_ADDRS", "ADMIN_PASSWORD", "CACHE_TTL"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %s", err, want)
		}
	}
}
