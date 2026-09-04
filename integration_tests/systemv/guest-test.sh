#!/usr/bin/env bash

set -Eeuo pipefail

phase=${1:-}
service_name=${2:-}
port=${3:-18080}
install_dir=${DAEMON_ITEST_INSTALL_DIR:-/opt/daemon-itest}
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
registration_name="lz_lz_${service_name}"
service_path="/etc/init.d/$registration_name"
metadata_path="/var/lib/daemon-util/services/${registration_name}.json"
pidfile="/var/run/${registration_name}.pid"
watcher_pidfile="/var/run/${registration_name}.watchdog.pid"
lockfile="/var/lock/subsys/$registration_name"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[systemv-itest] %s\n' "$*"
}

fail() {
	printf '[systemv-itest] ERROR: %s\n' "$*" >&2
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
        assert data["config"]["message"] == "hello systemv", data
        assert data["config"]["count"] == 7, data
        assert data["config"]["port"] == port, data
        assert "hello systemv" in data["args"], data["args"]
        assert "relative-path-test.txt" in data["args"], data["args"]
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

existing_runlevel_links() {
	local runlevel directory
	for runlevel in 2 3 4 5; do
		directory="/etc/rc${runlevel}.d"
		[[ -d "$directory" ]] && printf '%s\n' "$directory/S87$registration_name"
	done
	for runlevel in 0 1 6; do
		directory="/etc/rc${runlevel}.d"
		[[ -d "$directory" ]] && printf '%s\n' "$directory/K17$registration_name"
	done
}

verify_links_present() {
	local link count=0
	while IFS= read -r link; do
		[[ -L "$link" ]] || fail "System V runlevel link is missing: $link"
		[[ "$(readlink "$link")" == "$service_path" ]] || fail "unexpected runlevel link target: $link"
		count=$((count + 1))
	done < <(existing_runlevel_links)
	(( count > 0 )) || fail "no System V runlevel directories were found"
}

verify_links_absent() {
	local link
	while IFS= read -r link; do
		[[ ! -e "$link" && ! -L "$link" ]] || fail "System V runlevel link remains: $link"
	done < <(existing_runlevel_links)
}

wait_for_watcher() {
	local deadline=$((SECONDS + 10))
	local watcher_pid
	while (( SECONDS < deadline )); do
		if [[ -r "$watcher_pidfile" ]]; then
			watcher_pid=$(cat "$watcher_pidfile")
			if [[ "$watcher_pid" =~ ^[0-9]+$ ]] && kill -0 "$watcher_pid" 2>/dev/null; then
				printf '%s\n' "$watcher_pid"
				return
			fi
		fi
		sleep 0.2
	done
	fail "System V watchdog did not become ready"
}

collect_artifacts() {
	local label=${1:-$current_scenario}
	mkdir -p "$artifact_dir"
	{
		printf 'phase=%s\nscenario=%s\nservice=%s\n' "$phase" "$current_scenario" "$service_name"
		uname -a
		cat /etc/os-release
		printf '\nPID 1:\n'
		ps -p 1 -o pid=,comm=,args=
		printf '\nrunlevel:\n'
		runlevel
		printf '\ninit package:\n'
		dpkg-query -W -f='${Package} ${Version}\n' sysvinit-core sysv-rc initscripts 2>/dev/null || true
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	service "$registration_name" status >"$artifact_dir/${label}-status.txt" 2>&1 || true
	service --status-all >"$artifact_dir/${label}-all-services.txt" 2>&1 || true
	ps -ef >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	find /etc/rc?.d -maxdepth 1 -name "[SK][0-9][0-9]$registration_name" -ls >"$artifact_dir/${label}-runlevel-links.txt" 2>&1 || true
	if [[ -f "$service_path" ]]; then
		cp -f "$service_path" "$artifact_dir/${label}-service-script"
	fi
	for path in "$pidfile" "$watcher_pidfile"; do
		if [[ -r "$path" ]]; then
			cp -f "$path" "$artifact_dir/${label}-$(basename "$path")"
		fi
	done
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
	chmod -R a+rX "$artifact_dir" 2>/dev/null || true
}

cleanup_service() {
	if [[ -f "$service_path" ]]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			"$service_path" unwatch >/dev/null 2>&1 || true
			"$service_path" stop >/dev/null 2>&1 || true
			while IFS= read -r link; do
				rm -f "$link"
			done < <(existing_runlevel_links)
			rm -f "$service_path"
		}
	fi
	rm -f "$metadata_path" "$pidfile" "$watcher_pidfile" "$lockfile"
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
	[[ ! -d /run/systemd/system ]] || fail "systemd is active in the System V guest"
	[[ "$(cat /proc/1/comm)" == init ]] || fail "System V init is not PID 1"
	dpkg-query -W sysvinit-core >/dev/null 2>&1 || fail "sysvinit-core is not installed"
	command -v service >/dev/null || fail "service is required in the guest"
	command -v setsid >/dev/null || fail "setsid is required in the guest"
	command -v python3 >/dev/null || fail "python3 is required in the guest"
	for runlevel in 2 3 4 5; do
		[[ -d "/etc/rc${runlevel}.d" ]] && break
		[[ "$runlevel" != 5 ]] || fail "no System V start runlevel directory exists"
	done
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
		--message "hello systemv" \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[[ -x "$service_path" ]] || fail "System V init script was not created as executable"
	assert_file_contains "$service_path" '### BEGIN INIT INFO'
	assert_file_contains "$service_path" "# Provides: $registration_name"
	assert_file_contains "$service_path" '# Default-Start: 2 3 4 5'
	assert_file_contains "$service_path" '# Default-Stop: 0 1 6'
	assert_file_contains "$service_path" "exec='$app_bin'"
	assert_file_contains "$service_path" "working_directory='$install_dir'"
	assert_file_contains "$service_path" "pidfile=\"/var/run/\$proc.pid\""
	assert_file_contains "$service_path" "watcher_pidfile=\${pidfile%.pid}.watchdog.pid"
	assert_file_contains "$service_path" "setsid \"\$exec\""
	assert_file_contains "$service_path" "kill -TERM -- \"-\$target_pid\""
	assert_file_contains "$service_path" "kill -KILL -- \"-\$target_pid\""
	verify_links_present
}

verify_management_commands() {
	local output
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" 'running' 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command application path'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello systemv' 'long list arguments'
}

pre_reboot() {
	local events="$install_dir/boot-events.jsonl"
	local child_pid watcher_pid
	current_scenario=pre-reboot
	log "installing boot-persistence scenario"
	cleanup_service
	install_scenario 5s "$events" \
		--stop_delay 1s \
		--spawn-child=true \
		--child-pid-path child.pid
	verify_definition
	assert_file_contains "$service_path" 'stop_timeout=5'
	"$daemon_bin" start "$service_name"
	wait_for_http true >"$state_dir/pre-reboot-http.json"
	verify_management_commands
	child_pid=$(cat "$install_dir/child.pid")
	process_is_test_app "$child_pid" || fail "child process $child_pid is not running"
	watcher_pid=$(wait_for_watcher)
	kill -0 "$watcher_pid" 2>/dev/null || fail "watchdog process $watcher_pid is not running"
	http_pid >"$state_dir/pre-reboot-parent.pid"
	printf '%s\n' "$watcher_pid" >"$state_dir/pre-reboot-watcher.pid"
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
	local watcher_pid

	current_scenario=post-reboot
	log "verifying boot persistence"
	verify_links_present
	wait_for_http true >"$state_dir/post-reboot-http.json"
	(( $(event_count "$boot_events" started) >= 2 )) || fail "service did not record a second startup after reboot"
	watcher_pid=$(wait_for_watcher)
	kill -0 "$watcher_pid" 2>/dev/null || fail "watchdog did not restart after reboot"
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
	assert_contains "$(service "$registration_name" status 2>&1 || true)" 'is stopped' 'System V status after graceful stop'
	[[ ! -e "$watcher_pidfile" ]] || fail "watchdog PID file remains after stop"
	"$daemon_bin" remove "$service_name"
	[[ ! -e "$service_path" ]] || fail "init script remains after graceful scenario removal"
	verify_links_absent

	current_scenario=automatic-restart
	log "verifying watchdog restart after application failure"
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 55)
	[[ "$new_parent" != "$restart_parent" ]] || fail "automatic restart reused parent PID $restart_parent"
	(( $(event_count "$auto_events" started) >= 2 )) || fail "watchdog restart did not record a second startup"
	assert_event "$auto_events" failure
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log "verifying timeout escalation and process-group cleanup"
	install_scenario 2s "$forced_events" \
		--stop_delay 30s \
		--spawn-child=true \
		--child-pid-path child.pid
	assert_file_contains "$service_path" 'stop_timeout=2'
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
	[[ ! -e "$service_path" ]] || fail "init script remains after final removal"
	[[ ! -e "$metadata_path" ]] || fail "metadata remains after final removal"
	[[ ! -e "$pidfile" ]] || fail "application PID file remains after final removal"
	[[ ! -e "$watcher_pidfile" ]] || fail "watchdog PID file remains after final removal"
	verify_links_absent
	assert_no_test_app_processes
	log "all System V application-level tests passed"
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
