#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
	printf 'usage: %s BACKUP_DIRECTORY\n' "$0" >&2
	exit 2
fi

backup_root=$1
data_root=${INVS_DATA_DIR:-${INVS_DATA_ROOT:-data}}
config_file=${INVS_CONFIG_FILE:-config/config.local.yaml}
umask 077

if [[ -e "$backup_root" ]]; then
	printf 'refusing to overwrite existing backup directory: %s\n' "$backup_root" >&2
	exit 2
fi

mkdir -p "$backup_root/immutable/raw" "$backup_root/immutable/normalized" "$backup_root/immutable/features"
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
git_commit=$(git rev-parse --verify HEAD 2>/dev/null || printf 'unknown')

copy_layer() {
	local layer=$1
	local source="$data_root/$layer"
	local destination="$backup_root/immutable/$layer"
	if [[ -d "$source" ]]; then
		cp -a "$source/." "$destination/"
	fi
}

copy_layer raw
copy_layer normalized
copy_layer features

# The dump is emitted by the running PostgreSQL service so credentials never
# need to be sourced into this shell or printed in the backup metadata.
docker compose up -d --wait postgres >/dev/null
docker compose exec -T postgres sh -c \
	'pg_dump --format=plain --no-owner --no-privileges --file=- -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
	> "$backup_root/postgres.sql"

if [[ -f "$config_file" ]]; then
	config_sha256=$(sha256sum "$config_file" | awk '{print $1}')
	config_status=present
else
	config_sha256=missing
	config_status=missing
fi

postgres_sha256=$(sha256sum "$backup_root/postgres.sql" | awk '{print $1}')
{
	printf 'version=1\n'
	printf 'created_at=%s\n' "$created_at"
	printf 'git_commit=%s\n' "$git_commit"
	printf 'config_path=%s\n' "$config_file"
	printf 'config_status=%s\n' "$config_status"
	printf 'config_sha256=%s\n' "$config_sha256"
	printf 'postgres_dump_sha256=%s\n' "$postgres_sha256"
	find "$backup_root/immutable" -type f -print0 | sort -z | while IFS= read -r -d '' file; do
		relative=${file#"$backup_root/"}
		size=$(stat -c '%s' "$file")
		sha256=$(sha256sum "$file" | awk '{print $1}')
		printf 'file\t%s\t%s\t%s\n' "$relative" "$size" "$sha256"
	done
} > "$backup_root/backup-manifest.txt"

printf 'backup created at %s\n' "$backup_root"
printf 'immutable files: %s\n' "$(find "$backup_root/immutable" -type f | wc -l | tr -d ' ')"
printf 'postgres dump sha256: %s\n' "$postgres_sha256"
