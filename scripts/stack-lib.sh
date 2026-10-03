# Shared by e2e-stack.sh and redaction-audit.sh (source it): a real `vitamux serve` on a
# throwaway database. Needs VITAMUX_DATABASE_URL (superuser, as in .env.example and CI), psql,
# curl and go. Set `db` and `addr` first, then: stack_setup [BUILD_TAGS]; stack_owner USER PASS;
# stack_serve. Every CLI and server line lands in $work/*.log; the EXIT trap cleans up.

stack_setup() {
	: "${VITAMUX_DATABASE_URL:?set VITAMUX_DATABASE_URL (see .env.example)}"
	admin=$VITAMUX_DATABASE_URL
	url=$(printf %s "$admin" | sed -E "s#(://[^/]+)/[^?]*#\1/$db#")
	work=$(mktemp -d)
	server=
	trap stack_cleanup EXIT

	psql "$admin" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db"
	psql "$url" -v ON_ERROR_STOP=1 -q -f deploy/sql/roles.sql
	CGO_ENABLED=0 go build ${1:+-tags "$1"} -o "$work/vitamux" ./cmd/vitamux

	export VITAMUX_ENV=development VITAMUX_DATABASE_URL=$url VITAMUX_DATABASE_URL_FILE= \
		VITAMUX_HTTP_ADDR=$addr VITAMUX_PUBLIC_URL=http://$addr VITAMUX_DATA_DIR=$work/data \
		VITAMUX_MASTER_KEY_FILE=$work/data/master.key VITAMUX_LOG_LEVEL=warn
	stack_cli migrate up
	stack_cli admin init-secrets
}

# stack_cli runs a vitamux command with its output appended to $work/cli.log.
stack_cli() {
	"$work/vitamux" "$@" >>"$work/cli.log" 2>&1 || { echo "vitamux $1 failed:"; cat "$work/cli.log"; return 1; }
}

stack_owner() { printf '%s\n%s\n' "$1" "$2" | stack_cli admin create-owner; }

stack_serve() {
	"$work/vitamux" serve 2>"$work/server.log" &
	server=$!
	for _ in $(seq 60); do curl -fs "http://$addr/readyz" >/dev/null && return 0; sleep 0.5; done
	echo "server not ready:"
	cat "$work/server.log"
	return 1
}

# stack_stop sends SIGTERM and waits, so shutdown lines reach the log too.
stack_stop() {
	[ -z "$server" ] || { kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true; }
	server=
}

stack_cleanup() {
	stack_stop
	psql "$admin" -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
	rm -rf "$work"
}
