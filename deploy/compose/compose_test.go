// Package compose holds the policy test for the release Compose file (J13.3) and its Coolify
// variant (../coolify/compose.yaml, J14.2). It parses the files and .env.example statically, so
// it needs no Docker. See docs/deploy/compose.md and docs/install.md#coolify.
package compose

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type service struct {
	Image           string    `yaml:"image"`
	ReadOnly        bool      `yaml:"read_only"`
	CapDrop         []string  `yaml:"cap_drop"`
	CapAdd          []string  `yaml:"cap_add"`
	SecurityOpt     []string  `yaml:"security_opt"`
	Privileged      bool      `yaml:"privileged"`
	NetworkMode     string    `yaml:"network_mode"`
	Ports           []any     `yaml:"ports"`
	Networks        []string  `yaml:"networks"`
	Volumes         []any     `yaml:"volumes"`
	Environment     any       `yaml:"environment"`
	Healthcheck     *struct{} `yaml:"healthcheck"`
	MemLimit        string    `yaml:"mem_limit"`
	StopGracePeriod string    `yaml:"stop_grace_period"`
}

type file struct {
	Services map[string]service `yaml:"services"`
	Networks map[string]struct {
		Internal bool `yaml:"internal"`
	} `yaml:"networks"`
}

const (
	release = "compose.yaml"
	coolify = "../coolify/compose.yaml"
)

// each runs fn as a subtest for both Compose files.
func each(t *testing.T, fn func(t *testing.T, path string, f file, raw []byte)) {
	for _, path := range []string{release, coolify} {
		t.Run(path, func(t *testing.T) {
			f, raw := load(t, path)
			fn(t, path, f, raw)
		})
	}
}

func load(t *testing.T, path string) (file, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repository paths
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vitamux", "postgres", "migrate"} {
		if _, ok := f.Services[name]; !ok {
			t.Fatalf("service %q missing", name)
		}
	}
	return f, raw
}

// env returns a service's environment as key -> value, whether written as a map or a list.
func env(t *testing.T, s service) map[string]string {
	t.Helper()
	out := map[string]string{}
	switch e := s.Environment.(type) {
	case nil:
	case map[string]any:
		for k, v := range e {
			out[k] = fmt.Sprint(v)
		}
	case []any:
		for _, item := range e {
			k, v, _ := strings.Cut(fmt.Sprint(item), "=")
			out[k] = v
		}
	default:
		t.Fatalf("unsupported environment form %T", e)
	}
	return out
}

func TestHardening(t *testing.T) {
	each(t, func(t *testing.T, _ string, f file, _ []byte) { hardening(t, f) })
}

func hardening(t *testing.T, f file) {
	for name, s := range f.Services {
		if !s.ReadOnly {
			t.Errorf("%s: read_only must be true", name)
		}
		if !slices.Contains(s.CapDrop, "ALL") || len(s.CapAdd) > 0 {
			t.Errorf("%s: must cap_drop ALL and add no capabilities", name)
		}
		if !slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("%s: security_opt must include no-new-privileges:true", name)
		}
		if s.Privileged || s.NetworkMode == "host" {
			t.Errorf("%s: privileged or host networking is not allowed", name)
		}
		if s.Healthcheck == nil {
			t.Errorf("%s: declare a healthcheck (or disable it explicitly for one-shot services)", name)
		}
		if s.MemLimit == "" {
			t.Errorf("%s: mem_limit is required", name)
		}
		for _, v := range s.Volumes {
			if strings.Contains(fmt.Sprint(v), "docker.sock") {
				t.Errorf("%s: the Docker socket must not be mounted", name)
			}
		}
	}
}

// Budgets from docs/architecture/project.md#resource-budget.
func TestMemoryLimits(t *testing.T) {
	each(t, func(t *testing.T, _ string, f file, _ []byte) { memoryLimits(t, f) })
}

func memoryLimits(t *testing.T, f file) {
	for name, max := range map[string]int64{"vitamux": 512 << 20, "postgres": 1 << 30} {
		got, err := parseBytes(f.Services[name].MemLimit)
		if err != nil || got > max {
			t.Errorf("%s: mem_limit %q must parse and be <= %d bytes (%v)", name, f.Services[name].MemLimit, max, err)
		}
	}
}

