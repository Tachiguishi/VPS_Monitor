package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (transport fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestKiwiSamplesPersistAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "monitor.db")
	db, err := openStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.Close()

	servers := []serverConfig{
		{Name: "First", ID: "123", key: "fixture-key"},
		{Name: "Second", ID: "456", key: "fixture-key"},
	}
	transport := fixtureTransport(func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		if r.URL.Path != "/v1/getLiveServiceInfo" || query.Get("api_key") != "fixture-key" {
			t.Error("unexpected provider request")
		}
		body := `{"error": 42, "message": "sensitive fixture-key"}`
		if query.Get("veid") == "123" {
			body = `{"error":"0","hostname":"fixture","ve_status":"Running","plan_ram":"1073741824",
				"mem_available_kb":524288,"data_counter":4096,"plan_monthly_data":16384,
				"monthly_data_multiplier":"2","ve_used_disk_space_b":"3221225472",
				"ve_disk_quota_gb":20,"load_average":"0.25 0.15 0.08"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	fetch := fetchFromKiwi(&http.Client{Transport: transport}, "https://api.64clouds.com/v1/getLiveServiceInfo")
	m := &monitor{config: config{Servers: servers, Retention: 90, PollSeconds: 300}, store: db, fetch: fetch}

	old := time.Now().UTC().Add(-5 * time.Minute)
	if err := db.save(ctx, []record{
		{ServerID: "456", snapshot: snapshot{Status: stopped, At: old, Hostname: "previous"}},
		{ServerID: "123", snapshot: snapshot{Status: running, At: old.AddDate(0, 0, -100)}},
	}, old.AddDate(0, 0, -120)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.collect(ctx); err != errCooldown {
		t.Fatalf("immediate repeat returned %v", err)
	}
	if err := db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.Close()
	m.store = db

	handler := newHandler(ctx, m, false, "web/dist")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest("GET", "http://localhost/api/overview", nil))
	if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "fixture-key") {
		t.Fatalf("unexpected overview response %d: %s", result.Code, result.Body.String())
	}
	var overview overview
	if err := json.Unmarshal(result.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Servers) != 2 {
		t.Fatalf("got %d VPS entries", len(overview.Servers))
	}
	first, second := overview.Servers[0], overview.Servers[1]
	if first.Status != running || first.Latest.MemoryUsed == nil || *first.Latest.MemoryUsed != 512<<20 ||
		first.Latest.TrafficUsed == nil || *first.Latest.TrafficUsed != 8192 || first.Latest.TrafficCap == nil ||
		*first.Latest.TrafficCap != 32768 || first.Latest.Load == nil || *first.Latest.Load != .25 {
		t.Fatalf("unexpected parsed sample: %+v", first.Latest)
	}
	if second.Status != failed || second.Latest.Error != "kiwivm_42" || second.LastSuccess == nil ||
		second.LastSuccess.Status != stopped {
		t.Fatalf("missing failed or prior sample: %+v", second)
	}

	result = httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest("GET", "http://localhost/api/history?range=7d&server=456", nil))
	var historical history
	if err := json.Unmarshal(result.Body.Bytes(), &historical); err != nil {
		t.Fatal(err)
	}
	if result.Code != http.StatusOK || historical.Total != 2 || len(historical.Records) != 2 || historical.Records[0].Status != failed ||
		historical.Records[1].Status != stopped || len(historical.Series) != 1 {
		t.Fatalf("unexpected filtered history: %d %s", result.Code, result.Body.String())
	}
	if err := db.db.QueryRow("SELECT count(*) FROM samples").Scan(&historical.Total); err != nil || historical.Total != 3 {
		t.Fatalf("retention kept %d samples, error %v", historical.Total, err)
	}
}
