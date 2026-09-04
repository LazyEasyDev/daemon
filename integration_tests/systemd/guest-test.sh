#!/usr/bin/env bash

set -Eeuo pipefail

phase=${1:-}
service_name=${2:-}
port=${3:-18080}
install_dir=${DAEMON_ITEST_INSTALL_DIR:-/opt/daemon-itest}
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
unit_name="lz_lz_${service_name}.service"
unit_path="/etc/systemd/system/$unit_name"
metadata_path="/var/lib/daemon-util/services/lz_lz_${service_name}.json"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[systemd-itest] %s\n' "$*"
}

fail() {
	printf '[systemd-itest] ERROR: %s\n' "$*" >&2
	return 1
}

assert_contains() {
	local value=$1
	local expected=$2
	local description=$3
	if [[ "$value" != *"$expected"* ]]; then
		fail "$description: expected output to contain '$expected', got '$value'"
	fi
}

assert_file_contains() {
	local path=$1
	local expected=$2
	if ! grep -Fq -- "$expected" "$path"; then
		fail "$path does not contain '$expected'"
	fi
}

process_is_test_app() {
	local pid=$1
	[[ -e "/proc/$pid/exe" ]] && [[ "$(readlink "/proc/$pid/exe" 2>/dev/null || true)" == "$app_bin" ]]
}

wait_process_gone() {
	local pid=$1
	local deadline=$((SECONDS + 15))
	while process_is_test_app "$pid"; do
		if (( SECONDS >= deadline )); then
			fail "test application process $pid is still running"
		fi
		sleep 0.2
	done
}

assert_no_test_app_processes() {
	local process executable
	for process in /proc/[0-9]*; do
		executable=$(readlink "$process/exe" 2>/dev/null || true)
		if [[ "$executable" == "$app_bin" ]]; then
			fail "test application process ${process##*/} leaked after cleanup"
		fi
	done
}

wait_for_http() {
	local expect_child=${1:-false}
	python3 - "$port" "$app_bin" "$expect_child" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

port = int(sys.argv[1])
expected_executable = sys.argv[2]
expect_child = sys.argv[3] == "true"
deadline = time.monotonic() + 30
last_error = None
while time.monotonic() < deadline:
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=2) as response:
            data = json.load(response)
        assert data["executable"] == expected_executable, data
        assert data["file_content"] == "daemon-util relative path test passed\n", data
        assert data["config"]["enabled"] is True, data
        assert data["config"]["message"] == "hello systemd", data
        assert data["config"]["count"] == 7, data
        assert data["config"]["port"] == port, data
        args = data["args"]
        assert "hello systemd" in args, args
        assert "relative-path-test.txt" in args, args
        if expect_child:
            assert data.get("child_pid", 0) > 0, data
        print(json.dumps(data, sort_keys=True))
        raise SystemExit(0)
    except (AssertionError, OSError, urllib.error.URLError, json.JSONDecodeError) as error:
        last_error = error
        time.sleep(0.2)
raise SystemExit(f"HTTP application did not become ready: {last_error}")
PY
}

http_pid() {
	python3 - "$port" <<'PY'
import json
import sys
import urllib.request
with urllib.request.urlopen(f"http://127.0.0.1:{sys.argv[1]}/", timeout=2) as response:
    print(json.load(response)["pid"])
PY
}

wait_for_new_http_pid() {
	local old_pid=$1
	local timeout_seconds=$2
	python3 - "$port" "$old_pid" "$timeout_seconds" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

port = int(sys.argv[1])
old_pid = int(sys.argv[2])
deadline = time.monotonic() + int(sys.argv[3])
last_error = None
while time.monotonic() < deadline:
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=1) as response:
            pid = int(json.load(response)["pid"])
        if pid != old_pid:
            print(pid)
            raise SystemExit(0)
    except (OSError, urllib.error.URLError, json.JSONDecodeError) as error:
        last_error = error
    time.sleep(0.2)
raise SystemExit(f"application did not restart from PID {old_pid}: {last_error}")
PY
}

assert_event() {
	local path=$1
	local expected=$2
	python3 - "$path" "$expected" <<'PY'
import json
import sys

path, expected = sys.argv[1:]
with open(path, encoding="utf-8") as events:
    records = [json.loads(line) for line in events if line.strip()]
if not any(record.get("event") == expected for record in records):
    raise SystemExit(f"event {expected!r} not found in {path}: {records!r}")
PY
}

