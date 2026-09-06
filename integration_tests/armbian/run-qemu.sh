#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

lane_id=${INTEGRATION_LANE_ID:-armbian}
lane_display=${INTEGRATION_LANE_DISPLAY:-Armbian}
armbian_release=${ARMBIAN_RELEASE:-26.8.1}
armbian_board=${ARMBIAN_BOARD:-Orangepi5}
armbian_codename=${ARMBIAN_CODENAME:-trixie}
armbian_kernel_branch=${ARMBIAN_KERNEL_BRANCH:-current}
armbian_kernel_version=${ARMBIAN_KERNEL_VERSION:-6.18.43}
image_name=${ARMBIAN_IMAGE_NAME:-Armbian_${armbian_release}_${armbian_board}_${armbian_codename}_${armbian_kernel_branch}_${armbian_kernel_version}_minimal.img.xz}
image_base_url=${ARMBIAN_IMAGE_BASE_URL:-https://dl.armbian.com/orangepi5/archive}
image_url=${ARMBIAN_IMAGE_URL:-$image_base_url/$image_name}
checksum_url=${ARMBIAN_CHECKSUM_URL:-$image_url.sha}
root_partition_name=${ARMBIAN_ROOT_PARTITION_NAME:-rootfs}
root_partition_index=${ARMBIAN_ROOT_PARTITION_INDEX:-}
expected_guest_marker=${INTEGRATION_EXPECTED_GUEST_MARKER:-Armbian}
image_compression=${ARMBIAN_IMAGE_COMPRESSION:-xz}
kernel_path=${ARMBIAN_KERNEL_PATH:-}
initrd_path=${ARMBIAN_INITRD_PATH:-}
boot_kernel=${ARMBIAN_BOOT_KERNEL:-}
boot_initrd=${ARMBIAN_BOOT_INITRD:-}
uncompressed_digest=${ARMBIAN_UNCOMPRESSED_SHA512:-${ARMBIAN_UNCOMPRESSED_SHA256:-}}
compressed_image=${ARMBIAN_IMAGE:-${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}/$image_name}
vm_memory_mib=${VM_MEMORY_MIB:-1024}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-1200}
rootfs_size_mib=${ARMBIAN_ROOTFS_SIZE_MIB:-3072}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-${lane_id}$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-${lane_id}-itest-$run_id}
artifact_dir="$artifact_root/${lane_id}-$run_id"
qemu_pid=

log() {
	printf '[%s-qemu] %s\n' "$lane_id" "$*"
}

fail() {
	printf '[%s-qemu] ERROR: %s\n' "$lane_id" "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in debugfs dd e2fsck go gzip python3 qemu-system-aarch64 resize2fs sfdisk sha256sum sha512sum stat tar truncate wget xz; do
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

digest_matches() {
	local path=$1 expected=$2
	case ${#expected} in
	64) printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status - ;;
	128) printf '%s  %s\n' "$expected" "$path" | sha512sum --check --status - ;;
	*) fail "unsupported $lane_display image digest length: ${#expected}" ;;
	esac
}

checksum_file="$work_dir/${lane_id}-image.sha"
if [[ -n "${ARMBIAN_IMAGE_SHA512:-}" ]]; then
	image_sha=$ARMBIAN_IMAGE_SHA512
elif [[ -n "${ARMBIAN_IMAGE_SHA256:-}" ]]; then
	image_sha=$ARMBIAN_IMAGE_SHA256
else
	wget -qO "$checksum_file" "$checksum_url"
	image_sha=$(awk '/^[0-9a-fA-F]{128}[[:space:]]+/ {print $1; exit} /^[0-9a-fA-F]{64}[[:space:]]+/ {print $1; exit}' "$checksum_file")
fi
[[ "$image_sha" =~ ^([0-9a-fA-F]{64}|[0-9a-fA-F]{128})$ ]] || fail "invalid $lane_display image digest"

