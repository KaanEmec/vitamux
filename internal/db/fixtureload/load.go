// Package fixtureload COPYs the canonical NDJSON written by tools/fixturegen into a migrated
// database, for the volume baseline (J04.4) and other tests that need a realistic year. It
// writes the same rows a normalizer would (source identity, dedupe keys, one raw payload per
// source and day) but not revisions.ndjson: every record loads as the original, active row.
package fixtureload

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db"
)

// Stats counts the loaded rows.
type Stats struct {
	Measurements int64 // including group components
	HeartRate    int64
	Groups       int
	Sleep        int
	Stages       int
	Raw          int
	UserID       uuid.UUID
}

const (
	copyChunk = 10_000 // rows per COPY, as in docs/architecture/project.md#resource-budget

	flagManual  = 1 << 0 // measurements.quality_flags bits
	flagRelayed = 1 << 3
)

type srcRec struct {
	Key             string `json:"key"`
	Provider        string `json:"provider"`
	DeviceType      string `json:"device_type"`
	Fingerprint     string `json:"fingerprint"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	OriginKey       string `json:"origin_key"`
	RelayedProvider string `json:"relayed_provider"`
}

// source is a srcRec resolved to database ids.
type source struct {
	srcRec
	provider int16
	conn     uuid.UUID
	account  string // hex of connections.account_key, part of every dedupe key
	device   any    // uuid.UUID or nil
	origin   any
	batch    uuid.UUID
}

type rawKey struct{ src, date string }

type loader struct {
	pool    *pgxpool.Pool
	d       *db.DB
	user    uuid.UUID
	norm    int32
	metrics map[string]int16
	sources map[string]*source
	raws    map[rawKey]int64
	stats   Stats
}

// Load inserts the dataset in dir as one synthetic owner. pool must be a migrated database
// (internal/catalog seeded) opened as the app role.
func Load(ctx context.Context, pool *pgxpool.Pool, dir string) (Stats, error) {
	l := &loader{pool: pool, d: db.New(pool), user: uuid.Must(uuid.NewV7()), metrics: map[string]int16{},
		sources: map[string]*source{}, raws: map[rawKey]int64{}}
	l.stats.UserID = l.user
	if err := l.seed(ctx, dir); err != nil {
		return l.stats, fmt.Errorf("seed: %w", err)
	}
	for _, step := range []struct {
		name string
		fn   func(context.Context, string) error
	}{{"groups", l.groups}, {"sleep", l.sleep}, {"measurements", l.measurements}} {
		if err := step.fn(ctx, filepath.Join(dir, step.name+".ndjson")); err != nil {
			return l.stats, fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return l.stats, nil
}

// lines calls fn for every record line after the header.
func lines(path string, fn func([]byte) error) error {
	f, err := os.Open(path) //nolint:gosec // dataset dir chosen by the caller
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan() // header
	for sc.Scan() {
		if err := fn(sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (l *loader) seed(ctx context.Context, dir string) error {
	p := l.pool
	if _, err := p.Exec(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, 'synthetic-owner', 'not-a-real-hash')`, l.user); err != nil {
		return err
	}
	if err := p.QueryRow(ctx, `INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('fixtureload', 1, 'synthetic') RETURNING id`).Scan(&l.norm); err != nil {
		return err
	}
	rows, err := p.Query(ctx, `SELECT code, id FROM metric_catalog`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		var id int16
		if err := rows.Scan(&code, &id); err != nil {
			return err
		}
		l.metrics[code] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	conns := map[string]*source{} // first source of each provider carries its connection
	err = lines(filepath.Join(dir, "sources.ndjson"), func(b []byte) error {
		var s srcRec
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		src := &source{srcRec: s}
		l.sources[s.Key] = src
		if err := p.QueryRow(ctx, `SELECT id FROM providers WHERE code = $1`, s.Provider).Scan(&src.provider); err != nil {
			return fmt.Errorf("provider %q: %w", s.Provider, err)
		}
		if c, ok := conns[s.Provider]; ok {
			src.conn, src.account, src.batch = c.conn, c.account, c.batch
		} else {
			key := sha256.Sum256([]byte("synthetic-" + s.Provider))
			src.conn, src.batch, src.account = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), hex.EncodeToString(key[:])
			if _, err := p.Exec(ctx, `INSERT INTO connections (id, user_id, provider_id, account_key, mode, status) VALUES ($1, $2, $3, $4, 'push', 'active')`,
				src.conn, l.user, src.provider, key[:]); err != nil {
				return err
			}
			if _, err := p.Exec(ctx, `INSERT INTO ingest_batches (id, user_id, connection_id, source_kind) VALUES ($1, $2, $3, 'import')`,
				src.batch, l.user, src.conn); err != nil {
				return err
			}
			conns[s.Provider] = src
		}
		if s.Fingerprint != "" {
			id := uuid.Must(uuid.NewV7())
			if _, err := p.Exec(ctx, `INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type, manufacturer, model) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (user_id, provider_id, fingerprint) DO NOTHING`, id, l.user, src.provider, s.Fingerprint, s.DeviceType, s.Manufacturer, s.Model); err != nil {
				return err
			}
			var dev uuid.UUID
			if err := p.QueryRow(ctx, `SELECT id FROM devices WHERE user_id = $1 AND provider_id = $2 AND fingerprint = $3`, l.user, src.provider, s.Fingerprint).Scan(&dev); err != nil {
				return err
			}
			src.device = dev
		}
		if s.OriginKey != "" {
			var relayed *int16
			if s.RelayedProvider != "" {
				var id int16
				if err := p.QueryRow(ctx, `SELECT id FROM providers WHERE code = $1`, s.RelayedProvider).Scan(&id); err != nil {
					return err
				}
				relayed = &id
			}
			id := uuid.Must(uuid.NewV7())
			if _, err := p.Exec(ctx, `INSERT INTO data_origins (id, user_id, provider_id, origin_key, name, relayed_provider_id, is_native) VALUES ($1, $2, $3, $4, $4, $5, $6)`,
				id, l.user, src.provider, s.OriginKey, relayed, relayed == nil); err != nil {
				return err
			}
			src.origin = id
		}
		return nil
	})
	return err
}