event_count() {
	local path=$1
	local expected=$2
	python3 - "$path" "$expected" <<'PY'
import json
import sys

path, expected = sys.argv[1:]
with open(path, encoding="utf-8") as events:
    print(sum(json.loads(line).get("event") == expected for line in events if line.strip()))
PY
}

collect_artifacts() {
	local label=${1:-$current_scenario}
	mkdir -p "$artifact_dir"
	{
		printf 'phase=%s\nscenario=%s\nservice=%s\n' "$phase" "$current_scenario" "$service_name"
		uname -a
		printf '\nPID 1:\n'
		ps -p 1 -o pid=,comm=,args=
		printf '\nsystemd:\n'
		systemctl --version
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	systemctl status "$unit_name" --no-pager >"$artifact_dir/${label}-status.txt" 2>&1 || true
	systemctl show "$unit_name" --no-pager >"$artifact_dir/${label}-show.txt" 2>&1 || true
	systemctl cat "$unit_name" --no-pager >"$artifact_dir/${label}-unit.txt" 2>&1 || true
	journalctl -u "$unit_name" --no-pager >"$artifact_dir/${label}-journal.txt" 2>&1 || true
	ps -ef >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
	chmod -R a+rX "$artifact_dir" 2>/dev/null || true
}

cleanup_service() {
	if [[ -f "$unit_path" ]]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			systemctl disable --now "$unit_name" >/dev/null 2>&1 || true
			rm -f "$unit_path"
			systemctl daemon-reload >/dev/null 2>&1 || true
		}
	fi
	rm -f "$metadata_path"
}

on_exit() {
	local status=$?
	trap - EXIT
	if (( status != 0 )); then
		collect_artifacts failure
		cleanup_service
	fi
	exit "$status"
}
trap on_exit EXIT

require_environment() {
	[[ $(id -u) -eq 0 ]] || fail "guest test must run as root"
	[[ -x "$daemon_bin" ]] || fail "missing daemon binary at $daemon_bin"
	[[ -x "$app_bin" ]] || fail "missing test application at $app_bin"
	[[ -f "$fixture_path" ]] || fail "missing relative-path fixture at $fixture_path"
	[[ "$(ps -p 1 -o comm= | tr -d '[:space:]')" == systemd ]] || fail "systemd is not PID 1"
	command -v python3 >/dev/null || fail "python3 is required in the guest"
	mkdir -p "$state_dir" "$artifact_dir"
}

install_scenario() {
	local timeout=$1
	local events=$2
	shift 2
	rm -f "$events" "$install_dir/child.pid"
	"$daemon_bin" install --stop-timeout "$timeout" --ignore-warnings \
		"$service_name" "$app_bin" \
		--enabled=true \
		--message "hello systemd" \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[[ -f "$unit_path" ]] || fail "systemd unit was not created"
	assert_file_contains "$unit_path" 'Type=exec'
	assert_file_contains "$unit_path" "ExecStart=\"$app_bin\""
	assert_file_contains "$unit_path" "WorkingDirectory=$install_dir"
	assert_file_contains "$unit_path" 'Restart=on-failure'
	assert_file_contains "$unit_path" 'RestartPreventExitStatus=203'
	assert_file_contains "$unit_path" 'RestartSec=20s'
	assert_file_contains "$unit_path" 'KillMode=control-group'
	[[ "$(systemctl is-enabled "$unit_name")" == enabled ]] || fail "systemd unit is not enabled"
}

verify_management_commands() {
	local output
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" 'running' 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command application path'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello systemd' 'long list arguments'
}

pre_reboot() {
	local events="$install_dir/boot-events.jsonl"
	local child_pid
	current_scenario=pre-reboot
	log "installing boot-persistence scenario"
	cleanup_service
	install_scenario 5s "$events" \
		--stop_delay 1s \
		--spawn-child=true \
		--child-pid-path child.pid
	verify_definition
	assert_file_contains "$unit_path" 'TimeoutStopSec=5s'
	"$daemon_bin" start "$service_name"
	wait_for_http true >"$state_dir/pre-reboot-http.json"
	verify_management_commands
	child_pid=$(cat "$install_dir/child.pid")
	process_is_test_app "$child_pid" || fail "child process $child_pid is not running"
	http_pid >"$state_dir/pre-reboot-parent.pid"
	collect_artifacts pre-reboot
	log "pre-reboot checks passed"
}

