#!/bin/sh

set -eu

phase=${1:-}
service_name=${2:-}
port=${3:-18080}
install_dir=${DAEMON_ITEST_INSTALL_DIR:-/opt/daemon-itest}
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
registration_name="lz_lz_${service_name}"
service_path="/usr/local/etc/rc.d/$registration_name"
metadata_path="/var/db/daemon-util/services/${registration_name}.json"
supervisor_pidfile="/var/run/$registration_name.pid"
child_pidfile="/var/run/$registration_name.child.pid"
state_dir="/var/tmp/daemon-itest-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[freebsd-itest] %s\n' "$*"
}

fail() {
	printf '[freebsd-itest] ERROR: %s\n' "$*" >&2
	return 1
}

assert_contains() {
	value=$1
	expected=$2
	description=$3
	case "$value" in
		*"$expected"*) ;;
		*) fail "$description: expected output to contain '$expected', got '$value'" ;;
	esac
}

assert_file_contains() {
	path=$1
	expected=$2
	grep -Fq -- "$expected" "$path" || fail "$path does not contain '$expected'"
}

process_is_test_app() {
	pid=$1
	command=$(ps -p "$pid" -o command= 2>/dev/null || true)
	case "$command" in
		"$app_bin"|"$app_bin "*) return 0 ;;
		*) return 1 ;;
	esac
}

wait_process_gone() {
	pid=$1
	attempt=0
	while process_is_test_app "$pid"; do
		if [ "$attempt" -ge 15 ]; then
			fail "test application process $pid is still running"
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
}

assert_no_test_app_processes() {
	if pgrep -f "^$app_bin\([[:space:]]\|$\)" >/dev/null 2>&1; then
		fail "test application process leaked after cleanup"
	fi
}

http_response() {
	fetch -qo - "http://127.0.0.1:$port/"
}

wait_for_http() {
	output_path=$1
	attempt=0
	while [ "$attempt" -lt 30 ]; do
		if response=$(http_response 2>/dev/null); then
			printf '%s\n' "$response" >"$output_path"
			assert_file_contains "$output_path" "\"executable\": \"$app_bin\""
			assert_file_contains "$output_path" '"file_content": "daemon-util relative path test passed\n"'
			assert_file_contains "$output_path" '"enabled": true'
			assert_file_contains "$output_path" '"message": "hello freebsd"'
			assert_file_contains "$output_path" '"count": 7'
			assert_file_contains "$output_path" "\"port\": $port"
			return 0
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail "HTTP application did not become ready"
}

http_pid() {
	http_response | sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
}

wait_for_new_http_pid() {
	old_pid=$1
	timeout_seconds=$2
	attempt=0
	while [ "$attempt" -lt "$timeout_seconds" ]; do
		new_pid=$(http_pid 2>/dev/null || true)
		if [ -n "$new_pid" ] && [ "$new_pid" != "$old_pid" ]; then
			printf '%s\n' "$new_pid"
			return 0
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail "application did not restart from PID $old_pid"
}

assert_event() {
	path=$1
	expected=$2
	grep -Fq "\"event\":\"$expected\"" "$path" || fail "event '$expected' not found in $path"
}

event_count() {
	path=$1
	expected=$2
	grep -Fc "\"event\":\"$expected\"" "$path" || true
}

collect_artifacts() {
	label=${1:-$current_scenario}
	mkdir -p "$artifact_dir"
	{
		printf 'phase=%s\nscenario=%s\nservice=%s\n' "$phase" "$current_scenario" "$service_name"
		uname -a
		freebsd-version -ku
		printf '\nPID 1:\n'
		ps -p 1 -o pid=,command=
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	service "$registration_name" status >"$artifact_dir/${label}-status.txt" 2>&1 || true
	service -e >"$artifact_dir/${label}-enabled-services.txt" 2>&1 || true
	sysrc -a >"$artifact_dir/${label}-sysrc.txt" 2>&1 || true
	if [ -f "$service_path" ]; then
		cp -f "$service_path" "$artifact_dir/${label}-service-script"
	fi
	ps auxww >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
	chmod -R a+rX "$artifact_dir" 2>/dev/null || true
}

cleanup_service() {
	if [ -f "$service_path" ]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			service "$registration_name" stop >/dev/null 2>&1 || true
			service "$registration_name" disable >/dev/null 2>&1 || true
			rm -f "$service_path"
		}
	fi
	rm -f "$metadata_path" "$supervisor_pidfile" "$child_pidfile"
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
	[ "$(id -u)" -eq 0 ] || fail "guest test must run as root"
	[ "$(uname -s)" = FreeBSD ] || fail "guest test requires FreeBSD"
	[ -x "$daemon_bin" ] || fail "missing daemon binary at $daemon_bin"
	[ -x "$app_bin" ] || fail "missing test application at $app_bin"
	[ -f "$fixture_path" ] || fail "missing relative-path fixture at $fixture_path"
	[ -x /usr/sbin/daemon ] || fail "/usr/sbin/daemon is missing"
	command -v fetch >/dev/null || fail "fetch is required in the guest"
	command -v pgrep >/dev/null || fail "pgrep is required in the guest"
	mkdir -p "$state_dir" "$artifact_dir"
}

