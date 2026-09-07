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
runlevel_link="/etc/runlevels/default/$registration_name"
metadata_path="/var/lib/daemon-util/services/${registration_name}.json"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
interpreted_test="$install_dir/interpreted-app-test.sh"
current_scenario=initialization

log() {
	printf '[openrc-itest] %s\n' "$*"
}

fail() {
	printf '[openrc-itest] ERROR: %s\n' "$*" >&2
	return 1
}

. "$interpreted_test"

interpreted_definition_path() {
	printf '/etc/init.d/lz_lz_%s\n' "$1"
}

interpreted_removed() {
	local auxiliary=$1 registration="lz_lz_$1"
	[[ ! -e "/etc/init.d/$registration" ]] || fail "auxiliary OpenRC service remains: $registration"
	[[ ! -e "/etc/runlevels/default/$registration" ]] || fail "auxiliary OpenRC enablement remains: $registration"
	[[ ! -e "/var/lib/daemon-util/services/${registration}.json" ]] || fail "auxiliary metadata remains: $auxiliary"
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
	local pid=$1 executable
	[[ -e "/proc/$pid/exe" ]] || return 1
	executable=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
	[[ "$executable" == "$app_bin" || "$executable" == "$app_bin (deleted)" ]]
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
		if [[ "$executable" == "$app_bin" || "$executable" == "$app_bin (deleted)" ]]; then
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
        assert data["config"]["message"] == "hello openrc", data
        assert data["config"]["count"] == 7, data
        assert data["config"]["port"] == port, data
        args = data["args"]
        assert "hello openrc" in args, args
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
		printf '\nOS release:\n'
		cat /etc/os-release
		uname -a
		printf '\nPID 1:\n'
		cat /proc/1/comm
		printf '\nOpenRC version:\n'
		openrc --version
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	rc-service "$registration_name" status >"$artifact_dir/${label}-status.txt" 2>&1 || true
	rc-status --all >"$artifact_dir/${label}-rc-status.txt" 2>&1 || true
	rc-update show >"$artifact_dir/${label}-runlevels.txt" 2>&1 || true
	if [[ -f "$service_path" ]]; then
		cp -f "$service_path" "$artifact_dir/${label}-service-script"
	fi
	ps -ef >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
	chmod -R a+rX "$artifact_dir" 2>/dev/null || true
}

cleanup_service() {
	if [[ -f "$service_path" ]]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			rc-service "$registration_name" stop >/dev/null 2>&1 || true
			rc-update delete "$registration_name" default >/dev/null 2>&1 || true
			rm -f "$service_path" "$runlevel_link"
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
	[[ -r "$interpreted_test" ]] || fail "missing interpreted application test at $interpreted_test"
	[[ -f "$fixture_path" ]] || fail "missing relative-path fixture at $fixture_path"
	[[ ! -d /run/systemd/system ]] || fail "systemd unexpectedly detected in OpenRC guest"
	[[ -d /run/openrc ]] || fail "/run/openrc is missing"
	[[ -x /sbin/openrc-run ]] || fail "/sbin/openrc-run is missing"
	command -v openrc >/dev/null || fail "openrc is required in the guest"
	command -v rc-service >/dev/null || fail "rc-service is required in the guest"
	command -v supervise-daemon >/dev/null || fail "supervise-daemon is required in the guest"
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
		--message "hello openrc" \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[[ -x "$service_path" ]] || fail "OpenRC service script was not created as executable"
	assert_file_contains "$service_path" '#!/sbin/openrc-run'
	assert_file_contains "$service_path" "command='$app_bin'"
	assert_file_contains "$service_path" "directory='$install_dir'"
	assert_file_contains "$service_path" 'supervisor=supervise-daemon'
	assert_file_contains "$service_path" 'stopgroup=true'
	assert_file_contains "$service_path" 'rc_cgroup_cleanup=yes'
	assert_file_contains "$service_path" 'respawn_delay=30'
	assert_file_contains "$service_path" 'respawn_max=0'
	if grep -Eq 'stop_pre\(\)|stop_post\(\)|daemon_stop_process_group|kill[[:space:]]+-KILL' "$service_path"; then
		fail "OpenRC service script contains redundant manual process-group cleanup"
	fi
	[[ -e "$runlevel_link" ]] || fail "OpenRC default-runlevel link was not created"
	rc-update show default | grep -Fq "$registration_name" || fail "service is not enabled in the default runlevel"
}

verify_management_commands() {
	local output
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" 'running' 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command application path'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello openrc' 'long list arguments'
}

pre_reboot() {
	local events="$install_dir/boot-events.jsonl"
	current_scenario=pre-reboot
	log "installing boot-persistence scenario"
	cleanup_service
	install_scenario 5s "$events" \
		--stop_delay 1s
	verify_definition
	assert_file_contains "$service_path" 'retry="TERM/5/KILL/5"'
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/pre-reboot-http.json"
	verify_management_commands
	http_pid >"$state_dir/pre-reboot-parent.pid"
	collect_artifacts pre-reboot
	log "pre-reboot checks passed"
}

