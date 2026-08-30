#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
	printf 'usage: %s BACKUP_DIRECTORY\n' "$0" >&2
	exit 2
fi

backup_root=$1
manifest="$backup_root/backup-manifest.txt"
dump="$backup_root/postgres.sql"
if [[ ! -d "$backup_root" || ! -f "$manifest" || ! -f "$dump" ]]; then
	printf 'backup is incomplete or missing required files: %s\n' "$backup_root" >&2
	exit 2
fi

sha256_pattern='^[0-9a-f]{64}$'
expected_dump=$(awk -F= '$1 == "postgres_dump_sha256" { print $2 }' "$manifest")
if [[ ! "$expected_dump" =~ $sha256_pattern ]]; then
	printf 'backup manifest has no valid PostgreSQL dump hash: %s\n' "$backup_root" >&2
	exit 1
fi
actual_dump=$(sha256sum "$dump" | awk '{print $1}')
if [[ "$expected_dump" != "$actual_dump" ]]; then
	printf 'PostgreSQL dump hash mismatch: expected %s got %s\n' "$expected_dump" "$actual_dump" >&2
	exit 1
fi

file_count=0
while IFS=$'\t' read -r marker relative expected_size expected_sha256 extra; do
	[[ "$marker" == "file" ]] || continue
	if [[ -n "$extra" || -z "$relative" || ! "$expected_size" =~ ^[0-9]+$ || ! "$expected_sha256" =~ $sha256_pattern ]]; then
		printf 'invalid backup file record\n' >&2
		exit 1
	fi
	case "$relative" in
		immutable/raw/*|immutable/normalized/*|immutable/features/*|immutable/research/*) ;;
		*) printf 'unsupported backup manifest path: %s\n' "$relative" >&2; exit 1 ;;
	esac
	if [[ "$relative" == *'..'* || "$relative" == /* ]]; then
		printf 'unsafe backup manifest path: %s\n' "$relative" >&2
		exit 1
	fi
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
	file_count=$((file_count + 1))
done < "$manifest"

printf 'backup validated at %s\n' "$backup_root"
printf 'immutable files: %s\n' "$file_count"
printf 'postgres dump sha256: %s\n' "$actual_dump"
