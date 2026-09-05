#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

variants=${RASPIOS_VARIANTS:-armhf,arm64}
raspios_release=${RASPIOS_RELEASE:-2026-06-18}
raspios_image_release=${RASPIOS_IMAGE_RELEASE:-2026-06-19}
debian_release=${DEBIAN_RELEASE:-trixie}
vm_memory_mib=${VM_MEMORY_MIB:-1024}
vm_vcpus=${VM_VCPUS:-2}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-900}
qemu_accel=${QEMU_ACCEL:-tcg}
ssh_port_base=${RASPIOS_SSH_PORT:-55222}
port=${TEST_APP_PORT:-18080}
keep_vm=${KEEP_VM:-0}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
work_root=${VM_WORK_DIR:-/var/tmp/daemon-raspios-itest-$run_id}
artifact_dir="$artifact_root/raspios-$run_id"
ssh_user=daemon-itest
current_variant=
current_work=
current_pid=
remote_ready=0

log() {
	printf '[raspios-qemu] %s\n' "$*" >&2
}

fail() {
	printf '[raspios-qemu] ERROR: %s\n' "$*" >&2
	return 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in go qemu-system-arm qemu-system-aarch64 ssh scp ssh-keygen wget \
	xz cpio dpkg-deb depmod fdisk dd mcopy sha256sum python3; do
	require_command "$command"
done

mkdir -p "$cache_dir" "$artifact_dir" "$work_root"
chmod 0755 "$cache_dir" "$work_root"

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

download_verified() {
	local url=$1 path=$2 expected=$3
	mkdir -p "$(dirname "$path")"
	if [[ -f "$path" ]] && ! sha256_matches "$path" "$expected"; then
		log "discarding cached file with stale checksum: $path"
		rm -f "$path"
	fi
	if [[ ! -f "$path" ]]; then
		local partial="$path.partial"
		log "downloading $(basename "$path")"
		rm -f "$partial"
		wget --progress=dot:giga -O "$partial" "$url"
		sha256_matches "$partial" "$expected" || fail "SHA-256 verification failed for $url"
		mv "$partial" "$path"
	fi
	sha256_matches "$path" "$expected" || fail "SHA-256 verification failed for $path"
}

published_checksum() {
	local sums=$1 relative_path=$2
	awk -v path="$relative_path" '$2 == path || $2 == "*" path {print $1; exit}' "$sums"
}

package_metadata() {
	local packages=$1
	shift
	python3 - "$packages" "$@" <<'PY'
import gzip
import sys

path, *wanted = sys.argv[1:]
with gzip.open(path, "rt", encoding="utf-8") as source:
    records = source.read().split("\n\n")
packages = {}
for record in records:
    fields = {}
    for line in record.splitlines():
        if ": " in line:
            key, value = line.split(": ", 1)
            fields[key] = value
    if "Package" in fields:
        packages[fields["Package"]] = fields
for name in wanted:
    fields = packages.get(name)
    if not fields:
        raise SystemExit(f"package metadata not found: {name}")
    print("\t".join((name, fields["Filename"], fields["SHA256"])))
PY
}

copy_guest_artifacts() {
	if [[ "$remote_ready" != 1 || -z "$current_work" || -z "$current_variant" ]]; then
		return
	fi
	local target="$artifact_dir/$current_variant/guest"
	mkdir -p "$target"
	local service_name="raspios${current_variant}"
	scp "${scp_options[@]}" -r \
		"$ssh_user@127.0.0.1:/var/tmp/daemon-itest-$service_name/artifacts/." \
		"$target/" >/dev/null 2>&1 || true
}

stop_current_vm() {
	if [[ "$current_pid" =~ ^[0-9]+$ ]]; then
		kill "$current_pid" >/dev/null 2>&1 || true
		for _ in $(seq 1 20); do
			kill -0 "$current_pid" >/dev/null 2>&1 || break
			sleep 1
		done
		kill -9 "$current_pid" >/dev/null 2>&1 || true
	fi
	current_pid=
}

collect_host_artifacts() {
	[[ -n "$current_work" && -n "$current_variant" ]] || return
	local target="$artifact_dir/$current_variant"
	mkdir -p "$target"
	cp -f "$current_work/serial.log" "$target/" 2>/dev/null || true
	cp -f "$current_work/qemu.log" "$target/" 2>/dev/null || true
	{
		printf 'variant=%s\n' "$current_variant"
		printf 'qemu_pid=%s\n' "$current_pid"
		printf 'qemu_accel=%s\n' "$qemu_accel"
		printf 'memory_mib=%s\n' "$vm_memory_mib"
		printf 'vcpus=%s\n' "$vm_vcpus"
	} >"$target/host-environment.txt"
}

cleanup() {
	local status=$?
	trap - EXIT
	copy_guest_artifacts
	collect_host_artifacts
	if [[ "$keep_vm" == 1 ]]; then
		if [[ -n "$current_pid" ]]; then
			log "keeping $current_variant QEMU PID $current_pid and work directory $current_work"
		fi
		log "keeping Raspberry Pi OS work root $work_root"
	else
		stop_current_vm
		if [[ -n "$current_work" ]]; then
			rm -rf "$current_work" || true
		fi
		rm -rf "$work_root" || true
	fi
	if (( status == 0 )); then
		log "artifacts: $artifact_dir"
	else
		log "test failed; artifacts: $artifact_dir" >&2
	fi
	exit "$status"
}
trap cleanup EXIT

prepare_debian_boot() {
	local variant=$1 work=$2
	local installer_base="https://deb.debian.org/debian/dists/$debian_release/main/installer-$variant/current/images"
	local kernel_relative kernel_url kernel_path initrd_relative initrd_url initrd_path
	case "$variant" in
		arm64)
			kernel_relative=./netboot/debian-installer/arm64/linux
			initrd_relative=./netboot/debian-installer/arm64/initrd.gz
			;;
		armhf)
			kernel_relative=./netboot/vmlinuz
			initrd_relative=./netboot/initrd.gz
			;;
		*) fail "unsupported Raspberry Pi OS variant: $variant" ;;
	esac
	kernel_url="$installer_base/${kernel_relative#./}"
	initrd_url="$installer_base/${initrd_relative#./}"
	kernel_path="$cache_dir/debian-$debian_release-$variant-${kernel_relative##*/}"
	initrd_path="$cache_dir/debian-$debian_release-$variant-initrd.gz"

	local sums="$work/debian-SHA256SUMS"
	wget -q -O "$sums" "$installer_base/SHA256SUMS"
	local kernel_sum initrd_sum
	kernel_sum=$(published_checksum "$sums" "$kernel_relative")
	initrd_sum=$(published_checksum "$sums" "$initrd_relative")
	[[ -n "$kernel_sum" && -n "$initrd_sum" ]] || fail "Debian checksums are missing for $variant netboot artifacts"
	download_verified "$kernel_url" "$kernel_path" "$kernel_sum"
	download_verified "$initrd_url" "$initrd_path" "$initrd_sum"

	local root="$work/initrd-root"
	mkdir -p "$root"
	(
		cd "$root"
		gzip -dc "$initrd_path" | cpio -idmu --quiet 2>/dev/null || true
	)
	[[ -x "$root/usr/bin/busybox" ]] || fail "Debian $variant initrd did not contain BusyBox"

	local version
	version=$(find "$root/usr/lib/modules" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | head -n 1)
	[[ -n "$version" ]] || fail "could not determine the Debian $variant kernel version"

	local packages="$work/Packages.gz"
	wget -q -O "$packages" "https://deb.debian.org/debian/dists/$debian_release/main/debian-installer/binary-$variant/Packages.gz"
	local package_names=(
		"kernel-image-$version-di"
		"ext4-modules-$version-di"
		"fat-modules-$version-di"
		"scsi-core-modules-$version-di"
		"scsi-modules-$version-di"
		"usb-modules-$version-di"
		"usb-storage-modules-$version-di"
	)
	local package filename checksum package_path
	while IFS=$'\t' read -r package filename checksum; do
		package_path="$cache_dir/${filename##*/}"
		download_verified "https://deb.debian.org/debian/$filename" "$package_path" "$checksum"
		dpkg-deb -x "$package_path" "$root"
	done < <(package_metadata "$packages" "${package_names[@]}")

	mkdir -p "$root/lib/modules/$version/kernel"
	cp -a "$root/usr/lib/modules/$version/kernel/." "$root/lib/modules/$version/kernel/"
	case "$variant" in
		arm64)
			install -m 0755 "$root/usr/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1" \
				"$root/lib/ld-linux-aarch64.so.1"
			;;
		armhf)
			install -m 0755 "$root/usr/lib/arm-linux-gnueabihf/ld-linux-armhf.so.3" \
				"$root/lib/ld-linux-armhf.so.3"
			;;
	esac
	depmod -b "$root" "$version"

	cat >"$root/init" <<'EOF'
