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
if [[ -L "$backup_root" || -L "$manifest" || -L "$dump" ]]; then
	printf 'backup root and required files must not be symlinked: %s\n' "$backup_root" >&2
	exit 1
fi
backup_mode=$(stat -c '%a' -- "$backup_root")
if (( (8#$backup_mode & 077) != 0 )); then
	printf 'backup directory is group or world accessible: %s\n' "$backup_root" >&2
	exit 1
fi

while IFS= read -r -d '' symlink; do
	printf 'backup contains a symlink: %s\n' "$symlink" >&2
	exit 1
done < <(find "$backup_root" -type l -print0)

while IFS= read -r top_level; do
	case "$top_level" in
		immutable|postgres.sql|backup-manifest.txt) ;;
		*) printf 'backup contains an unsupported top-level entry: %s\n' "$top_level" >&2; exit 1 ;;
	esac
done < <(find "$backup_root" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)

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

for key in version created_at git_commit config_path config_status config_sha256 postgres_dump_sha256; do
	if [[ $(awk -F= -v key="$key" '$1 == key { count++ } END { print count + 0 }' "$manifest") != 1 ]]; then
		printf 'backup manifest must contain exactly one %s field\n' "$key" >&2
		exit 1
	fi
done
if [[ $(awk -F= '$1 == "version" { print $2 }' "$manifest") != 1 ]]; then
	printf 'unsupported backup manifest version\n' >&2
	exit 1
fi
config_status=$(awk -F= '$1 == "config_status" { print $2 }' "$manifest")
config_sha256=$(awk -F= '$1 == "config_sha256" { print $2 }' "$manifest")
if [[ "$config_status" == present && ! "$config_sha256" =~ $sha256_pattern ]]; then
	printf 'present config must have a valid SHA-256\n' >&2
	exit 1
fi
if [[ "$config_status" == missing && "$config_sha256" != missing ]]; then
	printf 'missing config must use config_sha256=missing\n' >&2
	exit 1
fi
if [[ "$config_status" != present && "$config_status" != missing ]]; then
	printf 'unsupported config status in backup manifest\n' >&2
	exit 1
fi

file_count=0
declare -A listed_files=()
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
	if [[ -n "${listed_files[$relative]+present}" ]]; then
		printf 'duplicate backup manifest file record: %s\n' "$relative" >&2
		exit 1
	fi
	listed_files["$relative"]=1
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

while IFS= read -r -d '' source; do
	relative=${source#"$backup_root/"}
	if [[ -z "${listed_files[$relative]+present}" ]]; then
		printf 'backup contains an unlisted immutable file: %s\n' "$relative" >&2
		exit 1
	fi
done < <(find "$backup_root/immutable" -type f -print0)

printf 'backup validated at %s\n' "$backup_root"
printf 'immutable files: %s\n' "$file_count"
printf 'postgres dump sha256: %s\n' "$actual_dump"
