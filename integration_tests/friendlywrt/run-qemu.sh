#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

friendlywrt_release=${FRIENDLYWRT_RELEASE:-2026-08-07}
archive_name=${FRIENDLYWRT_ARCHIVE_NAME:-images-R5S-R5C-Series-FriendlyWrt-25.12.tgz}
archive_url=${FRIENDLYWRT_ARCHIVE_URL:-https://github.com/friendlyarm/Actions-FriendlyWrt/releases/download/FriendlyWrt-${friendlywrt_release}/$archive_name}
archive_sha256=${FRIENDLYWRT_ARCHIVE_SHA256:-a9dcc562e16bab1d3695f64566ff77c41e648ffa122bfcfe471dd61d9af225b4}
archive_root=${FRIENDLYWRT_ARCHIVE_ROOT:-friendlywrt25-rk3568}
vm_memory_mib=${VM_MEMORY_MIB:-768}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-900}
state_size_mib=${FRIENDLYWRT_STATE_SIZE_MIB:-128}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
tool_root=${FRIENDLYWRT_TOOL_ROOT:-/var/tmp/daemon-friendlywrt-tools}
tool_packages=${FRIENDLYWRT_TOOL_PACKAGES:-/var/tmp/daemon-friendlywrt-packages}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-friendlywrt$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-friendlywrt-itest-$run_id}
artifact_dir="$artifact_root/friendlywrt-$run_id"
archive=${FRIENDLYWRT_ARCHIVE:-$cache_dir/$archive_name}
qemu_pid=

log() {
	printf '[friendlywrt-qemu] %s\n' "$*"
}

