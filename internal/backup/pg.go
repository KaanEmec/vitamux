package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// libpq turns a postgres:// URL into a password-free URL for the command line and the
// environment that carries the password, so it never shows in the process list. Query
// parameters only pgx understands are dropped.
func libpq(raw string) (string, []string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", nil, errors.New("backup: the database URL must be a postgres:// URL") // never echo it
	}
	password, _ := u.User.Password()
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	q := u.Query()
	if p := q.Get("password"); p != "" {
		password = p
	}
	for k := range q {
		if k == "password" || strings.HasPrefix(k, "pool_") || pgxOnly[k] {
			q.Del(k)
		}
	}
	q.Set("application_name", "vitamux-backup")
	u.RawQuery = q.Encode()

	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PGPASSWORD=") {
			env = append(env, kv)
		}
	}
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return u.String(), env, nil
}

var pgxOnly = map[string]bool{
	"statement_cache_capacity": true, "description_cache_capacity": true,
	"default_query_exec_mode": true, "min_read_buffer_size": true,
}

// run executes a PostgreSQL client tool. A failure carries the tail of its stderr, which names
// the server and role but never the password (it is only in the environment); DETAIL and
// CONTEXT lines are dropped because they can quote row values.
func run(ctx context.Context, env []string, stdout io.Writer, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("backup: %s not found on PATH (install the PostgreSQL client tools matching the server's major version)", name)
	}
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed tool, arguments built here
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr // a nil stdout discards
	if err := cmd.Run(); err != nil {
		var lines []string
		for l := range strings.SplitSeq(stderr.String(), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, "DETAIL:") && !strings.Contains(l, "CONTEXT:") {
				lines = append(lines, l)
			}
		}
		msg := strings.Join(lines, "; ")
		if len(msg) > 600 {
			msg = "…" + msg[len(msg)-600:]
		}
		return fmt.Errorf("backup: %s: %w: %s", name, err, msg)
	}
	return nil
}
