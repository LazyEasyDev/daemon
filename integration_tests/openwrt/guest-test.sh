#!/bin/sh

set -eu

phase=${1:-}
service_name=${2:-}
port=${3:-18080}
install_dir=${DAEMON_ITEST_INSTALL_DIR:-/opt/daemon-itest}
daemon_bin="$install_dir/daemon"
app_bin="$install_dir/test-app"
registration_name="lz_lz_${service_name}"
service_path="/etc/init.d/$registration_name"
enable_link="/etc/rc.d/S98$registration_name"
metadata_path="/var/lib/daemon-util/services/${registration_name}.json"
state_dir="$install_dir/state-$service_name"
artifact_dir="$state_dir/artifacts"
fixture_path="$install_dir/relative-path-test.txt"
current_scenario=initialization

log() {
	printf '[openwrt-itest] %s\n' "$*"
}

fail() {
	printf '[openwrt-itest] ERROR: %s\n' "$*" >&2
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
	[ -e "/proc/$pid/exe" ] && [ "$(readlink "/proc/$pid/exe" 2>/dev/null || true)" = "$app_bin" ]
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
	for process in /proc/[0-9]*; do
		executable=$(readlink "$process/exe" 2>/dev/null || true)
		if [ "$executable" = "$app_bin" ]; then
			fail "test application process ${process##*/} leaked after cleanup"
		fi
	done
}

http_response() {
	uclient-fetch -qO- "http://127.0.0.1:$port/"
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
			assert_file_contains "$output_path" '"message": "hello openwrt"'
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
	http_response 2>/dev/null | sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
}

wait_for_new_http_pid() {
	old_pid=$1
	timeout_seconds=$2
	attempt=0
	while [ "$attempt" -lt "$timeout_seconds" ]; do
		new_pid=$(http_pid || true)
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
		cat /etc/openwrt_release
		printf '\nPID 1:\n'
		cat /proc/1/comm
	} >"$artifact_dir/${label}-environment.txt" 2>&1 || true
	"$service_path" status >"$artifact_dir/${label}-status.txt" 2>&1 || true
	ubus call service list "{\"name\":\"$registration_name\"}" >"$artifact_dir/${label}-ubus.json" 2>&1 || true
	if [ -f "$service_path" ]; then
		cp -f "$service_path" "$artifact_dir/${label}-service-script"
	fi
	ps w >"$artifact_dir/${label}-processes.txt" 2>&1 || true
	cp -f "$install_dir"/*-events.jsonl "$artifact_dir/" 2>/dev/null || true
	cp -f "$state_dir"/*.json "$state_dir"/*.pid "$artifact_dir/" 2>/dev/null || true
}

cleanup_service() {
	if [ -f "$service_path" ]; then
		"$daemon_bin" remove "$service_name" >/dev/null 2>&1 || {
			"$service_path" stop >/dev/null 2>&1 || true
			"$service_path" disable >/dev/null 2>&1 || true
			rm -f "$service_path" "$enable_link"
		}
	fi
	rm -f "$metadata_path"
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
	grep -qi openwrt /etc/os-release || fail "guest is not OpenWrt"
	[ -x "$daemon_bin" ] || fail "missing daemon binary at $daemon_bin"
	[ -x "$app_bin" ] || fail "missing test application at $app_bin"
	[ -f "$fixture_path" ] || fail "missing relative-path fixture at $fixture_path"
	[ -x /etc/rc.common ] || fail "/etc/rc.common is missing"
	command -v procd >/dev/null || fail "procd is required in the guest"
	command -v ubus >/dev/null || fail "ubus is required in the guest"
	command -v uclient-fetch >/dev/null || fail "uclient-fetch is required in the guest"
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
		--message "hello openwrt" \
		--count 7 \
		--port "$port" \
		--file-path relative-path-test.txt \
		--event-path "$events" \
		"$@"
}

verify_definition() {
	[ -x "$service_path" ] || fail "OpenWrt service script was not created as executable"
	assert_file_contains "$service_path" '#!/bin/sh /etc/rc.common'
	assert_file_contains "$service_path" "PROG='$app_bin'"
	assert_file_contains "$service_path" "WORKING_DIRECTORY='$install_dir'"
	assert_file_contains "$service_path" 'USE_PROCD=1'
	assert_file_contains "$service_path" 'procd_set_param respawn 0 30 0'
	assert_file_contains "$service_path" "procd_set_param term_timeout \"\$STOP_TIMEOUT\""
	[ -e "$enable_link" ] || fail "OpenWrt boot enablement link was not created"
}

verify_management_commands() {
	expect_metadata=$1
	output=$("$daemon_bin" status "$service_name")
	assert_contains "$output" 'running' 'status command'
	output=$("$daemon_bin" list)
	assert_contains "$output" "$service_name" 'list command'
	if [ "$expect_metadata" = true ]; then
		assert_contains "$output" "$app_bin" 'list command application path'
		output=$("$daemon_bin" list -l)
		assert_contains "$output" 'hello openwrt' 'long list arguments'
	fi
}

pre_reboot() {
	events="$install_dir/boot-events.jsonl"
	current_scenario=pre-reboot
	log "installing boot-persistence scenario"
	cleanup_service
	install_scenario 5s "$events" --stop_delay 1s
	verify_definition
	assert_file_contains "$service_path" 'STOP_TIMEOUT=5'
	"$daemon_bin" start "$service_name"
	wait_for_http "$state_dir/pre-reboot-http.json"
	verify_management_commands true
	parent_pid=$(http_pid)
	[ -n "$parent_pid" ] || fail "could not read application PID"
	process_is_test_app "$parent_pid" || fail "application process $parent_pid is not running"
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
	[ -e "$enable_link" ] || fail "service lost boot enablement after reboot"
	wait_for_http "$state_dir/post-reboot-http.json"
	[ "$(event_count "$boot_events" started)" -ge 2 ] || fail "service did not record a second startup after reboot"
	verify_management_commands false

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
	status_output=$("$service_path" status 2>&1 || true)
	case "$status_output" in
		*inactive*|*"not running"*) ;;
		*) fail "unexpected OpenWrt status after graceful stop: $status_output" ;;
	esac
	"$daemon_bin" remove "$service_name"
	[ ! -e "$service_path" ] || fail "service script remains after graceful scenario removal"
	[ ! -e "$enable_link" ] || fail "enablement link remains after graceful scenario removal"

	current_scenario=automatic-restart
	log "verifying restart after application failure"
	install_scenario 5s "$auto_events" --stop-after 5s
	"$daemon_bin" start "$service_name"
	wait_for_http "$state_dir/automatic-restart-first-http.json"
	restart_parent=$(http_pid)
	new_parent=$(wait_for_new_http_pid "$restart_parent" 50)
	[ "$new_parent" != "$restart_parent" ] || fail "automatic restart reused parent PID $restart_parent"
	[ "$(event_count "$auto_events" started)" -ge 2 ] || fail "automatic restart did not record a second startup"
	assert_event "$auto_events" failure
	"$daemon_bin" stop "$service_name"
	"$daemon_bin" remove "$service_name"

	current_scenario=forced-stop
	log "verifying timeout escalation"
	install_scenario 2s "$forced_events" --stop_delay 30s
	assert_file_contains "$service_path" 'STOP_TIMEOUT=2'
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
	[ ! -e "$enable_link" ] || fail "enablement link remains after final removal"
	[ ! -e "$metadata_path" ] || fail "metadata remains after final removal"
	assert_no_test_app_processes
	log "all OpenWrt application-level tests passed"
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
