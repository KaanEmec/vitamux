//go:build integration

// Package resourcebudget measures the resource budget (J14.3, docs/resource-budget.md): RSS, CPU
// and disk of a built `vitamux` binary and of PostgreSQL while it works on the one-year synthetic
// dataset. It needs macOS or Linux `ps`, a built binary and, for PostgreSQL numbers, a Docker
// container with a memory limit. Nothing here ships; run it by hand:
//
//	go run ./tools/fixturegen -seed 42 -out $TMP/year
//	CGO_ENABLED=0 go build -tags webui -trimpath -o $TMP/vitamux ./cmd/vitamux
//	docker run -d --name vitamux-budget-pg --memory 1g -p 127.0.0.1:55432:5432 -e POSTGRES_USER=vitamux \
//	  -e POSTGRES_PASSWORD=throwaway -e POSTGRES_DB=vitamux postgres:18-alpine postgres -c shared_buffers=128MB
//	VITAMUX_DATABASE_URL=postgres://vitamux:throwaway@127.0.0.1:55432/vitamux?sslmode=disable \
//	VITAMUX_BUDGET=1 VITAMUX_BUDGET_BIN=$TMP/vitamux VITAMUX_BUDGET_PG=vitamux-budget-pg \
//	VITAMUX_VOLUME_DATASET=$TMP/year GOMAXPROCS_SERVER=2 \
//	go test -tags integration -run TestResourceBudget -v -timeout 60m ./tools/resourcebudget/
//
// The owner password is random and never printed. Results are logged as `RESULT` lines.
//
//nolint:noctx // a hand-run measurement script: plain exec and http calls are fine
package resourcebudget

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool" //nolint:depguard // measurement script, not product code

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

const (
	addr   = "127.0.0.1:18089"
	from   = "2025-03-02" // 90 local days, as in docs/benchmarks/baseline.md#resolution-and-cache
	to     = "2025-05-30"
	rounds = 3
)

var dashMetrics = []string{"steps", "distance_walk_run", "active_energy", "resting_heart_rate", "resting_heart_rate_nocturnal",
	"weight", "body_fat_ratio", "blood_pressure", "sleep", "spo2"}

func mib(b float64) string { return fmt.Sprintf("%.0f MiB", b/(1<<20)) }

// sample is one reading of a process and of the PostgreSQL container.
type sample struct {
	at      time.Time
	rss     float64 // vitamux process bytes
	cpu     float64 // vitamux cumulative CPU seconds
	pgWork  float64 // container working set: memory.current - inactive_file
	pgAnon  float64 // anon + shmem: what the postgres processes hold privately, without page cache
	pgCPU   float64 // container cumulative CPU seconds
	havePID bool
}

type meter struct {
	pid     int // 0: only the container
	pg      string
	pgEvery time.Duration // container sampling interval, default 1 s; each `docker exec` costs the container about 4 ms of CPU
}

func (m meter) read(pg bool) sample {
	s := sample{at: time.Now()}
	if m.pid > 0 {
		out, err := exec.Command("ps", "-o", "rss=,time=", "-p", strconv.Itoa(m.pid)).Output()
		if f := strings.Fields(string(out)); err == nil && len(f) == 2 {
			kb, _ := strconv.ParseFloat(f[0], 64)
			s.rss, s.cpu, s.havePID = kb*1024, parseCPU(f[1]), true
		}
	}
	if m.pg != "" && pg {
		out, err := exec.Command("docker", "exec", m.pg, "sh", "-c",
			"cat /sys/fs/cgroup/memory.current; grep -E '^(anon|shmem|inactive_file) ' /sys/fs/cgroup/memory.stat; grep usage_usec /sys/fs/cgroup/cpu.stat").Output()
		if err == nil {
			var cur, anon, shmem, inactive, usec float64
			for i, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				f := strings.Fields(l)
				v, _ := strconv.ParseFloat(f[len(f)-1], 64)
				switch {
				case i == 0:
					cur = v
				case f[0] == "anon":
					anon = v
				case f[0] == "shmem":
					shmem = v
				case f[0] == "inactive_file":
					inactive = v
				case f[0] == "usage_usec":
					usec = v
				}
			}
			s.pgWork, s.pgAnon, s.pgCPU = cur-inactive, anon+shmem, usec/1e6
		}
	}
	return s
}

