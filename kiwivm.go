package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	running   = "running"
	stopped   = "stopped"
	starting  = "starting"
	suspended = "suspended"
	unknown   = "unknown"
	failed    = "failed"
	pending   = "pending"
)

type snapshot struct {
	Status      string     `json:"status"`
	At          time.Time  `json:"at"`
	Hostname    string     `json:"hostname,omitempty"`
	Location    string     `json:"location,omitempty"`
	OS          string     `json:"os,omitempty"`
	MemoryUsed  *float64   `json:"memoryUsed,omitempty"`
	MemoryTotal *float64   `json:"memoryTotal,omitempty"`
	DiskUsed    *float64   `json:"diskUsed,omitempty"`
	DiskTotal   *float64   `json:"diskTotal,omitempty"`
	TrafficUsed *float64   `json:"trafficUsed,omitempty"`
	TrafficCap  *float64   `json:"trafficCap,omitempty"`
	ResetAt     *time.Time `json:"resetAt,omitempty"`
	Load        *float64   `json:"load,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type fetchSnapshot func(context.Context, serverConfig, time.Time) snapshot

func kiwiFetcher() fetchSnapshot {
	client := &http.Client{
		Timeout: 22 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return fetchFromKiwi(client, "https://api.64clouds.com/v1/getLiveServiceInfo")
}

func fetchFromKiwi(client *http.Client, endpoint string) fetchSnapshot {
	return func(ctx context.Context, server serverConfig, now time.Time) snapshot {
		failure := snapshot{At: now, Status: failed}
		query := url.Values{"veid": {server.ID}, "api_key": {server.key}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
		if err != nil {
			failure.Error = "invalid_response"
			return failure
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			// Transport errors can contain the request URL, including the credentials.
			failure.Error = "connection"
			return failure
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			failure.Error = fmt.Sprintf("http_%d", resp.StatusCode)
			return failure
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			failure.Error = "invalid_response"
			return failure
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			failure.Error = "invalid_response"
			return failure
		}
		code := number(fields["error"])
		if code == nil || *code != math.Trunc(*code) {
			failure.Error = "invalid_response"
			return failure
		}
		if *code != 0 {
			failure.Error = fmt.Sprintf("kiwivm_%g", *code)
			return failure
		}
		return parseSnapshot(fields, now)
	}
}

func stringField(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func number(raw json.RawMessage) *float64 {
	value := stringField(raw)
	if value == "" {
		value = string(raw)
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}

func nonnegative(raw json.RawMessage) *float64 {
	n := number(raw)
	if n == nil || *n < 0 {
		return nil
	}
	return n
}

func boolField(raw json.RawMessage) bool {
	switch strings.ToLower(strings.TrimSpace(stringField(raw))) {
	case "true", "yes":
		return true
	}
	n := number(raw)
	return string(raw) == "true" || n != nil && *n == 1
}

func parseSnapshot(fields map[string]json.RawMessage, now time.Time) snapshot {
	result := snapshot{
		At: now, Status: unknown,
		Hostname: stringField(fields["hostname"]), Location: stringField(fields["node_location"]),
		OS: stringField(fields["os"]), MemoryTotal: nonnegative(fields["plan_ram"]),
		DiskUsed: nonnegative(fields["ve_used_disk_space_b"]), DiskTotal: nonnegative(fields["plan_disk"]),
		TrafficUsed: nonnegative(fields["data_counter"]), TrafficCap: nonnegative(fields["plan_monthly_data"]),
	}
	if result.Hostname == "" {
		result.Hostname = stringField(fields["live_hostname"])
	}
	switch strings.ToLower(strings.TrimSpace(stringField(fields["ve_status"]))) {
	case "running":
		result.Status = running
	case "starting":
		result.Status = starting
	case "stopped":
		result.Status = stopped
	}
	if boolField(fields["suspended"]) {
		result.Status = suspended
	}
	if disk := nonnegative(fields["ve_disk_quota_gb"]); disk != nil && *disk > 0 && *disk < 1e9 {
		totalBytes := *disk * (1 << 30)
		result.DiskTotal = &totalBytes
	}
	if reset := nonnegative(fields["data_next_reset"]); reset != nil && *reset >= 1 && *reset < 253402300800 {
		resetTime := time.Unix(int64(*reset), 0).UTC()
		result.ResetAt = &resetTime
	}
	if factor := nonnegative(fields["monthly_data_multiplier"]); factor != nil && *factor > 0 {
		for _, metric := range []**float64{&result.TrafficUsed, &result.TrafficCap} {
			if *metric != nil {
				scaled := **metric * *factor
				if math.IsInf(scaled, 0) {
					*metric = nil
				} else {
					*metric = &scaled
				}
			}
		}
	}
	if result.Status == running {
		if available := nonnegative(fields["mem_available_kb"]); available != nil && result.MemoryTotal != nil && *result.MemoryTotal > 0 {
			used := math.Max(*result.MemoryTotal-*available*1024, 0)
			result.MemoryUsed = &used
		}
		if parts := strings.Fields(stringField(fields["load_average"])); len(parts) > 0 {
			n, err := strconv.ParseFloat(parts[0], 64)
			if err == nil && n >= 0 && !math.IsInf(n, 0) {
				result.Load = &n
			}
		} else {
			result.Load = nonnegative(fields["load_average"])
		}
	}
	return result
}
