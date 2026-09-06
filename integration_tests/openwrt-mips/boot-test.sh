#!/bin/sh /etc/rc.common

START=99
STOP=1

run_integration_test() {
	install_dir=/opt/daemon-itest
	state_dir=/var/lib/daemon-itest
	guest_test="$install_dir/guest-test.sh"
	config="$install_dir/test-config"
	state_device=/dev/sdb
	lock=/var/run/daemon-itest-boot.lock

	[ -f "$config" ] || return 0
	. "$config"

	mkdir -p "$state_dir"
	if ! mount -t ext4 "$state_device" "$state_dir"; then
		printf 'DAEMON_ITEST_FAIL state-disk-mount\n'
		poweroff -f
		return 1
	fi
	phase_file="$state_dir/$STATE_PREFIX-phase"
	result_file="$state_dir/$STATE_PREFIX-result"
	full_log="$state_dir/$STATE_PREFIX-test.log"
	phase_log="$state_dir/$STATE_PREFIX-phase.log"
	artifact_archive="$state_dir/$STATE_PREFIX-artifacts.tar"
	service_state="$state_dir/service-state"
	mkdir -p "$service_state"
	rm -rf "$install_dir/state-$SERVICE_NAME"
	ln -s "$service_state" "$install_dir/state-$SERVICE_NAME"

	if ! mkdir "$lock" 2>/dev/null; then
		return 0
	fi

	phase=$(cat "$phase_file" 2>/dev/null || printf '%s' pre-reboot)
	printf '[openwrt-mips-boot] running %s\n' "$phase"

	if "$guest_test" "$phase" "$SERVICE_NAME" "$TEST_APP_PORT" >"$phase_log" 2>&1; then
		status=0
	else
		status=$?
	fi
	cat "$phase_log"
	cat "$phase_log" >>"$full_log"
	if [ -d "$service_state/artifacts" ]; then
		tar -cf "$artifact_archive" -C "$service_state/artifacts" . 2>/dev/null || true
	fi

	if [ "$status" -ne 0 ]; then
		printf 'FAIL phase=%s status=%s\n' "$phase" "$status" >"$result_file"
		sync
		printf 'DAEMON_ITEST_FAIL phase=%s status=%s\n' "$phase" "$status"
		sleep 1
		poweroff -f
		return "$status"
	fi

	case "$phase" in
	pre-reboot)
		printf '%s\n' post-reboot >"$phase_file"
		sync
		printf 'DAEMON_ITEST_REBOOT\n'
		sleep 1
		poweroff -f
		;;
	post-reboot)
		printf '%s\n' PASS >"$result_file"
		printf '%s\n' complete >"$phase_file"
		sync
		printf 'DAEMON_ITEST_PASS\n'
		sleep 1
		poweroff -f
		;;
	*)
		printf 'FAIL unexpected-phase=%s\n' "$phase" >"$result_file"
		sync
		printf 'DAEMON_ITEST_FAIL unexpected-phase=%s\n' "$phase"
		poweroff -f
		return 1
		;;
	esac
}

boot() {
	run_integration_test
}

start() {
	run_integration_test
}
