#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

gentoo_build=${GENTOO_STAGE3_BUILD:-20260830T234553Z}
gentoo_stage_name=${GENTOO_STAGE3_NAME:-stage3-arm64-openrc-${gentoo_build}.tar.xz}
gentoo_base_url=${GENTOO_STAGE3_BASE_URL:-https://distfiles.gentoo.org/releases/arm64/autobuilds}
gentoo_stage_url=${GENTOO_STAGE3_URL:-$gentoo_base_url/$gentoo_build/$gentoo_stage_name}
kernel_release=${GENTOO_BOOT_KERNEL_RELEASE:-5.0.19}
kernel_name=${GENTOO_BOOT_KERNEL_NAME:-Image-qemuarm64.bin}
kernel_url=${GENTOO_BOOT_KERNEL_URL:-https://downloads.yoctoproject.org/releases/yocto/yocto-${kernel_release}/machines/qemu/qemuarm64/$kernel_name}
vm_memory_mib=${VM_MEMORY_MIB:-1024}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-900}
rootfs_size_mib=${GENTOO_ROOTFS_SIZE_MIB:-2048}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-gentoo$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-gentoo-itest-$run_id}
artifact_dir="$artifact_root/gentoo-$run_id"
stage_archive=${GENTOO_STAGE3:-$cache_dir/$gentoo_stage_name}
kernel_image=${GENTOO_BOOT_KERNEL:-$cache_dir/yocto-${kernel_release}-$kernel_name}
qemu_pid=

log() {
	printf '[gentoo-qemu] %s\n' "$*"
}

