#!/bin/sh

set -u

install_dir=/opt/daemon-itest
state_dir=/var/lib/daemon-itest
guest_test="$install_dir/guest-test.sh"
config="$install_dir/test-config"
phase_file="$state_dir/yocto-phase"
result_file="$state_dir/yocto-result"
full_log="$state_dir/yocto-test.log"
phase_log="$state_dir/yocto-phase.log"
artifact_archive="$state_dir/yocto-artifacts.tar"
lock=/var/run/daemon-itest-boot.lock

[ -f "$config" ] || exit 0
. "$config"

if ! mkdir "$lock" 2>/dev/null; then
	exit 0
fi

phase=$(cat "$phase_file" 2>/dev/null || printf '%s' pre-reboot)
printf '[yocto-boot] running %s\n' "$phase"

if "$guest_test" "$phase" "$SERVICE_NAME" "$TEST_APP_PORT" >"$phase_log" 2>&1; then
	status=0
else
	status=$?
fi
cat "$phase_log"
cat "$phase_log" >>"$full_log"
if [ -d "/var/tmp/daemon-itest-$SERVICE_NAME/artifacts" ]; then
	tar -cf "$artifact_archive" -C "/var/tmp/daemon-itest-$SERVICE_NAME/artifacts" . 2>/dev/null || true
fi

if [ "$status" -ne 0 ]; then
	printf 'FAIL phase=%s status=%s\n' "$phase" "$status" >"$result_file"
	sync
	printf 'DAEMON_ITEST_FAIL phase=%s status=%s\n' "$phase" "$status"
	sleep 1
	poweroff -f
	exit "$status"
fi

case "$phase" in
	pre-reboot)
		printf '%s\n' post-reboot >"$phase_file"
		sync
		printf 'DAEMON_ITEST_REBOOT\n'
		sleep 1
		reboot -f
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
		printf 'DAEMON_ITEST_FAIL unexpected-phase=%s\n' "$phase"
		sync
		poweroff -f
		exit 1
		;;
esac
