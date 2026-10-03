#!/bin/sh
# Creates ./secrets (next to this script) with random database passwords and the connection
# URLs that embed them. Safe to re-run: existing files are never overwritten.
# The master key is separate: `docker compose --profile setup run --rm init-secrets`.
set -eu
cd "$(dirname "$0")"
umask 077
mkdir -p secrets
chmod 700 secrets

rand() { od -An -tx1 -N24 /dev/urandom | tr -d ' \n'; }
# Container users (postgres, uid 65532) cannot own host files, so files are 0444; the 0700
# directory is what keeps other host users out.
put() { # put NAME VALUE
	[ -e "secrets/$1" ] && return 0
	printf '%s\n' "$2" >"secrets/$1"
	chmod 444 "secrets/$1"
	echo "created secrets/$1"
}

if [ ! -e secrets/pg_app_password ]; then # the passwords and the URLs embedding them are created together
	app_pw=$(rand)
	owner_pw=$(rand)
	put pg_superuser_password "$(rand)"
	put pg_app_password "$app_pw"
	put pg_owner_password "$owner_pw"
	put database_url "postgres://vitamux_app:${app_pw}@postgres:5432/vitamux?sslmode=disable"
	put migrate_database_url "postgres://vitamux_owner:${owner_pw}@postgres:5432/vitamux?sslmode=disable"
fi
# Paste your own Withings application secret here (docs/providers/withings.md); empty is fine otherwise.
[ -e secrets/withings_client_secret ] || { : >secrets/withings_client_secret; chmod 444 secrets/withings_client_secret; }
