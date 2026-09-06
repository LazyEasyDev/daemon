#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

openwrt_version=${OPENWRT_MIPS_VERSION:-25.12.0}
variants=${OPENWRT_MIPS_VARIANTS:-le,be}
vm_memory_mib=${VM_MEMORY_MIB:-256}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-600}
state_size_mib=${OPENWRT_MIPS_STATE_SIZE_MIB:-128}
rootfs_size_mib=${OPENWRT_MIPS_ROOTFS_SIZE_MIB:-256}
keep_vm=${KEEP_VM:-0}
port=${TEST_APP_PORT:-18080}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
tool_root=${OPENWRT_MIPS_TOOL_ROOT:-/var/tmp/daemon-openwrt-mips-tools}
tool_packages=${OPENWRT_MIPS_TOOL_PACKAGES:-/var/tmp/daemon-openwrt-mips-packages}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
artifact_dir="$artifact_root/openwrt-mips-$run_id"
qemu_pid=
current_work=

log() { printf '[openwrt-mips] %s\n' "$*"; }
fail() { printf '[openwrt-mips] ERROR: %s\n' "$*" >&2; return 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"; }

for command in apt-get debugfs dpkg-deb e2fsck gzip go mkfs.ext4 resize2fs sha256sum tar truncate wget; do
	require_command "$command"
done

mkdir -p "$cache_dir" "$artifact_dir" "$tool_root" "$tool_packages"

install_tool_package() {
	local package=$1 pattern=$2 deb
	if ! compgen -G "$tool_packages/$pattern" >/dev/null; then
		(cd "$tool_packages" && apt-get download "$package")
	fi
	for deb in "$tool_packages"/$pattern; do
		dpkg-deb -x "$deb" "$tool_root"
	done
}

prepare_qemu() {
	if [[ ! -x "$tool_root/usr/bin/qemu-system-mips" ]]; then
		log 'extracting rootless qemu-system-mips package'
		install_tool_package qemu-system-mips 'qemu-system-mips_*.deb'
	fi
	qemu_mips="$tool_root/usr/bin/qemu-system-mips"
	qemu_mipsel="$tool_root/usr/bin/qemu-system-mipsel"
	[[ -x "$qemu_mips" && -x "$qemu_mipsel" ]] || fail 'rootless MIPS QEMU binaries are missing'
}

prepare_qemu

cleanup() {
	local status=$?
	trap - EXIT
	if [[ "$qemu_pid" =~ ^[0-9]+$ ]] && kill -0 "$qemu_pid" 2>/dev/null; then
		kill "$qemu_pid" >/dev/null 2>&1 || true
		for _ in $(seq 1 20); do
			kill -0 "$qemu_pid" >/dev/null 2>&1 || break
			sleep 1
		done
		kill -9 "$qemu_pid" >/dev/null 2>&1 || true
	fi
	if [[ "$keep_vm" != 1 && -n "$current_work" ]]; then
		rm -rf "$current_work" || true
	fi
	if (( status == 0 )); then
		log "artifacts: $artifact_dir"
	else
		log "test failed; artifacts: $artifact_dir" >&2
	fi
	exit "$status"
}
trap cleanup EXIT

sha256_matches() {
	local path=$1 expected=$2
	printf '%s  %s\n' "$expected" "$path" | sha256sum --check --status -
}

download_verified() {
	local url=$1 path=$2 expected=$3
	if [[ -f "$path" ]] && ! sha256_matches "$path" "$expected"; then
		rm -f "$path"
	fi
	if [[ ! -f "$path" ]]; then
		log "downloading ${url##*/}"
		wget --progress=dot:giga -O "$path.partial" "$url"
		sha256_matches "$path.partial" "$expected" || fail "checksum verification failed: ${url##*/}"
		mv "$path.partial" "$path"
	fi
	sha256_matches "$path" "$expected" || fail "cached checksum verification failed: $path"
}

write_file() {
	local image=$1 source=$2 destination=$3 mode=$4 log_file=$5
	debugfs -w -R "rm $destination" "$image" >>"$log_file" 2>&1 || true
	debugfs -w -R "write $source $destination" "$image" >>"$log_file" 2>&1
	debugfs -w -R "set_inode_field $destination mode $mode" "$image" >>"$log_file" 2>&1
}

qemu_is_running() {
	local state
	kill -0 "$qemu_pid" 2>/dev/null || return 1
	state=$(awk '/^State:/ {print $2; exit}' "/proc/$qemu_pid/status" 2>/dev/null || true)
	[[ "$state" != Z ]]
}

check_filesystem() {
	local image=$1 output=$2 status=0
	e2fsck -fy "$image" >"$output" 2>&1 || status=$?
	(( status <= 1 )) || fail "filesystem check failed with status $status: $image"
}

read_state_file() {
	local state_image=$1 path=$2
	debugfs -R "cat $path" "$state_image" 2>/dev/null || true
}

run_variant() {
	local variant=$1 qemu go_arch target kernel_sha rootfs_sha
	local base_url filename_prefix kernel rootfs_gz rootfs state_image variant_artifact
	local build_dir service_name state_prefix source_info debugfs_log pidfile serial_log qemu_log
	local phase result result_file guest_log artifact_tar fsck_status

	case "$variant" in
		le)
			qemu=$qemu_mipsel
			go_arch=mipsle
			target=malta/le
			kernel_sha=56393aa7279faf747e5c07ef8e7712c73e641f2d92328c07d97cd95b368cc086
			rootfs_sha=6402c44bf8346e9186264bee542b0d3765555334c711aa05b485d81b1b7ee5af
			;;
		be)
			qemu=$qemu_mips
			go_arch=mips
			target=malta/be
			kernel_sha=7640168225bfaff20707634e0445b9a8a4d5e3ba52ed9dbee8126dc455d73794
			rootfs_sha=868f0d18e6e0b1e6b303ee2f306d74e0444f17bc47f939df0e61083c40068b5d
			;;
		*) fail "OPENWRT_MIPS_VARIANTS entries must be le or be: $variant" ;;
	esac

	current_work="${VM_WORK_DIR:-/var/tmp/daemon-openwrt-mips-$run_id-$variant}"
	variant_artifact="$artifact_dir/$variant"
	mkdir -p "$current_work" "$variant_artifact"
	filename_prefix="openwrt-${openwrt_version}-malta-${variant}"
	base_url="https://downloads.openwrt.org/releases/${openwrt_version}/targets/malta/${variant}"
	kernel="$cache_dir/$filename_prefix-vmlinux.elf"
	rootfs_gz="$cache_dir/$filename_prefix-rootfs-ext4.img.gz"
	download_verified "$base_url/$filename_prefix-vmlinux.elf" "$kernel" "$kernel_sha"
	download_verified "$base_url/$filename_prefix-rootfs-ext4.img.gz" "$rootfs_gz" "$rootfs_sha"
	printf '%s  %s\n%s  %s\n' "$kernel_sha" "${kernel##*/}" "$rootfs_sha" "${rootfs_gz##*/}" >"$variant_artifact/base-images.sha256"

	rootfs="$current_work/rootfs.ext4"
	gzip -dc "$rootfs_gz" >"$rootfs"
	check_filesystem "$rootfs" "$variant_artifact/pre-resize-e2fsck.log"
	truncate -s "${rootfs_size_mib}M" "$rootfs"
	resize2fs "$rootfs" >"$variant_artifact/resize2fs.log" 2>&1

	state_image="$current_work/state.ext4"
	truncate -s "${state_size_mib}M" "$state_image"
	mkfs.ext4 -q -F -L daemon-state "$state_image"

	build_dir="$current_work/build"
	mkdir -p "$build_dir"
	service_name="mips${variant}$(date +%s)$$"
	state_prefix="openwrt-mips-$variant"
	log "building Linux/$go_arch soft-float integration binaries"
	(
		cd "$repo_dir"
		CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" GOMIPS=softfloat go build -trimpath -o "$build_dir/daemon" .
		CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" GOMIPS=softfloat go build -trimpath -o "$build_dir/test-app" ./test_app
	)
	printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
	cp "$repo_dir/integration_tests/openwrt/guest-test.sh" "$build_dir/guest-test.sh"
	cp "$repo_dir/integration_tests/interpreted-app-test.sh" "$build_dir/interpreted-app-test.sh"
	cp "$script_dir/boot-test.sh" "$build_dir/boot-test.sh"
	cat >"$build_dir/test-config" <<EOF