// raw returns the raw payload for one source and local date, creating it on first use.
func (l *loader) raw(ctx context.Context, src *source, date string, at time.Time) (int64, error) {
	k := rawKey{src.Key, date}
	if id, ok := l.raws[k]; ok {
		return id, nil
	}
	key := src.Key + "/" + date
	sum := sha256.Sum256([]byte(key))
	if _, err := l.pool.Exec(ctx, `INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, refcount) VALUES ($1, 400000, 60000, 'zstd', 1) ON CONFLICT DO NOTHING`, sum[:]); err != nil {
		return 0, err
	}
	var id int64
	err := l.pool.QueryRow(ctx, `INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
		VALUES ($1, $2, $3, 'synthetic', $4, $5, 'application/x-ndjson', $6) RETURNING id`,
		l.user, src.conn, src.batch, key, sum[:], at).Scan(&id)
	if err != nil {
		return 0, err
	}
	l.raws[k] = id
	l.stats.Raw++
	return id, nil
}

// dedupe is the data-model.md key: with an upstream id, v1|provider|account|record|ext|component;
// otherwise v1|provider|account|metric|kind|start_us|end_us|device|origin.
func dedupe(buf []byte, s *source, record, ext, component, kind string, start, end time.Time) ([]byte, []byte) {
	buf = append(buf[:0], "v1|"...)
	buf = append(buf, s.Provider...)
	buf = append(buf, '|')
	buf = append(buf, s.account...)
	buf = append(buf, '|')
	if ext != "" {
		buf = append(buf, record...)
		buf = append(buf, '|')
		buf = append(buf, ext...)
		buf = append(buf, '|')
		buf = append(buf, component...)
	} else {
		buf = append(buf, component...)
		buf = append(buf, '|')
		buf = append(buf, kind...)
		buf = append(buf, '|')
		buf = strconv.AppendInt(buf, start.UnixMicro(), 10)
		buf = append(buf, '|')
		if !end.IsZero() {
			buf = strconv.AppendInt(buf, end.UnixMicro(), 10)
		}
		buf = append(buf, '|')
		buf = append(buf, s.Fingerprint...)
		buf = append(buf, '|')
		buf = append(buf, s.OriginKey...)
	}
	sum := sha256.Sum256(buf)
	return buf, sum[:16]
}

