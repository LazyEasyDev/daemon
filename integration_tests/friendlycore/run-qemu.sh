#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
integration_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
image_name=${FRIENDLYCORE_IMAGE_NAME:-rk3568-sd-friendlycore-focal-qt5-6.1-arm64-20260721.img.gz}
drive_id=${FRIENDLYCORE_DRIVE_ID:-1PmPt6-kfSF2BdG-iGrupLGIjZOFEc44k}
image_url=${FRIENDLYCORE_IMAGE_URL:-https://drive.usercontent.google.com/download?id=$drive_id\&export=download\&confirm=t}
kernel=$cache_dir/yocto-5.0.19-Image-qemuarm64.bin
kernel_url=https://downloads.yoctoproject.org/releases/yocto/yocto-5.0.19/machines/qemu/qemuarm64/Image-qemuarm64.bin
kernel_sha256=0c0c54abedcd21de71b955fe02328939a03040a5a38ddf9d4f60e6600cf96155

mkdir -p "$cache_dir"
if [[ ! -f "$kernel" ]]; then
	wget --progress=dot:giga -O "$kernel.partial" "$kernel_url"
	printf '%s  %s\n' "$kernel_sha256" "$kernel.partial" | sha256sum --check --status -
	mv "$kernel.partial" "$kernel"
fi
printf '%s  %s\n' "$kernel_sha256" "$kernel" | sha256sum --check --status -

export INTEGRATION_LANE_ID=${INTEGRATION_LANE_ID:-friendlycore}
export INTEGRATION_LANE_DISPLAY=${INTEGRATION_LANE_DISPLAY:-FriendlyCore}
export INTEGRATION_EXPECTED_GUEST_MARKER=${INTEGRATION_EXPECTED_GUEST_MARKER:-FriendlyCore}
export ARMBIAN_RELEASE=${ARMBIAN_RELEASE:-20260721}
export ARMBIAN_BOARD=${ARMBIAN_BOARD:-NanoPi-R5S}
export ARMBIAN_CODENAME=${ARMBIAN_CODENAME:-focal}
export ARMBIAN_IMAGE_NAME=$image_name
export ARMBIAN_IMAGE_URL=$image_url
export ARMBIAN_IMAGE_SHA256=${FRIENDLYCORE_IMAGE_SHA256:-f6f2ae2fb397902e52b7e836a16645aaba91e310c09d9afa3ac7bc5e819dfcc9}
export ARMBIAN_IMAGE_COMPRESSION=gzip
export ARMBIAN_ROOT_PARTITION_INDEX=${FRIENDLYCORE_ROOT_PARTITION_INDEX:-8}
export ARMBIAN_ROOTFS_SIZE_MIB=${FRIENDLYCORE_ROOTFS_SIZE_MIB:-4096}
export ARMBIAN_BOOT_KERNEL=$kernel
export ARMBIAN_BOOT_KERNEL_SOURCE=$kernel_url
export VM_MEMORY_MIB=${VM_MEMORY_MIB:-1536}
export VM_BOOT_TIMEOUT=${VM_BOOT_TIMEOUT:-1200}

exec "$integration_dir/armbian/run-qemu.sh" "$@"