#!/bin/sh -e

mount -t devtmpfs devtmpfs /dev
mount -t proc proc /proc
mount -t sysfs sysfs /sys

modprobe xhci_pci
modprobe usb_storage
modprobe sd_mod
modprobe virtio_mmio
modprobe virtio_net
modprobe crc32c_generic
modprobe ext4
modprobe nls_cp437
modprobe nls_ascii
modprobe nls_utf8
modprobe vfat

attempt=0
while [ ! -b /dev/sda2 ] && [ "$attempt" -lt 100 ]; do
        sleep 1
        attempt=$((attempt + 1))
done

[ -b /dev/sda2 ] || { echo 'Pi root device /dev/sda2 not found'; exec sh; }
mkdir -p /newroot
mount -t ext4 -o rw /dev/sda2 /newroot || { echo 'Pi root mount failed'; exec sh; }
mount --move /dev /newroot/dev
mount --move /proc /newroot/proc
mount --move /sys /newroot/sys
exec switch_root /newroot /sbin/init
EOF
	chmod 0755 "$root/init"

	local module
	for module in xhci_pci usb_storage sd_mod virtio_mmio virtio_net crc32c_generic ext4 \
		nls_cp437 nls_ascii nls_utf8 vfat; do
		modprobe -d "$root" -S "$version" -D "$module" >/dev/null || fail "missing $variant initramfs module: $module"
	done

	local custom_initrd="$work/pi-initrd.gz"
	(
		cd "$root"
		find . -print0 | cpio --null -o --format=newc --quiet 2>/dev/null | gzip -1 >"$custom_initrd"
	)
	printf '%s\n%s\n' "$kernel_path" "$custom_initrd"
}

