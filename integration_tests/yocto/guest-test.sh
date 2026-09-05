#!/bin/sh

set -eu

phase=${1:?phase is required}
service_name=${2:?service name is required}
port=${3:-18080}
install_dir=/opt/daemon-itest
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
registration_name="lz_lz_${service_name}"
service_path="/etc/init.d/$registration_name"
metadata_path="/var/lib/daemon-util/services/${registration_name}.json"
pidfile="/var/run/${registration_name}.pid"
identityfile="${pidfile}.identity"
watcher_pidfile="/var/run/${registration_name}.watchdog.pid"
lockfile="/var/lock/subsys/$registration_name"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[yocto-itest] %s\n' "$*"
}

fail() {
	printf '[yocto-itest] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_contains() {
	value=$1
	expected=$2
	description=$3
	case "$value" in
		*"$expected"*) ;;
		*) fail "$description: expected '$expected' in '$value'" ;;
	esac
}

assert_file_contains() {
	path=$1
	expected=$2
	grep -Fq -- "$expected" "$path" || fail "$path does not contain '$expected'"
}

process_is_test_app() {
	pid=$1
	executable=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
	[ "$executable" = "$app_bin" ] || [ "$executable" = "$app_bin (deleted)" ]
}

wait_process_gone() {
	pid=$1
	attempt=0
	while process_is_test_app "$pid"; do
		[ "$attempt" -lt 30 ] || fail "test application process $pid is still running"
		sleep 1
		attempt=$((attempt + 1))
	done
}

assert_no_test_app_processes() {
	for process in /proc/[0-9]*; do
		executable=$(readlink "$process/exe" 2>/dev/null || true)
		if [ "$executable" = "$app_bin" ] || [ "$executable" = "$app_bin (deleted)" ]; then
			fail "test application process ${process##*/} leaked after cleanup"
		fi
	done
}

http_response() {
	wget -q -O - "http://127.0.0.1:$port/" 2>/dev/null
}

http_pid() {
	http_response | sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
}

wait_for_http() {
	expect_child=${1:-false}
	attempt=0
	while [ "$attempt" -lt 40 ]; do
		if output=$(http_response); then
			printf '%s\n' "$output" | grep -Fq '"executable": "/opt/daemon-itest/test-app"' || fail 'unexpected application executable'
			printf '%s\n' "$output" | grep -Fq '"message": "hello yocto"' || fail 'application argument was not preserved'
			printf '%s\n' "$output" | grep -Fq '"count": 7' || fail 'integer application argument was not preserved'
			printf '%s\n' "$output" | grep -Fq 'daemon-util relative path test passed' || fail 'working-directory fixture was not read'
			if [ "$expect_child" = true ]; then
				printf '%s\n' "$output" | grep -Eq '"child_pid": [1-9][0-9]*' || fail 'child process was not reported'
			fi
			printf '%s\n' "$output"
			return
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail 'HTTP application did not become ready'
}

wait_for_new_http_pid() {
	old_pid=$1
	timeout_seconds=$2
	attempt=0
	while [ "$attempt" -lt "$timeout_seconds" ]; do
		candidate=$(http_pid 2>/dev/null || true)
		if [ -n "$candidate" ] && [ "$candidate" != "$old_pid" ]; then
			printf '%s\n' "$candidate"
			return
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail "application did not restart from PID $old_pid"
}

event_count() {
	path=$1
	expected=$2
	grep -Fc "\"event\":\"$expected\"" "$path" 2>/dev/null || true
}

assert_event() {
	path=$1
	expected=$2
	[ "$(event_count "$path" "$expected")" -ge 1 ] || fail "event '$expected' is missing from $path"
}

existing_runlevel_links() {
	for runlevel in 2 3 4 5; do
		directory="/etc/rc${runlevel}.d"
		[ -d "$directory" ] && printf '%s\n' "$directory/S87$registration_name"
	done
	for runlevel in 0 1 6; do
		directory="/etc/rc${runlevel}.d"
		[ -d "$directory" ] && printf '%s\n' "$directory/K17$registration_name"
	done
}

