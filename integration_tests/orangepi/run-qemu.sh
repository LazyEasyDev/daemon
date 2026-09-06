#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
integration_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
tool_dir=${INTEGRATION_TOOL_DIR:-/var/tmp/daemon-sbc-tools/7zip}
archive_name=Orangepi5_1.2.4_ubuntu_jammy_server_linux6.1.99.7z
image_name=Orangepi5_1.2.4_ubuntu_jammy_server_linux6.1.99.img
drive_id=${ORANGEPI_DRIVE_ID:-13k0XdEd6fREzo12Qa1VfthYxKaCHcOAr}
archive_url=${ORANGEPI_IMAGE_URL:-https://drive.usercontent.google.com/download?id=$drive_id\&export=download\&confirm=t}
archive_sha256=${ORANGEPI_ARCHIVE_SHA256:-f9af8e19f8abfe8e55d763b31930d220a95975561d4b1d4112bf5221153cd20d}
image_sha256=${ORANGEPI_IMAGE_SHA256:-d47109d059f47d8a3d33a7639715a82527dadbb46dd78b7d1e06f2e624a42426}
archive=$cache_dir/$archive_name
image=$cache_dir/$image_name
kernel_name=yocto-5.0.19-Image-qemuarm64.bin
kernel=$cache_dir/$kernel_name
kernel_url=https://downloads.yoctoproject.org/releases/yocto/yocto-5.0.19/machines/qemu/qemuarm64/Image-qemuarm64.bin
kernel_sha256=0c0c54abedcd21de71b955fe02328939a03040a5a38ddf9d4f60e6600cf96155

mkdir -p "$cache_dir" "$tool_dir"
if [[ ! -f "$archive" ]]; then
	wget --progress=dot:giga -O "$archive.partial" "$archive_url"
	mv "$archive.partial" "$archive"
fi
printf '%s  %s\n' "$archive_sha256" "$archive" | sha256sum --check --status -

seven_zip=$(command -v 7zz || command -v 7z || true)
if [[ -z "$seven_zip" ]]; then
	if [[ ! -x "$tool_dir/root/usr/lib/7zip/7zz" ]]; then
		(
			cd "$tool_dir"
			apt-get download 7zip-standalone
			dpkg-deb -x 7zip-standalone_*.deb root
		)
	fi
	seven_zip=$tool_dir/root/usr/lib/7zip/7zz
fi
if [[ ! -f "$image" ]]; then
	"$seven_zip" e -y -o"$cache_dir" "$archive" "$image_name"
fi
printf '%s  %s\n' "$image_sha256" "$image" | sha256sum --check --status -

if [[ ! -f "$kernel" ]]; then
	wget --progress=dot:giga -O "$kernel.partial" "$kernel_url"
	printf '%s  %s\n' "$kernel_sha256" "$kernel.partial" | sha256sum --check --status -
	mv "$kernel.partial" "$kernel"
fi
printf '%s  %s\n' "$kernel_sha256" "$kernel" | sha256sum --check --status -

export INTEGRATION_LANE_ID=${INTEGRATION_LANE_ID:-orangepi}
export INTEGRATION_LANE_DISPLAY=${INTEGRATION_LANE_DISPLAY:-Orange Pi Ubuntu}
export INTEGRATION_EXPECTED_GUEST_MARKER=${INTEGRATION_EXPECTED_GUEST_MARKER:-Orange Pi Ubuntu}
export ARMBIAN_RELEASE=${ARMBIAN_RELEASE:-1.2.4}
export ARMBIAN_BOARD=${ARMBIAN_BOARD:-Orange-Pi-5}
export ARMBIAN_CODENAME=${ARMBIAN_CODENAME:-jammy}
export ARMBIAN_IMAGE=$image
export ARMBIAN_IMAGE_NAME=$image_name
export ARMBIAN_IMAGE_URL=$archive_url
export ARMBIAN_IMAGE_SHA256=$image_sha256
export ARMBIAN_IMAGE_COMPRESSION=none
export ARMBIAN_ROOT_PARTITION_NAME=${ORANGEPI_ROOT_PARTITION_NAME:-rootfs}
export ARMBIAN_ROOT_PARTITION_INDEX=${ORANGEPI_ROOT_PARTITION_INDEX:-1}
export ARMBIAN_ROOTFS_SIZE_MIB=${ORANGEPI_ROOTFS_SIZE_MIB:-4096}
export ARMBIAN_BOOT_KERNEL=$kernel
export ARMBIAN_BOOT_KERNEL_SOURCE=$kernel_url
export VM_MEMORY_MIB=${VM_MEMORY_MIB:-1536}
export VM_BOOT_TIMEOUT=${VM_BOOT_TIMEOUT:-1200}

exec "$integration_dir/armbian/run-qemu.sh" "$@"
