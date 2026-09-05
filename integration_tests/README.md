# Application-level integration tests

These tests boot disposable virtual machines and exercise daemon-util through a
real operating-system service manager. They complement `go test ./...`; they do
not replace the package-level tests.

## Current coverage

The systemd lane verifies:

- backend detection with systemd running as PID 1;
- installation, native unit rendering, and boot enablement;
- application argument and working-directory preservation;
- `start`, `status`, `list`, `list -l`, `restart`, `stop`, and `remove`;
- service startup after a guest reboot;
- automatic restart after a nonzero application exit or direct `SIGKILL`;
- graceful shutdown within the configured timeout;
- forced termination after the configured timeout;
- systemd control-group cleanup of a child process; and
- removal of the unit, enablement links, metadata, and application processes.

The Raspberry Pi OS lane runs that systemd lifecycle against both the official
32-bit ARMHF and 64-bit ARM64 Raspberry Pi OS Lite images. It additionally
asserts the guest bitness, mounts the image's real Pi firmware partition, and
verifies service recovery after a direct `SIGKILL`.

The Rocky Linux lane runs the systemd lifecycle on an official Rocky Linux 9
GenericCloud image with SELinux Enforcing. It verifies the real risky-path
warning and noninteractive refusal, safe installation from `/opt` without a
warning bypass, persistent file labels, runtime process context, and absence of
service-specific AVC denials.

The OpenRC lane performs the same application-level lifecycle checks and also
verifies its generated `openrc-run` script, `supervise-daemon` configuration,
default-runlevel registration, respawn behavior, and process-group cleanup.

The Upstart lane boots Ubuntu 14.04 LTS with Upstart as PID 1 and verifies the
generated job definition, boot auto-start, explicit restart, graceful stop,
configured-failure respawn, hard-crash respawn, and complete removal.

The Windows lane installs Windows Server 2019 Evaluation Server Core and
verifies SCM registration, automatic startup, recovery actions, external HTTP
behavior, configured-failure and hard-crash recovery, reboot persistence,
stop/start behavior, metadata cleanup, and service removal.

The FreeBSD lane verifies the rc.d backend, `/usr/sbin/daemon` supervision,
supervisor and application PID files, boot enablement, restart behavior,
graceful and forced shutdown, and removal.

The OpenWrt lane verifies procd backend detection, generated `rc.common`
scripts, boot enablement, respawn behavior, stop timeout handling, and removal.

The Yocto lane boots the official Poky 5.0.19 Scarthgap LTS `qemuarm64`
`core-image-minimal` image with SysVinit. It verifies runlevel registration,
reboot persistence, explicit restart, configured-failure and direct `SIGKILL`
watchdog recovery, graceful and forced process-group cleanup, and removal.

The Buildroot backend is currently covered by package tests and an image-matrix
builder for generating multiple Buildroot variants from source. A dedicated
Buildroot libvirt guest runner can consume these generated images.

The tests use immutable Ubuntu, Rocky Linux, Raspberry Pi OS, Poky, Alpine,
FreeBSD, and OpenWrt images with disposable overlays or copies. Base images are
never modified.

## Ubuntu host prerequisites

Install the packages appropriate for the host architecture:

```sh
sudo apt update
sudo apt install libvirt-daemon-system libvirt-clients virtinst \
  cloud-image-utils qemu-utils qemu-system-x86 qemu-system-arm \
  qemu-efi-aarch64 genisoimage e2fsprogs util-linux wget openssh-client \
  cpio kmod mtools xz-utils
sudo usermod -aG libvirt,kvm "$USER"
```

Log out and back in after changing group membership. A readable `/dev/kvm`
provides hardware acceleration. Without it, the runner selects QEMU software
emulation, which is substantially slower. On ARM hosts, software emulation uses
the non-Secure-Boot AAVMF firmware and a Cortex-A72 CPU model for compatibility
with nested virtualized environments such as Parallels.

The repository's required Go version must also be available on `PATH`. The
module version is defined in [../go.mod](../go.mod).

Verify access before running the test:

```sh
virsh --connect qemu:///system list
virsh --connect qemu:///system net-info default
```

The default libvirt network must exist. The runner starts it when it exists but
is inactive.

