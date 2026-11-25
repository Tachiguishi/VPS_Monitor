package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type store struct {
	db *sql.DB
}

type record struct {
	ServerID string `json:"serverId"`
	snapshot
}

type chartPoint struct {
	At      time.Time `json:"at"`
	Memory  *float64  `json:"memory"`
	Load    *float64  `json:"load"`
	Disk    *float64  `json:"disk"`
	Traffic *float64  `json:"traffic"`
}

type history struct {
	From    time.Time               `json:"from"`
	To      time.Time               `json:"to"`
	StepSec int64                   `json:"stepSeconds"`
	Series  map[string][]chartPoint `json:"series"`
	Records []record                `json:"records"`
	Page    int                     `json:"page"`
	Total   int                     `json:"total"`
}

const pageSize = 10

func openStore(path string, demo bool) (*store, error) {
	dsn := ":memory:"
	if !demo {
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		dsn = (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS samples (
		server_id TEXT NOT NULL,
		sampled_at INTEGER NOT NULL,
		status TEXT NOT NULL,
		payload TEXT NOT NULL,
		PRIMARY KEY (server_id, sampled_at)
	);
	CREATE INDEX IF NOT EXISTS samples_by_time ON samples(sampled_at);
	CREATE INDEX IF NOT EXISTS samples_success ON samples(server_id, sampled_at) WHERE status <> 'failed';`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &store{db}, nil
}

func (s *store) save(ctx context.Context, records []record, cutoff time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO samples (server_id, sampled_at, status, payload) VALUES (?, ?, ?, ?)
		ON CONFLICT (server_id, sampled_at) DO UPDATE SET status=excluded.status, payload=excluded.payload`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range records {
		payload, err := json.Marshal(row.snapshot)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, row.ServerID, row.At.UnixMilli(), row.Status, string(payload)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM samples WHERE sampled_at < ?", cutoff.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func latest(ctx context.Context, tx *sql.Tx, id string, successOnly bool) (*snapshot, error) {
	query := "SELECT payload FROM samples WHERE server_id = ?"
	if successOnly {
		query += " AND status <> 'failed'"
	}
	var payload string
	err := tx.QueryRowContext(ctx, query+" ORDER BY sampled_at DESC LIMIT 1", id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value snapshot
	err = json.Unmarshal([]byte(payload), &value)
	return &value, err
}

func (s *store) history(ctx context.Context, ids []string, from, to time.Time, step time.Duration, page int) (history, error) {
	result := history{
		From: from, To: to, StepSec: int64(step / time.Second),
		Series: make(map[string][]chartPoint), Records: []record{}, Page: page,
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	args := []any{from.UnixMilli(), to.UnixMilli()}
	for _, id := range ids {
		args = append(args, id)
	}
	where := fmt.Sprintf(" WHERE sampled_at BETWEEN ? AND ? AND server_id IN (%s)", strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","))
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM samples"+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	maxPage := max(1, (result.Total+pageSize-1)/pageSize)
	result.Page = min(page, maxPage)
	list, err := queryRecords(ctx, tx, "SELECT server_id, payload FROM samples"+where+
		" ORDER BY sampled_at DESC, server_id LIMIT ? OFFSET ?", append(args, pageSize, (result.Page-1)*pageSize)...)
	if err != nil {
		return result, err
	}
	result.Records = list

	chartArgs := append([]any{from.UnixMilli(), step.Milliseconds()}, args...)
	points, err := queryRecords(ctx, tx, `SELECT server_id, payload FROM (
		SELECT server_id, payload, sampled_at,
		row_number() OVER (PARTITION BY server_id, (sampled_at-?)/? ORDER BY sampled_at DESC) AS ordinal
		FROM samples`+where+`) WHERE ordinal=1 ORDER BY sampled_at`, chartArgs...)
	if err != nil {
		return result, err
	}
	for _, p := range points {
		point := chartPoint{At: p.At, Load: p.Load, Disk: ratio(p.DiskUsed, p.DiskTotal),
			Memory: ratio(p.MemoryUsed, p.MemoryTotal), Traffic: ratio(p.TrafficUsed, p.TrafficCap)}
		result.Series[p.ServerID] = append(result.Series[p.ServerID], point)
	}
	return result, tx.Commit()
}

func queryRecords(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]record, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []record{}
	for rows.Next() {
		var row record
		var payload string
		if err := rows.Scan(&row.ServerID, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &row.snapshot); err != nil {
			return nil, err
		}
		records = append(records, row)
	}
	return records, rows.Err()
}

func ratio(used, limit *float64) *float64 {
	if used == nil || limit == nil || *limit <= 0 {
		return nil
	}
	ratio := *used / *limit * 100
	if math.IsInf(ratio, 0) {
		return nil
	}
	return &ratio
}
