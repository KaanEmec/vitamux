package remote

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	exampleDir  = "../../../examples/sidecar-python"
	exampleScn  = exampleDir + "/conformance.json"
	secretValue = "synthetic-kit-test-secret"
)

// python3.12 or newer (the example's floor): $VITAMUX_TEST_PYTHON, else the newest on PATH.
// VITAMUX_REQUIRE_PYTHON=1 (CI) turns "not found" into a failure.
func python(t *testing.T) string {
	t.Helper()
	cands := []string{os.Getenv("VITAMUX_TEST_PYTHON"), "python3", "python3.14", "python3.13", "python3.12"}
	for _, c := range cands {
		if c == "" {
			continue
		}
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, err := exec.CommandContext(t.Context(), p, "-c", "import sys; print(int(sys.version_info >= (3, 12)))").Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			return p
		}
	}
	if os.Getenv("VITAMUX_REQUIRE_PYTHON") != "" {
		t.Fatal("python 3.12 or newer is required (VITAMUX_REQUIRE_PYTHON is set)")
	}
	t.Skip("python 3.12+ not found; set VITAMUX_TEST_PYTHON")
	return ""
}

var servingRe = regexp.MustCompile(`serving \S+ on (\S+)`)

// startExample runs the example sidecar (with BREAK=brk when set) and returns its base URL.
func startExample(t *testing.T, py, brk string) string {
	t.Helper()
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte(secretValue), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, py, "sidecar.py")
	cmd.Dir = exampleDir
	cmd.Env = append(os.Environ(), "VITAMUX_SIDECAR_SECRET_FILE="+secretFile, "SIDECAR_ADDR=127.0.0.1:0", "BREAK="+brk)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	addr := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := servingRe.FindStringSubmatch(sc.Text()); m != nil {
				addr <- m[1]
				break
			}
		}
		for sc.Scan() { // keep draining so the sidecar never blocks on stderr
		}
	}()
	select {
	case a := <-addr:
		return "http://" + a
	case <-time.After(10 * time.Second):
		t.Fatal("the example sidecar did not start")
		return ""
	}
}

func runKit(t *testing.T, url string) (results []ConformanceResult, failed int) {
	t.Helper()
	scn, err := LoadScenario(exampleScn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	failed, err = RunConformance(ctx, ConformanceOptions{URL: url, Secret: secretValue, Scenario: scn}, func(r ConformanceResult) {
		results = append(results, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	return results, failed
}

func failures(rs []ConformanceResult) (names []string) {
	for _, r := range rs {
		if r.Status == StatusFail {
			names = append(names, r.Check)
		}
	}
	return names
}

// breakChecks names the one check each BREAK variant of the example must fail.
var breakChecks = map[string]string{
	"bad_describe":       "describe",
	"bad_auth_step":      "auth_flow",
	"no_auth_check":      "bad_bearer",
	"no_protocol_header": "protocol_header",
	"no_result_line":     "fetch_lines",
	"malformed_line":     "fetch_lines",
	"bad_cursor":         "fetch_paging",
	"unstable_replay":    "fetch_replay",
	"no_rotation":        "rotated_credentials",
	"wrong_error_code":   "typed_errors",
	"no_retry_after":     "retry_after",
	"bad_drift":          "drift_report",
	"leaks_secret":       "no_secret_echo",
}

func TestExampleSidecarPasses(t *testing.T) {
	py := python(t)
	results, failed := runKit(t, startExample(t, py, ""))
	if failed != 0 {
		t.Fatalf("the conformant example fails %v: %+v", failures(results), results)
	}
	for _, r := range results {
		if r.Status == StatusSkip {
			t.Errorf("check %s was skipped on the example: %s", r.Check, r.Message)
		}
	}
	if len(results) != len(confChecks) {
		t.Fatalf("%d results for %d checks", len(results), len(confChecks))
	}
}

// TestBrokenVariantsFailOneCheck: each BREAK value of the example fails exactly its named check.
func TestBrokenVariantsFailOneCheck(t *testing.T) {
	py := python(t)
	list := exec.CommandContext(t.Context(), py, "-c", "import sidecar; print(*sidecar.BREAKS)")
	list.Dir = exampleDir
	out, err := list.Output()
	if err != nil {
		t.Fatal(err)
	}
	listed := strings.Fields(string(out))
	if len(listed) != len(breakChecks) {
		t.Fatalf("the example has BREAK values %v, the test names %d", listed, len(breakChecks))
	}
	for _, brk := range listed {
		want, ok := breakChecks[brk]
		if !ok {
			t.Fatalf("BREAK=%s has no expected check", brk)
		}
		t.Run(brk, func(t *testing.T) {
			results, _ := runKit(t, startExample(t, py, brk))
			if got := failures(results); len(got) != 1 || got[0] != want {
				t.Fatalf("BREAK=%s fails %v, want only %s: %+v", brk, got, want, results)
			}
		})
	}
}

func TestUnreachableSidecarFailsOnce(t *testing.T) {
	var results []ConformanceResult
	failed, err := RunConformance(context.Background(), ConformanceOptions{URL: "http://127.0.0.1:1", Secret: secretValue}, func(r ConformanceResult) {
		results = append(results, r)
	})
	if err != nil || failed != 1 || results[0].Check != "protocol_header" || results[0].Status != StatusFail {
		t.Fatalf("failed=%d err=%v results=%+v", failed, err, results)
	}
	for _, r := range results[1:] {
		if r.Status != StatusSkip {
			t.Errorf("%s: %s, want SKIP", r.Check, r.Status)
		}
	}
}

func TestScenarioSchema(t *testing.T) {
	if _, err := LoadScenario(exampleScn); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"unknown key":      `{"logn": []}`,
		"login and creds":  `{"login": [], "credentials": {}}`,
		"unknown class":    `{"errors": {"boom": {}}}`,
		"bad mode":         `{"mode": "nightly"}`,
		"nested both":      `{"rotated": {"login": [], "credentials": {}}}`,
		"values not text":  `{"login": [{"values": {"code": 1}}]}`,
		"callback not map": `{"login": [{"callback": ["x"]}]}`,
	} {
		f := filepath.Join(t.TempDir(), "s.json")
		if err := os.WriteFile(f, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadScenario(f); err == nil {
			t.Errorf("%s: scenario accepted", name)
		}
	}
}
