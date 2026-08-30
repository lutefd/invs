#!/usr/bin/env bash
set -euo pipefail

repo_root=${INVS_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
repo_root=$(cd "$repo_root" && pwd)
cd "$repo_root"

issues=0

issue() {
	printf 'security_issue=%s\n' "$1"
	issues=1
}

if [[ ! -f docker-compose.yml ]]; then
	issue 'docker-compose.yml is missing'
else
	bind_defaults=$(grep -Fc '${INVS_BIND_ADDRESS:-127.0.0.1}' docker-compose.yml || true)
	if (( bind_defaults < 3 )); then
		issue 'docker-compose.yml does not keep all host services loopback by default'
	fi
	if grep -Eq '^[[:space:]]*-[[:space:]]*"?0\.0\.0\.0:' docker-compose.yml; then
		issue 'docker-compose.yml contains an explicit public host binding'
	fi
fi

compose_json=
if compose_json=$(docker compose config --format json 2>/dev/null); then
	declare -A seen_services=()
	while IFS=$'\t' read -r service host_ip; do
		[[ -n "$service" ]] || continue
		seen_services["$service"]=1
		if [[ "$host_ip" != '127.0.0.1' ]]; then
			issue "effective_${service}_bind_is_${host_ip:-unset}"
		fi
	done < <(
		jq -r '
			.services
			| to_entries[]
			| select(.key == "postgres" or .key == "jupyter" or .key == "grafana")
			| .key as $service
			| (.value.ports // [])[]
			| [$service, (.host_ip // "")] | @tsv
		' <<<"$compose_json"
	)
	for service in postgres jupyter grafana; do
		if [[ -z "${seen_services[$service]+present}" ]]; then
			issue "${service}_has_no_host_port_contract"
		fi
	done
	grafana_anonymous=$(jq -r '.services.grafana.environment.GF_AUTH_ANONYMOUS_ENABLED // ""' <<<"$compose_json")
	if [[ "$grafana_anonymous" != 'false' ]]; then
		issue 'grafana_anonymous_access_is_not_disabled'
	fi
	grafana_signup=$(jq -r '.services.grafana.environment.GF_USERS_ALLOW_SIGN_UP // ""' <<<"$compose_json")
	if [[ "$grafana_signup" != 'false' ]]; then
		issue 'grafana_user_signup_is_not_disabled'
	fi
else
	issue 'docker compose configuration could not be rendered'
fi

check_local_secret_file() {
	local path=$1
	if [[ ! -e "$path" ]]; then
		return
	fi
	if [[ ! -f "$path" || -L "$path" ]]; then
		issue "local_secret_path_is_not_a_regular_file:${path#"$repo_root/"}"
		return
	fi
	local mode
	mode=$(stat -c '%a' -- "$path")
	if (( (8#$mode & 077) != 0 )); then
		issue "local_secret_file_is_group_or_world_accessible:${path#"$repo_root/"}"
	fi
	if git ls-files --error-unmatch -- "$path" >/dev/null 2>&1; then
		issue "local_secret_file_is_tracked:${path#"$repo_root/"}"
	fi
}

check_local_secret_file "$repo_root/.env"
check_local_secret_file "$repo_root/config/config.local.yaml"

if [[ -f .env ]]; then
	configured_path=$(awk -F= '$1 == "INVS_CONFIG_FILE" { print $2 }' .env | tail -n 1)
	if [[ -n "$configured_path" ]]; then
		configured_path=${configured_path#./}
		case "$configured_path" in
			/*) resolved_config=$configured_path ;;
			*) resolved_config="$repo_root/$configured_path" ;;
		esac
		check_local_secret_file "$resolved_config"
	fi
fi

artifact_files=()
while IFS= read -r -d '' path; do
	artifact_files+=("$path")
done < <(git ls-files -z -- docs release python/notebooks)
if [[ -d logs ]]; then
	while IFS= read -r -d '' path; do
		artifact_files+=("$path")
	done < <(find logs -type f -print0)
fi

secret_pattern='(AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|-----BEGIN[[:space:]]+([A-Z0-9]+[[:space:]]+)?PRIVATE KEY-----|gh[pousr]_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{20,}|Bearer[[:space:]]+[A-Za-z0-9._~+/=-]{20,}|FRED_API_KEY=[^$<{[:space:]][^[:space:]]{15,}|JUPYTER_TOKEN=[^$<{[:space:]][^[:space:]]{15,}|DATABASE_URL=[^<[:space:]]+:[^<[:space:]]+@)'
email_pattern='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
for path in "${artifact_files[@]}"; do
	[[ -f "$path" && ! -L "$path" ]] || continue
	if grep -E -I -q -- "$secret_pattern" "$path"; then
		issue "secret_like_content:${path}"
	fi
	if grep -E -I -o -- "$email_pattern" "$path" | grep -E -I -v -q -- '@(example\.com|real\.test)$'; then
		issue "unapproved_contact_in_artifact:${path}"
	fi
done

if (( issues != 0 )); then
	printf 'security_status=failed\n'
	exit 1
fi

printf 'security_status=passed\n'
printf 'security_scope=loopback,authentication,local-secret-permissions,tracked-artifact-scan\n'
