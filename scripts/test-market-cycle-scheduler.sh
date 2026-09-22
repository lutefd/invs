#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/invs-market-cycle-scheduler.XXXXXX")
paper_output_host=""
paper_output_created=0
cleanup() {
	rm -rf -- "$test_root"
	if [[ "$paper_output_created" == 1 ]]; then
		rm -rf -- "$paper_output_host"
	fi
}
trap cleanup EXIT

make_root="$test_root/make-bin"
tool_root="$test_root/tools"
runtime_bin="$test_root/runtime-bin"
config_root="$test_root/xdg"
make_log="$test_root/make.log"
docker_log="$test_root/docker.log"
systemctl_log="$test_root/systemctl.log"
sg_log="$test_root/sg.log"
mkdir -p "$make_root" "$tool_root" "$runtime_bin" "$config_root"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'printf "%s\\n" "$*" >> "${INVS_SCHEDULER_TEST_MAKE_LOG:?}"' \
'if [[ -n "${INVS_SCHEDULER_TEST_EXPECTED_PAPER_CLOCK:-}" ]] && [[ " $* " == *" paper-validate-inputs "* || " $* " == *" paper-run "* ]]; then' \
'    case " $* " in' \
'        *"PAPER_DECISION_AT=${INVS_SCHEDULER_TEST_EXPECTED_PAPER_CLOCK}"*) ;;' \
'        *) echo "unexpected paper decision clock: $*" >&2; exit 1 ;;' \
'    esac' \
'fi' \
'if [[ " $* " == *" feature-batch "* ]]; then' \
'    printf "%s\\n" '\''{"manifest_path":"/data/research/features/market-momentum/test.json"}'\''' \
'fi' \
> "$make_root/make"
chmod 0755 "$make_root/make"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'printf "%s\\n" "$*" >> "${INVS_SCHEDULER_TEST_DOCKER_LOG:?}"' \
'if [[ " $* " == *" exec "* ]]; then' \
'    printf "%s\\n" '\''{"manifest":{"data_source_id":"test-source","mic":"XNAS","calendar_version":"test-calendar","session_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","available_at":"2026-09-18T00:00:00Z"},"sessions":[]}'\''' \
'elif [[ " $* " == *" run "* ]]; then' \
'    if [[ "${INVS_SCHEDULER_TEST_PAPER_READY:-0}" == 1 ]]; then' \
'        paper_output="${INVS_SCHEDULER_TEST_PAPER_OUTPUT_CONTAINER:?}"' \
'        printf '\''{"status":"paper_ready","session_date":"2026-09-18","feature":{"universe":"/data/research/forward/test-scheduler/universe.json","schedule":"/data/research/forward/test-scheduler/schedule.json","calendar_pin":"/data/research/forward/test-scheduler/calendar.json","security_mappings":"/data/research/forward/test-scheduler/mappings.json"},"paper":{"account_id":"test-paper-account","spec":"%s/ledger/generated-account.json","inputs":"%s/ledger/session-inputs.json","ledger_root":"%s/ledger"}}\n'\'' "$paper_output" "$paper_output" "$paper_output"' \
'    else' \
'        printf "%s\\n" '\''{"status":"awaiting_forward_session","session_date":"2026-09-18","feature":{"universe":"/data/research/forward/test-scheduler/universe.json","schedule":"/data/research/forward/test-scheduler/schedule.json","calendar_pin":"/data/research/forward/test-scheduler/calendar.json","security_mappings":"/data/research/forward/test-scheduler/mappings.json"},"paper":null}'\''' \
'    fi' \
'fi' \
> "$tool_root/docker"
chmod 0755 "$tool_root/docker"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'printf "%s\\n" "$*" >> "${INVS_SCHEDULER_TEST_SYSTEMCTL_LOG:?}"' \
> "$tool_root/systemctl"
chmod 0755 "$tool_root/systemctl"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'printf "%s\\n" "$*" >> "${INVS_SCHEDULER_TEST_SG_LOG:?}"' \
'test "$#" -eq 3' \
'test "$1" = docker' \
'test "$2" = -c' \
'/usr/bin/bash -c "$3"' \
> "$tool_root/sg"
chmod 0755 "$tool_root/sg"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'if [[ "$1" == group && "$2" == docker ]]; then' \
'    if [[ "${INVS_SCHEDULER_TEST_NO_DOCKER_GROUP:-0}" == 1 ]]; then exit 2; fi' \
'    printf "%s\\n" '\''docker:x:983:luis'\''' \
'    exit 0' \
'fi' \
'exit 2' \
> "$tool_root/getent"
chmod 0755 "$tool_root/getent"

printf '%s\n' \
'#!/usr/bin/env bash' \
'set -euo pipefail' \
'if [[ "$1" == -nG ]]; then' \
'    printf "%s\\n" '\''luis docker'\''' \
'    exit 0' \
'fi' \
'exit 2' \
> "$tool_root/id"
chmod 0755 "$tool_root/id"

export INVS_SCHEDULER_TEST_MAKE_LOG="$make_log"
export INVS_SCHEDULER_TEST_DOCKER_LOG="$docker_log"
export INVS_SCHEDULER_TEST_SYSTEMCTL_LOG="$systemctl_log"
export INVS_SCHEDULER_TEST_SG_LOG="$sg_log"
export XDG_CONFIG_HOME="$config_root"
export PATH="$make_root:$tool_root:/usr/bin:/bin"