verify_links_present() {
	count=0
	for runlevel in 2 3 4 5; do
		directory="/etc/rc${runlevel}.d"
		[ -d "$directory" ] || continue
		link="$directory/S87$registration_name"
		[ -L "$link" ] || fail "System V runlevel link is missing: $link"
		[ "$(readlink "$link")" = "$service_path" ] || fail "unexpected runlevel link target: $link"
		count=$((count + 1))
	done
	for runlevel in 0 1 6; do
		directory="/etc/rc${runlevel}.d"
		[ -d "$directory" ] || continue
		link="$directory/K17$registration_name"
		[ -L "$link" ] || fail "System V runlevel link is missing: $link"
		[ "$(readlink "$link")" = "$service_path" ] || fail "unexpected runlevel link target: $link"
		count=$((count + 1))
	done
	[ "$count" -gt 0 ] || fail 'no System V runlevel directories were found'
}

verify_links_absent() {
	for link in $(existing_runlevel_links); do
		[ ! -e "$link" ] && [ ! -L "$link" ] || fail "System V runlevel link remains: $link"
	done
}

wait_for_watcher() {
	attempt=0
	while [ "$attempt" -lt 15 ]; do
		if [ -r "$watcher_pidfile" ]; then
			watcher_pid=$(cat "$watcher_pidfile")
			case "$watcher_pid" in
				''|*[!0-9]*) ;;
				*)
					if kill -0 "$watcher_pid" 2>/dev/null; then
						printf '%s\n' "$watcher_pid"
						return
					fi
					;;
			esac
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail 'System V watchdog did not become ready'
}