if [[ ! -f "$compressed_image" && -n "${ARMBIAN_IMAGE:-}" ]]; then
	fail "ARMBIAN_IMAGE does not exist: $compressed_image"
fi
if [[ -f "$compressed_image" ]] && ! digest_matches "$compressed_image" "$image_sha"; then
	log "discarding cached file with stale checksum: $compressed_image"
	rm -f "$compressed_image"
fi
if [[ ! -f "$compressed_image" ]]; then
	temporary_image="$compressed_image.partial"
	log "downloading official $armbian_board $lane_display image"
	rm -f "$temporary_image"
	wget --progress=dot:giga -O "$temporary_image" "$image_url"
	digest_matches "$temporary_image" "$image_sha" || fail "$lane_display image digest verification failed"
	mv "$temporary_image" "$compressed_image"
fi
digest_matches "$compressed_image" "$image_sha" || fail "cached $lane_display image digest verification failed"
printf '%s  %s\n' "$image_sha" "$image_name" >"$artifact_dir/base-images.sha256"
cat >"$artifact_dir/image-sources.txt" <<EOF
$lane_display image: $image_url
Board userspace: $armbian_board
Boot kernel: ${ARMBIAN_BOOT_KERNEL_SOURCE:-native image kernel}
EOF

raw_image="$work_dir/${lane_id}-board.img"
vm_rootfs="$work_dir/${lane_id}-root.ext4"
log "decompressing the official $lane_display board image"
case "$image_compression" in
	xz) xz -dc "$compressed_image" | dd of="$raw_image" bs=4M status=progress ;;
	gzip|gz) gzip -dc "$compressed_image" | dd of="$raw_image" bs=4M status=progress ;;
	none) cp --reflink=auto "$compressed_image" "$raw_image" ;;
	*) fail "unsupported $lane_display image compression: $image_compression" ;;
esac
if [[ -n "$uncompressed_digest" ]]; then
	digest_matches "$raw_image" "$uncompressed_digest" || fail "$lane_display decompressed image digest verification failed"
	printf '%s  %s\n' "$uncompressed_digest" "${image_name%.xz}" >>"$artifact_dir/base-images.sha256"
fi

read -r root_start root_size < <(
	sfdisk -J "$raw_image" | python3 -c '
import json
import sys
expected = sys.argv[1]
requested_index = sys.argv[2]
partitions = json.load(sys.stdin)["partitiontable"]["partitions"]
matches = [partition for index, partition in enumerate(partitions, 1) if partition.get("name") == expected or (requested_index and index == int(requested_index))]
partition = matches[0] if matches else None
partition is not None or sys.exit(f"root partition {expected!r} (index {requested_index!r}) was not found")
print(partition["start"], partition["size"])
' "$root_partition_name" "$root_partition_index"
)
[[ -n "$root_start" && -n "$root_size" ]] || fail "could not identify the $lane_display root partition"
log "extracting the $lane_display root partition"
dd if="$raw_image" of="$vm_rootfs" bs=512 skip="$root_start" count="$root_size" status=progress
rm -f "$raw_image"

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/pre-resize-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "$lane_display root filesystem check failed with status $fsck_status"
current_rootfs_bytes=$(stat -c %s "$vm_rootfs")
target_rootfs_bytes=$((rootfs_size_mib * 1024 * 1024))
if (( target_rootfs_bytes < current_rootfs_bytes )); then
	resize2fs "$vm_rootfs" "${rootfs_size_mib}M" >"$artifact_dir/resize2fs.log" 2>&1
	truncate -s "${rootfs_size_mib}M" "$vm_rootfs"
else
	truncate -s "${rootfs_size_mib}M" "$vm_rootfs"
	resize2fs "$vm_rootfs" >"$artifact_dir/resize2fs.log" 2>&1
fi