post_reboot() {
	local boot_events="$install_dir/boot-events.jsonl"
	local restart_parent restart_child new_parent
	local graceful_started graceful_elapsed
	local auto_events="$install_dir/restart-events.jsonl"
	local forced_events="$install_dir/forced-events.jsonl"
	local forced_child forced_started forced_elapsed

	current_scenario=post-reboot
	log "verifying boot persistence"
	[[ "$(systemctl is-enabled "$unit_name")" == enabled ]] || fail "service lost enablement after reboot"
	wait_for_http true >"$state_dir/post-reboot-http.json"
	(( $(event_count "$boot_events" started) >= 2 )) || fail "service did not record a second startup after reboot"
	verify_management_commands

	current_scenario=explicit-restart
	restart_parent=$(http_pid)
	restart_child=$(cat "$install_dir/child.pid")
	"$daemon_bin" restart "$service_name"
	new_parent=$(wait_for_new_http_pid "$restart_parent" 20)
	[[ "$new_parent" != "$restart_parent" ]] || fail "restart reused parent PID $restart_parent"
	wait_for_http true >"$state_dir/restart-http.json"
	wait_process_gone "$restart_parent"
	wait_process_gone "$restart_child"
	assert_event "$boot_events" signal
	assert_event "$boot_events" stopped

	current_scenario=graceful-stop
	graceful_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	graceful_elapsed=$(($(date +%s) - graceful_started))
	(( graceful_elapsed >= 1 )) || fail "graceful stop returned before the configured one-second delay"
	(( graceful_elapsed < 10 )) || fail "graceful stop took ${graceful_elapsed}s"
	assert_event "$boot_events" stopped
	[[ "$(systemctl is-active "$unit_name" 2>/dev/null || true)" == inactive ]] || fail "service is not inactive after graceful stop"
	"$daemon_bin" remove "$service_name"
	[[ ! -e "$unit_path" ]] || fail "unit remains after graceful scenario removal"

	current_scenario=automatic-restart
	log "verifying restart after application failure"
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 45)
	[[ "$new_parent" != "$restart_parent" ]] || fail "automatic restart reused parent PID $restart_parent"
	(( $(event_count "$auto_events" started) >= 2 )) || fail "automatic restart did not record a second startup"
	assert_event "$auto_events" failure
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log "verifying timeout escalation and cgroup cleanup"
	install_scenario 2s "$forced_events" \
		--stop_delay 30s \
		--spawn-child=true \
		--child-pid-path child.pid
	assert_file_contains "$unit_path" 'TimeoutStopSec=2s'
	"$daemon_bin" start "$service_name"
	wait_for_http true >"$state_dir/forced-stop-http.json"
	forced_child=$(cat "$install_dir/child.pid")
	forced_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	forced_elapsed=$(($(date +%s) - forced_started))
	(( forced_elapsed >= 2 )) || fail "forced stop returned before the configured timeout"
	(( forced_elapsed < 15 )) || fail "forced stop took ${forced_elapsed}s"
	assert_event "$forced_events" signal
	if grep -Fq '"event":"stopped"' "$forced_events"; then
		fail "application reported graceful completion despite forced termination"
	fi
	wait_process_gone "$forced_child"
	assert_no_test_app_processes
	"$daemon_bin" remove "$service_name"

	current_scenario=cleanup
	collect_artifacts success
	[[ ! -e "$unit_path" ]] || fail "unit remains after final removal"
	[[ ! -e "$metadata_path" ]] || fail "metadata remains after final removal"
	if find /etc/systemd/system -type l -lname "$unit_path" -print -quit | grep -q .; then
		fail "systemd enablement link remains after final removal"
	fi
	assert_no_test_app_processes
	log "all systemd application-level tests passed"
}

require_environment
case "$phase" in
	pre-reboot)
		[[ -n "$service_name" ]] || fail "service name is required"
		pre_reboot
		;;
	post-reboot)
		[[ -n "$service_name" ]] || fail "service name is required"
		post_reboot
		;;
	cleanup)
		cleanup_service
		;;
	*)
		fail "usage: $0 {pre-reboot|post-reboot|cleanup} SERVICE_NAME [PORT]"
		;;
esac