fail() {
	printf '[friendlywrt-qemu] ERROR: %s\n' "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in apt-get debugfs dpkg-deb e2fsck file go mkfs.ext4 qemu-system-aarch64 sha256sum tar truncate wget; do
	require_command "$command"
done

host_arch=$(uname -m)
if [[ -n "${QEMU_ACCEL:-}" ]]; then
	qemu_accel=$QEMU_ACCEL
elif [[ "$host_arch" =~ ^(aarch64|arm64)$ && -r /dev/kvm ]]; then
	qemu_accel=kvm
else
	qemu_accel=tcg
fi
if [[ "$qemu_accel" == kvm ]]; then
	cpu=host
else
	cpu=cortex-a57
	log 'KVM is unavailable or disabled; using QEMU software emulation with Cortex-A57 compatibility'
fi

mkdir -p "$cache_dir" "$artifact_dir" "$work_dir" "$tool_root" "$tool_packages"
chmod 0755 "$cache_dir" "$work_dir"

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

if [[ ! -f "$archive" && -n "${FRIENDLYWRT_ARCHIVE:-}" ]]; then
	fail "FRIENDLYWRT_ARCHIVE does not exist: $archive"
fi
if [[ -f "$archive" ]] && ! sha256_matches "$archive" "$archive_sha256"; then
	log "discarding cached archive with stale checksum: $archive"
	rm -f "$archive"
fi
if [[ ! -f "$archive" ]]; then
	temporary_archive="$archive.partial"
	log 'downloading official NanoPi R5S/R5C FriendlyWrt image archive'
	rm -f "$temporary_archive"
	wget --progress=dot:giga -O "$temporary_archive" "$archive_url"
	sha256_matches "$temporary_archive" "$archive_sha256" || fail 'FriendlyWrt archive SHA-256 verification failed'
	mv "$temporary_archive" "$archive"
fi
sha256_matches "$archive" "$archive_sha256" || fail 'cached FriendlyWrt archive SHA-256 verification failed'
printf '%s  %s\n' "$archive_sha256" "$archive_name" >"$artifact_dir/base-image.sha256"
cat >"$artifact_dir/image-source.txt" <<EOF
FriendlyWrt archive: $archive_url
Board family: NanoPi R5S/R5C
Vendor target: RK3568
EOF

install_tool_package() {
	local package=$1 pattern=$2 deb
	if ! compgen -G "$tool_packages/$pattern" >/dev/null; then
		(cd "$tool_packages" && apt-get download "$package")
	fi
	for deb in "$tool_packages"/$pattern; do
		dpkg-deb -x "$deb" "$tool_root"
	done
}

prepare_sparse_tools() {
	local libdir="$tool_root/usr/lib/aarch64-linux-gnu/android"
	if [[ ! -x "$tool_root/usr/bin/simg2img" ]]; then
		install_tool_package android-sdk-libsparse-utils 'android-sdk-libsparse-utils_*.deb'
	fi
	for package in android-libbase android-liblog android-libsparse; do
		if ! compgen -G "$tool_packages/${package}_*.deb" >/dev/null; then
			install_tool_package "$package" "${package}_*.deb"
		else
			for deb in "$tool_packages"/${package}_*.deb; do
				dpkg-deb -x "$deb" "$tool_root"
			done
		fi
	done
	LD_LIBRARY_PATH="$libdir" "$tool_root/usr/bin/simg2img" --help >/dev/null 2>&1 || true
	[[ -f "$libdir/libbase.so.0" && -f "$libdir/liblog.so.0" && -f "$libdir/libsparse.so.0" ]] || fail 'sparse-image converter libraries are incomplete'
}

prepare_sparse_tools
sparse_rootfs="$work_dir/rootfs.sparse.img"
vm_rootfs="$work_dir/rootfs.ext4"
log 'extracting the official FriendlyWrt sparse root filesystem'
tar -xOf "$archive" "$archive_root/rootfs.img" >"$sparse_rootfs"
[[ "$(file -b "$sparse_rootfs")" == Android\ sparse\ image* ]] || fail 'FriendlyWrt rootfs is not an Android sparse image'
LD_LIBRARY_PATH="$tool_root/usr/lib/aarch64-linux-gnu/android" \
	"$tool_root/usr/bin/simg2img" "$sparse_rootfs" "$vm_rootfs"
rm -f "$sparse_rootfs"
[[ "$(file -b "$vm_rootfs")" == Linux\ rev\ 1.0\ ext4\ filesystem* ]] || fail 'converted FriendlyWrt rootfs is not ext4'

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/pre-boot-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "FriendlyWrt root filesystem check failed with status $fsck_status"

release_info=$(debugfs -R 'cat /etc/openwrt_release' "$vm_rootfs" 2>/dev/null || true)
grep -Fq "DISTRIB_ID='OpenWrt'" <<<"$release_info" || fail 'FriendlyWrt rootfs does not identify its OpenWrt base'
grep -Fq "DISTRIB_TARGET='rockchip/armv8'" <<<"$release_info" || fail 'FriendlyWrt rootfs is not the expected Rockchip ARM64 target'
debugfs -R 'stat /sbin/procd' "$vm_rootfs" 2>/dev/null | grep -Fq 'Type: regular' || fail 'FriendlyWrt rootfs is missing procd'

kernel_image=${FRIENDLYWRT_QEMU_KERNEL:-$cache_dir/yocto-5.0.19-Image-qemuarm64.bin}
[[ -s "$kernel_image" ]] || fail "QEMU ARM64 kernel is missing: $kernel_image"

build_dir="$work_dir/build"
mkdir -p "$build_dir"
log 'building Linux/arm64 integration binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/daemon" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/test-app" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$repo_dir/integration_tests/openwrt/guest-test.sh" "$build_dir/guest-test.sh"
cp "$repo_dir/integration_tests/interpreted-app-test.sh" "$build_dir/interpreted-app-test.sh"
cp "$script_dir/boot-test.sh" "$build_dir/boot-test.sh"
cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
EOF
cat >"$build_dir/source-info" <<EOF
FriendlyWrt release: $friendlywrt_release
FriendlyWrt archive: $archive_name
Board family: NanoPi R5S/R5C
Vendor target: RK3568
Root filesystem: official FriendlyWrt rootfs.img converted from Android sparse ext4
Boot kernel: generic qemuarm64 test kernel (guest userspace remains unmodified FriendlyWrt)
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/interpreted-app-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config" "$build_dir/source-info"

write_file() {
	local source=$1 destination=$2 mode=$3
	debugfs -w -R "rm $destination" "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
	debugfs -w -R "write $source $destination" "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1
	debugfs -w -R "set_inode_field $destination mode $mode" "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1
}

: >"$artifact_dir/debugfs.log"
for directory in /opt /opt/daemon-itest /var/lib/daemon-itest; do
	debugfs -w -R "mkdir $directory" "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
done
write_file "$build_dir/daemon" /opt/daemon-itest/daemon 0100755
write_file "$build_dir/test-app" /opt/daemon-itest/test-app 0100755
write_file "$build_dir/guest-test.sh" /opt/daemon-itest/guest-test.sh 0100755
write_file "$build_dir/interpreted-app-test.sh" /opt/daemon-itest/interpreted-app-test.sh 0100755
write_file "$build_dir/relative-path-test.txt" /opt/daemon-itest/relative-path-test.txt 0100644
write_file "$build_dir/test-config" /opt/daemon-itest/test-config 0100644
write_file "$build_dir/source-info" /etc/daemon-itest-friendlywrt-source 0100644
write_file "$build_dir/boot-test.sh" /etc/init.d/daemon-itest-boot 0100755
debugfs -w -R 'rm /etc/rc.d/S99daemon-itest-boot' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
debugfs -w -R 'symlink /etc/rc.d/S99daemon-itest-boot ../init.d/daemon-itest-boot' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1

for path in /sbin/procd /etc/rc.common /opt/daemon-itest/daemon /opt/daemon-itest/test-app /opt/daemon-itest/guest-test.sh /etc/init.d/daemon-itest-boot /etc/rc.d/S99daemon-itest-boot; do
	debugfs -R "stat $path" "$vm_rootfs" >>"$artifact_dir/injected-files.txt" 2>&1
done

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/prepared-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "prepared FriendlyWrt filesystem check failed with status $fsck_status"
chmod 0644 "$vm_rootfs" "$kernel_image"

serial_log="$artifact_dir/serial.log"
qemu_log="$artifact_dir/qemu.log"
pidfile="$work_dir/qemu.pid"
state_image="$work_dir/state.ext4"
truncate -s "${state_size_mib}M" "$state_image"
mkfs.ext4 -q -F -L daemon-state "$state_image"
chmod 0644 "$state_image"

cleanup() {
	local status=$?
	trap - EXIT
	if [[ "$qemu_pid" =~ ^[0-9]+$ ]] && kill -0 "$qemu_pid" 2>/dev/null; then
		if [[ "$keep_vm" == 1 ]]; then
			log "keeping QEMU PID $qemu_pid and work directory $work_dir"
		else
			kill "$qemu_pid" >/dev/null 2>&1 || true
			for _ in $(seq 1 20); do
				kill -0 "$qemu_pid" >/dev/null 2>&1 || break
				sleep 1
			done
			kill -9 "$qemu_pid" >/dev/null 2>&1 || true
		fi
	fi
	if [[ "$keep_vm" != 1 ]]; then
		rm -rf "$work_dir" || true
	fi
	if (( status == 0 )); then
		log "artifacts: $artifact_dir"
	else
		log "test failed; artifacts: $artifact_dir" >&2
	fi
	exit "$status"
}
trap cleanup EXIT

qemu_is_running() {
	local state
	kill -0 "$qemu_pid" 2>/dev/null || return 1
	state=$(awk '/^State:/ {print $2; exit}' "/proc/$qemu_pid/status" 2>/dev/null || true)
	[[ "$state" != Z ]]
}

launch_guest() {
	local boot_phase=$1
	local phase_serial="$artifact_dir/serial-$boot_phase.log"
	rm -f "$pidfile" "$phase_serial"
	log "booting official NanoPi FriendlyWrt ARM64 userspace for $boot_phase"
	qemu-system-aarch64 \
		-name daemon-friendlywrt-procd-itest \
		-M virt \
		-accel "$qemu_accel" \
		-cpu "$cpu" \
		-smp "$vm_vcpus" \
		-m "$vm_memory_mib" \
		-kernel "$kernel_image" \
		-append 'root=/dev/vda rootfstype=ext4 rw rootwait console=ttyAMA0,115200 init=/sbin/init swiotlb=0 panic=1' \
		-drive "id=rootdisk,file=$vm_rootfs,if=none,format=raw" \
		-device virtio-blk-pci,drive=rootdisk \
		-drive "id=statedisk,file=$state_image,if=none,format=raw" \
		-device virtio-blk-pci,drive=statedisk \
		-object rng-random,filename=/dev/urandom,id=rng0 \
		-device virtio-rng-pci,rng=rng0 \
		-netdev user,id=net0 \
		-device virtio-net-pci,netdev=net0 \
		-display none \
		-serial "file:$phase_serial" \
		-monitor none \
		-D "$qemu_log" \
		-daemonize \
		-pidfile "$pidfile"
	qemu_pid=$(cat "$pidfile")
}

wait_for_guest_poweroff() {
	local boot_phase=$1
	local phase_serial="$artifact_dir/serial-$boot_phase.log"
	local deadline=$((SECONDS + vm_boot_timeout)) next_progress=$SECONDS last_marker
	while qemu_is_running; do
		if grep -aqE 'Kernel panic|VFS: Unable to mount root fs|No working init found|DAEMON_ITEST_FAIL' "$phase_serial" 2>/dev/null; then
			fail "FriendlyWrt guest reported a boot or test failure during $boot_phase"
		fi
		if (( SECONDS >= deadline )); then
			fail "timed out waiting for the FriendlyWrt $boot_phase phase after ${vm_boot_timeout}s"
		fi
		if (( SECONDS >= next_progress )); then
			last_marker=$(grep -aE 'DAEMON_ITEST_|\[(friendlywrt-boot|openwrt-itest)\]' "$phase_serial" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
			if [[ -n "$last_marker" ]]; then
				log "waiting for $boot_phase poweroff ($last_marker)"
			else
				log "waiting for FriendlyWrt $boot_phase boot/test ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
			fi
			next_progress=$((SECONDS + 15))
		fi
		sleep 2
	done
	qemu_pid=
}

check_filesystem() {
	local image=$1 output=$2 status=0
	e2fsck -fy "$image" >"$output" 2>&1 || status=$?
	(( status <= 1 )) || fail "filesystem check failed with status $status: $image"
}

read_state_file() {
	local path=$1
	debugfs -R "cat $path" "$state_image" 2>/dev/null || true
}

launch_guest pre-reboot
wait_for_guest_poweroff pre-reboot
check_filesystem "$vm_rootfs" "$artifact_dir/post-pre-reboot-rootfs-e2fsck.log"
check_filesystem "$state_image" "$artifact_dir/post-pre-reboot-state-e2fsck.log"
phase=$(read_state_file /friendlywrt-phase | tr -d '\r\n')
if [[ "$phase" != post-reboot ]]; then
	read_state_file /friendlywrt-test.log >"$artifact_dir/guest-test.log"
	tail -n 240 "$artifact_dir/serial-pre-reboot.log" >&2 || true
	cat "$artifact_dir/guest-test.log" >&2 || true
	fail "FriendlyWrt pre-reboot phase did not complete: ${phase:-missing phase marker}"
fi

launch_guest post-reboot
wait_for_guest_poweroff post-reboot
cat "$artifact_dir/serial-pre-reboot.log" "$artifact_dir/serial-post-reboot.log" >"$serial_log"
qemu_pid=

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/post-test-e2fsck.log" 2>&1 || fsck_status=$?
check_filesystem "$state_image" "$artifact_dir/post-test-state-e2fsck.log"
(( fsck_status <= 1 )) || fail "post-test FriendlyWrt filesystem check failed with status $fsck_status"

result_file="$artifact_dir/result.txt"
guest_log="$artifact_dir/guest-test.log"
artifact_tar="$artifact_dir/guest-artifacts.tar"
debugfs -R "dump /friendlywrt-result $result_file" "$state_image" >/dev/null 2>&1 || true
debugfs -R "dump /friendlywrt-test.log $guest_log" "$state_image" >/dev/null 2>&1 || true
debugfs -R "dump /friendlywrt-artifacts.tar $artifact_tar" "$state_image" >/dev/null 2>&1 || true
if [[ -s "$artifact_tar" ]]; then
	mkdir -p "$artifact_dir/guest"
	tar -C "$artifact_dir/guest" -xf "$artifact_tar"
fi

[[ -f "$result_file" ]] || {
	tail -n 240 "$serial_log" >&2 || true
	fail 'FriendlyWrt guest did not write a result'
}
result=$(tr -d '\r\n' <"$result_file")
[[ "$result" == PASS ]] || {
	tail -n 240 "$serial_log" >&2 || true
	fail "FriendlyWrt guest result: $result"
}
phase=$(read_state_file /friendlywrt-phase | tr -d '\r\n')
[[ "$phase" == complete ]] || fail "FriendlyWrt guest did not complete its post-reboot phase: ${phase:-missing phase marker}"
if grep -aEq 'Kernel panic|DAEMON_ITEST_FAIL' "$serial_log"; then
	fail 'FriendlyWrt serial log contains a failure marker'
fi
grep -Fq "DISTRIB_ID='OpenWrt'" "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify the OpenWrt base'
grep -Fq 'FriendlyWrt release:' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify the FriendlyWrt source'
grep -Fq 'procd' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify procd'

log "NanoPi R5S FriendlyWrt $friendlywrt_release application-level test passed"