// parseCPU reads ps time: [[H:]M:]S.ss
func parseCPU(s string) float64 {
	t := 0.0
	for _, p := range strings.Split(s, ":") {
		v, _ := strconv.ParseFloat(p, 64)
		t = t*60 + v
	}
	return t
}

// run samples every 250 ms while fn runs and logs the peaks of the phase.
func (m meter) run(t *testing.T, name string, fn func()) {
	t.Helper()
	var (
		mu      sync.Mutex
		samples []sample
	)
	every := max(1, int(max(m.pgEvery, time.Second)/(250*time.Millisecond)))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for tick := 0; ; tick++ {
			select {
			case <-stop: // the last sample always reads the container, so the CPU delta spans the whole phase
				s := m.read(true)
				mu.Lock()
				samples = append(samples, s)
				mu.Unlock()
				return
			default:
			}
			s := m.read(tick%every == 0)
			if tick%every != 0 && len(samples) > 0 {
				s.pgWork, s.pgAnon, s.pgCPU = samples[len(samples)-1].pgWork, samples[len(samples)-1].pgAnon, samples[len(samples)-1].pgCPU
			}
			mu.Lock()
			samples = append(samples, s)
			mu.Unlock()
			time.Sleep(250 * time.Millisecond)
		}
	}()
	start := time.Now()
	fn()
	wall := time.Since(start)
	close(stop)
	<-done
	report(t, name, wall, samples)
}

func report(t *testing.T, name string, wall time.Duration, ss []sample) {
	t.Helper()
	var rss, work, anon float64
	for _, s := range ss {
		rss, work, anon = max(rss, s.rss), max(work, s.pgWork), max(anon, s.pgAnon)
	}
	first, last := ss[0], ss[len(ss)-1]
	avg := func(get func(sample) float64) float64 {
		return (get(last) - get(first)) / last.at.Sub(first.at).Seconds() * 100
	}
	peak := func(get func(sample) float64) float64 { // best 1 s window
		p := 0.0
		for i := range ss {
			for j := i + 1; j < len(ss); j++ {
				if d := ss[j].at.Sub(ss[i].at); d >= time.Second {
					p = max(p, (get(ss[j])-get(ss[i]))/d.Seconds()*100)
					break
				}
			}
		}
		return p
	}
	vcpu, pcpu := func(s sample) float64 { return s.cpu }, func(s sample) float64 { return s.pgCPU }
	t.Logf("RESULT %-28s wall %8s | vitamux rss peak %9s last %9s cpu avg %5.1f%% peak1s %5.1f%% | pg anon+shm peak %9s workset peak %9s cpu avg %5.1f%% peak1s %5.1f%%",
		name, wall.Round(100*time.Millisecond), mib(rss), mib(last.rss), avg(vcpu), peak(vcpu), mib(anon), mib(work), avg(pcpu), peak(pcpu))
}

type server struct {
	cmd    *exec.Cmd
	logs   *bytes.Buffer
	cookie string
	csrf   string
}

var hc = &http.Client{Timeout: 5 * time.Minute}

