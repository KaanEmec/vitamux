package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
)

const adminUsage = `usage: vitamux admin <command>

Commands:
  init-secrets [--out PATH] [--if-missing]
                              generate the master key file (default: VITAMUX_MASTER_KEY_FILE, else
                              <data dir>/master.key); never overwrites, --if-missing exits 0 if it exists.
                              Also creates every missing sidecar secret of VITAMUX_SIDECARS.
  create-owner                create the owner account
  reset-password              set a new owner password and end every session
  purge-user [--username NAME] [--yes]
                              delete everything of the account (the only one by default);
                              without --yes it only prints what would be deleted

create-owner and reset-password prompt for the username and password on a terminal, or
read them as two lines from piped stdin. They are never taken from flags or the environment.
`

func admin(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) > 0 && args[0] == "init-secrets":
		return initSecrets(args[1:], stdout, stderr)
	case len(args) == 1 && (args[0] == "create-owner" || args[0] == "reset-password"):
		return owner(args[0], os.Stdin, stdout, stderr)
	case len(args) > 0 && args[0] == "purge-user":
		return purgeUserCmd(args[1:], stdout, stderr)
	}
	fmt.Fprint(stderr, adminUsage)
	return 2
}

func owner(cmd string, stdin *os.File, stdout, stderr io.Writer) int {
	username, password, err := readCredentials(stdin, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 1
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := db.CheckSchema(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 1
	}

	if cmd == "create-owner" {
		_, err = auth.CreateOwner(ctx, db.New(pool), username, password)
	} else {
		err = auth.ResetPassword(ctx, db.New(pool), username, password)
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		fmt.Fprintf(stderr, "%s: no account named %q\n", cmd, username)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 1
	case cmd == "create-owner":
		fmt.Fprintf(stdout, "owner %q created; sign in and enable TOTP\n", username)
	default:
		fmt.Fprintf(stdout, "password for %q reset; all sessions ended\n", username)
	}
	return 0
}

// readCredentials prompts on a terminal (password not echoed, asked twice) or reads two
// lines, username then password, from a pipe.
func readCredentials(in *os.File, prompt io.Writer) (username, password string, err error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		sc := bufio.NewScanner(in)
		lines := make([]string, 0, 2)
		for len(lines) < 2 && sc.Scan() {
			lines = append(lines, strings.TrimSuffix(sc.Text(), "\r"))
		}
		if err := sc.Err(); err != nil {
			return "", "", err
		}
		if len(lines) < 2 {
			return "", "", errors.New("expected two lines on stdin: username, then password")
		}
		return strings.TrimSpace(lines[0]), lines[1], nil
	}
	fmt.Fprint(prompt, "Username: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return "", "", err
	}
	fmt.Fprint(prompt, "Password: ")
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(prompt)
	if err != nil {
		return "", "", err
	}
	fmt.Fprint(prompt, "Repeat password: ")
	again, err := term.ReadPassword(fd)
	fmt.Fprintln(prompt)
	if err != nil {
		return "", "", err
	}
	if string(pw) != string(again) {
		return "", "", errors.New("passwords do not match")
	}
	return strings.TrimSpace(line), string(pw), nil
}

func initSecrets(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("admin init-secrets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", defaultKeyPath(), "master key file to create")
	ifMissing := fs.Bool("if-missing", false, "succeed without changes when the key file exists (one-shot deploy steps)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	sidecars, err := config.Sidecars(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "init-secrets: %v\n", err)
		return 1
	}
	id, err := crypto.WriteKeyFile(*out)
	switch {
	case errors.Is(err, os.ErrExist) && *ifMissing:
		fmt.Fprintf(stdout, "master key %s already exists; unchanged\n", *out)
	case errors.Is(err, os.ErrExist):
		fmt.Fprintf(stderr, "init-secrets: %s already exists; refusing to overwrite the master key\n", *out)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "init-secrets: %v\n", err)
		return 1
	default:
		fmt.Fprintf(stdout, "master key written to %s\nkey id: %s\nBack it up separately from database backups; losing it loses provider tokens and encrypted documents.\n", *out, id)
	}
	for _, s := range sidecars {
		if s.SecretFile == "" {
			continue // set directly (development)
		}
		switch err := writeSidecarSecret(s.SecretFile); {
		case errors.Is(err, os.ErrExist):
		case err != nil:
			fmt.Fprintf(stderr, "init-secrets: sidecar %s: %v\n", s.Name, err)
			return 1
		default:
			fmt.Fprintf(stdout, "sidecar %s secret written to %s; mount it into that sidecar only\n", s.Name, s.SecretFile)
		}
	}
	return 0
}

// writeSidecarSecret creates a missing sidecar secret: 32 random bytes, hex, mode 0600.
func writeSidecarSecret(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the admin-configured secret path
	if err != nil {
		return err
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	_, err = fmt.Fprintln(f, hex.EncodeToString(b))
	return errors.Join(err, f.Close())
}

func defaultKeyPath() string {
	if p := os.Getenv("VITAMUX_MASTER_KEY_FILE"); p != "" {
		return p
	}
	dir := os.Getenv("VITAMUX_DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	return filepath.Join(dir, "master.key")
}