install_scenario() {
	timeout=$1
	events=$2
	shift 2
	rm -f "$events"
	"$daemon_bin" install --stop-timeout "$timeout" --ignore-warnings \
		"$service_name" "$app_bin" \
		--enabled=true \
		--message "hello freebsd" \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[ -x "$service_path" ] || fail "FreeBSD rc.d service script was not created as executable"
	assert_file_contains "$service_path" '#!/bin/sh'
	assert_file_contains "$service_path" "app_command='$app_bin'"
	assert_file_contains "$service_path" "app_directory='$install_dir'"
	assert_file_contains "$service_path" 'command="/usr/sbin/daemon"'
	assert_file_contains "$service_path" "\"\$command\" -R 30 -P \"\$pidfile\" -p \"\$child_pidfile\" -f \"\$app_command\""
	service "$registration_name" enabled
}

verify_management_commands() {
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" 'running' 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	assert_contains "$output" "$app_bin" 'list command application path'
	output=$("$daemon_bin" list -l)
	assert_contains "$output" 'hello freebsd' 'long list arguments'
}

pre_reboot() {
	events="$install_dir/boot-events.jsonl"
	current_scenario=pre-reboot
	log "installing boot-persistence scenario"
	cleanup_service
	install_scenario 5s "$events" --stop_delay 1s
	verify_definition
	assert_file_contains "$service_path" 'stop_timeout=5'
	"$daemon_bin" start "$service_name"
	wait_for_http "$state_dir/pre-reboot-http.json"
	verify_management_commands
	parent_pid=$(http_pid)
	[ -n "$parent_pid" ] || fail "could not read application PID"
	process_is_test_app "$parent_pid" || fail "application process $parent_pid is not running"
	[ -s "$supervisor_pidfile" ] || fail "supervisor PID file was not created"
	[ -s "$child_pidfile" ] || fail "child PID file was not created"
	printf '%s\n' "$parent_pid" >"$state_dir/pre-reboot-parent.pid"
	collect_artifacts pre-reboot
	log "pre-reboot checks passed"
}

post_reboot() {
	boot_events="$install_dir/boot-events.jsonl"
	auto_events="$install_dir/restart-events.jsonl"
	forced_events="$install_dir/forced-events.jsonl"

	current_scenario=post-reboot
	log "verifying boot persistence"
	service "$registration_name" enabled
	wait_for_http "$state_dir/post-reboot-http.json"
	[ "$(event_count "$boot_events" started)" -ge 2 ] || fail "service did not record a second startup after reboot"
	verify_management_commands

	current_scenario=explicit-restart
	restart_parent=$(http_pid)
	"$daemon_bin" restart "$service_name"
	new_parent=$(wait_for_new_http_pid "$restart_parent" 20)
	[ "$new_parent" != "$restart_parent" ] || fail "restart reused parent PID $restart_parent"
	wait_for_http "$state_dir/restart-http.json"
	wait_process_gone "$restart_parent"
	assert_event "$boot_events" signal
	assert_event "$boot_events" stopped

	current_scenario=graceful-stop
	graceful_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	graceful_elapsed=$(($(date +%s) - graceful_started))
	[ "$graceful_elapsed" -ge 1 ] || fail "graceful stop returned before the configured one-second delay"
	[ "$graceful_elapsed" -lt 10 ] || fail "graceful stop took ${graceful_elapsed}s"
	assert_event "$boot_events" stopped
	assert_contains "$(service "$registration_name" status 2>&1 || true)" 'not running' 'FreeBSD status after graceful stop'
	"$daemon_bin" remove "$service_name"
	[ ! -e "$service_path" ] || fail "service script remains after graceful scenario removal"

	current_scenario=automatic-restart
	log "verifying restart after application failure"
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http "$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 60)
	[ "$new_parent" != "$restart_parent" ] || fail "automatic restart reused parent PID $restart_parent"
	[ "$(event_count "$auto_events" started)" -ge 2 ] || fail "automatic restart did not record a second startup"
	assert_event "$auto_events" failure
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log "verifying timeout escalation"
	install_scenario 2s "$forced_events" --stop_delay 30s
	assert_file_contains "$service_path" 'stop_timeout=2'
	"$daemon_bin" start "$service_name"
	wait_for_http "$state_dir/forced-stop-http.json"
	forced_parent=$(http_pid)
	forced_started=$(date +%s)
	"$daemon_bin" stop "$service_name"
	forced_elapsed=$(($(date +%s) - forced_started))
	[ "$forced_elapsed" -ge 2 ] || fail "forced stop returned before the configured timeout"
	[ "$forced_elapsed" -lt 15 ] || fail "forced stop took ${forced_elapsed}s"
	assert_event "$forced_events" signal
	if grep -Fq '"event":"stopped"' "$forced_events"; then
		fail "application reported graceful completion despite forced termination"
	fi
	wait_process_gone "$forced_parent"
	assert_no_test_app_processes
	"$daemon_bin" remove "$service_name"

	current_scenario=cleanup
	collect_artifacts success
	[ ! -e "$service_path" ] || fail "service script remains after final removal"
	[ ! -e "$metadata_path" ] || fail "metadata remains after final removal"
	[ ! -e "$supervisor_pidfile" ] || fail "supervisor PID file remains after final removal"
	[ ! -e "$child_pidfile" ] || fail "child PID file remains after final removal"
	assert_no_test_app_processes
	log "all FreeBSD application-level tests passed"
}

require_environment
case "$phase" in
	pre-reboot)
		[ -n "$service_name" ] || fail "service name is required"
		pre_reboot
		;;
	post-reboot)
		[ -n "$service_name" ] || fail "service name is required"
		post_reboot
		;;
	cleanup)
		cleanup_service
		;;
	*)
		fail "usage: $0 {pre-reboot|post-reboot|cleanup} SERVICE_NAME [PORT]"
		;;
esac
