#!/usr/bin/env bash

set -Eeuo pipefail

phase=${1:-}
service_name=${2:-}
port=${3:-18080}
install_dir=${DAEMON_ITEST_INSTALL_DIR:-/opt/daemon-itest}
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
registration_name="lz_lz_${service_name}"
service_path="/etc/init/${registration_name}.conf"
metadata_path="/var/lib/daemon-util/services/${registration_name}.json"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[upstart-itest] %s\n' "$*"
}

fail() {
	printf '[upstart-itest] ERROR: %s\n' "$*" >&2
	return 1
}

assert_contains() {
	local value=$1 expected=$2 description=$3
	[[ "$value" == *"$expected"* ]] || fail "$description: expected '$expected' in '$value'"
}

assert_file_contains() {
	local path=$1 expected=$2
	grep -Fq -- "$expected" "$path" || fail "$path does not contain '$expected'"
}

process_is_test_app() {
	local pid=$1
	[[ -e "/proc/$pid/exe" ]] && [[ "$(readlink "/proc/$pid/exe" 2>/dev/null || true)" == "$app_bin" ]]
}

wait_process_gone() {
	local pid=$1 deadline=$((SECONDS + 20))
	while process_is_test_app "$pid"; do
		(( SECONDS < deadline )) || fail "test application process $pid is still running"
		sleep 1
	done
}

assert_no_test_app_processes() {
	local process executable
	for process in /proc/[0-9]*; do
		executable=$(readlink "$process/exe" 2>/dev/null || true)
		[[ "$executable" != "$app_bin" ]] || fail "test application process ${process##*/} leaked after cleanup"
	done
}

http_response() {
	wget -q -O - "http://127.0.0.1:$port/" 2>/dev/null
}

http_pid() {
	http_response | sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
}

wait_for_http() {
	local deadline=$((SECONDS + 40)) output
	while (( SECONDS < deadline )); do
		if output=$(http_response); then
			grep -Fq '"executable": "/opt/daemon-itest/test-app"' <<<"$output" || fail 'unexpected application executable'
			grep -Fq '"message": "hello upstart"' <<<"$output" || fail 'application argument was not preserved'
			grep -Fq '"count": 7' <<<"$output" || fail 'integer application argument was not preserved'
			grep -Fq 'daemon-util relative path test passed' <<<"$output" || fail 'working-directory fixture was not read'
			printf '%s\n' "$output"
			return
		fi
		sleep 1
	done
	fail 'HTTP application did not become ready'
}

wait_for_new_http_pid() {
	local old_pid=$1 timeout_seconds=$2 candidate
	local deadline=$((SECONDS + timeout_seconds))
	while (( SECONDS < deadline )); do
		candidate=$(http_pid 2>/dev/null || true)
		if [[ -n "$candidate" && "$candidate" != "$old_pid" ]]; then
			printf '%s\n' "$candidate"
			return
		fi
		sleep 1
	done
	fail "application did not restart from PID $old_pid"
}

event_count() {
	local path=$1 expected=$2
	grep -Fc "\"event\":\"$expected\"" "$path" 2>/dev/null || true
}

assert_event() {
	local path=$1 expected=$2
	[[ "$(event_count "$path" "$expected")" -ge 1 ]] || fail "event '$expected' is missing from $path"
}