scripts/install-market-timer.sh > "$test_root/install.log"

service_path="$config_root/systemd/user/invs-market-cycle.service"
timer_path="$config_root/systemd/user/invs-market-cycle.timer"
make_path="$make_root/make"
sg_path="$tool_root/sg"
test -s "$service_path"
test -s "$timer_path"
grep -Fq -- "ExecStart=$sg_path docker -c \"$make_path market-cycle\"" "$service_path"
grep -Fq -- "Environment=\"INVS_MAKE_PATH=$make_path\"" "$service_path"
grep -Fq -- "Environment=\"PATH=$make_root:" "$service_path"
grep -Fq -- 'enable --now invs-market-cycle.timer' "$systemctl_log"
if command -v systemd-analyze >/dev/null 2>&1; then
	systemd-analyze verify "$service_path" "$timer_path" >/dev/null 2>&1
fi

"$sg_path" docker -c "$make_path market-cycle"
grep -Fq -- 'docker -c '"$make_path market-cycle"'' "$sg_log"
grep -Fq -- 'market-cycle' "$make_log"

no_sg_root="$test_root/no-sg-bin"
no_sg_config="$test_root/no-sg-xdg"
no_sg_log="$test_root/no-sg-install.log"
mkdir -p "$no_sg_root"
for command_name in bash dirname make; do
	command_path=$(command -v "$command_name")
	ln -s "$command_path" "$no_sg_root/$command_name"
done
if XDG_CONFIG_HOME="$no_sg_config" PATH="$no_sg_root" scripts/install-market-timer.sh > "$no_sg_log" 2>&1; then
	echo 'installer unexpectedly accepted a missing sg tool' >&2
	exit 1
else
	no_sg_exit=$?
fi
test "$no_sg_exit" -eq 127
grep -Fq -- 'requires an executable absolute sg tool' "$no_sg_log"
test ! -e "$no_sg_config/systemd/user/invs-market-cycle.service"

no_group_config="$test_root/no-group-xdg"
no_group_log="$test_root/no-group-install.log"
if XDG_CONFIG_HOME="$no_group_config" INVS_SCHEDULER_TEST_NO_DOCKER_GROUP=1 PATH="$make_root:$tool_root:/usr/bin:/bin" scripts/install-market-timer.sh > "$no_group_log" 2>&1; then
	echo 'installer unexpectedly accepted a missing docker group' >&2
	exit 1
else
	no_group_exit=$?
fi
test "$no_group_exit" -eq 1
grep -Fq -- 'requires the docker group to exist' "$no_group_log"
test ! -e "$no_group_config/systemd/user/invs-market-cycle.service"

for command_name in bash date dirname flock git jq mkdir tee tr; do
	command_path=$(command -v "$command_name")
	ln -s "$command_path" "$runtime_bin/$command_name"
done
ln -s "$tool_root/docker" "$runtime_bin/docker"
test ! -e "$runtime_bin/make"

INVS_MAKE_PATH="$make_path" \
INVS_MARKET_RUNTIME_DIR="$test_root/runtime" \
INVS_MARKET_LOG_ROOT="$test_root/logs" \
INVS_MARKET_OUTPUT_ROOT=data/research/forward/test-scheduler \
PATH="$runtime_bin" \
/usr/bin/bash scripts/market-cycle.sh > "$test_root/market-cycle.log"

grep -Fq -- 'market_cycle_status=ok' "$test_root/market-cycle.log"
grep -Fq -- 'ingest' "$make_log"
grep -Fq -- 'reconcile' "$make_log"
grep -Fq -- 'feature-batch' "$make_log"
grep -Fq -- 'discovery-run' "$make_log"

paper_output_host="$repo_root/data/research/forward/.market-cycle-scheduler-${test_root##*.}"
test ! -e "$paper_output_host"
mkdir -p "$paper_output_host/ledger/accounts/test-paper-account/reports"
paper_output_created=1
printf '%s\n' '{"account_id":"test-paper-account"}' \
    > "$paper_output_host/ledger/accounts/test-paper-account/account.json"
printf '%s\n' '{"risk":{"checked_at":"2026-09-18T21:05:00Z"}}' \
    > "$paper_output_host/ledger/accounts/test-paper-account/reports/report-2026-09-18.json"

INVS_SCHEDULER_TEST_EXPECTED_PAPER_CLOCK=2026-09-18T21:05:00Z \
INVS_SCHEDULER_TEST_PAPER_OUTPUT_CONTAINER="/data/research/forward/.market-cycle-scheduler-${test_root##*.}" \
INVS_SCHEDULER_TEST_PAPER_READY=1 \
INVS_MAKE_PATH="$make_path" \
INVS_MARKET_RUNTIME_DIR="$test_root/runtime-paper" \
INVS_MARKET_LOG_ROOT="$test_root/logs-paper" \
INVS_MARKET_OUTPUT_ROOT="data/research/forward/.market-cycle-scheduler-${test_root##*.}" \
PATH="$runtime_bin" \
/usr/bin/bash scripts/market-cycle.sh > "$test_root/market-cycle-paper-retry.log"

grep -Fq -- 'paper_status=existing_report session_date=2026-09-18 decision_at=2026-09-18T21:05:00Z' \
    "$test_root/market-cycle-paper-retry.log"
grep -Fq -- 'PAPER_DECISION_AT=2026-09-18T21:05:00Z' "$make_log"

printf '%s\n' 'market-cycle scheduler environment acceptance passed'