prepare_raspios_image() {
	local variant=$1 work=$2 key_path=$3
	local image_name="${raspios_release}-raspios-trixie-${variant}-lite.img.xz"
	local image_url_default="https://downloads.raspberrypi.com/raspios_lite_${variant}/images/raspios_lite_${variant}-${raspios_image_release}/$image_name"
	local image_var checksum_var url_var
	case "$variant" in
		arm64)
			image_var=${RASPIOS_ARM64_IMAGE:-$cache_dir/$image_name}
			url_var=${RASPIOS_ARM64_IMAGE_URL:-$image_url_default}
			checksum_var=${RASPIOS_ARM64_IMAGE_SHA256:-}
			;;
		armhf)
			image_var=${RASPIOS_ARMHF_IMAGE:-$cache_dir/$image_name}
			url_var=${RASPIOS_ARMHF_IMAGE_URL:-$image_url_default}
			checksum_var=${RASPIOS_ARMHF_IMAGE_SHA256:-}
			;;
	esac
	if [[ -z "$checksum_var" ]]; then
		local checksum_file="$work/raspios.sha256"
		wget -q -O "$checksum_file" "$url_var.sha256"
		checksum_var=$(awk 'NF >= 1 {print $1; exit}' "$checksum_file")
	fi
	[[ "$checksum_var" =~ ^[0-9a-fA-F]{64}$ ]] || fail "invalid published Raspberry Pi OS $variant SHA-256"
	download_verified "$url_var" "$image_var" "$checksum_var"

	local raw="$work/raspios.img"
	log "decompressing Raspberry Pi OS $variant"
	xz -dc "$image_var" >"$raw"
	local start sectors
	start=$(fdisk -l "$raw" | awk '$1 ~ /img1$/ {print $2; exit}')
	sectors=$(fdisk -l "$raw" | awk '$1 ~ /img1$/ {print $4; exit}')
	[[ "$start" =~ ^[0-9]+$ && "$sectors" =~ ^[0-9]+$ ]] || fail "could not locate the Pi firmware partition"
	local boot="$work/boot.fat"
	dd if="$raw" of="$boot" bs=512 skip="$start" count="$sectors" status=none

	ssh-keygen -q -t ecdsa -b 256 -N '' -f "$key_path"
	local public_key
	public_key=$(cat "$key_path.pub")
	cat >"$work/user-data" <<EOF