fail() {
	printf '[gentoo-qemu] ERROR: %s\n' "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in fakeroot go mkfs.ext4 qemu-system-aarch64 wget sha256sum debugfs e2fsck tar truncate; do
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

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

published_checksum() {
	local sidecar_url=$1 override=$2 output=$3
	if [[ -n "$override" ]]; then
		printf '%s\n' "$override"
		return
	fi
	wget -q -O "$output" "$sidecar_url"
	awk '/^[0-9a-fA-F]{64}[[:space:]]+/ {print $1; exit}' "$output"
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

stage_checksum_file="$work_dir/stage3.sha256"
kernel_checksum_file="$work_dir/kernel.sha256sum"
stage_sha=$(published_checksum "$gentoo_stage_url.sha256" "${GENTOO_STAGE3_SHA256:-}" "$stage_checksum_file")
kernel_sha=$(published_checksum "$kernel_url.sha256sum" "${GENTOO_BOOT_KERNEL_SHA256:-}" "$kernel_checksum_file")
[[ "$stage_sha" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'invalid Gentoo stage3 SHA-256'
[[ "$kernel_sha" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'invalid boot-kernel SHA-256'

if [[ ! -f "$stage_archive" && -n "${GENTOO_STAGE3:-}" ]]; then
	fail "GENTOO_STAGE3 does not exist: $stage_archive"
fi
if [[ ! -f "$kernel_image" && -n "${GENTOO_BOOT_KERNEL:-}" ]]; then
	fail "GENTOO_BOOT_KERNEL does not exist: $kernel_image"
fi
download_verified "$gentoo_stage_url" "$stage_archive" "$stage_sha"
download_verified "$kernel_url" "$kernel_image" "$kernel_sha"
printf '%s  %s\n%s  %s\n' "$stage_sha" "$gentoo_stage_name" "$kernel_sha" "$kernel_name" >"$artifact_dir/base-images.sha256"
cp "$stage_checksum_file" "$artifact_dir/gentoo-stage3.sha256"
cat >"$artifact_dir/image-sources.txt" <<EOF
Gentoo stage3: $gentoo_stage_url
Boot kernel: $kernel_url
EOF

build_dir="$work_dir/build"
root_dir="$work_dir/root"
vm_rootfs="$work_dir/gentoo.ext4"
mkdir -p "$build_dir" "$root_dir"

log 'building Linux/arm64 integration binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/daemon" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/test-app" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$repo_dir/integration_tests/openrc/guest-test.sh" "$build_dir/guest-test.sh"
cp "$script_dir/boot-test.sh" "$build_dir/boot-test.sh"
cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config"

log "creating writable Gentoo OpenRC root filesystem (${rootfs_size_mib} MiB)"
fakeroot -- sh -eu -c '
	stage=$1
	root=$2
	image=$3
	size_mib=$4
	build=$5

	tar --numeric-owner -xpf "$stage" -C "$root"
	install -d -m 0755 "$root/opt/daemon-itest" "$root/var/lib/daemon-itest" "$root/etc/local.d"
	install -m 0755 "$build/daemon" "$root/opt/daemon-itest/daemon"
	install -m 0755 "$build/test-app" "$root/opt/daemon-itest/test-app"
	install -m 0755 "$build/guest-test.sh" "$root/opt/daemon-itest/guest-test.sh"
	install -m 0755 "$build/boot-test.sh" "$root/etc/local.d/daemon-itest.start"
	install -m 0644 "$build/relative-path-test.txt" "$root/opt/daemon-itest/relative-path-test.txt"
	install -m 0644 "$build/test-config" "$root/opt/daemon-itest/test-config"
	printf "daemon-gentoo-openrc\n" >"$root/etc/hostname"
	printf "\nrc_verbose=YES\n" >>"$root/etc/rc.conf"
	truncate -s "${size_mib}M" "$image"
	mkfs.ext4 -q -F -L gentoo-root -d "$root" "$image"
' sh "$stage_archive" "$root_dir" "$vm_rootfs" "$rootfs_size_mib" "$build_dir"
rm -rf "$root_dir"

if e2fsck -pf "$vm_rootfs" >/dev/null; then
	fsck_status=0
else
	fsck_status=$?
fi
(( fsck_status <= 1 )) || fail "prepared Gentoo rootfs filesystem check failed with status $fsck_status"
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

log "booting official Gentoo $gentoo_build ARM64 OpenRC stage3"
qemu-system-aarch64 \
	-name daemon-gentoo-openrc-itest \
	-M virt \
	-accel "$qemu_accel" \
	-cpu "$cpu" \
	-smp "$vm_vcpus" \
	-m "$vm_memory_mib" \
	-kernel "$kernel_image" \
	-append 'root=/dev/vda rootfstype=ext4 rw rootwait console=ttyAMA0,115200 panic=1' \
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
	if grep -aqE 'Kernel panic|VFS: Unable to mount root fs' "$serial_log" 2>/dev/null; then
		fail 'Gentoo guest reported a kernel boot failure'
	fi
	if (( SECONDS >= deadline )); then
		fail "timed out waiting for the Gentoo guest after ${vm_boot_timeout}s"
	fi
	if (( SECONDS >= next_progress )); then
		last_marker=$(grep -aE 'DAEMON_ITEST_|\[(gentoo-boot|openrc-itest)\]' "$serial_log" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
		if [[ -n "$last_marker" ]]; then
			log "waiting for guest poweroff ($last_marker)"
		else
			log "waiting for Gentoo guest boot/test ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
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
(( fsck_status <= 1 )) || fail "post-test Gentoo rootfs filesystem check failed with status $fsck_status"

result_file="$artifact_dir/result.txt"
guest_log="$artifact_dir/guest-test.log"
artifact_tar="$artifact_dir/guest-artifacts.tar"
debugfs -R "dump /var/lib/daemon-itest/gentoo-result $result_file" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/gentoo-test.log $guest_log" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/gentoo-artifacts.tar $artifact_tar" "$vm_rootfs" >/dev/null 2>&1 || true
if [[ -s "$artifact_tar" ]]; then
	mkdir -p "$artifact_dir/guest"
	tar -C "$artifact_dir/guest" -xf "$artifact_tar"
fi

[[ -f "$result_file" ]] || {
	tail -n 180 "$serial_log" >&2 || true
	fail 'Gentoo guest did not write a result'
}
result=$(tr -d '\r\n' <"$result_file")
[[ "$result" == PASS ]] || {
	tail -n 180 "$serial_log" >&2 || true
	fail "Gentoo guest result: $result"
}
grep -Fq 'DAEMON_ITEST_REBOOT' "$serial_log" || fail 'Gentoo guest did not record its reboot phase'
grep -Fq 'DAEMON_ITEST_PASS' "$serial_log" || fail 'Gentoo guest did not emit the pass marker'
if grep -aEq 'Kernel panic|DAEMON_ITEST_FAIL' "$serial_log"; then
	fail 'Gentoo serial log contains a failure marker'
fi
grep -Fq 'Gentoo Linux' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify Gentoo Linux'
grep -Fq 'OpenRC' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify OpenRC'

log "Gentoo $gentoo_build ARM64 OpenRC application-level test passed"