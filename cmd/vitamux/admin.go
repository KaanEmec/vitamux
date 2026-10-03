package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

const adminUsage = `usage: vitamux admin <command>

Commands:
  init-secrets [--out PATH]   generate the master key file (default: VITAMUX_MASTER_KEY_FILE, else <data dir>/master.key)
`

func admin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "init-secrets" {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	return initSecrets(args[1:], stdout, stderr)
}

func initSecrets(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("admin init-secrets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", defaultKeyPath(), "master key file to create")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	id, err := crypto.WriteKeyFile(*out)
	if errors.Is(err, os.ErrExist) {
		fmt.Fprintf(stderr, "init-secrets: %s already exists; refusing to overwrite the master key\n", *out)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "init-secrets: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "master key written to %s\nkey id: %s\nBack it up separately from database backups; losing it loses provider tokens and encrypted documents.\n", *out, id)
	return 0
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
