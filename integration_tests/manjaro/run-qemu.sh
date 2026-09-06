#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
integration_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
image_name=Manjaro-ARM-minimal-generic-23.02.img.xz
image_url=https://github.com/manjaro-arm/generic-images/releases/download/23.02/$image_name
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

export INTEGRATION_LANE_ID=${INTEGRATION_LANE_ID:-manjaro}
export INTEGRATION_LANE_DISPLAY=${INTEGRATION_LANE_DISPLAY:-Manjaro ARM}
export INTEGRATION_EXPECTED_GUEST_MARKER=${INTEGRATION_EXPECTED_GUEST_MARKER:-Manjaro}
export ARMBIAN_RELEASE=23.02
export ARMBIAN_BOARD=${ARMBIAN_BOARD:-generic-aarch64}
export ARMBIAN_CODENAME=${ARMBIAN_CODENAME:-stable}
export ARMBIAN_IMAGE_NAME=$image_name
export ARMBIAN_IMAGE_URL=$image_url
export ARMBIAN_IMAGE_SHA256=d780b4ed1d0cb734a70a9fdb81626bc00f6aa732a07891e79a797d1b3078849a
export ARMBIAN_ROOT_PARTITION_INDEX=${MANJARO_ROOT_PARTITION_INDEX:-2}
export ARMBIAN_ROOTFS_SIZE_MIB=${MANJARO_ROOTFS_SIZE_MIB:-4096}
export ARMBIAN_BOOT_KERNEL=$kernel
export ARMBIAN_BOOT_KERNEL_SOURCE=$kernel_url
export VM_BOOT_TIMEOUT=${VM_BOOT_TIMEOUT:-1200}

exec "$integration_dir/armbian/run-qemu.sh" "$@"