var measurementColumns = []string{"user_id", "metric_id", "kind", "start_at", "end_at", "tz_offset_min", "local_date", "value",
	"provider_id", "connection_id", "device_id", "origin_id", "group_id", "external_id", "dedupe_key", "quality_flags",
	"raw_payload_id", "normalizer_version_id"}

type measurementRec struct {
	Src    string   `json:"src"`
	Metric string   `json:"metric"`
	Kind   string   `json:"kind"`
	Start  string   `json:"start"`
	End    string   `json:"end"`
	TZ     int16    `json:"tz"`
	Date   string   `json:"date"`
	Value  float64  `json:"value"`
	Ext    string   `json:"ext"`
	Flags  []string `json:"flags"`
}

func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

func (l *loader) measurements(ctx context.Context, path string) error {
	rows := make([][]any, 0, copyChunk)
	var buf []byte
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		n, err := l.d.CopyFrom(ctx, "measurements", measurementColumns, rows)
		l.stats.Measurements += n
		rows = rows[:0]
		return err
	}
	err := lines(path, func(b []byte) error {
		var m measurementRec
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		src, ok := l.sources[m.Src]
		metric, mok := l.metrics[m.Metric]
		if !ok || !mok {
			return fmt.Errorf("unknown source %q or metric %q", m.Src, m.Metric)
		}
		start, err := time.Parse(time.RFC3339, m.Start)
		if err != nil {
			return err
		}
		var end any
		var endT time.Time
		if m.End != "" {
			if endT, err = time.Parse(time.RFC3339, m.End); err != nil {
				return err
			}
			end = endT
		}
		date, err := parseDate(m.Date)
		if err != nil {
			return err
		}
		raw, err := l.raw(ctx, src, m.Date, start)
		if err != nil {
			return err
		}
		var ext any
		if m.Ext != "" {
			ext = m.Ext
		}
		var key []byte
		buf, key = dedupe(buf, src, "measurement", m.Ext, m.Metric, m.Kind, start, endT)
		var flags int32
		for _, f := range m.Flags {
			switch f {
			case "manual_entry":
				flags |= flagManual
			case "relayed":
				flags |= flagRelayed
			}
		}
		if m.Metric == "heart_rate" {
			l.stats.HeartRate++
		}
		rows = append(rows, []any{l.user, metric, m.Kind, start, end, m.TZ, date, m.Value, src.provider, src.conn, src.device, src.origin,
			nil, ext, key, flags, raw, l.norm})
		if len(rows) == copyChunk {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

type groupRec struct {
	Src     string          `json:"src"`
	Kind    string          `json:"kind"`
	At      string          `json:"at"`
	TZ      int16           `json:"tz"`
	Date    string          `json:"date"`
	Ext     string          `json:"ext"`
	Context json.RawMessage `json:"context"`
	Parts   []struct {
		Metric string  `json:"metric"`
		Value  float64 `json:"value"`
	} `json:"parts"`
}

// groups inserts the groups one by one (a few thousand a year) and their components with COPY.
func (l *loader) groups(ctx context.Context, path string) error {
	var parts [][]any
	var buf []byte
	err := lines(path, func(b []byte) error {
		var g groupRec
		if err := json.Unmarshal(b, &g); err != nil {
			return err
		}
		src, ok := l.sources[g.Src]
		if !ok {
			return fmt.Errorf("unknown source %q", g.Src)
		}
		at, err := time.Parse(time.RFC3339, g.At)
		if err != nil {
			return err
		}
		date, err := parseDate(g.Date)
		if err != nil {
			return err
		}
		raw, err := l.raw(ctx, src, g.Date, at)
		if err != nil {
			return err
		}
		var key []byte
		buf, key = dedupe(buf, src, "group", g.Ext, "", g.Kind, at, time.Time{})
		var id int64
		if err := l.pool.QueryRow(ctx, `INSERT INTO measurement_groups (user_id, kind, measured_at, tz_offset_min, local_date, context, provider_id, connection_id,
			device_id, origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
			l.user, g.Kind, at, g.TZ, date, g.Context, src.provider, src.conn, src.device, src.origin, g.Ext, key, raw, l.norm).Scan(&id); err != nil {
			return err
		}
		l.stats.Groups++
		for _, p := range g.Parts {
			var pkey []byte
			buf, pkey = dedupe(buf, src, "measurement", g.Ext, p.Metric, "sample", at, time.Time{})
			parts = append(parts, []any{l.user, l.metrics[p.Metric], "sample", at, nil, g.TZ, date, p.Value, src.provider, src.conn, src.device, src.origin,
				id, g.Ext, pkey, int32(0), raw, l.norm})
		}
		return nil
	})
	if err != nil {
		return err
	}
	n, err := l.d.CopyFrom(ctx, "measurements", measurementColumns, parts)
	l.stats.Measurements += n
	return err
}

type sleepRec struct {
	Src       string `json:"src"`
	Ext       string `json:"ext"`
	Start     string `json:"start"`
	End       string `json:"end"`
	TZ        int16  `json:"tz"`
	Date      string `json:"date"`
	Nap       bool   `json:"nap"`
	HasStages bool   `json:"has_stages"`
	Basis     string `json:"totals_basis"`
	Asleep    *int   `json:"asleep_s"`
	Deep      *int   `json:"deep_s"`
	Light     *int   `json:"light_s"`
	REM       *int   `json:"rem_s"`
	Awake     *int   `json:"awake_s"`
	Latency   *int   `json:"latency_s"`
	Stages    []struct {
		Stage string `json:"stage"`
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"stages"`
}

func (l *loader) sleep(ctx context.Context, path string) error {
	var sessions, stages [][]any
	var buf []byte
	err := lines(path, func(b []byte) error {
		var s sleepRec
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		src, ok := l.sources[s.Src]
		if !ok {
			return fmt.Errorf("unknown source %q", s.Src)
		}
		start, err := time.Parse(time.RFC3339, s.Start)
		if err != nil {
			return err
		}
		end, err := time.Parse(time.RFC3339, s.End)
		if err != nil {
			return err
		}
		date, err := parseDate(s.Date)
		if err != nil {
			return err
		}
		raw, err := l.raw(ctx, src, s.Date, start)
		if err != nil {
			return err
		}
		var key []byte
		buf, key = dedupe(buf, src, "sleep", s.Ext, "", "", start, end)
		id := uuid.Must(uuid.NewV7())
		sessions = append(sessions, []any{id, l.user, start, end, s.TZ, date, s.Nap, s.HasStages, s.Basis, s.Asleep, s.Deep, s.Light, s.REM, s.Awake,
			s.Latency, src.provider, src.conn, src.device, src.origin, s.Ext, key, raw, l.norm})
		for _, st := range s.Stages {
			from, err := time.Parse(time.RFC3339, st.Start)
			if err != nil {
				return err
			}
			to, err := time.Parse(time.RFC3339, st.End)
			if err != nil {
				return err
			}
			stages = append(stages, []any{id, st.Stage, from, to})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := l.d.CopyFrom(ctx, "sleep_sessions", []string{"id", "user_id", "start_at", "end_at", "tz_offset_min", "sleep_date", "is_nap", "has_stages",
		"totals_basis", "asleep_s", "deep_s", "light_s", "rem_s", "awake_s", "latency_s", "provider_id", "connection_id", "device_id", "origin_id",
		"external_id", "dedupe_key", "raw_payload_id", "normalizer_version_id"}, sessions); err != nil {
		return err
	}
	l.stats.Sleep = len(sessions)
	if _, err := l.d.CopyFrom(ctx, "sleep_stages", []string{"session_id", "stage", "start_at", "end_at"}, stages); err != nil {
		return err
	}
	l.stats.Stages = len(stages)
	return nil
}