post_reboot() {
	local boot_events="$install_dir/boot-events.jsonl"
	local restart_parent new_parent
	local hot_parent hot_new_parent replacement
	local graceful_started graceful_elapsed
	local auto_events="$install_dir/restart-events.jsonl"
	local forced_events="$install_dir/forced-events.jsonl"
	local forced_started forced_elapsed

	current_scenario=post-reboot
	log "verifying boot persistence"
	[[ -e "$runlevel_link" ]] || fail "service lost default-runlevel enablement after reboot"
	wait_for_http false >"$state_dir/post-reboot-http.json"
	(( $(event_count "$boot_events" started) >= 2 )) || fail "service did not record a second startup after reboot"
	verify_management_commands

	current_scenario=explicit-restart
	restart_parent=$(http_pid)
	"$daemon_bin" restart "$service_name"
	new_parent=$(wait_for_new_http_pid "$restart_parent" 20)
	[[ "$new_parent" != "$restart_parent" ]] || fail "restart reused parent PID $restart_parent"
	wait_for_http false >"$state_dir/restart-http.json"
	wait_process_gone "$restart_parent"
	assert_event "$boot_events" signal
	assert_event "$boot_events" stopped

	current_scenario=hot-replacement
	log "verifying status, list, and stop after atomic executable replacement"
	hot_parent=$(http_pid)
	replacement="$install_dir/.test-app.replacement.$$"
	cp -p "$app_bin" "$replacement"
	mv -f "$replacement" "$app_bin"
	[[ "$(readlink "/proc/$hot_parent/exe" 2>/dev/null || true)" == "$app_bin (deleted)" ]] || fail "running executable was not atomically replaced"
	sleep 2
	[[ "$(http_pid)" == "$hot_parent" ]] || fail "OpenRC restarted the application after hot replacement"
	verify_management_commands
	assert_contains "$(rc-service "$registration_name" status 2>&1 || true)" 'started' 'OpenRC status after hot replacement'
	"$daemon_bin" stop "$service_name"
	wait_process_gone "$hot_parent"
	assert_contains "$(rc-service "$registration_name" status 2>&1 || true)" 'stopped' 'OpenRC status after hot-replacement stop'
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/hot-replacement-http.json"
	hot_new_parent=$(http_pid)
	[[ "$hot_new_parent" != "$hot_parent" ]] || fail "hot-replacement restart reused PID $hot_parent"
	verify_management_commands

	current_scenario=graceful-stop
	graceful_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	graceful_elapsed=$(($(date +%s) - graceful_started))
	(( graceful_elapsed >= 1 )) || fail "graceful stop returned before the configured one-second delay"
	(( graceful_elapsed < 10 )) || fail "graceful stop took ${graceful_elapsed}s"
	assert_event "$boot_events" stopped
	assert_contains "$(rc-service "$registration_name" status 2>&1 || true)" 'stopped' 'OpenRC status after graceful stop'
	"$daemon_bin" remove "$service_name"
	[[ ! -e "$service_path" ]] || fail "service script remains after graceful scenario removal"
	[[ ! -e "$runlevel_link" ]] || fail "runlevel link remains after graceful scenario removal"

	current_scenario=automatic-restart
	log "verifying restart after application failure"
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 50)
	[[ "$new_parent" != "$restart_parent" ]] || fail "automatic restart reused parent PID $restart_parent"
	(( $(event_count "$auto_events" started) >= 2 )) || fail "automatic restart did not record a second startup"
	assert_event "$auto_events" failure
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log "verifying timeout escalation for the supervised main process"
	install_scenario 2s "$forced_events" \
		--stop_delay 30s
	assert_file_contains "$service_path" 'retry="TERM/2/KILL/5"'
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/forced-stop-http.json"
	forced_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	forced_elapsed=$(($(date +%s) - forced_started))
	(( forced_elapsed >= 2 )) || fail "forced stop returned before the configured timeout"
	(( forced_elapsed < 15 )) || fail "forced stop took ${forced_elapsed}s"
	assert_event "$forced_events" signal
	if grep -Fq '"event":"stopped"' "$forced_events"; then
		fail "application reported graceful completion despite forced termination"
	fi
	assert_no_test_app_processes
	"$daemon_bin" remove "$service_name"

	current_scenario=interpreted-applications
	log 'verifying symlinked native, shell, Python, and rejected direct-script applications'
	verify_interpreted_applications

	current_scenario=cleanup
	collect_artifacts success
	[[ ! -e "$service_path" ]] || fail "service script remains after final removal"
	[[ ! -e "$runlevel_link" ]] || fail "runlevel link remains after final removal"
	[[ ! -e "$metadata_path" ]] || fail "metadata remains after final removal"
	assert_no_test_app_processes
	log "all OpenRC application-level tests passed"
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