## Run the systemd lane

From the repository root:

```sh
./integration_tests/systemd/run-libvirt.sh
```

The default guest architecture follows the host: AMD64 on x86-64 and ARM64 on
AArch64. The runner downloads the matching Ubuntu 24.04 cloud image, verifies it
against the release checksum, caches it under `/var/tmp`, builds matching static
Go binaries, and creates a disposable guest.

No application port is exposed to the host. HTTP assertions execute inside the
guest against the test application's loopback listener.

## Run the Rocky Linux lane

The Rocky Linux lane targets the official Rocky Linux 9.8 GenericCloud Base
image and keeps SELinux in Enforcing mode:

```sh
./integration_tests/rocky/run-libvirt.sh
```

The default guest architecture follows the host: x86-64 on x86-64 and ARM64 on
AArch64. The image is verified against Rocky's per-image published SHA-256 and
used through a disposable qcow2 overlay. The guest test does not install
packages or weaken SELinux policy.

In addition to the shared systemd lifecycle, the lane verifies:

- SELinux is enabled and enforcing before and after reboot;
- installing the temporary `/tmp/test-app` without `--ignore-warnings` prints
  the SELinux warning, refuses the noninteractive install, and creates no unit
  or metadata;
- the root-owned `/opt/daemon-itest/test-app` installs without bypassing
  warnings and runs successfully;
- application, executable, unit, and metadata labels match policy and are not
  `unlabeled_t`;
- the live application process has a valid SELinux execution context; and
- kernel and bounded audit-log checks contain no service-specific AVC denial.

Rocky-specific settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `ROCKY_RELEASE` | `9.8` | Rocky Linux release embedded in the image filename |
| `ROCKY_BUILD` | `20260525.0` | Version-pinned GenericCloud build |
| `ROCKY_BASE_IMAGE` | Cached official image | Existing Rocky qcow2 image |
| `ROCKY_BASE_IMAGE_SHA256` | Published checksum | Optional pinned image checksum |
| `ROCKY_IMAGE_URL` | Official versioned image | Download source |
| `ROCKY_IMAGE_CHECKSUM_URL` | Image URL plus `.CHECKSUM` | Published checksum source |
| `VM_OS_VARIANT` | `rocky9` | libosinfo identifier |

The shared `VM_ARCH`, `VM_MEMORY_MIB`, `VM_VCPUS`, `VM_DISK_GIB`,
`VM_BOOT_TIMEOUT`, `VM_VIRT_TYPE`, `VM_NETWORK`, `LIBVIRT_URI`,
`ARM_UEFI_CODE`, `ARM_UEFI_VARS`, `INTEGRATION_CACHE_DIR`,
`INTEGRATION_ARTIFACT_DIR`, and `KEEP_VM` settings also apply. The default boot
timeout is 900 seconds because SELinux initialization and reboot are slower
under QEMU TCG.

## Run the Raspberry Pi OS lane

The Raspberry Pi OS lane defaults to both supported Pi userspace architectures:

```sh
./integration_tests/raspios/run-qemu.sh
```

It downloads the official Raspberry Pi OS Lite ARMHF and ARM64 images, verifies
each image against Raspberry Pi's published SHA-256, and builds `GOARM=6` and
ARM64 binaries respectively. For each image, it provisions a disposable raw
copy, boots it, runs the full systemd application lifecycle, reboots it, and
then tests configured-failure recovery, direct `SIGKILL` recovery, forced-stop
cleanup, metadata cleanup, and removal.

QEMU's Raspberry Pi machine models do not provide sufficiently complete and
stable networking/reset behavior for this automated suite. The runner therefore
uses an architecture-matched Debian Trixie kernel on QEMU `virt` hardware while
retaining the official Raspberry Pi OS root filesystem, systemd services,
libraries, configuration, cloud-init, and mounted `/boot/firmware` partition.
The Debian kernel, initrd, and matching module packages are also checksum
verified. Pi-specific peripherals and firmware behavior still require physical
Raspberry Pi hardware.

Run one architecture when needed:

```sh
RASPIOS_VARIANTS=armhf ./integration_tests/raspios/run-qemu.sh
RASPIOS_VARIANTS=arm64 ./integration_tests/raspios/run-qemu.sh
```