#cloud-config
hostname: daemon-rpios-$variant
manage_etc_hosts: true
ssh_pwauth: false
disable_root: true
users:
  - name: $ssh_user
    gecos: daemon integration test
    groups: [adm, sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: true
    ssh_authorized_keys:
      - $public_key
runcmd:
  - [systemctl, enable, --now, ssh]
EOF
	cat >"$work/meta-data" <<EOF
instance_id: daemon-rpios-$variant-$run_id
dsmode: local
EOF
	cat >"$work/network-config" <<'EOF'
network:
  version: 2
  ethernets:
    eth0:
      dhcp4: true
      optional: false
EOF
	: >"$work/ssh"
	local file
	for file in user-data meta-data network-config ssh; do
		mcopy -o -i "$boot" "$work/$file" "::$file"
	done
	dd if="$boot" of="$raw" bs=512 seek="$start" conv=notrunc status=none
	chmod 0600 "$key_path"
	printf '%s\n' "$raw"
}

ssh_guest() {
	# Arguments are intentionally serialized by SSH for execution in the guest.
	# shellcheck disable=SC2029
	ssh "${ssh_options[@]}" "$ssh_user@127.0.0.1" "$@"
}

wait_for_ssh() {
	local deadline=$((SECONDS + vm_boot_timeout))
	local started=$SECONDS next_progress=$SECONDS
	while (( SECONDS < deadline )); do
		if ssh_guest true >/dev/null 2>&1; then
			remote_ready=1
			return
		fi
		if [[ "$current_pid" =~ ^[0-9]+$ ]] && ! kill -0 "$current_pid" 2>/dev/null; then
			fail "$current_variant QEMU exited before SSH became ready"
		fi
		if grep -aqE 'Kernel panic|Emergency Mode|Pi root (device|mount) failed' "$current_work/serial.log" 2>/dev/null; then
			fail "$current_variant guest reported a boot failure"
		fi
		if (( SECONDS >= next_progress )); then
			log "waiting for $current_variant SSH ($((SECONDS - started))s elapsed)"
			next_progress=$((SECONDS + 15))
		fi
		sleep 3
	done
	fail "timed out waiting for $current_variant SSH"
}

wait_for_cloud_init() {
	local deadline=$((SECONDS + vm_boot_timeout))
	local started=$SECONDS next_progress=$SECONDS
	while (( SECONDS < deadline )); do
		if ssh_guest test -f /var/lib/cloud/instance/boot-finished >/dev/null 2>&1; then
			ssh_guest cloud-init status --long >"$artifact_dir/$current_variant/cloud-init-status.txt" 2>&1 || true
			return
		fi
		if (( SECONDS >= next_progress )); then
			log "waiting for $current_variant cloud-init ($((SECONDS - started))s elapsed)"
			next_progress=$((SECONDS + 15))
		fi
		sleep 3
	done
	fail "timed out waiting for $current_variant cloud-init"
}