kernel_image="$work_dir/Image"
initrd_image="$work_dir/initrd.img"
if [[ -n "$boot_kernel" ]]; then
	[[ -s "$boot_kernel" ]] || fail "configured boot kernel does not exist: $boot_kernel"
	cp "$boot_kernel" "$kernel_image"
	if [[ -n "$boot_initrd" ]]; then
		[[ -s "$boot_initrd" ]] || fail "configured boot initramfs does not exist: $boot_initrd"
		cp "$boot_initrd" "$initrd_image"
	fi
else
	if [[ -z "$kernel_path" || -z "$initrd_path" ]]; then
		boot_listing=$(debugfs -R 'ls -p /boot' "$vm_rootfs" 2>/dev/null || true)
		if [[ -z "$kernel_path" ]]; then
			kernel_name=$(awk -F/ '$6 ~ /^(vmlinuz-|Image$|Image-)/ {print $6}' <<<"$boot_listing" | sort -V | tail -n 1)
			kernel_path=/boot/$kernel_name
		fi
		if [[ -z "$initrd_path" ]]; then
			initrd_name=$(awk -F/ '$6 ~ /^(initrd\.img-|initramfs-)/ {print $6}' <<<"$boot_listing" | sort -V | tail -n 1)
			initrd_path=/boot/$initrd_name
		fi
	fi
	[[ "$kernel_path" != /boot/ && "$initrd_path" != /boot/ ]] || fail "could not discover the native $lane_display kernel and initramfs"
	debugfs -R "dump $kernel_path $kernel_image" "$vm_rootfs" >/dev/null 2>&1
	debugfs -R "dump $initrd_path $initrd_image" "$vm_rootfs" >/dev/null 2>&1
fi
[[ -s "$kernel_image" ]] || fail "could not prepare the $lane_display boot kernel"

build_dir="$work_dir/build"
mkdir -p "$build_dir"
log 'building Linux/arm64 integration binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/daemon" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/test-app" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$repo_dir/integration_tests/systemd/guest-test.sh" "$build_dir/guest-test.sh"
cp "$script_dir/boot-test.sh" "$build_dir/boot-test.sh"
cp "$script_dir/daemon-itest.service" "$build_dir/daemon-itest.service"
cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
EOF
cat >"$build_dir/image-source" <<EOF
$lane_display image: $image_url
Board userspace: $armbian_board
Boot kernel: ${ARMBIAN_BOOT_KERNEL_SOURCE:-native image kernel}
EOF
cat >"$build_dir/fstab" <<'EOF'
/dev/vda / ext4 defaults,errors=remount-ro 0 1
tmpfs /tmp tmpfs defaults,nosuid 0 0
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config" "$build_dir/image-source" "$build_dir/daemon-itest.service" "$build_dir/fstab"

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
write_file "$build_dir/boot-test.sh" /opt/daemon-itest/boot-test.sh 0100755
write_file "$build_dir/relative-path-test.txt" /opt/daemon-itest/relative-path-test.txt 0100644
write_file "$build_dir/test-config" /opt/daemon-itest/test-config 0100644
write_file "$build_dir/image-source" /etc/daemon-itest-image-source 0100644
write_file "$build_dir/daemon-itest.service" /etc/systemd/system/daemon-itest.service 0100644
write_file "$build_dir/fstab" /etc/fstab 0100644
debugfs -w -R 'rm /root/.not_logged_in_yet' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
debugfs -w -R 'rm /etc/systemd/system/multi-user.target.wants/armbian-firstrun.service' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
for unit in dietpi-firstboot.service dietpi-preboot.service dietpi-postboot.service dietpi-ramlog.service; do
	debugfs -w -R "rm /etc/systemd/system/multi-user.target.wants/$unit" "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
done
debugfs -w -R 'rm /etc/systemd/system/multi-user.target.wants/daemon-itest.service' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1 || true
debugfs -w -R 'symlink /etc/systemd/system/multi-user.target.wants/daemon-itest.service ../daemon-itest.service' "$vm_rootfs" >>"$artifact_dir/debugfs.log" 2>&1

