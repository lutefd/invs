#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp_root=$(mktemp -d)
cleanup() {
	rm -rf -- "$tmp_root"
}
trap cleanup EXIT

backup_root="$tmp_root/backup"
mkdir -p "$backup_root/immutable/raw/source=fixture"
printf '%s\n' 'immutable test payload' > "$backup_root/immutable/raw/source=fixture/part.txt"
printf '%s\n' '-- PostgreSQL restore fixture' > "$backup_root/postgres.sql"

payload_size=$(stat -c '%s' "$backup_root/immutable/raw/source=fixture/part.txt")
payload_sha256=$(sha256sum "$backup_root/immutable/raw/source=fixture/part.txt" | awk '{print $1}')
dump_sha256=$(sha256sum "$backup_root/postgres.sql" | awk '{print $1}')
{
	printf 'version=1\n'
	printf 'created_at=2026-08-30T00:00:00Z\n'
	printf 'git_commit=fixture\n'
	printf 'config_path=config/config.local.yaml\n'
	printf 'config_status=missing\n'
	printf 'config_sha256=missing\n'
	printf 'postgres_dump_sha256=%s\n' "$dump_sha256"
	printf 'file\timmutable/raw/source=fixture/part.txt\t%s\t%s\n' "$payload_size" "$payload_sha256"
} > "$backup_root/backup-manifest.txt"
chmod 700 "$backup_root"
chmod 600 "$backup_root/postgres.sql" "$backup_root/backup-manifest.txt"
chmod 600 "$backup_root/immutable/raw/source=fixture/part.txt"

"$repo_root/scripts/backup-validate.sh" "$backup_root" >/dev/null
restore_root="$tmp_root/restored"
"$repo_root/scripts/restore.sh" "$backup_root" "$restore_root" >/dev/null
cmp -s "$backup_root/immutable/raw/source=fixture/part.txt" "$restore_root/data/raw/source=fixture/part.txt"
cmp -s "$backup_root/backup-manifest.txt" "$restore_root/backup-manifest.txt"

if "$repo_root/scripts/restore.sh" "$backup_root" "$restore_root" >/dev/null 2>&1; then
	printf '%s\n' 'restore unexpectedly overwrote an existing destination' >&2
	exit 1
fi

tampered_root="$tmp_root/tampered"
cp -a -- "$backup_root" "$tampered_root"
printf '%s\n' 'tampered' >> "$tampered_root/postgres.sql"
if "$repo_root/scripts/backup-validate.sh" "$tampered_root" >/dev/null 2>&1; then
	printf '%s\n' 'backup validator unexpectedly accepted a tampered dump' >&2
	exit 1
fi

printf '%s\n' 'backup and restore acceptance passed'
