#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

lane_id=${ROOTFS_LANE_ID:-archlinux}
lane_display=${ROOTFS_LANE_DISPLAY:-Arch Linux ARM}
expected_guest_marker=${ROOTFS_EXPECTED_GUEST_MARKER:-Arch Linux ARM}
rootfs_name=${ARCHLINUXARM_ROOTFS_NAME:-ArchLinuxARM-aarch64-latest.tar.gz}
rootfs_url=${ARCHLINUXARM_ROOTFS_URL:-http://os.archlinuxarm.org/os/$rootfs_name}
rootfs_md5=${ARCHLINUXARM_ROOTFS_MD5:-23eec86365b24f7913c403e8f4e8719b}
signing_fingerprint=${ARCHLINUXARM_SIGNING_FINGERPRINT:-68B3537F39A313B3E574D06777193F152BDBE6A6}
keyserver=${ARCHLINUXARM_KEYSERV:-hkps://keyserver.ubuntu.com}
skip_signature=${ARCHLINUXARM_SKIP_SIGNATURE:-0}
external_kernel=${ARCHLINUXARM_BOOT_KERNEL:-}
vm_memory_mib=${VM_MEMORY_MIB:-1536}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-1200}
rootfs_size_mib=${ARCHLINUXARM_ROOTFS_SIZE_MIB:-4096}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
service_name=${SERVICE_NAME:-${lane_id}$(date +%s)$$}
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-${lane_id}-itest-$run_id}
artifact_dir="$artifact_root/${lane_id}-$run_id"
rootfs_archive=${ARCHLINUXARM_ROOTFS:-$cache_dir/$rootfs_name}
qemu_pid=

log() {
	printf '[archlinux-qemu] %s\n' "$*"
}

fail() {
	printf '[archlinux-qemu] ERROR: %s\n' "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in debugfs e2fsck fakeroot find go gpg md5sum mkfs.ext4 qemu-system-aarch64 sha256sum tar truncate wget; do
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

md5_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | md5sum --check --status -
}

if [[ ! -f "$rootfs_archive" && -n "${ARCHLINUXARM_ROOTFS:-}" ]]; then
	fail "ARCHLINUXARM_ROOTFS does not exist: $rootfs_archive"
fi
if [[ -f "$rootfs_archive" ]] && ! md5_matches "$rootfs_archive" "$rootfs_md5"; then
	log "discarding cached file with stale checksum: $rootfs_archive"
	rm -f "$rootfs_archive"
fi
signature="$work_dir/$rootfs_name.sig"
if [[ ! -f "$rootfs_archive" ]]; then
	temporary_archive="$rootfs_archive.partial"
	log 'downloading official Arch Linux ARM generic AArch64 root filesystem'
	rm -f "$temporary_archive"
	wget --progress=dot:giga -O "$temporary_archive" "$rootfs_url"
	md5_matches "$temporary_archive" "$rootfs_md5" || fail 'Arch Linux ARM published MD5 verification failed'
	mv "$temporary_archive" "$rootfs_archive"
fi
md5_matches "$rootfs_archive" "$rootfs_md5" || fail 'cached Arch Linux ARM rootfs failed published MD5 verification'
if [[ "$skip_signature" != 1 ]]; then
	wget -qO "$signature" "$rootfs_url.sig"
	gnupg_home="$work_dir/gnupg"
	mkdir -m 0700 "$gnupg_home"
	GNUPGHOME="$gnupg_home" gpg --batch --keyserver "$keyserver" --recv-keys "$signing_fingerprint" >/dev/null 2>&1
	actual_fingerprint=$(GNUPGHOME="$gnupg_home" gpg --batch --with-colons --fingerprint "$signing_fingerprint" | awk -F: '$1 == "fpr" {print $10; exit}')
	[[ "$actual_fingerprint" == "$signing_fingerprint" ]] || fail "$lane_display signing-key fingerprint mismatch"
	GNUPGHOME="$gnupg_home" gpg --batch --verify "$signature" "$rootfs_archive" >"$artifact_dir/signature-verification.txt" 2>&1 || fail "$lane_display rootfs signature verification failed"
fi
rootfs_sha256=$(sha256sum "$rootfs_archive" | awk '{print $1}')
printf '%s  %s\n' "$rootfs_sha256" "$rootfs_name" >"$artifact_dir/base-images.sha256"
printf '%s  %s\n' "$rootfs_md5" "$rootfs_name" >"$artifact_dir/published-rootfs.md5"
cat >"$artifact_dir/image-sources.txt" <<EOF
Arch Linux ARM rootfs: $rootfs_url
Signature: $rootfs_url.sig
Signing fingerprint: $signing_fingerprint
EOF

build_dir="$work_dir/build"
root_dir="$work_dir/root"
vm_rootfs="$work_dir/archlinux.ext4"
kernel_image="$work_dir/Image"
initrd_image="$work_dir/initramfs-linux.img"
mkdir -p "$build_dir" "$root_dir"

log 'building Linux/arm64 integration binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/daemon" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build_dir/test-app" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$repo_dir/integration_tests/systemd/guest-test.sh" "$build_dir/guest-test.sh"
cp "$repo_dir/integration_tests/armbian/boot-test.sh" "$build_dir/boot-test.sh"
cp "$repo_dir/integration_tests/armbian/daemon-itest.service" "$build_dir/daemon-itest.service"
cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
EOF
cat >"$build_dir/image-source" <<EOF
Arch Linux ARM rootfs: $rootfs_url
Architecture: aarch64
EOF
chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/boot-test.sh"
chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config" "$build_dir/image-source" "$build_dir/daemon-itest.service"