for path in /opt/daemon-itest/daemon /opt/daemon-itest/test-app /opt/daemon-itest/guest-test.sh /etc/systemd/system/daemon-itest.service /etc/systemd/system/multi-user.target.wants/daemon-itest.service; do
	debugfs -R "stat $path" "$vm_rootfs" >>"$artifact_dir/injected-files.txt" 2>&1

done

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/pre-boot-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "prepared $lane_display filesystem check failed with status $fsck_status"
chmod 0644 "$vm_rootfs" "$kernel_image"
qemu_initrd_args=()
if [[ -s "$initrd_image" ]]; then
	chmod 0644 "$initrd_image"
	qemu_initrd_args=(-initrd "$initrd_image")
fi

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

log "booting official $lane_display $armbian_release $armbian_codename $armbian_board userspace"
qemu-system-aarch64 \
	-name "daemon-${lane_id}-systemd-itest" \
	-M virt \
	-accel "$qemu_accel" \
	-cpu "$cpu" \
	-smp "$vm_vcpus" \
	-m "$vm_memory_mib" \
	-kernel "$kernel_image" \
	"${qemu_initrd_args[@]}" \
	-append 'root=/dev/vda rootfstype=ext4 rw rootwait console=ttyAMA0,115200 systemd.unified_cgroup_hierarchy=1 panic=1' \
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
	if grep -aqE 'Kernel panic|VFS: Unable to mount root fs|Dependency failed for /' "$serial_log" 2>/dev/null; then
		fail "$lane_display guest reported a kernel or root-filesystem boot failure"
	fi
	if (( SECONDS >= deadline )); then
		fail "timed out waiting for the $lane_display guest after ${vm_boot_timeout}s"
	fi
	if (( SECONDS >= next_progress )); then
		last_marker=$(grep -aE 'DAEMON_ITEST_|\[(armbian-boot|systemd-itest)\]' "$serial_log" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
		if [[ -n "$last_marker" ]]; then
			log "waiting for guest poweroff ($last_marker)"
		else
			log "waiting for $lane_display guest boot/test ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
		fi
		next_progress=$((SECONDS + 15))
	fi
	sleep 2
done
qemu_pid=

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/post-test-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "post-test $lane_display filesystem check failed with status $fsck_status"

result_file="$artifact_dir/result.txt"
guest_log="$artifact_dir/guest-test.log"
artifact_tar="$artifact_dir/guest-artifacts.tar"
debugfs -R "dump /var/lib/daemon-itest/armbian-result $result_file" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/armbian-test.log $guest_log" "$vm_rootfs" >/dev/null 2>&1 || true
debugfs -R "dump /var/lib/daemon-itest/armbian-artifacts.tar $artifact_tar" "$vm_rootfs" >/dev/null 2>&1 || true
if [[ -s "$artifact_tar" ]]; then
	mkdir -p "$artifact_dir/guest"
	tar -C "$artifact_dir/guest" -xf "$artifact_tar"
fi

[[ -f "$result_file" ]] || {
	tail -n 240 "$serial_log" >&2 || true
	fail "$lane_display guest did not write a result"
}
result=$(tr -d '\r\n' <"$result_file")
[[ "$result" == PASS ]] || {
	tail -n 240 "$serial_log" >&2 || true
	fail "$lane_display guest result: $result"
}
grep -Fq 'DAEMON_ITEST_REBOOT' "$serial_log" || fail "$lane_display guest did not record its reboot phase"
grep -Fq 'DAEMON_ITEST_PASS' "$serial_log" || fail "$lane_display guest did not emit the pass marker"
if grep -aEq 'Kernel panic|DAEMON_ITEST_FAIL' "$serial_log"; then
	fail "$lane_display serial log contains a failure marker"
fi
grep -Fq "$expected_guest_marker" "$artifact_dir/guest/success-environment.txt" || fail "guest artifacts do not identify $lane_display"
grep -Fq 'systemd' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify systemd'

log "$lane_display $armbian_release $armbian_codename $armbian_board application-level test passed"