func parseBytes(s string) (int64, error) {
	units := map[string]int64{"k": 1 << 10, "m": 1 << 20, "g": 1 << 30}
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 1 {
		if mul, ok := units[s[len(s)-1:]]; ok {
			n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
			return n * mul, err
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

func TestNetworkExposure(t *testing.T) {
	each(t, func(t *testing.T, path string, f file, _ []byte) { networkExposure(t, path, f) })
}

func networkExposure(t *testing.T, path string, f file) {
	if len(f.Services["postgres"].Ports) > 0 {
		t.Error("postgres must not publish ports")
	}
	for name, s := range f.Services {
		if path == coolify && len(s.Ports) > 0 {
			t.Errorf("%s: publishes a port; Coolify's proxy routes the domain instead", name)
		}
		if name != "vitamux" && len(s.Ports) > 0 {
			t.Errorf("%s: only vitamux may publish a port", name)
		}
		for _, p := range s.Ports {
			spec, ok := p.(string) // the long syntax is not used; reject it so the check stays simple
			if !ok || !strings.HasPrefix(spec, "127.0.0.1:") {
				t.Errorf("%s: published port %v must be a string bound to 127.0.0.1", name, p)
			}
		}
	}
	for _, n := range []string{"postgres", "migrate"} {
		if len(f.Services[n].Networks) == 0 {
			t.Errorf("%s: must name its networks explicitly", n)
		}
		for _, net := range f.Services[n].Networks {
			if !f.Networks[net].Internal {
				t.Errorf("%s: network %q must be internal (no route out)", n, net)
			}
		}
	}
	if got := f.Services["vitamux"].StopGracePeriod; got != "60s" {
		t.Errorf("vitamux stop_grace_period = %q, want 60s (job drain)", got)
	}
}

var secretName = regexp.MustCompile(`(?i)(PASSWORD|SECRET|TOKEN|API_?KEY|MASTER_KEY|DATABASE_URL|PRIVATE_KEY)`)

// credentialURL matches scheme://user:password@host.
var credentialURL = regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`)

func TestNoPlaintextSecrets(t *testing.T) {
	each(t, func(t *testing.T, path string, f file, raw []byte) {
		for name, s := range f.Services {
			// Coolify keeps secrets in its environment store: only the offline `secrets` service may
			// receive one, and it writes it to a file for the others.
			if path == coolify && name == "secrets" && s.NetworkMode == "none" {
				continue
			}
			for k, v := range env(t, s) {
				if secretName.MatchString(k) && !strings.HasSuffix(k, "_FILE") && v != "" {
					t.Errorf("%s: %s must be passed as a *_FILE path, not a value", name, k)
				}
			}
		}
		if credentialURL.Match(raw) {
			t.Errorf("%s contains a URL with embedded credentials", path)
		}
	})
	example, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(example), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		k, v, ok := strings.Cut(line, "=")
		if ok && !strings.Contains(k, " ") && secretName.MatchString(k) && !strings.HasSuffix(k, "_FILE") && v != "" {
			t.Errorf(".env.example: %s must not carry a secret value", k)
		}
	}
	if credentialURL.Match(example) {
		t.Error(".env.example contains a URL with embedded credentials")
	}
}

func TestStartupOrder(t *testing.T) {
	each(t, func(t *testing.T, path string, _ file, raw []byte) {
		var f struct {
			Services map[string]struct {
				DependsOn map[string]struct {
					Condition string `yaml:"condition"`
				} `yaml:"depends_on"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		d := f.Services["vitamux"].DependsOn
		if d["migrate"].Condition != "service_completed_successfully" || d["postgres"].Condition != "service_healthy" {
			t.Errorf("vitamux must wait for postgres healthy and migrate completed, got %+v", d)
		}
		if path == coolify && (d["master-key"].Condition != "service_completed_successfully" ||
			f.Services["postgres"].DependsOn["secrets"].Condition != "service_completed_successfully") {
			t.Error("vitamux must wait for master-key, and postgres for secrets")
		}
	})
}

// Each generated secret volume is mounted only where the release file mounts that secret.
func TestCoolifySecretVolumes(t *testing.T) {
	f, _ := load(t, coolify)
	allowed := map[string][]string{
		"pg-secrets":      {"secrets", "postgres"},
		"migrate-secret":  {"secrets", "migrate", "restore"},
		"app-secret":      {"secrets", "vitamux"},
		"vitamux-secrets": {"master-key", "vitamux", "restore"},
	}
	for name, s := range f.Services {
		for _, v := range s.Volumes {
			spec, ok := v.(string)
			if !ok {
				continue
			}
			vol, _, _ := strings.Cut(spec, ":")
			if users, ok := allowed[vol]; ok && !slices.Contains(users, name) {
				t.Errorf("%s: must not mount %s", name, vol)
			}
		}
	}
	e := env(t, f.Services["vitamux"])
	if _, ok := e["SERVICE_URL_VITAMUX_8080"]; !ok || e["VITAMUX_PUBLIC_URL"] != "${SERVICE_URL_VITAMUX_8080}" {
		t.Error("vitamux must take VITAMUX_PUBLIC_URL from Coolify's SERVICE_URL_VITAMUX_8080")
	}
}

// The Coolify init SQL (inlined with `content:`) must carry every statement of the release
// stack's init files, and no `$`, which Compose or Coolify might interpolate.
func TestCoolifyInitSQL(t *testing.T) {
	var f struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	raw, err := os.ReadFile(coolify)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	var content string
	for _, v := range f.Services["postgres"].Volumes {
		if m, ok := v.(map[string]any); ok && m["target"] == "/docker-entrypoint-initdb.d/10-init.sql" {
			content, _ = m["content"].(string)
		}
	}
	if content == "" {
		t.Fatal("postgres: no inline /docker-entrypoint-initdb.d/10-init.sql")
	}
	if strings.Contains(content, "$") {
		t.Error("init SQL must not contain $")
	}
	got := " " + strings.Join(strings.Fields(content), " ") + " "
	roles, err := os.ReadFile("../sql/roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	logins, err := os.ReadFile("initdb/20-role-logins.sql")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CREATE ROLE vitamux_owner NOLOGIN;", "CREATE ROLE vitamux_app NOLOGIN;"} // the DO block's effect
	body := regexp.MustCompile(`(?s)DO \$\$.*?\$\$;`).ReplaceAllString(stripComments(string(roles)), "")
	for stmt := range strings.SplitSeq(body, ";") {
		if stmt = strings.Join(strings.Fields(stmt), " "); stmt != "" {
			want = append(want, stmt+";")
		}
	}
	for line := range strings.SplitSeq(stripComments(string(logins)), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			want = append(want, line)
		}
	}
	for _, w := range want {
		if !strings.Contains(got, " "+w+" ") {
			t.Errorf("init SQL lacks %q", w)
		}
	}
}

func stripComments(sql string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
