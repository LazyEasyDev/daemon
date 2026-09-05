#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

yocto_release=${YOCTO_RELEASE:-5.0.19}
vm_memory_mib=${VM_MEMORY_MIB:-512}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-600}
rootfs_size_mib=${YOCTO_ROOTFS_SIZE_MIB:-256}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-yocto$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-yocto-itest-$run_id}
artifact_dir="$artifact_root/yocto-$run_id"
qemu_pid=

log() {
	printf '[yocto-qemu] %s\n' "$*"
}

fail() {
	printf '[yocto-qemu] ERROR: %s\n' "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in go qemu-system-aarch64 wget sha256sum debugfs e2fsck resize2fs truncate tar; do
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

mkdir -p "$cache_dir" "$artifact_dir" "$work_dir"
chmod 0755 "$cache_dir" "$work_dir"

base_url=${YOCTO_IMAGE_BASE_URL:-https://downloads.yoctoproject.org/releases/yocto/yocto-${yocto_release}/machines/qemu/qemuarm64}
kernel_name=${YOCTO_KERNEL_NAME:-Image-qemuarm64.bin}
rootfs_name=${YOCTO_ROOTFS_NAME:-core-image-minimal-qemuarm64.rootfs.ext4}
kernel_url=${YOCTO_KERNEL_URL:-$base_url/$kernel_name}
rootfs_url=${YOCTO_ROOTFS_URL:-$base_url/$rootfs_name}
kernel_image=${YOCTO_KERNEL:-$cache_dir/yocto-${yocto_release}-$kernel_name}
base_rootfs=${YOCTO_ROOTFS:-$cache_dir/yocto-${yocto_release}-$rootfs_name}

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

published_checksum() {
	local url=$1 override=$2 output=$3
	if [[ -n "$override" ]]; then
		printf '%s\n' "$override"
		return
	fi
	wget -q -O "$output" "$url.sha256sum"
	awk 'NF >= 1 {print $1; exit}' "$output"
}

download_verified() {
	local url=$1 path=$2 expected=$3
	if [[ -f "$path" ]] && ! sha256_matches "$path" "$expected"; then
		log "discarding cached file with stale checksum: $path"
		rm -f "$path"
	fi
	if [[ ! -f "$path" ]]; then
		local partial="$path.partial"
		log "downloading ${url##*/}"
		rm -f "$partial"
		wget --progress=dot:giga -O "$partial" "$url"
		sha256_matches "$partial" "$expected" || fail "SHA-256 verification failed for $url"
		mv "$partial" "$path"
	fi
	sha256_matches "$path" "$expected" || fail "SHA-256 verification failed for $path"
}

kernel_sha=$(published_checksum "$kernel_url" "${YOCTO_KERNEL_SHA256:-}" "$work_dir/kernel.sha256sum")
rootfs_sha=$(published_checksum "$rootfs_url" "${YOCTO_ROOTFS_SHA256:-}" "$work_dir/rootfs.sha256sum")
[[ "$kernel_sha" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'invalid Yocto kernel SHA-256'
[[ "$rootfs_sha" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'invalid Yocto rootfs SHA-256'

if [[ ! -f "$kernel_image" && -n "${YOCTO_KERNEL:-}" ]]; then
	fail "YOCTO_KERNEL does not exist: $kernel_image"
fi
if [[ ! -f "$base_rootfs" && -n "${YOCTO_ROOTFS:-}" ]]; then
	fail "YOCTO_ROOTFS does not exist: $base_rootfs"
fi
download_verified "$kernel_url" "$kernel_image" "$kernel_sha"
download_verified "$rootfs_url" "$base_rootfs" "$rootfs_sha"
printf '%s  %s\n%s  %s\n' "$kernel_sha" "$kernel_name" "$rootfs_sha" "$rootfs_name" >"$artifact_dir/base-images.sha256"

vm_rootfs="$work_dir/rootfs.ext4"
build_dir="$work_dir/build"
mkdir -p "$build_dir"

log "preparing writable Yocto $yocto_release rootfs"
cp --reflink=auto "$base_rootfs" "$vm_rootfs"
if e2fsck -pf "$vm_rootfs" >/dev/null; then
	fsck_status=0
else
	fsck_status=$?
fi
(( fsck_status <= 1 )) || fail "base rootfs filesystem check failed with status $fsck_status"
truncate -s "${rootfs_size_mib}M" "$vm_rootfs"
resize2fs -f "$vm_rootfs" >/dev/null

log 'building Linux/arm64 integration binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/daemon" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/test-app" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$script_dir/guest-test.sh" "$build_dir/guest-test.sh"
cp "$script_dir/boot-test.sh" "$build_dir/boot-test.sh"
cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config"

debugfs_mkdir() {
	debugfs -w -R "mkdir $1" "$vm_rootfs" >/dev/null 2>&1 || true
}

debugfs_write() {
	local source=$1 destination=$2 mode=$3
	debugfs -w -R "rm $destination" "$vm_rootfs" >/dev/null 2>&1 || true
	debugfs -w -R "write $source $destination" "$vm_rootfs" >/dev/null 2>&1
	debugfs -w -R "set_inode_field $destination mode $mode" "$vm_rootfs" >/dev/null 2>&1
}

debugfs_mkdir /opt
debugfs_mkdir /opt/daemon-itest
debugfs_mkdir /var/lib/daemon-itest
debugfs_write "$build_dir/daemon" /opt/daemon-itest/daemon 0100755
debugfs_write "$build_dir/test-app" /opt/daemon-itest/test-app 0100755
debugfs_write "$build_dir/guest-test.sh" /opt/daemon-itest/guest-test.sh 0100755
debugfs_write "$build_dir/relative-path-test.txt" /opt/daemon-itest/relative-path-test.txt 0100644
debugfs_write "$build_dir/test-config" /opt/daemon-itest/test-config 0100644
debugfs_write "$build_dir/boot-test.sh" /etc/init.d/daemon-itest-boot 0100755
debugfs_write "$build_dir/boot-test.sh" /etc/rc5.d/S99daemon-itest-boot 0100755

if e2fsck -pf "$vm_rootfs" >/dev/null; then
	fsck_status=0
else
	fsck_status=$?
fi
(( fsck_status <= 1 )) || fail "modified rootfs filesystem check failed with status $fsck_status"
chmod 0644 "$vm_rootfs" "$kernel_image"

serial_log="$artifact_dir/serial.log"
qemu_log="$artifact_dir/qemu.log"
pidfile="$work_dir/qemu.pid"

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

log "booting official Yocto $yocto_release qemuarm64 image"
qemu-system-aarch64 \
	-name daemon-yocto-itest \
	-M virt \
	-accel "$qemu_accel" \
	-cpu "$cpu" \
	-smp "$vm_vcpus" \
	-m "$vm_memory_mib" \
	-kernel "$kernel_image" \
	-append 'root=/dev/vda rw rootwait console=ttyAMA0,115200 swiotlb=0 panic=1' \
	-drive "id=disk0,file=$vm_rootfs,if=none,format=raw" \
	-device virtio-blk-pci,drive=disk0 \
	-object rng-random,filename=/dev/urandom,id=rng0 \
	-device virtio-rng-pci,rng=rng0 \
	-netdev user,id=net0 \
	-device virtio-net-pci,netdev=net0 \
	-display none \
	-serial "file:$serial_log" \
	-monitor none \
	-D "$qemu_log" \
	-daemonize \
	-pidfile "$pidfile"
qemu_pid=$(cat "$pidfile")

qemu_is_running() {
	local state
	kill -0 "$qemu_pid" 2>/dev/null || return 1
	state=$(awk '/^State:/ {print $2; exit}' "/proc/$qemu_pid/status" 2>/dev/null || true)
	[[ "$state" != Z ]]
}

deadline=$((SECONDS + vm_boot_timeout))
next_progress=$SECONDS
while qemu_is_running; do
	if grep -aqE 'DAEMON_ITEST_(PASS|FAIL)' "$serial_log" 2>/dev/null; then
		for _ in $(seq 1 10); do
			qemu_is_running || break
			sleep 1
		done
		if qemu_is_running; then
			kill "$qemu_pid" >/dev/null 2>&1 || true
			for _ in $(seq 1 10); do
				qemu_is_running || break
				sleep 1
			done
		fi
		break
	fi
	if (( SECONDS >= deadline )); then
		fail "timed out waiting for the Yocto guest after ${vm_boot_timeout}s"
	fi
	if (( SECONDS >= next_progress )); then
		last_marker=$(grep -aE 'DAEMON_ITEST_|\[yocto-(boot|itest)\]' "$serial_log" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
		if [[ -n "$last_marker" ]]; then
			log "waiting for guest poweroff ($last_marker)"
		else
			log "waiting for Yocto guest boot/test ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
		fi
		next_progress=$((SECONDS + 15))
	fi
	sleep 2
done
qemu_pid=

if e2fsck -pf "$vm_rootfs" >/dev/null; then
	fsck_status=0
else
	fsck_status=$?
fi
(( fsck_status <= 1 )) || fail "post-test rootfs filesystem check failed with status $fsck_status"

result_file="$artifact_dir/result.txt"
guest_log="$artifact_dir/guest-test.log"
artifact_tar="$artifact_dir/guest-artifacts.tar"
debugfs -R "dump /var/lib/daemon-itest/yocto-result $result_file" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/yocto-test.log $guest_log" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/yocto-artifacts.tar $artifact_tar" "$vm_rootfs" >/dev/null 2>&1 || true
if [[ -s "$artifact_tar" ]]; then
	mkdir -p "$artifact_dir/guest"
	tar -C "$artifact_dir/guest" -xf "$artifact_tar"
fi

[[ -f "$result_file" ]] || {
	tail -n 160 "$serial_log" >&2 || true
	fail 'Yocto guest did not write a result'
}
result=$(tr -d '\r\n' <"$result_file")
[[ "$result" == PASS ]] || {
	tail -n 160 "$serial_log" >&2 || true
	fail "Yocto guest result: $result"
}
grep -Fq 'DAEMON_ITEST_REBOOT' "$serial_log" || fail 'Yocto guest did not record its reboot phase'
grep -Fq 'DAEMON_ITEST_PASS' "$serial_log" || fail 'Yocto guest did not emit the pass marker'
if grep -aEq 'Kernel panic|DAEMON_ITEST_FAIL' "$serial_log"; then
	fail 'Yocto serial log contains a failure marker'
fi

log "Yocto $yocto_release ARM64 System V application-level test passed"