Raspberry Pi OS-specific settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `RASPIOS_VARIANTS` | `armhf,arm64` | Comma-separated architectures to test |
| `RASPIOS_RELEASE` | `2026-06-18` | Raspberry Pi OS image filename date |
| `RASPIOS_IMAGE_RELEASE` | `2026-06-19` | Raspberry Pi download-directory date |
| `RASPIOS_ARMHF_IMAGE` | Cached official image | Existing compressed ARMHF image |
| `RASPIOS_ARMHF_IMAGE_URL` | Official Raspberry Pi URL | ARMHF download source |
| `RASPIOS_ARMHF_IMAGE_SHA256` | Published checksum | Optional pinned ARMHF checksum |
| `RASPIOS_ARM64_IMAGE` | Cached official image | Existing compressed ARM64 image |
| `RASPIOS_ARM64_IMAGE_URL` | Official Raspberry Pi URL | ARM64 download source |
| `RASPIOS_ARM64_IMAGE_SHA256` | Published checksum | Optional pinned ARM64 checksum |
| `DEBIAN_RELEASE` | `trixie` | Generic boot-kernel and module release |
| `RASPIOS_SSH_PORT` | `55222` | First host SSH forwarding port |
| `QEMU_ACCEL` | `tcg` | QEMU accelerator |

The shared `VM_MEMORY_MIB`, `VM_VCPUS`, `VM_BOOT_TIMEOUT`, `TEST_APP_PORT`,
`INTEGRATION_CACHE_DIR`, `INTEGRATION_ARTIFACT_DIR`, `VM_WORK_DIR`, and
`KEEP_VM` settings also apply. The variants run sequentially and need about
8 GiB of free disk for downloads, one expanded image, and working files.

## Run the Yocto/Poky lane

The Yocto lane targets the official Poky 5.0.19 Scarthgap LTS ARM64 minimal
image:

```sh
./integration_tests/yocto/run-qemu.sh
```

The runner verifies the official kernel and root filesystem against their
published SHA-256 sidecars, copies and enlarges the 22 MiB ext4 image, and
injects static ARM64 test binaries without mounting the filesystem. The stock
minimal image has no SSH server, so a guest-side runlevel script performs the
two-boot lifecycle, records a durable PASS or FAIL result, and powers off. The
host extracts the result, serial log, and artifact archive directly from the
stopped ext4 image.

This lane uses Poky's native SysVinit environment without installing packages
or adding compatibility wrappers. It validates the presence and behavior of
the image's real `service`, `setsid`, BusyBox `wget`, runlevel scripts, and
watchdog process. The test also uncovered and now covers portable SysV
process-group signaling and graceful-exit races on BusyBox.

Yocto-specific settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `YOCTO_RELEASE` | `5.0.19` | Scarthgap LTS release |
| `YOCTO_IMAGE_BASE_URL` | Official qemuarm64 directory | Artifact source |
| `YOCTO_KERNEL` | Cached official kernel | Existing kernel image |
| `YOCTO_KERNEL_URL` | Official `Image-qemuarm64.bin` | Kernel source |
| `YOCTO_KERNEL_SHA256` | Published checksum | Optional pinned kernel checksum |
| `YOCTO_ROOTFS` | Cached official ext4 | Existing minimal rootfs |
| `YOCTO_ROOTFS_URL` | Official minimal ext4 | Rootfs source |
| `YOCTO_ROOTFS_SHA256` | Published checksum | Optional pinned rootfs checksum |
| `YOCTO_ROOTFS_SIZE_MIB` | `256` | Expanded disposable ext4 size |
| `QEMU_ACCEL` | Automatic | `kvm` on compatible ARM64 hosts, otherwise `tcg` |

The shared `VM_MEMORY_MIB`, `VM_VCPUS`, `VM_BOOT_TIMEOUT`, `TEST_APP_PORT`,
`INTEGRATION_CACHE_DIR`, `INTEGRATION_ARTIFACT_DIR`, `VM_WORK_DIR`, and
`KEEP_VM` settings also apply. No libvirt network, cloud-init, SSH key, or UEFI
firmware is required.