log "creating writable Arch Linux ARM root filesystem (${rootfs_size_mib} MiB)"
fakeroot -- sh -eu -c '
	archive=$1
	root=$2
	image=$3
	size_mib=$4
	build=$5
	kernel_output=$6
	initrd_output=$7

	tar --numeric-owner -xpf "$archive" -C "$root"
	if [ -z "$8" ]; then
		kernel=$(find "$root/boot" -maxdepth 1 -type f \( -name Image -o -name "Image-*" -o -name "vmlinuz-linux*" \) | sort | head -n 1)
		initrd=$(find "$root/boot" -maxdepth 1 -type f \( -name "initramfs-linux.img" -o -name "initramfs-linux-*.img" \) | sort | head -n 1)
		[ -n "$kernel" ] && [ -s "$kernel" ]
		[ -n "$initrd" ] && [ -s "$initrd" ]
		cp "$kernel" "$kernel_output"
		cp "$initrd" "$initrd_output"
	else
		cp "$8" "$kernel_output"
	fi

	install -d -m 0755 "$root/opt/daemon-itest" "$root/var/lib/daemon-itest" "$root/etc/systemd/system/multi-user.target.wants"
	install -m 0755 "$build/daemon" "$root/opt/daemon-itest/daemon"
	install -m 0755 "$build/test-app" "$root/opt/daemon-itest/test-app"
	install -m 0755 "$build/guest-test.sh" "$root/opt/daemon-itest/guest-test.sh"
	install -m 0755 "$build/boot-test.sh" "$root/opt/daemon-itest/boot-test.sh"
	install -m 0644 "$build/relative-path-test.txt" "$root/opt/daemon-itest/relative-path-test.txt"
	install -m 0644 "$build/test-config" "$root/opt/daemon-itest/test-config"
	install -m 0644 "$build/image-source" "$root/etc/daemon-itest-image-source"
	install -m 0644 "$build/daemon-itest.service" "$root/etc/systemd/system/daemon-itest.service"
	ln -sfn ../daemon-itest.service "$root/etc/systemd/system/multi-user.target.wants/daemon-itest.service"
	printf "%s\n" daemon-archlinux-systemd >"$root/etc/hostname"
	printf "%s\n" "/dev/vda / ext4 defaults,errors=remount-ro 0 1" "tmpfs /tmp tmpfs defaults,nosuid 0 0" >"$root/etc/fstab"
	: >"$root/etc/machine-id"
	truncate -s "${size_mib}M" "$image"
	mkfs.ext4 -q -F -L archlinux-root -d "$root" "$image"
' sh "$rootfs_archive" "$root_dir" "$vm_rootfs" "$rootfs_size_mib" "$build_dir" "$kernel_image" "$initrd_image" "$external_kernel"
rm -rf "$root_dir"

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/pre-boot-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "prepared Arch Linux ARM root filesystem check failed with status $fsck_status"
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

log 'booting official Arch Linux ARM generic AArch64 userspace and kernel'
qemu-system-aarch64 \
	-name daemon-archlinux-systemd-itest \
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
		fail 'Arch Linux ARM guest reported a kernel or root-filesystem boot failure'
	fi
	if (( SECONDS >= deadline )); then
		fail "timed out waiting for the Arch Linux ARM guest after ${vm_boot_timeout}s"
	fi
	if (( SECONDS >= next_progress )); then
		last_marker=$(grep -aE 'DAEMON_ITEST_|\[(armbian-boot|systemd-itest)\]' "$serial_log" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
		if [[ -n "$last_marker" ]]; then
			log "waiting for guest poweroff ($last_marker)"
		else
			log "waiting for Arch Linux ARM guest boot/test ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
		fi
		next_progress=$((SECONDS + 15))
	fi
	sleep 2
done
qemu_pid=

fsck_status=0
e2fsck -fy "$vm_rootfs" >"$artifact_dir/post-test-e2fsck.log" 2>&1 || fsck_status=$?
(( fsck_status <= 1 )) || fail "post-test Arch Linux ARM filesystem check failed with status $fsck_status"

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
	fail 'Arch Linux ARM guest did not write a result'
}
result=$(tr -d '\r\n' <"$result_file")
[[ "$result" == PASS ]] || {
	tail -n 240 "$serial_log" >&2 || true
	fail "Arch Linux ARM guest result: $result"
}
grep -Fq 'DAEMON_ITEST_REBOOT' "$serial_log" || fail 'Arch Linux ARM guest did not record its reboot phase'
grep -Fq 'DAEMON_ITEST_PASS' "$serial_log" || fail 'Arch Linux ARM guest did not emit the pass marker'
if grep -aEq 'Kernel panic|DAEMON_ITEST_FAIL' "$serial_log"; then
	fail 'Arch Linux ARM serial log contains a failure marker'
fi
grep -Fq "$expected_guest_marker" "$artifact_dir/guest/success-environment.txt" || fail "guest artifacts do not identify $lane_display"
grep -Fq 'systemd' "$artifact_dir/guest/success-environment.txt" || fail 'guest artifacts do not identify systemd'

log "$lane_display generic AArch64 application-level test passed"