func (s *server) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://"+addr+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
		if method != http.MethodGet {
			req.Header.Set("X-CSRF-Token", s.csrf)
			req.Header.Set("Idempotency-Key", uuid.NewString())
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// bench is what every phase shares.
type bench struct {
	bin, keyFile, tmp, dbURL, pg, password string
	owner, app                             *pgxpool.Pool
	userID                                 uuid.UUID
	env                                    []string // serve settings, as in deploy/compose/compose.yaml
}

func (b *bench) vx(env []string, args ...string) *exec.Cmd {
	c := exec.Command(b.bin, args...)
	c.Env = append(os.Environ(), env...)
	return c
}

// start runs `vitamux serve` and waits for /readyz. Every phase gets a fresh process, so one
// phase's heap cannot hide another's peak (Go keeps freed memory for a while).
func (b *bench) start(t *testing.T, login bool) *server {
	t.Helper()
	s := &server{logs: &bytes.Buffer{}}
	s.cmd = b.vx(b.env, "serve")
	s.cmd.Stderr = s.logs
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s.cmd.ProcessState == nil {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
		}
	})
	for i := 0; ; i++ {
		if resp, err := http.Get("http://" + addr + "/readyz"); err == nil {
			ok := resp.StatusCode == 200
			resp.Body.Close()
			if ok {
				break
			}
		}
		if i > 120 {
			t.Fatalf("server not ready:\n%s", s.logs.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if login { // the cookie is attached by hand: it is Secure and the listener is plain http
		req, _ := http.NewRequest("POST", "http://"+addr+"/api/v1/auth/login", strings.NewReader(`{"username":"synthetic-owner","password":"`+b.password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("login failed (%v)", err)
		}
		var sess struct {
			CSRF string `json:"csrf_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&sess)
		resp.Body.Close()
		for _, c := range resp.Cookies() {
			s.cookie = c.Name + "=" + c.Value
		}
		s.csrf = sess.CSRF
	}
	return s
}

// stop ends the server like SIGTERM in a container and logs the whole-process maximums.
func (s *server) stop(t *testing.T, label string) {
	t.Helper()
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	_ = s.cmd.Wait()
	rusage(t, label, s.cmd.ProcessState)
}

func rusage(t *testing.T, label string, ps *os.ProcessState) {
	t.Helper()
	ru := ps.SysUsage().(*syscall.Rusage)
	t.Logf("RESULT %-28s whole process: max RSS %s (rusage), user %.1f s, sys %.1f s", label, mib(float64(ru.Maxrss)),
		time.Duration(ru.Utime.Nano()).Seconds(), time.Duration(ru.Stime.Nano()).Seconds())
}

// rebuild marks (metric, day) pairs as dirty, old enough to settle, queues the job and waits for it.
func (b *bench) rebuild(t *testing.T, m meter, label, where string) {
	t.Helper()
	ctx := context.Background()
	var marks int64
	if err := b.owner.QueryRow(ctx, `WITH ins AS (INSERT INTO resolution_dirty (user_id, metric_id, local_date, marked_at)
		SELECT DISTINCT user_id, metric_id, local_date, now() - interval '10 minutes' FROM measurements `+where+` RETURNING 1)
		SELECT count(*) FROM ins`).Scan(&marks); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	m.run(t, label, func() {
		id, _, err := jobs.Enqueue(ctx, db.New(b.app).Q(), jobs.NewJob{Kind: resolve.KindRebuildAggregates, DedupeKey: "budget"})
		if err != nil {
			t.Fatal(err)
		}
		for {
			var st string
			var left int
			_ = b.owner.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&st)
			_ = b.owner.QueryRow(ctx, `SELECT count(*) FROM resolution_dirty`).Scan(&left)
			if st == "succeeded" && left == 0 {
				return
			}
			if st == "dead" || st == "failed" || time.Now().After(deadline) {
				t.Logf("RESULT %s did not finish: job %s", label, st) // e.g. the container was killed mid-run
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
	t.Logf("RESULT %s: %d (metric, day) marks", label, marks)
}

// container runs a linux build of the binary under the Compose limits (deploy/compose/compose.yaml:
// 512 MiB, GOMEMLIMIT 400MiB, read-only root) on distroless, idle for a minute and then the
// full-year rebuild, and reports its peak memory and whether the kernel killed it. The numbers
// come from `docker stats` (the working set), as distroless has no shell to read the cgroup.
func (b *bench) container(t *testing.T, linuxBin string) {
	t.Helper()
	const name = "vitamux-budget-app"
	// Docker Desktop only mounts directories it shares (not the system temp dir): VITAMUX_BUDGET_SHARE_DIR.
	share, err := os.MkdirTemp(os.Getenv("VITAMUX_BUDGET_SHARE_DIR"), "budget-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(share) })
	for _, f := range []struct{ from, to string }{{linuxBin, "vitamux"}, {b.keyFile, "master.key"}} {
		data, err := os.ReadFile(f.from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(share, f.to), data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	linuxBin, b.keyFile = filepath.Join(share, "vitamux"), filepath.Join(share, "master.key")
	urlFile := filepath.Join(share, "dburl")
	if err := os.WriteFile(urlFile, []byte(strings.Replace(b.dbURL, "127.0.0.1", "host.docker.internal", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(share, "data")
	_ = os.MkdirAll(dataDir, 0o777)
	_ = os.Chmod(dataDir, 0o777)
	_ = exec.Command("docker", "rm", "-f", name).Run()
	args := []string{"run", "-d", "--name", name, "--memory", "512m", "--read-only", "--user", "0", "--tmpfs", "/tmp:size=64m,mode=1777", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "-p", "127.0.0.1:18089:8080", "-v", linuxBin + ":/vitamux:ro", "-v", b.keyFile + ":/secrets/master.key:ro",
		"-v", urlFile + ":/secrets/database_url:ro", "-v", dataDir + ":/data", "--entrypoint", "/vitamux",
		"-e", "VITAMUX_ENV=production", "-e", "VITAMUX_HTTP_ADDR=0.0.0.0:8080", "-e", "VITAMUX_PUBLIC_URL=http://127.0.0.1:18089",
		"-e", "VITAMUX_DATABASE_URL_FILE=/secrets/database_url", "-e", "VITAMUX_MASTER_KEY_FILE=/secrets/master.key", "-e", "VITAMUX_DATA_DIR=/data",
		"-e", "GOMEMLIMIT=400MiB", "gcr.io/distroless/static-debian12:nonroot", "serve"}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	for i := 0; ; i++ {
		if resp, err := http.Get("http://127.0.0.1:18089/readyz"); err == nil {
			ok := resp.StatusCode == 200
			resp.Body.Close()
			if ok {
				break
			}
		}
		if i > 120 {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("container not ready:\n%s", logs)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var mu sync.Mutex
	var peak, last float64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			out, _ := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", name).Output()
			if f := strings.Fields(string(out)); len(f) > 0 {
				v := parseSize(f[0])
				mu.Lock()
				peak, last = max(peak, v), v
				mu.Unlock()
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	time.Sleep(60 * time.Second)
	mu.Lock()
	t.Logf("RESULT container idle (60 s, 512 MiB limit): working set peak %s, last %s", mib(peak), mib(last))
	mu.Unlock()
	b.rebuild(t, meter{pg: b.pg, pgEvery: 10 * time.Second}, "container rebuild_aggregates full year", "")
	close(stop)
	<-done
	state, _ := exec.Command("docker", "inspect", "-f", "OOMKilled={{.State.OOMKilled}} status={{.State.Status}} exit={{.State.ExitCode}}", name).Output()
	mu.Lock()
	t.Logf("RESULT container after rebuild: working set peak sampled %s; %s", mib(peak), strings.TrimSpace(string(state)))
	mu.Unlock()
}

func parseSize(s string) float64 {
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if v, ok := strings.CutSuffix(s, u.suffix); ok {
			f, _ := strconv.ParseFloat(v, 64)
			return f * u.mult
		}
	}
	return 0
}

func TestResourceBudget(t *testing.T) {
	if os.Getenv("VITAMUX_BUDGET") == "" {
		t.Skip("set VITAMUX_BUDGET=1 (see the file comment; about 25 min)")
	}
	bin, dataset, pgName := os.Getenv("VITAMUX_BUDGET_BIN"), os.Getenv("VITAMUX_VOLUME_DATASET"), os.Getenv("VITAMUX_BUDGET_PG")
	if bin == "" || dataset == "" {
		t.Fatal("set VITAMUX_BUDGET_BIN and VITAMUX_VOLUME_DATASET")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	b := &bench{bin: bin, tmp: tmp, pg: pgName, keyFile: filepath.Join(tmp, "master.key")}
	if out, err := b.vx(nil, "admin", "init-secrets", "--out", b.keyFile).CombinedOutput(); err != nil {
		t.Fatalf("init-secrets: %v\n%s", err, out)
	}

	// ---- (a) full-year load by COPY from the test process; PostgreSQL side measured.
	var app *pgxpool.Pool
	b.dbURL, app = dbtest.Migrated(t)
	b.app, b.owner = app, dbtest.Pool(t, b.dbURL, db.OwnerRole)
	var stats fixtureload.Stats
	meter{pg: pgName}.run(t, "load fixtureload (PG side)", func() {
		var err error
		if stats, err = fixtureload.Load(ctx, app, dataset); err != nil {
			t.Fatal(err)
		}
	})
	b.userID = stats.UserID
	t.Logf("RESULT loaded %d measurements, %d groups, %d sleep sessions, %d raw payloads", stats.Measurements, stats.Groups, stats.Sleep, stats.Raw)
	pw := make([]byte, 16)
	_, _ = rand.Read(pw)
	b.password = hex.EncodeToString(pw)
	hash, err := auth.HashPassword(b.password)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, b.owner, `UPDATE users SET password_hash = $1`, hash)
	for _, p := range []struct{ from, tz string }{{"2024-01-01T00:00:00Z", "Europe/Berlin"}, {"2025-05-11T22:00:00Z", "America/New_York"}, {"2025-05-22T04:00:00Z", "Europe/Berlin"}} {
		mustExec(t, ctx, b.owner, `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, $3, $4)`, uuid.New(), b.userID, p.tz, p.from)
	}
	mustExec(t, ctx, b.owner, `VACUUM (ANALYZE)`)
	dbSizes(t, ctx, b.owner, "after load")

	if err := os.WriteFile(filepath.Join(tmp, "dburl"), []byte(b.dbURL), 0o600); err != nil {
		t.Fatal(err)
	}
	b.env = []string{"VITAMUX_ENV=production", "VITAMUX_HTTP_ADDR=" + addr, "VITAMUX_PUBLIC_URL=http://" + addr,
		"VITAMUX_DATABASE_URL_FILE=" + filepath.Join(tmp, "dburl"), "VITAMUX_DATABASE_URL=", "VITAMUX_DATA_DIR=" + filepath.Join(tmp, "data"),
		"VITAMUX_MASTER_KEY_FILE=" + b.keyFile, "VITAMUX_LOG_LEVEL=info", "GOMEMLIMIT=400MiB", "VITAMUX_BACKUP_DIR="}
	if g := os.Getenv("GOMAXPROCS_SERVER"); g != "" {
		b.env = append(b.env, "GOMAXPROCS="+g)
	}
	settle := func() { time.Sleep(5 * time.Second) }

	if os.Getenv("VITAMUX_BUDGET_ONLY") == "container" {
		b.container(t, os.Getenv("VITAMUX_BUDGET_CONTAINER_BIN"))
		return
	}

	// ---- idle: a fresh server, two minutes; CPU of the second minute is the reported figure.
	for _, login := range []bool{false, true} { // after a sign-in the process keeps its Argon2 buffers
		s := b.start(t, login)
		m := meter{pid: s.cmd.Process.Pid, pg: pgName, pgEvery: 10 * time.Second}
		name := map[bool]string{false: "idle fresh", true: "idle after sign-in"}[login]
		m.run(t, name+" 0-60 s", func() { time.Sleep(60 * time.Second) })
		m.run(t, name+" 60-120 s", func() { time.Sleep(60 * time.Second) })
		s.stop(t, name)
	}
	if os.Getenv("VITAMUX_BUDGET_ONLY") == "idle" {
		return
	}

	// ---- (c) dashboard: ten metrics over 90 days, in a helper process (the resolved endpoints are
	// not routed yet, docs/plan J10), cold sequential, cold parallel, warm parallel.
	for _, mode := range []string{"idle", "cold_seq", "cold_par", "warm_par"} {
		c := exec.Command(os.Args[0], "-test.run=^TestDashboardChild$")
		c.Env = append(os.Environ(), "VITAMUX_BUDGET_CHILD="+mode, "VITAMUX_BUDGET_DB="+b.dbURL, "VITAMUX_BUDGET_USER="+b.userID.String(), "GOMEMLIMIT=400MiB")
		if g := os.Getenv("GOMAXPROCS_SERVER"); g != "" {
			c.Env = append(c.Env, "GOMAXPROCS="+g)
		}
		var out bytes.Buffer
		c.Stdout = &out
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		var werr error
		meter{pid: c.Process.Pid, pg: pgName}.run(t, "dashboard "+mode, func() { werr = c.Wait() })
		if werr != nil {
			t.Fatalf("dashboard %s: %v\n%s", mode, werr, out.String())
		}
		rusage(t, "dashboard "+mode, c.ProcessState)
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(l, "DURATIONS ") {
				t.Logf("RESULT %s", l)
			}
		}
	}

	// ---- (d) export of the year with raw content, then download
	s := b.start(t, true)
	settle()
	m := meter{pid: s.cmd.Process.Pid, pg: pgName}
	exportZip := filepath.Join(tmp, "export.zip")
	m.run(t, "export + download", func() {
		code, body := s.do(t, "POST", "/api/v1/exports", map[string]any{"format": "ndjson", "include_raw": true})
		if code != 202 {
			t.Fatalf("export: %d %.200s", code, body)
		}
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &e)
		for {
			code, body = s.do(t, "GET", "/api/v1/exports/"+e.ID, nil)
			var st struct {
				Status      string  `json:"status"`
				DownloadURL *string `json:"download_url"`
			}
			_ = json.Unmarshal(body, &st)
			if code != 200 || st.Status == "failed" {
				t.Fatalf("export %d %s", code, st.Status)
			}
			if st.Status == "done" && st.DownloadURL != nil {
				req, _ := http.NewRequest("GET", "http://"+addr+*st.DownloadURL, nil)
				req.Header.Set("Cookie", s.cookie)
				resp, err := hc.Do(req)
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("download: %v", err)
				}
				f, _ := os.Create(exportZip)
				_, _ = io.Copy(f, resp.Body)
				_ = f.Close()
				resp.Body.Close()
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	})
	fi, err := os.Stat(exportZip)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RESULT export zip %s (%d bytes); blob dir %s", mib(float64(fi.Size())), fi.Size(), dirSize(filepath.Join(tmp, "data", "blobs")))
	s.stop(t, "export")

	// ---- (b) rebuild_aggregates: one week of marks (a normal sync), then every day (after an import or reprocess)
	s = b.start(t, false)
	settle()
	b.rebuild(t, meter{pid: s.cmd.Process.Pid, pg: pgName}, "rebuild_aggregates 7 days", `WHERE local_date >= (SELECT max(local_date) FROM measurements) - 6`)
	s.stop(t, "rebuild 7 days")
	s = b.start(t, false)
	settle()
	b.rebuild(t, meter{pid: s.cmd.Process.Pid, pg: pgName}, "rebuild_aggregates full year", "")
	s.stop(t, "rebuild full year")
	dbSizes(t, ctx, b.owner, "end")
	if lb := os.Getenv("VITAMUX_BUDGET_CONTAINER_BIN"); lb != "" {
		b.container(t, lb)
	}

	// ---- (a, vitamux side) the exported year loaded by `vitamux import ndjson` into a fresh database
	if os.Getenv("VITAMUX_BUDGET_SKIP_IMPORT") != "" {
		return
	}
	dbURL2, app2 := dbtest.Migrated(t)
	if err := os.WriteFile(filepath.Join(tmp, "dburl2"), []byte(dbURL2), 0o600); err != nil {
		t.Fatal(err)
	}
	env2 := []string{"VITAMUX_ENV=production", "VITAMUX_DATABASE_URL_FILE=" + filepath.Join(tmp, "dburl2"), "VITAMUX_DATABASE_URL=",
		"VITAMUX_DATA_DIR=" + filepath.Join(tmp, "data2"), "VITAMUX_MASTER_KEY_FILE=" + b.keyFile, "GOMEMLIMIT=400MiB"}
	if g := os.Getenv("GOMAXPROCS_SERVER"); g != "" {
		env2 = append(env2, "GOMAXPROCS="+g)
	}
	co := b.vx(env2, "admin", "create-owner")
	co.Stdin = strings.NewReader("synthetic-owner\n" + b.password + "\n")
	if out, err := co.CombinedOutput(); err != nil {
		t.Fatalf("create-owner: %v\n%s", err, out)
	}
	imp := b.vx(env2, "import", "ndjson", exportZip)
	var impOut bytes.Buffer
	imp.Stdout, imp.Stderr = &impOut, &impOut
	if err := imp.Start(); err != nil {
		t.Fatal(err)
	}
	var impErr error
	meter{pid: imp.Process.Pid, pg: pgName}.run(t, "import ndjson (vitamux load)", func() { impErr = imp.Wait() })
	if impErr != nil {
		t.Fatalf("import: %v\n%.500s", impErr, impOut.String())
	}
	rusage(t, "import", imp.ProcessState)
	var n, n2 int64
	_ = b.owner.QueryRow(ctx, `SELECT count(*) FROM measurements`).Scan(&n)
	_ = app2.QueryRow(ctx, `SELECT count(*) FROM measurements`).Scan(&n2)
	t.Logf("RESULT import: measurements original %d, imported %d", n, n2)
}

// TestDashboardChild is the helper process of TestResourceBudget: ten metrics over 90 days
// through resolve.Run, as docs/benchmarks/baseline.md#resolution-and-cache, in a process of its
// own so its RSS can be read. It does nothing unless the parent sets VITAMUX_BUDGET_CHILD.
func TestDashboardChild(t *testing.T) {
	mode := os.Getenv("VITAMUX_BUDGET_CHILD")
	if mode == "" {
		t.Skip("helper process of TestResourceBudget")
	}
	ctx := context.Background()
	url := os.Getenv("VITAMUX_BUDGET_DB")
	app, err := db.Open(ctx, url, db.AppRole)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner, err := db.Open(ctx, url, db.OwnerRole)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	d, user := db.New(app), uuid.MustParse(os.Getenv("VITAMUX_BUDGET_USER"))
	fromD, _ := time.Parse("2006-01-02", from)
	toD, _ := time.Parse("2006-01-02", to)
	one := func(metric string) {
		if _, err := resolve.Run(ctx, d, resolve.Request{UserID: user, Metric: metric, From: fromD, To: toD}); err != nil {
			t.Error(metric, err)
		}
	}
	par := func() time.Duration {
		st := time.Now()
		var wg sync.WaitGroup
		for _, mt := range dashMetrics {
			wg.Go(func() { one(mt) })
		}
		wg.Wait()
		return time.Since(st)
	}
	clear := func() {
		if _, err := owner.Exec(ctx, `TRUNCATE resolved_cache`); err != nil {
			t.Fatal(err)
		}
	}
	var ds []time.Duration
	switch mode {
	case "idle": // connect, then sit: the helper's own floor
		time.Sleep(5 * time.Second)
	case "cold_seq":
		for range rounds {
			clear()
			st := time.Now()
			for _, mt := range dashMetrics {
				one(mt)
			}
			ds = append(ds, time.Since(st))
		}
	case "cold_par":
		for range rounds {
			clear()
			ds = append(ds, par())
		}
	case "warm_par": // the cache is full from cold_par
		for range 10 {
			ds = append(ds, par())
		}
	}
	var out []string
	for _, x := range ds {
		out = append(out, x.Round(time.Millisecond).String())
	}
	fmt.Printf("DURATIONS %s wall of ten metrics x 90 days per round: %s\n", mode, strings.Join(out, " "))
}

func mustExec(t *testing.T, ctx context.Context, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func dbSizes(t *testing.T, ctx context.Context, p *pgxpool.Pool, label string) {
	t.Helper()
	var total int64
	if err := p.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	rows, err := p.Query(ctx, `SELECT c.relname, pg_total_relation_size(c.oid), pg_relation_size(c.oid), pg_indexes_size(c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = $1 ORDER BY 2 DESC LIMIT 6`, db.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var name string
		var tot, heap, idx int64
		_ = rows.Scan(&name, &tot, &heap, &idx)
		parts = append(parts, fmt.Sprintf("%s %s (heap %s, idx %s)", name, mib(float64(tot)), mib(float64(heap)), mib(float64(idx))))
	}
	t.Logf("RESULT db size %s: total %s (%d bytes); %s", label, mib(float64(total)), total, strings.Join(parts, "; "))
}

func dirSize(dir string) string {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return fmt.Sprintf("%s (%d bytes)", mib(float64(n)), n)
}