collect_artifacts() {
	label=${1:-$current_scenario}
	mkdir -p "$artifact_dir"
	{
		printf 'phase=%s\nscenario=%s\nservice=%s\n' "$phase" "$current_scenario" "$service_name"
		uname -a
		printf '\nPID 1:\n'
		cat /proc/1/comm
		printf '\nrunlevel:\n'
		runlevel 2>/dev/null || true
		printf '\nrelease:\n'
		cat /etc/version 2>/dev/null || true
		cat /etc/os-release 2>/dev/null || true
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	service "$registration_name" status >"$artifact_dir/${label}-status.txt" 2>&1 || true
	ps >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	{
		for link in $(existing_runlevel_links); do
			ls -l "$link" 2>&1 || true
		done
	} >"$artifact_dir/${label}-runlevel-links.txt" 2>&1 || true
	[ ! -f "$service_path" ] || cp -f "$service_path" "$artifact_dir/${label}-service-script"
	for path in "$pidfile" "$identityfile" "$watcher_pidfile"; do
		[ ! -r "$path" ] || cp -f "$path" "$artifact_dir/${label}-$(basename "$path")"
	done
	for path in "$install_dir"/*-events.jsonl "$state_dir"/*.json "$state_dir"/*.pid; do
		[ -f "$path" ] && cp -f "$path" "$artifact_dir/"
	done
	return 0
}

cleanup_service() {
	if [ -f "$service_path" ]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			"$service_path" unwatch >/dev/null 2>&1 || true
			"$service_path" stop >/dev/null 2>&1 || true
			for link in $(existing_runlevel_links); do
				rm -f "$link"
			done
			rm -f "$service_path"
		}
	fi
	rm -f "$metadata_path" "$pidfile" "$identityfile" "$watcher_pidfile" "$lockfile"
}

on_exit() {
	status=$?
	trap - EXIT
	if [ "$status" -ne 0 ]; then
		collect_artifacts failure
		cleanup_service
	fi
	exit "$status"
}
trap on_exit EXIT

require_environment() {
	[ "$(id -u)" -eq 0 ] || fail 'guest test must run as root'
	[ -x "$daemon_bin" ] || fail "missing daemon binary at $daemon_bin"
	[ -x "$app_bin" ] || fail "missing test application at $app_bin"
	[ -f "$fixture_path" ] || fail "missing relative-path fixture at $fixture_path"
	[ "$(uname -m)" = aarch64 ] || fail 'Yocto guest is not ARM64'
	[ "$(cat /proc/1/comm)" = init ] || fail 'System V init is not PID 1'
	[ ! -d /run/systemd/system ] || fail 'systemd unexpectedly detected in Yocto guest'
	if grep -Fq '/etc/init.d/S??*' /etc/init.d/rcS; then
		fail 'Buildroot-style init was unexpectedly detected in Yocto guest'
	fi
	command -v service >/dev/null || fail 'service is required in the Yocto guest'
	command -v setsid >/dev/null || fail 'setsid is required in the Yocto guest'
	command -v wget >/dev/null || fail 'wget is required in the Yocto guest'
	mkdir -p "$state_dir" "$artifact_dir"
}

install_scenario() {
	timeout=$1
	events=$2
	shift 2
	rm -f "$events" "$install_dir/child.pid"
	"$daemon_bin" install --stop-timeout "$timeout" --ignore-warnings \
		"$service_name" "$app_bin" \
		--enabled=true \
		--message 'hello yocto' \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[ -x "$service_path" ] || fail 'System V init script was not created as executable'
	assert_file_contains "$service_path" '### BEGIN INIT INFO'
	assert_file_contains "$service_path" "# Provides: $registration_name"
	assert_file_contains "$service_path" '# Default-Start: 2 3 4 5'
	assert_file_contains "$service_path" '# Default-Stop: 0 1 6'
	assert_file_contains "$service_path" "exec='$app_bin'"
	assert_file_contains "$service_path" "working_directory='$install_dir'"
	assert_file_contains "$service_path" 'identityfile="${pidfile}.identity"'
	assert_file_contains "$service_path" 'current_starttime=$(process_starttime "$pid")'
	assert_file_contains "$service_path" 'watcher_pidfile=${pidfile%.pid}.watchdog.pid'
	assert_file_contains "$service_path" 'setsid "$exec"'
	assert_file_contains "$service_path" 'signal_process_group TERM'
	assert_file_contains "$service_path" 'signal_process_group KILL'
	verify_links_present
}

verify_management_commands() {
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" running 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command application path'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello yocto' 'long list arguments'
}

pre_reboot() {
	events="$install_dir/boot-events.jsonl"
	current_scenario=pre-reboot
	log 'installing boot-persistence scenario'
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
	log 'pre-reboot checks passed'
}

post_reboot() {
	boot_events="$install_dir/boot-events.jsonl"
	auto_events="$install_dir/restart-events.jsonl"
	forced_events="$install_dir/forced-events.jsonl"

	current_scenario=post-reboot
	log 'verifying boot persistence'
	verify_links_present
	wait_for_http true >"$state_dir/post-reboot-http.json"
	[ "$(event_count "$boot_events" started)" -ge 2 ] || fail 'service did not start after reboot'
	watcher_pid=$(wait_for_watcher)
	kill -0 "$watcher_pid" 2>/dev/null || fail 'watchdog did not restart after reboot'
	verify_management_commands

	current_scenario=explicit-restart
	restart_parent=$(http_pid)
	restart_child=$(cat "$install_dir/child.pid")
	if ! restart_output=$("$daemon_bin" restart "$service_name" 2>&1); then
		printf '%s\n' "$restart_output" >&2
		service "$registration_name" status >&2 || true
		if [ -r "$pidfile" ]; then
			failed_pid=$(cat "$pidfile")
			printf 'PID file after failed restart: %s\n' "$failed_pid" >&2
			cat "/proc/$failed_pid/stat" >&2 2>/dev/null || true
		fi
		printf 'Direct init-script stop diagnostic:\n' >&2
		"$service_path" stop >&2 || true
		cat "$boot_events" >&2 || true
		fail 'explicit restart command failed'
	fi
	new_parent=$(wait_for_new_http_pid "$restart_parent" 30)
	wait_for_http true >"$state_dir/restart-http.json"
	wait_process_gone "$restart_parent"
	wait_process_gone "$restart_child"
	assert_event "$boot_events" signal
	assert_event "$boot_events" stopped
	[ "$new_parent" != "$restart_parent" ] || fail 'explicit restart reused the parent PID'

	current_scenario=hot-replacement
	log 'verifying status and stop after atomic executable replacement'
	hot_parent=$(http_pid)
	hot_identity=$(cat "$identityfile")
	case "$hot_identity" in
		"$hot_parent "*) ;;
		*) fail "identity file does not belong to PID $hot_parent" ;;
	esac
	replacement="$install_dir/.test-app.replacement.$$"
	cp -p "$app_bin" "$replacement"
	mv -f "$replacement" "$app_bin"
	[ "$(readlink "/proc/$hot_parent/exe" 2>/dev/null || true)" = "$app_bin (deleted)" ] || fail 'running executable was not atomically replaced'
	sleep 2
	[ "$(http_pid)" = "$hot_parent" ] || fail 'watchdog restarted the application after hot replacement'
	[ "$(cat "$identityfile")" = "$hot_identity" ] || fail 'process identity changed after hot replacement'
	assert_contains "$("$daemon_bin" status "$service_name")" running 'status after hot replacement'
	"$daemon_bin" stop "$service_name"
	wait_process_gone "$hot_parent"
	[ ! -e "$pidfile" ] || fail 'application PID file remains after hot-replacement stop'
	[ ! -e "$identityfile" ] || fail 'application identity file remains after hot-replacement stop'
	"$daemon_bin" start "$service_name"
	wait_for_http true >"$state_dir/hot-replacement-http.json"
	hot_new_parent=$(http_pid)
	[ "$hot_new_parent" != "$hot_parent" ] || fail "hot-replacement restart reused PID $hot_parent"
	case "$(cat "$identityfile")" in
		"$hot_new_parent "*) ;;
		*) fail 'new application identity was not recorded' ;;
	esac

	current_scenario=graceful-stop
	graceful_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	graceful_elapsed=$(($(date +%s) - graceful_started))
	[ "$graceful_elapsed" -ge 1 ] || fail 'graceful stop returned before the configured delay'
	[ "$graceful_elapsed" -lt 12 ] || fail "graceful stop took ${graceful_elapsed}s"
	if service "$registration_name" status >/dev/null 2>&1; then
		fail 'service still reports running after graceful stop'
	fi
	"$daemon_bin" remove "$service_name"
	[ ! -e "$service_path" ] || fail 'init script remains after graceful scenario removal'
	verify_links_absent

	current_scenario=automatic-restart
	log 'verifying configured failure and hard-crash watchdog recovery'
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http false >"$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 60)
	[ "$(event_count "$auto_events" started)" -ge 2 ] || fail 'configured failure did not restart the app'
	assert_event "$auto_events" failure
	kill -KILL "$new_parent"
	hard_parent=$(wait_for_new_http_pid "$new_parent" 60)
	wait_process_gone "$new_parent"
	[ "$(event_count "$auto_events" started)" -ge 3 ] || fail 'hard crash did not record a third startup'
	[ "$hard_parent" != "$new_parent" ] || fail 'hard-crash restart reused the parent PID'
	verify_management_commands
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log 'verifying timeout escalation and process-group cleanup'
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
	[ "$forced_elapsed" -ge 2 ] || fail 'forced stop returned before the configured timeout'
	[ "$forced_elapsed" -lt 15 ] || fail "forced stop took ${forced_elapsed}s"
	assert_event "$forced_events" signal
	if grep -Fq '"event":"stopped"' "$forced_events"; then
		fail 'application reported graceful completion despite forced termination'
	fi
	wait_process_gone "$forced_child"
	assert_no_test_app_processes
	"$daemon_bin" remove "$service_name"

	current_scenario=cleanup
	collect_artifacts success
	[ ! -e "$service_path" ] || fail 'init script remains after final removal'
	[ ! -e "$metadata_path" ] || fail 'metadata remains after final removal'
	[ ! -e "$pidfile" ] || fail 'application PID file remains after final removal'
	[ ! -e "$identityfile" ] || fail 'application identity file remains after final removal'
	[ ! -e "$watcher_pidfile" ] || fail 'watchdog PID file remains after final removal'
	verify_links_absent
	assert_no_test_app_processes
	log 'all Yocto System V application-level tests passed'
}

require_environment
case "$phase" in
	pre-reboot) pre_reboot ;;
	post-reboot) post_reboot ;;
	cleanup) cleanup_service ;;
	*) fail "usage: $0 {pre-reboot|post-reboot|cleanup} SERVICE_NAME [PORT]" ;;
esac
