#!/bin/sh

set -eu

phase=${1:?phase is required}
service_name=${2:?service name is required}
port=${3:-18080}
install_dir=/opt/daemon-itest
registration_name="lz_lz_${service_name}"
service_path="/etc/init.d/S90$registration_name"
app="$install_dir/test-app"
fixture="$install_dir/relative-path-test.txt"
events="$install_dir/buildroot-events.jsonl"

log() {
	printf '[buildroot-itest] %s\n' "$*"
}

fail() {
	printf '[buildroot-itest] ERROR: %s\n' "$*" >&2
	exit 1
}

wait_for_http() {
	attempt=0
	while [ "$attempt" -lt 30 ]; do
		if output=$(wget -q -O - "http://127.0.0.1:$port/" 2>/dev/null); then
			printf '%s\n' "$output" | grep -F '"executable": "/opt/daemon-itest/test-app"' >/dev/null || fail 'unexpected executable in HTTP response'
			printf '%s\n' "$output" | grep -F 'daemon-util relative path test passed' >/dev/null || fail 'working directory fixture was not read'
			return
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail 'test application did not become ready'
}

http_pid() {
	wget -q -O - "http://127.0.0.1:$port/" 2>/dev/null |
		sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
}

wait_for_new_pid() {
	old_pid=$1
	attempt=0
	while [ "$attempt" -lt 60 ]; do
		new_pid=$(http_pid || true)
		if [ -n "$new_pid" ] && [ "$new_pid" != "$old_pid" ]; then
			printf '%s\n' "$new_pid"
			return
		fi
		sleep 1
		attempt=$((attempt + 1))
	done
	fail "watchdog did not restart application PID $old_pid"
}

event_count() {
	grep -Fc "\"event\":\"$1\"" "$events" 2>/dev/null || true
}

require_buildroot() {
	grep -F '/etc/init.d/S??*' /etc/init.d/rcS >/dev/null || fail 'Buildroot rcS startup pattern is missing'
	command -v start-stop-daemon >/dev/null || fail 'start-stop-daemon is missing'
	[ -x "$install_dir/daemon" ] || fail 'daemon binary is missing'
	[ -x "$app" ] || fail 'test app binary is missing'
	[ -f "$fixture" ] || fail 'fixture is missing'
}

case "$phase" in
	pre-reboot)
		require_buildroot
		log 'installing Buildroot service'
		rm -f "$events"
		"$install_dir/daemon" install --stop-timeout 5s "$service_name" "$app" \
			--enabled=true --message 'hello buildroot' --count 7 --port "$port" \
			--file-path relative-path-test.txt --stop-after 8s --event-path "$events"
		[ -x "$service_path" ] || fail 'Buildroot S90 service script was not installed'
		"$service_path" start
		wait_for_http
		"$install_dir/daemon" status "$service_name" | grep -qi running || fail 'daemon status does not report running'
		old_pid=$(http_pid)
		[ -n "$old_pid" ] || fail 'could not read initial application PID'
		new_pid=$(wait_for_new_pid "$old_pid")
		[ "$new_pid" != "$old_pid" ] || fail 'watchdog reused the failed application PID'
		[ "$(event_count failure)" -ge 1 ] || fail 'application failure event was not recorded'
		[ "$(event_count started)" -ge 2 ] || fail 'watchdog did not record a second application start'
		wait_for_http
		log "watchdog restarted application from PID $old_pid to $new_pid"
		log 'pre-reboot checks passed'
		;;
	post-reboot)
		require_buildroot
		[ -x "$service_path" ] || fail 'Buildroot S90 service disappeared after reboot'
		wait_for_http
		"$install_dir/daemon" restart "$service_name"
		wait_for_http
		"$install_dir/daemon" stop "$service_name"
		if "$service_path" status >/dev/null 2>&1; then
			fail 'service still reports running after stop'
		fi
		"$install_dir/daemon" start "$service_name"
		wait_for_http
		"$install_dir/daemon" remove "$service_name"
		[ ! -e "$service_path" ] || fail 'Buildroot S90 service script remains after remove'
		log 'post-reboot checks passed'
		;;
	*)
		fail "unknown phase: $phase"
		;;
esac