wait_for_new_boot() {
	local previous_boot_id=$1
	local deadline=$((SECONDS + vm_boot_timeout))
	local started=$SECONDS next_progress=$SECONDS current_boot_id
	while (( SECONDS < deadline )); do
		current_boot_id=$(ssh_guest cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
		if [[ -n "$current_boot_id" && "$current_boot_id" != "$previous_boot_id" ]]; then
			return
		fi
		if (( SECONDS >= next_progress )); then
			log "waiting for $current_variant reboot ($((SECONDS - started))s elapsed)"
			next_progress=$((SECONDS + 15))
		fi
		sleep 3
	done
	fail "timed out waiting for $current_variant reboot"
}

run_variant() {
	local variant=$1 index=$2
	current_variant=$variant
	current_work="$work_root/$variant"
	current_pid=
	remote_ready=0
	mkdir -p "$current_work" "$artifact_dir/$variant"

	local host_port=$((ssh_port_base + index))
	local key_path="$current_work/id_ecdsa"
	local boot_paths kernel_path initrd_path raw_image
	log "preparing Raspberry Pi OS $variant"
	boot_paths=$(prepare_debian_boot "$variant" "$current_work")
	kernel_path=$(sed -n '1p' <<<"$boot_paths")
	initrd_path=$(sed -n '2p' <<<"$boot_paths")
	raw_image=$(prepare_raspios_image "$variant" "$current_work" "$key_path")

	local go_arch go_arm qemu_binary service_name
	local -a machine_args network_device
	case "$variant" in
		arm64)
			go_arch=arm64
			go_arm=
			qemu_binary=qemu-system-aarch64
			machine_args=(-M virt -cpu cortex-a72)
			network_device=(-device virtio-net-device,netdev=net0)
			;;
		armhf)
			go_arch=arm
			go_arm=6
			qemu_binary=qemu-system-arm
			machine_args=(-M virt,highmem=off -cpu cortex-a15)
			network_device=(-device virtio-net-pci,netdev=net0)
			;;
	esac
	service_name="raspios${variant}"

	local build="$current_work/build"
	mkdir -p "$build"
	log "building Linux/$go_arch${go_arm:+ GOARM=$go_arm} binaries"
	(
		cd "$repo_dir"
		if [[ -n "$go_arm" ]]; then
			CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" GOARM="$go_arm" go build -trimpath -o "$build/daemon" .
			CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" GOARM="$go_arm" go build -trimpath -o "$build/test-app" ./test_app
		else
			CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -trimpath -o "$build/daemon" .
			CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -trimpath -o "$build/test-app" ./test_app
		fi
	)
	cp "$repo_dir/integration_tests/systemd/guest-test.sh" "$build/guest-test.sh"
	printf '%s\n' 'daemon-util relative path test passed' >"$build/relative-path-test.txt"
	chmod 0755 "$build/daemon" "$build/test-app" "$build/guest-test.sh"

	log "starting Raspberry Pi OS $variant on QEMU $qemu_accel"
	"$qemu_binary" \
		-name "daemon-raspios-$variant" \
		"${machine_args[@]}" \
		-accel "$qemu_accel" \
		-smp "$vm_vcpus" \
		-m "$vm_memory_mib" \
		-kernel "$kernel_path" \
		-initrd "$initrd_path" \
		-append 'console=ttyAMA0 root=/dev/sda2 rootfstype=ext4 rootwait rw' \
		-drive "file=$raw_image,if=none,id=rootdisk,format=raw" \
		-device qemu-xhci,id=xhci \
		-device usb-storage,bus=xhci.0,drive=rootdisk \
		"${network_device[@]}" \
		-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$host_port-:22" \
		-display none \
		-serial "file:$current_work/serial.log" \
		-monitor none \
		-D "$current_work/qemu.log" \
		-daemonize \
		-pidfile "$current_work/qemu.pid"
	current_pid=$(cat "$current_work/qemu.pid")
	ssh_options=(
		-i "$key_path"
		-p "$host_port"
		-o BatchMode=yes
		-o ConnectTimeout=5
		-o StrictHostKeyChecking=no
		-o UserKnownHostsFile=/dev/null
		-o LogLevel=ERROR
	)
	scp_options=(
		-i "$key_path"
		-P "$host_port"
		-o BatchMode=yes
		-o ConnectTimeout=5
		-o StrictHostKeyChecking=no
		-o UserKnownHostsFile=/dev/null
		-o LogLevel=ERROR
	)
	wait_for_ssh
	wait_for_cloud_init

	local machine_bits
	machine_bits=$(ssh_guest getconf LONG_BIT)
	case "$variant" in
		arm64)
			[[ "$(ssh_guest uname -m)" == aarch64 && "$machine_bits" == 64 ]] || fail "ARM64 guest architecture check failed"
			;;
		armhf)
			[[ "$(ssh_guest uname -m)" == armv7l && "$machine_bits" == 32 ]] || fail "ARMHF guest architecture check failed"
			;;
	esac
	ssh_guest "test \"\$(cat /proc/1/comm)\" = systemd"
	ssh_guest "test \"\$(findmnt -n -o FSTYPE /boot/firmware)\" = vfat"
	log "$variant guest architecture and Pi firmware mount verified"

	log "copying $variant integration payload"
	scp "${scp_options[@]}" \
		"$build/daemon" \
		"$build/test-app" \
		"$build/relative-path-test.txt" \
		"$build/guest-test.sh" \
		"$ssh_user@127.0.0.1:/tmp/"
	ssh_guest sudo install -d -m 0755 /opt/daemon-itest
	ssh_guest sudo install -m 0755 /tmp/daemon /opt/daemon-itest/daemon
	ssh_guest sudo install -m 0755 /tmp/test-app /opt/daemon-itest/test-app
	ssh_guest sudo install -m 0644 /tmp/relative-path-test.txt /opt/daemon-itest/relative-path-test.txt
	ssh_guest sudo install -m 0755 /tmp/guest-test.sh /opt/daemon-itest/guest-test.sh

	log "running $variant pre-reboot lifecycle checks"
	ssh_guest sudo /opt/daemon-itest/guest-test.sh pre-reboot "$service_name" "$port"
	local previous_boot_id
	previous_boot_id=$(ssh_guest cat /proc/sys/kernel/random/boot_id)
	log "rebooting $variant guest to verify automatic startup"
	ssh_guest sudo systemctl reboot >/dev/null 2>&1 || true
	wait_for_new_boot "$previous_boot_id"
	remote_ready=1

	log "running $variant post-reboot and crash-recovery checks"
	ssh_guest sudo /opt/daemon-itest/guest-test.sh post-reboot "$service_name" "$port"
	copy_guest_artifacts
	collect_host_artifacts
	log "Raspberry Pi OS $variant application-level test passed"

	if [[ "$keep_vm" == 1 ]]; then
		log "keeping $variant QEMU PID $current_pid and work directory $current_work"
	else
		stop_current_vm
		rm -rf "$current_work"
	fi
	current_variant=
	current_work=
	current_pid=
	remote_ready=0
}

IFS=',' read -r -a requested_variants <<<"$variants"
(( ${#requested_variants[@]} > 0 )) || fail 'RASPIOS_VARIANTS must not be empty'
index=0
for variant in "${requested_variants[@]}"; do
	case "$variant" in
		armhf|arm64) ;;
		*) fail "RASPIOS_VARIANTS entries must be armhf or arm64: $variant" ;;
	esac
	run_variant "$variant" "$index"
	index=$((index + 1))
done

log "all requested Raspberry Pi OS application-level tests passed: $variants"
