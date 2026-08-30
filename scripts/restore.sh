#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 4 ]]; then
	printf 'usage: %s BACKUP_DIRECTORY RESTORE_DIRECTORY [--database-name RESTORE_DB]\n' "$0" >&2
	exit 2
fi

backup_root=$1
restore_root=$2
database_name=
if [[ $# -gt 2 ]]; then
	if [[ $# -ne 4 || "$3" != "--database-name" ]]; then
		printf 'usage: %s BACKUP_DIRECTORY RESTORE_DIRECTORY [--database-name RESTORE_DB]\n' "$0" >&2
		exit 2
	fi
	database_name=$4
fi

if [[ ! -d "$backup_root" || ! -f "$backup_root/backup-manifest.txt" || ! -f "$backup_root/postgres.sql" ]]; then
	printf 'backup is incomplete or missing required files: %s\n' "$backup_root" >&2
	exit 2
fi
if [[ -L "$backup_root" ]]; then
	printf 'backup directory must not be symlinked: %s\n' "$backup_root" >&2
	exit 1
fi

backup_root=$(cd -- "$backup_root" && pwd)
repo_root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
if [[ "$restore_root" != /* ]]; then
	restore_root="$PWD/$restore_root"
fi
restore_root=$(realpath -m -- "$restore_root")
if [[ "$restore_root" == "$repo_root" || "$restore_root" == "$repo_root"/* || "$restore_root" == / || "$restore_root" == /home || "$restore_root" == /tmp ]]; then
	printf 'restore destination must be a dedicated path outside the checkout: %s\n' "$restore_root" >&2
	exit 2
fi
if [[ -e "$restore_root" || -L "$restore_root" ]]; then
	printf 'refusing to restore into an existing path; choose a clean destination: %s\n' "$restore_root" >&2
	exit 2
fi
if [[ -n "$database_name" && ! "$database_name" =~ ^restore_[A-Za-z0-9_]+$ ]]; then
	printf 'restore database name must start with restore_ and contain only letters, digits, and underscores\n' >&2
	exit 2
fi

"$(dirname -- "$0")/backup-validate.sh" "$backup_root" >/dev/null
expected_dump=$(awk -F= '$1 == "postgres_dump_sha256" { print $2 }' "$backup_root/backup-manifest.txt")
actual_dump=$(sha256sum "$backup_root/postgres.sql" | awk '{print $1}')
if [[ -z "$expected_dump" || "$expected_dump" != "$actual_dump" ]]; then
	printf 'PostgreSQL dump hash mismatch: expected %s got %s\n' "${expected_dump:-missing}" "$actual_dump" >&2
	exit 1
fi

umask 077
restore_parent=$(dirname -- "$restore_root")
mkdir -p -- "$restore_parent"
staging_root=$(mktemp -d "$restore_parent/.invs-restore.XXXXXX")
cleanup() {
	rm -rf -- "$staging_root"
}
trap cleanup EXIT
while IFS=$'\t' read -r marker relative expected_size expected_sha256; do
	[[ "$marker" == "file" ]] || continue
	if [[ "$relative" == *'..'* || "$relative" == /* ]]; then
		printf 'unsafe backup manifest path: %s\n' "$relative" >&2
		exit 1
	fi
	case "$relative" in
		immutable/raw/*) destination="$staging_root/data/raw/${relative#immutable/raw/}" ;;
		immutable/normalized/*) destination="$staging_root/data/normalized/${relative#immutable/normalized/}" ;;
		immutable/features/*) destination="$staging_root/data/features/${relative#immutable/features/}" ;;
		immutable/research/*) destination="$staging_root/data/research/${relative#immutable/research/}" ;;
		*) printf 'unsupported backup manifest path: %s\n' "$relative" >&2; exit 1 ;;
	esac
	source="$backup_root/$relative"
	if [[ ! -f "$source" || -L "$source" ]]; then
		printf 'backup manifest file is missing or symlinked: %s\n' "$source" >&2
		exit 1
	fi
	actual_size=$(stat -c '%s' "$source")
	actual_sha256=$(sha256sum "$source" | awk '{print $1}')
	if [[ "$actual_size" != "$expected_size" || "$actual_sha256" != "$expected_sha256" ]]; then
		printf 'backup file integrity mismatch: %s\n' "$relative" >&2
		exit 1
	fi
	mkdir -p "$(dirname "$destination")"
	cp -p -- "$source" "$destination"
	if [[ "$(stat -c '%s' "$destination")" != "$expected_size" || "$(sha256sum "$destination" | awk '{print $1}')" != "$expected_sha256" ]]; then
		printf 'restored file integrity mismatch: %s\n' "$destination" >&2
		exit 1
	fi
done < "$backup_root/backup-manifest.txt"

cp -p -- "$backup_root/backup-manifest.txt" "$staging_root/backup-manifest.txt"

if [[ -n "$database_name" ]]; then
	# Only a freshly named restore_* database is accepted. The existing
	# application database is never dropped or overwritten by this script.
	docker compose up -d --wait postgres >/dev/null
	docker compose exec -T postgres sh -c \
		'test "$1" != "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" -- "$1"' \
		sh "$database_name"
	docker compose exec -T postgres sh -c \
		'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$1"' \
		sh "$database_name" < "$backup_root/postgres.sql"
	printf 'PostgreSQL restored to temporary database %s\n' "$database_name"
fi

if [[ -e "$restore_root" || -L "$restore_root" ]]; then
	printf 'refusing to overwrite restore destination created during restore: %s\n' "$restore_root" >&2
	exit 2
fi
mv -- "$staging_root" "$restore_root"
trap - EXIT

printf 'restore verified at %s\n' "$restore_root"
