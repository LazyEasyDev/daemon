#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

buildroot_ref=${BUILDROOT_REF:-2026.02.3}
buildroot_dir=${BUILDROOT_DIR:-$repo_dir/.cache/buildroot-$buildroot_ref}
output_root=${BUILDROOT_OUTPUT_ROOT:-/var/tmp/daemon-buildroot-matrix}
profiles_csv=${BUILDROOT_PROFILES:-baseline,debug,release}
jobs=${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}
kernel_fragment=${BUILDROOT_KERNEL_FRAGMENT:-$script_dir/fragments/linux-libvirt-aarch64.fragment}

log() {
    printf '[buildroot-matrix] %s\n' "$*"
}

fail() {
    printf '[buildroot-matrix] ERROR: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"
}

for command in git make sed awk; do
    require_command "$command"
done

[[ -f "$kernel_fragment" ]] || fail "kernel fragment not found: $kernel_fragment"

mkdir -p "$output_root"

# Buildroot rejects the Rust uutils implementation of install. Prefer GNU
# install only for this builder rather than changing the host-wide alternative.
if install --version 2>&1 | grep -q 'uutils'; then
    require_command gnuinstall
    host_tools_dir="$output_root/.host-tools"
    mkdir -p "$host_tools_dir"
    ln -sf "$(command -v gnuinstall)" "$host_tools_dir/install"
    export PATH="$host_tools_dir:$PATH"
    log "using GNU install through $host_tools_dir/install"
fi

if [[ ! -d "$buildroot_dir/.git" ]]; then
    log "cloning Buildroot $buildroot_ref into $buildroot_dir"
    mkdir -p "$(dirname "$buildroot_dir")"
    git clone --depth 1 --branch "$buildroot_ref" https://github.com/buildroot/buildroot "$buildroot_dir"
else
    log "using existing Buildroot tree at $buildroot_dir"
fi

profile_fragment() {
    case "$1" in
        baseline) printf '%s\n' "$script_dir/fragments/baseline.fragment" ;;
        debug) printf '%s\n' "$script_dir/fragments/debug.fragment" ;;
        release) printf '%s\n' "$script_dir/fragments/release.fragment" ;;
        *) return 1 ;;
    esac
}

manifest="$output_root/manifest.tsv"
printf 'profile\tout_dir\tkernel\trootfs_ext2\trootfs_cpio\n' >"$manifest"

IFS=',' read -r -a profiles <<<"$profiles_csv"
for profile in "${profiles[@]}"; do
    profile=$(printf '%s' "$profile" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -n "$profile" ]] || continue

    fragment=$(profile_fragment "$profile") || fail "unknown profile '$profile' (valid: baseline,debug,release)"
    [[ -f "$fragment" ]] || fail "profile fragment not found: $fragment"

    out_dir="$output_root/$profile"
    log "configuring profile '$profile' (output: $out_dir)"
    rm -rf "$out_dir"
    mkdir -p "$out_dir"

    make -C "$buildroot_dir" O="$out_dir" qemu_aarch64_virt_defconfig >/dev/null

    cat >>"$out_dir/.config" <<EOF

# daemon-util Buildroot matrix base options
BR2_TARGET_GENERIC_GETTY_PORT="ttyAMA0"
BR2_TARGET_GENERIC_GETTY_BAUDRATE_115200=y
BR2_TARGET_ENABLE_ROOT_LOGIN=y
BR2_TARGET_GENERIC_ROOT_PASSWD="root"
BR2_SYSTEM_DHCP="eth0"
BR2_PRIMARY_SITE="https://sources.buildroot.net"
BR2_PRIMARY_SITE_ONLY=y
BR2_LINUX_KERNEL_CONFIG_FRAGMENT_FILES="$kernel_fragment"
# The integration runner uses the host's libvirt/QEMU, so avoid building a
# second host QEMU just for the upstream QEMU board defconfig.
BR2_PACKAGE_HOST_QEMU=n
EOF

    cat "$fragment" >>"$out_dir/.config"
    make -C "$buildroot_dir" O="$out_dir" olddefconfig >/dev/null

    log "building profile '$profile' with -j$jobs"
    make -C "$buildroot_dir" O="$out_dir" -j"$jobs"

    kernel="$out_dir/images/Image"
    rootfs_ext2="$out_dir/images/rootfs.ext2"
    rootfs_cpio="$out_dir/images/rootfs.cpio"

    [[ -f "$kernel" ]] || fail "missing kernel image for profile '$profile': $kernel"
    [[ -f "$rootfs_ext2" ]] || fail "missing rootfs.ext2 for profile '$profile': $rootfs_ext2"

    printf '%s\t%s\t%s\t%s\t%s\n' \
        "$profile" "$out_dir" "$kernel" "$rootfs_ext2" "${rootfs_cpio:-}" >>"$manifest"
done

log "Buildroot matrix completed"
log "manifest: $manifest"
