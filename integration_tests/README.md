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
- automatic restart after a nonzero application exit;
- graceful shutdown within the configured timeout;
- forced termination after the configured timeout;
- systemd control-group cleanup of a child process; and
- removal of the unit, enablement links, metadata, and application processes.

The OpenRC lane performs the same application-level lifecycle checks and also
verifies its generated `openrc-run` script, `supervise-daemon` configuration,
default-runlevel registration, respawn behavior, and process-group cleanup.

The tests use immutable Ubuntu and Alpine cloud images with temporary qcow2
overlays. Base images are never modified.

## Ubuntu host prerequisites

Install the packages appropriate for the host architecture:

```sh
sudo apt update
sudo apt install libvirt-daemon-system libvirt-clients virtinst \
  cloud-image-utils qemu-utils qemu-system-x86 qemu-system-arm \
  qemu-efi-aarch64 wget openssh-client
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

- generated systemd unit or OpenRC service script;
- native service-manager status and registration output;
- journal entries where available;
- process list;
- HTTP response snapshots; and
- JSON Lines lifecycle records from the test application.

The VM is removed even when a test fails unless `KEEP_VM=1` is set.

During software-emulated boots, the runner prints DHCP, SSH, and reboot progress
every 15 seconds. These waits are expected to take longer than they do with KVM.

## Safety

The guest test installs a root service and deliberately triggers forced process
termination. Run it only in a disposable VM. The script refuses to execute the
guest phase unless the expected service manager is active and the caller is
root.