collect_artifacts() {
	local label=${1:-$current_scenario}
	mkdir -p "$artifact_dir"
	{
		printf 'phase=%s\nscenario=%s\nservice=%s\n' "$phase" "$current_scenario" "$service_name"
		uname -a
		printf '\nPID 1:\n'
		ps -p 1 -o pid=,comm=,args=
		printf '\nUpstart version:\n'
		initctl version
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	status "$registration_name" >"$artifact_dir/${label}-status.txt" 2>&1 || true
	initctl show-config "$registration_name" >"$artifact_dir/${label}-show-config.txt" 2>&1 || true
	[[ ! -f "$service_path" ]] || cp -f "$service_path" "$artifact_dir/${label}-job.conf"
	ps -ef >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	cp -f /var/log/upstart/${registration_name}.log "$artifact_dir/${label}-upstart.log" 2>/dev/null || true
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
	chmod -R a+rX "$artifact_dir" 2>/dev/null || true
}

cleanup_service() {
	if [[ -f "$service_path" ]]; then
		stop "$registration_name" >/dev/null 2>&1 || true
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || rm -f "$service_path"
	fi
	rm -f "$metadata_path"
}

on_exit() {
	local status_code=$?
	trap - EXIT
	if (( status_code != 0 )); then
		collect_artifacts failure
		cleanup_service
	fi
	exit "$status_code"
}
trap on_exit EXIT

require_environment() {
	[[ $(id -u) -eq 0 ]] || fail 'guest test must run as root'
	[[ -x "$daemon_bin" ]] || fail "missing daemon binary at $daemon_bin"
	[[ -x "$app_bin" ]] || fail "missing test application at $app_bin"
	[[ -f "$fixture_path" ]] || fail "missing fixture at $fixture_path"
	[[ ! -d /run/systemd/system ]] || fail 'systemd unexpectedly detected in Upstart guest'
	[[ -x /sbin/initctl ]] || fail '/sbin/initctl is missing'
	initctl version 2>&1 | grep -qi upstart || fail 'Upstart is not the active init implementation'
	[[ "$(cat /proc/1/comm)" == init ]] || fail 'init is not PID 1'
	command -v start >/dev/null || fail 'Upstart start command is missing'
	command -v stop >/dev/null || fail 'Upstart stop command is missing'
	command -v status >/dev/null || fail 'Upstart status command is missing'
	command -v wget >/dev/null || fail 'wget is required in the guest'
	mkdir -p "$state_dir" "$artifact_dir"
}

install_scenario() {
	local timeout=$1 events=$2
	shift 2
	rm -f "$events"
	"$daemon_bin" install --stop-timeout "$timeout" --ignore-warnings \
		"$service_name" "$app_bin" \
		--enabled=true \
		--message 'hello upstart' \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[[ -f "$service_path" ]] || fail 'Upstart job was not created'
	assert_file_contains "$service_path" 'start on runlevel [2345]'
	assert_file_contains "$service_path" 'stop on runlevel [016]'
	assert_file_contains "$service_path" 'respawn'
	assert_file_contains "$service_path" 'respawn limit 0 5'
	assert_file_contains "$service_path" 'kill timeout 5'
	assert_file_contains "$service_path" "chdir \"$install_dir\""
	assert_file_contains "$service_path" "exec '$app_bin'"
	initctl check-config "$registration_name" >/dev/null
}

verify_management_commands() {
	local output
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" running 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command executable'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello upstart' 'long list arguments'
}

pre_reboot() {
	local events="$install_dir/boot-events.jsonl"
	current_scenario=pre-reboot
	log 'installing boot-persistence scenario'
	cleanup_service
	install_scenario 5s "$events" --stop_delay 1s
	verify_definition
	"$daemon_bin" start "$service_name"
	wait_for_http >"$state_dir/pre-reboot-http.json"
	verify_management_commands
	http_pid >"$state_dir/pre-reboot-parent.pid"
	collect_artifacts pre-reboot
	log 'pre-reboot checks passed'
}

post_reboot() {
	local boot_events="$install_dir/boot-events.jsonl"
	local auto_events="$install_dir/restart-events.jsonl"
	local old_pid new_pid hard_pid graceful_started graceful_elapsed

	current_scenario=post-reboot
	log 'verifying reboot auto-start'
	wait_for_http >"$state_dir/post-reboot-http.json"
	[[ "$(event_count "$boot_events" started)" -ge 2 ]] || fail 'service did not start after reboot'
	verify_management_commands

	current_scenario=explicit-restart
	old_pid=$(http_pid)
	"$daemon_bin" restart "$service_name"
	new_pid=$(wait_for_new_http_pid "$old_pid" 30)
	wait_process_gone "$old_pid"
	wait_for_http >"$state_dir/explicit-restart-http.json"
	log "explicit restart changed PID $old_pid to $new_pid"

	current_scenario=graceful-stop
	graceful_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	graceful_elapsed=$(($(date +%s) - graceful_started))
	(( graceful_elapsed >= 1 )) || fail 'graceful stop returned before configured delay'
	(( graceful_elapsed < 12 )) || fail "graceful stop took ${graceful_elapsed}s"
	assert_event "$boot_events" stopped
	assert_contains "$(status "$registration_name" 2>&1 || true)" 'stop/waiting' 'Upstart status after stop'
	"$daemon_bin" remove "$service_name"
	[[ ! -e "$service_path" ]] || fail 'Upstart job remains after removal'

	current_scenario=automatic-restart
	log 'verifying configured failure and hard-crash respawn'
	install_scenario 5s "$auto_events" --stop-after 5s
	verify_definition
	"$daemon_bin" start "$service_name"
	wait_for_http >"$state_dir/automatic-restart-first-http.json"
	old_pid=$(http_pid)
	new_pid=$(wait_for_new_http_pid "$old_pid" 45)
	[[ "$(event_count "$auto_events" started)" -ge 2 ]] || fail 'configured failure did not respawn the app'
	assert_event "$auto_events" failure
	log "configured failure respawned PID $old_pid as $new_pid"

	kill -KILL "$new_pid"
	hard_pid=$(wait_for_new_http_pid "$new_pid" 30)
	wait_process_gone "$new_pid"
	[[ "$(event_count "$auto_events" started)" -ge 3 ]] || fail 'hard crash did not record another app start'
	verify_management_commands
	wait_for_http >"$state_dir/hard-crash-http.json"
	log "hard crash respawned PID $new_pid as $hard_pid"

	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"
	current_scenario=cleanup
	collect_artifacts success
	[[ ! -e "$service_path" ]] || fail 'Upstart job remains after final removal'
	[[ ! -e "$metadata_path" ]] || fail 'metadata remains after final removal'
	assert_no_test_app_processes
	log 'all Upstart application-level tests passed'
}

require_environment
case "$phase" in
	pre-reboot)
		[[ -n "$service_name" ]] || fail 'service name is required'
		pre_reboot
		;;
	post-reboot)
		[[ -n "$service_name" ]] || fail 'service name is required'
		post_reboot
		;;
	cleanup)
		cleanup_service
		;;
	*)
		fail "usage: $0 {pre-reboot|post-reboot|cleanup} SERVICE_NAME [PORT]"
		;;
esac