SERVICE_NAME='$service_name'
TEST_APP_PORT='$port'
STATE_PREFIX='$state_prefix'
EOF
	cat >"$build_dir/source-info" <<EOF
OpenWrt release: $openwrt_version
Target: $target
Architecture: $go_arch softfloat
Kernel: ${kernel##*/}
Root filesystem: ${rootfs_gz##*/}
EOF
	chmod 0755 "$build_dir/daemon" "$build_dir/test-app" "$build_dir/guest-test.sh" "$build_dir/interpreted-app-test.sh" "$build_dir/boot-test.sh"
	chmod 0644 "$build_dir/relative-path-test.txt" "$build_dir/test-config" "$build_dir/source-info"

	debugfs_log="$variant_artifact/debugfs.log"
	: >"$debugfs_log"
	for directory in /opt /opt/daemon-itest /var/lib/daemon-itest; do
		debugfs -w -R "mkdir $directory" "$rootfs" >>"$debugfs_log" 2>&1 || true
	done
	write_file "$rootfs" "$build_dir/daemon" /opt/daemon-itest/daemon 0100755 "$debugfs_log"
	write_file "$rootfs" "$build_dir/test-app" /opt/daemon-itest/test-app 0100755 "$debugfs_log"
	write_file "$rootfs" "$build_dir/guest-test.sh" /opt/daemon-itest/guest-test.sh 0100755 "$debugfs_log"
	write_file "$rootfs" "$build_dir/interpreted-app-test.sh" /opt/daemon-itest/interpreted-app-test.sh 0100755 "$debugfs_log"
	write_file "$rootfs" "$build_dir/relative-path-test.txt" /opt/daemon-itest/relative-path-test.txt 0100644 "$debugfs_log"
	write_file "$rootfs" "$build_dir/test-config" /opt/daemon-itest/test-config 0100644 "$debugfs_log"
	write_file "$rootfs" "$build_dir/source-info" /etc/daemon-itest-image-source 0100644 "$debugfs_log"
	write_file "$rootfs" "$build_dir/boot-test.sh" /etc/init.d/daemon-itest-boot 0100755 "$debugfs_log"
	debugfs -w -R 'rm /etc/rc.d/S99daemon-itest-boot' "$rootfs" >>"$debugfs_log" 2>&1 || true
	debugfs -w -R 'symlink /etc/rc.d/S99daemon-itest-boot ../init.d/daemon-itest-boot' "$rootfs" >>"$debugfs_log" 2>&1
	check_filesystem "$rootfs" "$variant_artifact/prepared-e2fsck.log"
	chmod 0644 "$rootfs" "$state_image" "$kernel"

	pidfile="$current_work/qemu.pid"
	qemu_log="$variant_artifact/qemu.log"
	launch_guest() {
		local boot_phase=$1
		local phase_serial="$variant_artifact/serial-$boot_phase.log"
		rm -f "$pidfile" "$phase_serial"
		log "booting OpenWrt $openwrt_version $target for $boot_phase"
		"$qemu" -M malta -cpu 24Kf -m "$vm_memory_mib" \
			-kernel "$kernel" \
			-append 'root=/dev/sda rootfstype=ext4 rw rootwait console=ttyS0,115200 panic=1' \
			-drive "file=$rootfs,format=raw,index=0,media=disk" \
			-drive "file=$state_image,format=raw,index=1,media=disk" \
			-netdev user,id=net0 -device pcnet,netdev=net0 \
			-display none -monitor none -serial "file:$phase_serial" \
			-D "$qemu_log" -daemonize -pidfile "$pidfile"
		qemu_pid=$(cat "$pidfile")
	}
	wait_for_poweroff() {
		local boot_phase=$1
		local phase_serial="$variant_artifact/serial-$boot_phase.log"
		local deadline=$((SECONDS + vm_boot_timeout)) next_progress=$SECONDS
		while qemu_is_running; do
			if grep -aqE 'Kernel panic|VFS: Unable to mount root fs|No working init found|DAEMON_ITEST_FAIL' "$phase_serial" 2>/dev/null; then
				fail "OpenWrt $target reported a boot or test failure during $boot_phase"
			fi
			if (( SECONDS >= deadline )); then
				fail "timed out waiting for OpenWrt $target $boot_phase after ${vm_boot_timeout}s"
			fi
			if (( SECONDS >= next_progress )); then
				log "waiting for OpenWrt $target $boot_phase ($((SECONDS + vm_boot_timeout - deadline))s elapsed)"
				next_progress=$((SECONDS + 15))
			fi
			sleep 2
		done
		qemu_pid=
	}

	launch_guest pre-reboot
	wait_for_poweroff pre-reboot
	check_filesystem "$rootfs" "$variant_artifact/post-pre-reboot-rootfs-e2fsck.log"
	check_filesystem "$state_image" "$variant_artifact/post-pre-reboot-state-e2fsck.log"
	phase=$(read_state_file "$state_image" "/$state_prefix-phase" | tr -d '\r\n')
	[[ "$phase" == post-reboot ]] || fail "OpenWrt $target pre-reboot phase did not complete: ${phase:-missing phase marker}"

	launch_guest post-reboot
	wait_for_poweroff post-reboot
	check_filesystem "$rootfs" "$variant_artifact/post-test-rootfs-e2fsck.log"
	check_filesystem "$state_image" "$variant_artifact/post-test-state-e2fsck.log"
	cat "$variant_artifact/serial-pre-reboot.log" "$variant_artifact/serial-post-reboot.log" >"$variant_artifact/serial.log"

	result_file="$variant_artifact/result.txt"
	guest_log="$variant_artifact/guest-test.log"
	artifact_tar="$variant_artifact/guest-artifacts.tar"
	debugfs -R "dump /$state_prefix-result $result_file" "$state_image" >/dev/null 2>&1 || true
	debugfs -R "dump /$state_prefix-test.log $guest_log" "$state_image" >/dev/null 2>&1 || true
	debugfs -R "dump /$state_prefix-artifacts.tar $artifact_tar" "$state_image" >/dev/null 2>&1 || true
	if [[ -s "$artifact_tar" ]]; then
		mkdir -p "$variant_artifact/guest"
		tar -C "$variant_artifact/guest" -xf "$artifact_tar"
	fi
	[[ "$(tr -d '\r\n' <"$result_file")" == PASS ]] || fail "OpenWrt $target guest result is not PASS"
	phase=$(read_state_file "$state_image" "/$state_prefix-phase" | tr -d '\r\n')
	[[ "$phase" == complete ]] || fail "OpenWrt $target did not complete its post-reboot phase"
	grep -Fq "DISTRIB_TARGET='$target'" "$variant_artifact/guest/success-environment.txt" || fail "guest does not identify target $target"
	grep -Fq 'all OpenWrt application-level tests passed' "$guest_log" || fail "OpenWrt $target lifecycle completion marker is missing"
	log "OpenWrt $openwrt_version $target application-level test passed"

	if [[ "$keep_vm" != 1 ]]; then
		rm -rf "$current_work"
		current_work=
	fi
}

IFS=',' read -r -a requested_variants <<<"$variants"
for variant in "${requested_variants[@]}"; do
	run_variant "$variant"
done
printf 'PASS %s\n' "$variants" >"$artifact_dir/result.txt"