## Run the OpenRC lane

The OpenRC lane currently targets the official Alpine ARM64 UEFI cloud image:

```sh
./integration_tests/openrc/run-libvirt.sh
```

The runner downloads Alpine 3.24.1, verifies the published SHA-512 checksum,
installs Bash through cloud-init for the guest test driver, connects through the
default `alpine` account and its passwordless `doas` policy, and confirms OpenRC
is the active backend before changing service state. Metadata is delivered with
NoCloud-Net over the libvirt bridge because Alpine cloud-init images do not
guarantee that the packages needed to mount a CIDATA ISO are present. The runner
also enables Alpine's PAM-backed SSH server because non-PAM SSH rejects key
authentication for the locked default cloud account.

OpenRC-specific image settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `ALPINE_VERSION` | `3.24.1` | Alpine cloud-image release |
| `ALPINE_BRANCH` | Version major/minor | Alpine repository branch |
| `ALPINE_BASE_IMAGE` | Cached official image | Existing Alpine qcow2 image |
| `ALPINE_BASE_IMAGE_URL` | Official generic image | Download source |
| `ALPINE_BASE_IMAGE_SHA512` | Published checksum | Optional pinned image checksum |
| `VM_METADATA_HOST` | `192.168.122.1` | Host address on the libvirt bridge |

The shared `VM_MEMORY_MIB`, `VM_VCPUS`, `VM_DISK_GIB`, `VM_BOOT_TIMEOUT`,
`VM_VIRT_TYPE`, `VM_NETWORK`, `LIBVIRT_URI`, `ARM_UEFI_CODE`, `ARM_UEFI_VARS`,
`INTEGRATION_CACHE_DIR`, `INTEGRATION_ARTIFACT_DIR`, and `KEEP_VM` settings also
apply. The OpenRC defaults are 1 GiB memory and a 4 GiB overlay.

## Run the Upstart lane

The Upstart lane targets the official Ubuntu 14.04.5 LTS ARM64 UEFI cloud image:

```sh
./integration_tests/upstart/run-libvirt.sh
```

Because modern AArch64 firmware may not boot this historical image reliably,
the runner defaults to direct boot with the image's Ubuntu 4.4 kernel and
initrd. It verifies the official image SHA-256 before creating a disposable
overlay. Ubuntu 14.04 is end-of-life; the lane does not install guest packages
or depend on archived package repositories.

Upstart-specific settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `UPSTART_BOOT_MODE` | `direct` | Use `direct` kernel boot or optional `uefi` boot |
| `UPSTART_BASE_IMAGE` | Cached official Trusty image | Existing qcow2 image |
| `UPSTART_BASE_IMAGE_SHA256` | Published checksum | Optional pinned image checksum |
| `UPSTART_IMAGE_URL` | Official Trusty release URL | Image download source |
| `UPSTART_KERNEL_VERSION` | `4.4.0-148-generic` | Kernel and initrd version extracted for direct boot |
| `UPSTART_KERNEL_IMAGE` | Cached extracted kernel | Existing direct-boot kernel |
| `UPSTART_INITRD_IMAGE` | Cached extracted initrd | Existing direct-boot initrd |

The shared VM, cache, artifact, and `KEEP_VM` settings listed above also apply.

## Run the Windows Server lane

The Windows lane targets the official Windows Server 2019 Evaluation ISO and
runs without requiring a prebuilt Windows image:

```sh
./integration_tests/windows/run-qemu.sh
```

The first run downloads the 4.9 GiB Microsoft ISO, records its SHA-256, and
performs one unattended Server Core installation. Later runs use a disposable
qcow2 overlay backed by the cached clean base disk. The test communicates with
the guest through WinRM and forwards the application's HTTP endpoint to the
host for external assertions.

Windows Server 2019 is x86-64-only. On an ARM64 host the runner extracts Ubuntu's
`qemu-system-x86` and WinRM client packages into `/var/tmp` without sudo, then
uses QEMU TCG cross-architecture emulation. Initial installation can take
several hours; at least 12 GiB of free disk space is required. The evaluation
image remains subject to Microsoft's licensing terms.

