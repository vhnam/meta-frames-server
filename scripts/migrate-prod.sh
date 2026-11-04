#!/usr/bin/env bash
# Shows or applies goose migrations on the production database (Neon).
#
#   scripts/migrate-prod.sh status        list applied and pending migrations (default)
#   scripts/migrate-prod.sh up [--yes]    apply pending migrations, after a confirmation
#
# DATABASE_URL comes from .env.prod, or from the file named by ENV_FILE.
set -euo pipefail

cd "$(dirname "$0")/.."

command_name="${1:-status}"
confirmed="${2:-}"
env_file="${ENV_FILE:-.env.prod}"

fail() {
  echo "error: $1" >&2
  exit 1
}

case "$command_name" in
  status | up) ;;
  *) fail "usage: scripts/migrate-prod.sh status | up [--yes]" ;;
esac

[ -f "$env_file" ] || fail "$env_file is missing"

# The value is read literally: sourcing the file would expand $ in the password.
database_url="$(awk '/^DATABASE_URL=/{sub(/^[^=]*=/,""); sub(/[[:space:]]+#.*/,""); print; exit}' "$env_file")"
[ -n "$database_url" ] || fail "DATABASE_URL is empty in $env_file"

# Passed through the environment so the password stays out of the process list.
export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING="$database_url"

goose_version="$(go list -m -f '{{.Version}}' github.com/pressly/goose/v3)"

run_goose() {
  go run "github.com/pressly/goose/v3/cmd/goose@$goose_version" -dir migrations "$@"
}

if [ "$command_name" = "status" ]; then
  run_goose status
  exit 0
fi

# A migration applied from an uncommitted file cannot be traced to a commit.
if [ -n "$(git status --porcelain -- migrations)" ]; then
  fail "migrations/ has uncommitted changes; commit them first"
fi

run_goose status

if [ "$confirmed" != "--yes" ]; then
  printf 'Apply pending migrations to the database in %s? Type "yes": ' "$env_file"
  read -r answer
  [ "$answer" = "yes" ] || fail "cancelled"
fi

run_goose up
run_goose version
