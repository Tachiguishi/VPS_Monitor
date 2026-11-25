package main

import (
	"context"
	"errors"
	"log"
	"math"
	"sync"
	"time"
)

const refreshCooldown = time.Minute

var errCollecting = errors.New("collection in progress")
var errCooldown = errors.New("collection cooldown")

type monitor struct {
	config  config
	store   *store
	fetch   fetchSnapshot
	lock    sync.Mutex
	lastRun time.Time
}

type serverView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Latest      *snapshot `json:"latest,omitempty"`
	LastSuccess *snapshot `json:"lastSuccess,omitempty"`
}

type overview struct {
	Demo        bool         `json:"demo"`
	PollSeconds int          `json:"pollIntervalSeconds"`
	UpdatedAt   *time.Time   `json:"updatedAt"`
	Servers     []serverView `json:"servers"`
}

func (m *monitor) collect(ctx context.Context) (time.Duration, error) {
	if !m.lock.TryLock() {
		return 0, errCollecting
	}
	defer m.lock.Unlock()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if retry := refreshCooldown - now.Sub(m.lastRun); retry > 0 {
		return retry, errCooldown
	}
	m.lastRun = now

	results := make([]record, len(m.config.Servers))
	var workers sync.WaitGroup
	for i, server := range m.config.Servers {
		workers.Add(1)
		go func(i int, server serverConfig) {
			defer workers.Done()
			if ctx.Err() != nil {
				return
			}
			result := m.fetch(ctx, server, now)
			results[i] = record{ServerID: server.ID, snapshot: result}
			if result.Status == failed {
				log.Printf("sample for VEID %s failed (%s)", server.ID, result.Error)
			}
		}(i, server)
	}
	workers.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return 0, m.store.save(ctx, results, now.AddDate(0, 0, -m.config.Retention))
}

func (m *monitor) run(ctx context.Context, initial bool) {
	ticker := time.NewTicker(m.config.interval())
	defer ticker.Stop()
	collect := func() {
		_, err := m.collect(ctx)
		if err != nil && err != errCollecting && err != errCooldown && ctx.Err() == nil {
			log.Printf("saving samples: %v", err)
		}
	}
	if initial {
		collect()
	}
	for {
		select {
		case <-ticker.C:
			collect()
		case <-ctx.Done():
			return
		}
	}
}

func (m *monitor) overview(ctx context.Context, demo bool) (overview, error) {
	result := overview{Demo: demo, PollSeconds: m.config.PollSeconds, Servers: make([]serverView, 0, len(m.config.Servers))}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, server := range m.config.Servers {
		view := serverView{ID: server.ID, Name: server.Name, Status: pending}
		view.Latest, err = latest(ctx, tx, server.ID, false)
		if err != nil {
			return result, err
		}
		if sample := view.Latest; sample != nil {
			view.Status = sample.Status
			if sample.Status == failed {
				view.LastSuccess, err = latest(ctx, tx, server.ID, true)
				if err != nil {
					return result, err
				}
			}
			if result.UpdatedAt == nil || sample.At.After(*result.UpdatedAt) {
				result.UpdatedAt = &sample.At
			}
		}
		result.Servers = append(result.Servers, view)
	}
	return result, tx.Commit()
}

func demoFetcher(start time.Time) fetchSnapshot {
	return func(_ context.Context, server serverConfig, at time.Time) snapshot {
		la := server.ID == "demo-la"
		hours := at.Sub(start).Hours()
		day := hours / 24
		minute := hours * 60
		phase := 0.0
		memoryBase, memoryTotal := 55.0, float64(2<<30)
		disk, diskTotal := 18.6*float64(1<<30), float64(40<<30)
		traffic, trafficCap := 386.4*float64(1<<30), float64(1<<40)
		loadBase := .43
		hostname, location, os := "tyo-node-01", "日本 · 东京", "Debian 12"
		if la {
			phase = 1.28
			memoryBase, memoryTotal = 34.0, float64(1<<30)
			disk, diskTotal = 7.2*float64(1<<30), float64(20<<30)
			traffic, trafficCap = 217.8*float64(1<<30), float64(500<<30)
			loadBase = .22
			hostname, location, os = "lax-node-02", "美国 · 洛杉矶", "Ubuntu 24.04"
		}
		wave := math.Sin(1.1+hours*.53+phase)*5.4 +
			math.Sin(hours*1.37+phase)*3.1 + math.Sin(hours*3.32+phase+1)*1.6 +
			math.Sin(minute*0.195+phase)*1.25
		memory := (memoryBase + wave) * memoryTotal / 100
		load := math.Max(.02, loadBase+wave*.025)
		traffic = math.Max(0, traffic+day*23*float64(1<<30))
		disk += math.Sin(hours/20)*.35*float64(1<<30) + day*.08*float64(1<<30)
		reset := start.AddDate(0, 0, 11)
		return snapshot{
			At: at, Status: running, Hostname: hostname, Location: location, OS: os,
			MemoryUsed: &memory, MemoryTotal: &memoryTotal, DiskUsed: &disk, DiskTotal: &diskTotal,
			TrafficUsed: &traffic, TrafficCap: &trafficCap, ResetAt: &reset, Load: &load,
		}
	}
}

func (m *monitor) seedDemo(ctx context.Context, now time.Time) error {
	var rows []record
	for at := now.AddDate(0, 0, -7); !at.After(now); at = at.Add(m.config.interval()) {
		for _, server := range m.config.Servers {
			rows = append(rows, record{ServerID: server.ID, snapshot: m.fetch(ctx, server, at)})
		}
	}
	return m.store.save(ctx, rows, now.AddDate(0, 0, -m.config.Retention))
}