Windows-specific settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `WINDOWS_ISO` | Cached official Server 2019 ISO | Existing installation ISO |
| `WINDOWS_ISO_URL` | Official Microsoft evaluation URL | ISO download source |
| `WINDOWS_ISO_SHA256` | Pinned official-image checksum | Override checksum for a supplied ISO |
| `WINDOWS_BASE_IMAGE` | Cached Server Core qcow2 | Existing prepared base image |
| `WINDOWS_ADMIN_USER` | `Administrator` | Disposable guest administrator |
| `WINDOWS_ADMIN_PASSWORD` | `DaemonTest!2026` | Disposable guest password |
| `WINDOWS_WINRM_PORT` | `55985` | Host port forwarded to guest WinRM |
| `WINDOWS_APP_HOST_PORT` | `58080` | Host port forwarded to the test application |
| `WINDOWS_PAYLOAD_PORT` | `58081` | Temporary host payload server port |
| `WINDOWS_VNC_DISPLAY` | `7` | Local-only VNC display used for diagnostics |
| `VM_INSTALL_TIMEOUT` | `14400` | First installation timeout in seconds |

The shared cache, artifact, memory, CPU, disk, boot-timeout, and `KEEP_VM`
settings also apply. The defaults are 2.5 GiB memory, two vCPUs, and a sparse
40 GiB virtual disk.

## Run the FreeBSD lane

The FreeBSD lane uses the official ARM64 BASIC-CLOUDINIT UFS image:

```sh
./integration_tests/freebsd/run-libvirt.sh
```

The default is FreeBSD 14.4-RELEASE. The compressed image is verified against
the release SHA-256 manifest before it is decompressed and cached. No packages
are installed in the guest; the test driver uses FreeBSD base-system tools. The
image's automatic first-boot base and package updates are disabled through
nuageinit's early config-2 file provisioning, before networking and the update
services. The same provisioning configures temporary key-only root SSH for the
disposable guest.

FreeBSD-specific image settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `FREEBSD_RELEASE` | `14.4-RELEASE` | FreeBSD VM-image release |
| `FREEBSD_BASE_IMAGE` | Cached decompressed image | Existing FreeBSD qcow2 image |
| `FREEBSD_BASE_IMAGE_SHA256` | None | Optional checksum for a supplied base image |
| `FREEBSD_COMPRESSED_IMAGE` | Cached official archive | Existing compressed image |
| `FREEBSD_IMAGE_URL` | Official release URL | Compressed-image download source |
| `FREEBSD_IMAGE_SHA256` | Release manifest value | Optional pinned archive checksum |

The shared VM and artifact settings listed above also apply. The FreeBSD
defaults are 2 GiB memory and an 8 GiB overlay.

## Run the OpenWrt lane

The OpenWrt lane uses the official ARM64 ext4 combined EFI image:

```sh
./integration_tests/openwrt/run-libvirt.sh
```

The default is OpenWrt 25.12.5. The runner verifies the published SHA-256,
copies the raw image for each run, and injects an ephemeral Dropbear key and a
first-boot UCI script with unprivileged ext4 tools. The UCI script changes the
LAN interface to DHCP and disables its DHCP server so the guest can safely join
the existing libvirt network.

OpenWrt-specific image settings are:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `OPENWRT_VERSION` | `25.12.5` | OpenWrt image release |
| `OPENWRT_BASE_IMAGE` | Cached decompressed image | Existing raw OpenWrt image |
| `OPENWRT_BASE_IMAGE_SHA256` | None | Optional checksum for a supplied base image |
| `OPENWRT_COMPRESSED_IMAGE` | Cached official archive | Existing compressed image |
| `OPENWRT_IMAGE_URL` | Official release URL | Compressed-image download source |
| `OPENWRT_IMAGE_SHA256` | Published checksum | Optional pinned archive checksum |

The shared VM and artifact settings also apply. The OpenWrt default is 512 MiB
of memory. OpenWrt mounts `/var` as volatile storage, so informational `APP` and
`ARGS` list metadata is expected to disappear after reboot; the persistent
procd service definition remains authoritative.

### Configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `VM_ARCH` | Host architecture | `amd64` or `arm64` guest |
| `VM_MEMORY_MIB` | `2048` | Guest memory |
| `VM_VCPUS` | `2` | Guest virtual CPUs |
| `VM_DISK_GIB` | `12` | Overlay disk capacity |
| `VM_BOOT_TIMEOUT` | `600` | Boot, SSH, and reboot timeout in seconds |
| `VM_VIRT_TYPE` | Automatic | Force `kvm` or `qemu` |
| `VM_NETWORK` | `default` | Existing libvirt NAT network |
| `LIBVIRT_URI` | `qemu:///system` | Libvirt connection URI |
| `UBUNTU_RELEASE` | `24.04` | Ubuntu cloud-image release |
| `BASE_IMAGE` | Cached release image | Existing qcow2 cloud image |
| `BASE_IMAGE_URL` | Ubuntu release URL | Download source |
| `BASE_IMAGE_SHA256` | Published checksum | Optional pinned image checksum |
| `ARM_UEFI_CODE` | AAVMF non-Secure-Boot code | ARM firmware code image |
| `ARM_UEFI_VARS` | AAVMF variables template | ARM firmware variables image |
| `INTEGRATION_CACHE_DIR` | `/var/tmp/daemon-util-integration-cache-<uid>` | Base-image cache |
| `INTEGRATION_ARTIFACT_DIR` | `integration_tests/artifacts` | Diagnostic output |
| `KEEP_VM` | `0` | Keep the domain and temporary disks when set to `1` |

For reproducible CI, provide a controlled `BASE_IMAGE` and pin its
`BASE_IMAGE_SHA256`.

Examples:

```sh
VM_ARCH=arm64 ./integration_tests/systemd/run-libvirt.sh

BASE_IMAGE=/srv/vm-images/ubuntu-systemd.qcow2 \
BASE_IMAGE_SHA256='<pinned-sha256>' \
./integration_tests/systemd/run-libvirt.sh

KEEP_VM=1 ./integration_tests/systemd/run-libvirt.sh
```

## Failure artifacts

Each run creates a timestamped artifact directory containing the libvirt domain
XML and available guest diagnostics. Guest diagnostics include:

- generated systemd unit, OpenRC service script, FreeBSD rc.d script, or
  OpenWrt procd script;
- SELinux enforcement state, file/process contexts, warning output, and scoped
  audit/kernel AVC diagnostics for Rocky Linux;
- native service-manager status and registration output;
- journal entries where available;
- process list;
- HTTP response snapshots; and
- JSON Lines lifecycle records from the test application.

The Yocto lane additionally retains the full serial log, durable guest result,
guest test log, and an artifact archive extracted offline from the ext4 image.

The VM is removed even when a test fails unless `KEEP_VM=1` is set.

During software-emulated boots, the runner prints DHCP, SSH, and reboot progress
every 15 seconds. These waits are expected to take longer than they do with KVM.

## Build multiple Buildroot variants

To simulate different real-world Buildroot configurations, build a profile
matrix from source:

```sh
./integration_tests/buildroot/build-matrix.sh
```

Default profiles are `baseline,debug,release` using
`qemu_aarch64_virt_defconfig` plus profile fragments in
`integration_tests/buildroot/fragments`.

Useful overrides:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `BUILDROOT_REF` | `2026.02.3` | Buildroot git tag/branch to clone |
| `BUILDROOT_DIR` | `.cache/buildroot-<ref>` | Existing Buildroot source tree |
| `BUILDROOT_OUTPUT_ROOT` | `/var/tmp/daemon-buildroot-matrix` | Per-profile output root |
| `BUILDROOT_PROFILES` | `baseline,debug,release` | Comma-separated profile list |
| `JOBS` | Host CPU count | Parallel build jobs |

Example building two profiles only:

```sh
BUILDROOT_PROFILES=baseline,release JOBS=8 ./integration_tests/buildroot/build-matrix.sh
```

The builder writes a manifest at:

- `BUILDROOT_OUTPUT_ROOT/manifest.tsv`

Each row contains the generated kernel and rootfs image paths for one profile.

## Safety

The guest test installs a root service and deliberately triggers forced process
termination. Run it only in a disposable VM. The script refuses to execute the
guest phase unless the expected service manager is active and the caller is
root.
