#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
integration_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)

release=${RADXA_RELEASE:-b42}
image_name=${RADXA_IMAGE_NAME:-rock-5b_debian_bullseye_cli_${release}.img.xz}
release_url=${RADXA_RELEASE_URL:-https://github.com/radxa-build/rock-5b/releases/download/$release}
cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
kernel_name=yocto-5.0.19-Image-qemuarm64.bin
kernel_path=$cache_dir/$kernel_name
kernel_url=https://downloads.yoctoproject.org/releases/yocto/yocto-5.0.19/machines/qemu/qemuarm64/Image-qemuarm64.bin
kernel_sha256=0c0c54abedcd21de71b955fe02328939a03040a5a38ddf9d4f60e6600cf96155

mkdir -p "$cache_dir"
if [[ ! -f "$kernel_path" ]]; then
	wget --progress=dot:giga -O "$kernel_path.partial" "$kernel_url"
	printf '%s  %s\n' "$kernel_sha256" "$kernel_path.partial" | sha256sum --check --status -
	mv "$kernel_path.partial" "$kernel_path"
fi
printf '%s  %s\n' "$kernel_sha256" "$kernel_path" | sha256sum --check --status -

export INTEGRATION_LANE_ID=${INTEGRATION_LANE_ID:-radxa}
export INTEGRATION_LANE_DISPLAY=${INTEGRATION_LANE_DISPLAY:-Radxa OS}
export INTEGRATION_EXPECTED_GUEST_MARKER=${INTEGRATION_EXPECTED_GUEST_MARKER:-Radxa OS}
export ARMBIAN_RELEASE=$release
export ARMBIAN_BOARD=${ARMBIAN_BOARD:-ROCK-5B}
export ARMBIAN_CODENAME=${ARMBIAN_CODENAME:-bullseye}
export ARMBIAN_IMAGE_NAME=$image_name
export ARMBIAN_IMAGE_URL=${RADXA_IMAGE_URL:-$release_url/$image_name}
export ARMBIAN_IMAGE_SHA256=${RADXA_IMAGE_SHA256:-80fe7186b9395a6c2495d215d14af4962be35abb3c2522613f3d4c1f5a0c186c}
export ARMBIAN_UNCOMPRESSED_SHA512=${RADXA_UNCOMPRESSED_SHA512:-e4b961687c2b86252a4b8aa193e70cf973543ec3feceaa4eae03bcc0fd33a676e9a1fdc7d66d9a7c0c7b336708ba2a579ca1b6c751e55954aebaf3c49ca218b4}
export ARMBIAN_BOOT_KERNEL=$kernel_path
export ARMBIAN_BOOT_KERNEL_SOURCE=$kernel_url
export ARMBIAN_ROOT_PARTITION_NAME=${RADXA_ROOT_PARTITION_NAME:-rootfs}
export ARMBIAN_ROOTFS_SIZE_MIB=${RADXA_ROOTFS_SIZE_MIB:-4096}
export VM_MEMORY_MIB=${VM_MEMORY_MIB:-1536}
export VM_BOOT_TIMEOUT=${VM_BOOT_TIMEOUT:-1200}

exec "$integration_dir/armbian/run-qemu.sh" "$@"
