#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
user_config_root="${XDG_CONFIG_HOME:-$(getent passwd "$(id -u)" | cut -d: -f6)/.config}"
unit_dir="$user_config_root/systemd/user"
make_path=$(command -v make)
mkdir -p "$unit_dir"

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
ExecStart=$make_path market-cycle
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
