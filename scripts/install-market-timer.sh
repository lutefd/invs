#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
user_config_root="${XDG_CONFIG_HOME:-$(getent passwd "$(id -u)" | cut -d: -f6)/.config}"
unit_dir="$user_config_root/systemd/user"
make_path=$(command -v make || true)
if [[ -z "$make_path" || ! -x "$make_path" || "$make_path" != /* ]]; then
	echo "market-cycle timer requires an executable absolute GNU Make path" >&2
	exit 127
fi
docker_group=docker
sg_path=$(command -v sg || true)
if [[ -z "$sg_path" || ! -x "$sg_path" || "$sg_path" != /* ]]; then
	echo "market-cycle timer requires an executable absolute sg tool for group $docker_group" >&2
	exit 127
fi
if ! getent group "$docker_group" >/dev/null 2>&1; then
	echo "market-cycle timer requires the $docker_group group to exist" >&2
	exit 1
fi
if ! id -nG | tr '[:space:]' '\n' | grep -Fxq "$docker_group"; then
	echo "market-cycle timer requires the current user to belong to the $docker_group group" >&2
	exit 1
fi
make_dir=$(dirname -- "$make_path")
service_environment_path="${PATH:-/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin}"
service_environment_path="$make_dir:$service_environment_path"

escape_systemd_environment() {
	local value=$1
	value=${value//\\/\\\\}
	value=${value//\"/\\\"}
	value=${value//$'\n'/\\n}
	value=${value//$'\t'/\\t}
	printf '%s' "$value"
}

make_path_unit=$(escape_systemd_environment "$make_path")
service_path_unit=$(escape_systemd_environment "$service_environment_path")
mkdir -p "$unit_dir"

# Ensure unattended runs use the current operator and catalog binaries before
# the timer is enabled. Later code upgrades should reinstall the timer.
docker compose build collector jupyter

service_path="$unit_dir/invs-market-cycle.service"
timer_path="$unit_dir/invs-market-cycle.timer"
temporary_service="$service_path.tmp"
temporary_timer="$timer_path.tmp"

cat >"$temporary_service" <<EOF
[Unit]
Description=INVS Nasdaq starter market collection, features, and paper cycle
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=$repo_root
ExecStart=$sg_path $docker_group -c "$make_path market-cycle"
# User services cannot add SupplementaryGroups reliably; sg switches to the
# already-authorized docker group before running the absolute Make command.
# market-cycle uses this same path for nested Make targets.
Environment="INVS_MAKE_PATH=$make_path_unit"
Environment="PATH=$service_path_unit"
TimeoutStartSec=2h
EOF

cat >"$temporary_timer" <<'EOF'
[Unit]
Description=Run INVS market cycle after the US close

[Timer]
OnCalendar=Mon..Fri *-*-* 18:30:00 America/Sao_Paulo
Persistent=true
Unit=invs-market-cycle.service

[Install]
WantedBy=timers.target
EOF

mv "$temporary_service" "$service_path"
mv "$temporary_timer" "$timer_path"
systemctl --user daemon-reload
systemctl --user enable --now invs-market-cycle.timer
systemctl --user status invs-market-cycle.timer --no-pager
