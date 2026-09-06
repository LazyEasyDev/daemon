#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

release=${IMMORTALWRT_RELEASE:-23.05.7}
target=${IMMORTALWRT_TARGET:-armsr/armv8}
image_name=${IMMORTALWRT_IMAGE_NAME:-immortalwrt-${release}-armsr-armv8-generic-ext4-combined-efi.qcow2.gz}
image_url=${IMMORTALWRT_IMAGE_URL:-https://downloads.immortalwrt.org/releases/${release}/targets/armsr/armv8/$image_name}
image_sha256=${IMMORTALWRT_IMAGE_SHA256:-f562a913286081aa4b8996e57fd6983d6bd9241a02af4ebf034818fef3a70c22}
vm_memory_mib=${VM_MEMORY_MIB:-768}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-900}
state_size_mib=${IMMORTALWRT_STATE_SIZE_MIB:-128}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-immortalwrt$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-immortalwrt-itest-$run_id}
artifact_dir="$artifact_root/immortalwrt-$run_id"
compressed_image=${IMMORTALWRT_IMAGE:-$cache_dir/$image_name}
qemu_pid=

log() { printf '[immortalwrt-qemu] %s\n' "$*"; }
fail() { printf '[immortalwrt-qemu] ERROR: %s\n' "$*" >&2; return 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"; }

for command in debugfs dd e2fsck fdisk go gzip mkfs.ext4 qemu-img qemu-system-aarch64 sha256sum tar truncate wget; do
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

uefi_code=${ARM_UEFI_CODE:-/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd}
uefi_vars_template=${ARM_UEFI_VARS:-/usr/share/AAVMF/AAVMF_VARS.fd}
[[ -f "$uefi_code" && -f "$uefi_vars_template" ]] || fail 'ARM64 UEFI firmware is unavailable'

mkdir -p "$cache_dir" "$artifact_dir" "$work_dir"
chmod 0755 "$cache_dir" "$work_dir"

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

if [[ ! -f "$compressed_image" && -n "${IMMORTALWRT_IMAGE:-}" ]]; then
	fail "IMMORTALWRT_IMAGE does not exist: $compressed_image"
fi
if [[ -f "$compressed_image" ]] && ! sha256_matches "$compressed_image" "$image_sha256"; then
	log "discarding cached file with stale checksum: $compressed_image"
	rm -f "$compressed_image"
fi
if [[ ! -f "$compressed_image" ]]; then
	log 'downloading official ImmortalWrt ARM64 EFI qcow2'
	wget --progress=dot:giga -O "$compressed_image.partial" "$image_url"
	sha256_matches "$compressed_image.partial" "$image_sha256" || fail 'ImmortalWrt image SHA-256 verification failed'
	mv "$compressed_image.partial" "$compressed_image"
fi
sha256_matches "$compressed_image" "$image_sha256" || fail 'cached ImmortalWrt image SHA-256 verification failed'
printf '%s  %s\n' "$image_sha256" "$image_name" >"$artifact_dir/base-image.sha256"
cat >"$artifact_dir/image-source.txt" <<EOF
ImmortalWrt image: $image_url
Release: $release
Target: $target
EOF

qcow_image="$work_dir/immortalwrt.qcow2"
raw_image="$work_dir/immortalwrt.raw"
rootfs="$work_dir/rootfs.ext4"
gzip -dc "$compressed_image" >"$qcow_image"
qemu-img convert -O raw "$qcow_image" "$raw_image"
rm -f "$qcow_image"

read -r root_start root_size < <(fdisk -l -o Start,Sectors,Type "$raw_image" | awk '$3 == "Linux" && $4 == "filesystem" {print $1, $2; exit}')
[[ "$root_start" =~ ^[0-9]+$ && "$root_size" =~ ^[0-9]+$ ]] || fail 'could not locate ImmortalWrt Linux root partition'
dd if="$raw_image" of="$rootfs" bs=512 skip="$root_start" count="$root_size" status=none

check_filesystem() {
	local image=$1 output=$2 status=0
	e2fsck -fy "$image" >"$output" 2>&1 || status=$?
	(( status <= 1 )) || fail "filesystem check failed with status $status: $image"
}
check_filesystem "$rootfs" "$artifact_dir/pre-boot-e2fsck.log"
release_info=$(debugfs -R 'cat /etc/openwrt_release' "$rootfs" 2>/dev/null || true)
grep -Fq "DISTRIB_ID='ImmortalWrt'" <<<"$release_info" || fail 'rootfs does not identify ImmortalWrt'
grep -Fq "DISTRIB_TARGET='$target'" <<<"$release_info" || fail "rootfs does not identify target $target"
debugfs -R 'stat /sbin/procd' "$rootfs" 2>/dev/null | grep -Fq 'Type: regular' || fail 'ImmortalWrt rootfs is missing procd'

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
ImmortalWrt release: $release
ImmortalWrt image: $image_name
Target: $target
Architecture: aarch64_generic
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/interpreted-app-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config" "$build_dir/source-info"

write_file() {
	local source=$1 destination=$2 mode=$3
	debugfs -w -R "rm $destination" "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
	debugfs -w -R "write $source $destination" "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1
	debugfs -w -R "set_inode_field $destination mode $mode" "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1
}
: >"$artifact_dir/debugfs.log"
for directory in /opt /opt/daemon-itest /var/lib/daemon-itest; do
	debugfs -w -R "mkdir $directory" "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
done
write_file "$build_dir/daemon" /opt/daemon-itest/daemon 0100755
write_file "$build_dir/test-app" /opt/daemon-itest/test-app 0100755
write_file "$build_dir/guest-test.sh" /opt/daemon-itest/guest-test.sh 0100755
write_file "$build_dir/interpreted-app-test.sh" /opt/daemon-itest/interpreted-app-test.sh 0100755
write_file "$build_dir/relative-path-test.txt" /opt/daemon-itest/relative-path-test.txt 0100644
write_file "$build_dir/test-config" /opt/daemon-itest/test-config 0100644
write_file "$build_dir/source-info" /etc/daemon-itest-image-source 0100644
write_file "$build_dir/boot-test.sh" /etc/init.d/daemon-itest-boot 0100755
debugfs -w -R 'rm /etc/rc.d/S99daemon-itest-boot' "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
debugfs -w -R 'symlink /etc/rc.d/S99daemon-itest-boot ../init.d/daemon-itest-boot' "$rootfs" >>"$artifact_dir/debugfs.log" 2>&1
check_filesystem "$rootfs" "$artifact_dir/prepared-e2fsck.log"
dd if="$rootfs" of="$raw_image" bs=512 seek="$root_start" conv=notrunc status=none

state_image="$work_dir/state.ext4"
truncate -s "${state_size_mib}M" "$state_image"
mkfs.ext4 -q -F -L daemon-state "$state_image"
uefi_vars="$work_dir/AAVMF_VARS.fd"
cp "$uefi_vars_template" "$uefi_vars"
chmod 0644 "$raw_image" "$state_image" "$uefi_vars"

qemu_log="$artifact_dir/qemu.log"
pidfile="$work_dir/qemu.pid"
cleanup() {
	local status=$?
	trap - EXIT
	if [[ "$qemu_pid" =~ ^[0-9]+$ ]] && kill -0 "$qemu_pid" 2>/dev/null; then
		kill "$qemu_pid" >/dev/null 2>&1 || true
		for _ in $(seq 1 20); do kill -0 "$qemu_pid" >/dev/null 2>&1 || break; sleep 1; done
		kill -9 "$qemu_pid" >/dev/null 2>&1 || true
	fi
	if [[ "$keep_vm" != 1 ]]; then rm -rf "$work_dir" || true; fi
	if (( status == 0 )); then log "artifacts: $artifact_dir"; else log "test failed; artifacts: $artifact_dir" >&2; fi
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
	local phase=$1
	local serial="$artifact_dir/serial-$phase.log"
	rm -f "$pidfile" "$serial"
	log "booting official ImmortalWrt ARM64 EFI image for $phase"
	qemu-system-aarch64 -name daemon-immortalwrt-procd-itest -M virt -accel "$qemu_accel" -cpu "$cpu" \
		-smp "$vm_vcpus" -m "$vm_memory_mib" \
		-drive "if=pflash,format=raw,readonly=on,file=$uefi_code" \
		-drive "if=pflash,format=raw,file=$uefi_vars" \
		-drive "id=rootdisk,file=$raw_image,if=none,format=raw" -device virtio-blk-pci,drive=rootdisk \
		-drive "id=statedisk,file=$state_image,if=none,format=raw" -device virtio-blk-pci,drive=statedisk \
		-object rng-random,filename=/dev/urandom,id=rng0 -device virtio-rng-pci,rng=rng0 \
		-netdev user,id=net0 -device virtio-net-pci,netdev=net0 \
		-display none -serial "file:$serial" -monitor none -D "$qemu_log" -daemonize -pidfile "$pidfile"
	qemu_pid=$(cat "$pidfile")
}

wait_for_poweroff() {
	local phase=$1
	local serial="$artifact_dir/serial-$phase.log"
	local deadline=$((SECONDS + vm_boot_timeout)) next_progress=$SECONDS marker
	while qemu_is_running; do
		if grep -aqE 'Kernel panic|VFS: Unable to mount root fs|No working init found|DAEMON_ITEST_FAIL' "$serial" 2>/dev/null; then
			fail "ImmortalWrt reported a boot or test failure during $phase"
		fi
		if (( SECONDS >= deadline )); then fail "timed out waiting for ImmortalWrt $phase after ${vm_boot_timeout}s"; fi
		if (( SECONDS >= next_progress )); then
			marker=$(grep -aE 'DAEMON_ITEST_|\[(immortalwrt-boot|openwrt-itest)\]' "$serial" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
			log "waiting for $phase poweroff (${marker:-$((SECONDS + vm_boot_timeout - deadline))s elapsed})"
			next_progress=$((SECONDS + 15))
		fi
		sleep 2
	done
	qemu_pid=
}

read_state_file() { debugfs -R "cat $1" "$state_image" 2>/dev/null || true; }

launch_guest pre-reboot
wait_for_poweroff pre-reboot
check_filesystem "$state_image" "$artifact_dir/post-pre-reboot-state-e2fsck.log"
phase=$(read_state_file /immortalwrt-phase | tr -d '\r\n')
[[ "$phase" == post-reboot ]] || fail "ImmortalWrt pre-reboot phase did not complete: ${phase:-missing phase marker}"

launch_guest post-reboot
wait_for_poweroff post-reboot
check_filesystem "$state_image" "$artifact_dir/post-test-state-e2fsck.log"
cat "$artifact_dir/serial-pre-reboot.log" "$artifact_dir/serial-post-reboot.log" >"$artifact_dir/serial.log"

result_file="$artifact_dir/result.txt"
guest_log="$artifact_dir/guest-test.log"
artifact_tar="$artifact_dir/guest-artifacts.tar"
debugfs -R "dump /immortalwrt-result $result_file" "$state_image" >/dev/null 2>&1 || true
debugfs -R "dump /immortalwrt-test.log $guest_log" "$state_image" >/dev/null 2>&1 || true
debugfs -R "dump /immortalwrt-artifacts.tar $artifact_tar" "$state_image" >/dev/null 2>&1 || true
if [[ -s "$artifact_tar" ]]; then mkdir -p "$artifact_dir/guest"; tar -C "$artifact_dir/guest" -xf "$artifact_tar"; fi
[[ -f "$result_file" ]] || { tail -n 240 "$artifact_dir/serial.log" >&2 || true; fail 'ImmortalWrt guest did not write a result'; }
[[ "$(tr -d '\r\n' <"$result_file")" == PASS ]] || fail "ImmortalWrt guest result is not PASS: $(cat "$result_file")"
[[ "$(read_state_file /immortalwrt-phase | tr -d '\r\n')" == complete ]] || fail 'ImmortalWrt post-reboot phase did not complete'
grep -Fq "DISTRIB_ID='ImmortalWrt'" "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify ImmortalWrt'
grep -Fq 'procd' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify procd'
grep -Fq 'all OpenWrt application-level tests passed' "$guest_log" || fail 'ImmortalWrt lifecycle completion marker is missing'
log "ImmortalWrt $release ARM64 procd application-level test passed"
