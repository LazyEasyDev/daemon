#!/usr/bin/env bash

set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)

cache_dir=${INTEGRATION_CACHE_DIR:-/var/tmp/daemon-util-integration-cache-$(id -u)}
tool_root=${WINDOWS_TOOL_ROOT:-/var/tmp/daemon-windows-qemu-root}
tool_packages=${WINDOWS_TOOL_PACKAGES:-/var/tmp/daemon-windows-qemu-packages}
artifact_root=${INTEGRATION_ARTIFACT_DIR:-$repo_dir/integration_tests/artifacts}
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$
artifact_dir="$artifact_root/windows-$run_id"
work_dir=${VM_WORK_DIR:-/var/tmp/daemon-windows-itest-$run_id}

iso_filename=${WINDOWS_ISO_FILENAME:-17763.737.190906-2324.rs5_release_svc_refresh_SERVER_EVAL_x64FRE_en-us_1.iso}
iso_url=${WINDOWS_ISO_URL:-https://software-download.microsoft.com/download/sg/$iso_filename}
iso_path=${WINDOWS_ISO:-$cache_dir/$iso_filename}
default_iso_sha256=549bca46c055157291be6c22a3aaaed8330e78ef4382c99ee82c896426a1cee1
if [[ -n "${WINDOWS_ISO:-}" ]]; then
	iso_sha256=${WINDOWS_ISO_SHA256:-}
else
	iso_sha256=${WINDOWS_ISO_SHA256:-$default_iso_sha256}
fi
base_disk=${WINDOWS_BASE_IMAGE:-$cache_dir/windows-server-2019-eval-core.qcow2}
base_marker="$base_disk.ready"
admin_user=${WINDOWS_ADMIN_USER:-Administrator}
admin_password=${WINDOWS_ADMIN_PASSWORD:-DaemonTest!2026}
vm_memory_mib=${VM_MEMORY_MIB:-2560}
vm_vcpus=${VM_VCPUS:-2}
vm_disk_gib=${VM_DISK_GIB:-40}
vm_install_timeout=${VM_INSTALL_TIMEOUT:-14400}
vm_boot_timeout=${VM_BOOT_TIMEOUT:-1800}
winrm_port=${WINDOWS_WINRM_PORT:-55985}
app_host_port=${WINDOWS_APP_HOST_PORT:-58080}
payload_port=${WINDOWS_PAYLOAD_PORT:-58081}
vnc_display=${WINDOWS_VNC_DISPLAY:-7}
keep_vm=${KEEP_VM:-0}
service_name=${SERVICE_NAME:-itest$(date +%s)$$}
app_port=${TEST_APP_PORT:-18080}

qemu_pid=
payload_pid=
remote_ready=0
installation_active=0

log() { printf '[windows-vm] %s\n' "$*"; }
fail() { printf '[windows-vm] ERROR: %s\n' "$*" >&2; return 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "required command '$1' is not installed"; }

for command in apt-get base64 dd dpkg-deb genisoimage go python3 qemu-img sha256sum ss wget; do
	require_command "$command"
done

host_arch=$(uname -m)
if [[ "$host_arch" != x86_64 && "$host_arch" != amd64 ]]; then
	log "host is $host_arch; Windows Server 2019 will use slow x86-64 TCG emulation"
fi

mkdir -p "$cache_dir" "$artifact_dir" "$work_dir" "$tool_root" "$tool_packages"
chmod 0755 "$cache_dir" "$work_dir"

install_deb_into_tool_root() {
	local package=$1 pattern=$2 deb
	if ! compgen -G "$tool_packages/$pattern" >/dev/null; then
		(cd "$tool_packages" && apt-get download "$package")
	fi
	for deb in "$tool_packages"/$pattern; do
		dpkg-deb -x "$deb" "$tool_root"
	done
}

prepare_rootless_tools() {
	if [[ ! -x "$tool_root/usr/bin/qemu-system-x86_64" ]]; then
		log 'extracting rootless qemu-system-x86_64 package'
		install_deb_into_tool_root qemu-system-x86 'qemu-system-x86_*.deb'
	fi
	if ! PYTHONPATH="$tool_root/usr/lib/python3/dist-packages" python3 -c 'import winrm, requests_ntlm, spnego, xmltodict' >/dev/null 2>&1; then
		log 'extracting rootless Python WinRM packages'
		install_deb_into_tool_root python3-winrm 'python3-winrm_*.deb'
		install_deb_into_tool_root python3-requests-ntlm 'python3-requests-ntlm_*.deb'
		install_deb_into_tool_root python3-ntlm-auth 'python3-ntlm-auth_*.deb'
		install_deb_into_tool_root python3-pyspnego 'python3-pyspnego_*.deb'
		install_deb_into_tool_root python3-xmltodict 'python3-xmltodict_*.deb'
	fi
	PYTHONPATH="$tool_root/usr/lib/python3/dist-packages" python3 -c 'import winrm, requests_ntlm, spnego, xmltodict' >/dev/null
}

prepare_rootless_tools
qemu="$tool_root/usr/bin/qemu-system-x86_64"
winrm_client="$script_dir/winrm-client.py"
[[ -x "$qemu" ]] || fail "rootless x86 QEMU is missing: $qemu"
[[ -f "$winrm_client" ]] || fail "WinRM helper is missing: $winrm_client"

export PYTHONPATH="$tool_root/usr/lib/python3/dist-packages"
export WINDOWS_WINRM_ENDPOINT="http://127.0.0.1:$winrm_port/wsman"
export WINDOWS_ADMIN_USER="$admin_user"
export WINDOWS_ADMIN_PASSWORD="$admin_password"

winrm() {
	python3 "$winrm_client" "$@"
}

winrm_ps() {
	winrm run "$1"
}

port_is_free() {
	! ss -ltnH "sport = :$1" | grep -q .
}

for port in "$winrm_port" "$app_host_port" "$payload_port"; do
	port_is_free "$port" || fail "host TCP port $port is already in use"
done

available_bytes=$(df --output=avail -B1 "$cache_dir" | tail -n 1 | tr -d ' ')
if [[ ! -f "$base_marker" ]]; then
	if [[ ! -f "$iso_path" && "$available_bytes" -lt 12000000000 ]]; then
		fail 'at least 12 GB free space is required to download and install Windows Server 2019'
	elif [[ -f "$iso_path" && "$available_bytes" -lt 7000000000 ]]; then
		fail 'at least 7 GB free space is required to install Windows Server 2019 from the cached ISO'
	fi
fi

verify_iso() {
	local expected=$iso_sha256
	if [[ -z "$expected" && -f "$iso_path.sha256" ]]; then
		expected=$(awk 'NR == 1 {print $1}' "$iso_path.sha256")
	fi
	if [[ -n "$expected" ]]; then
		printf '%s  %s\n' "$expected" "$iso_path" | sha256sum --check --status - || fail 'Windows Server ISO checksum verification failed'
	else
		log 'computing and caching SHA-256 for the Microsoft HTTPS download'
		sha256sum "$iso_path" >"$iso_path.sha256"
	fi
}

if [[ ! -f "$iso_path" ]]; then
	[[ -z "${WINDOWS_ISO:-}" ]] || fail "WINDOWS_ISO does not exist: $iso_path"
	log "downloading official Windows Server 2019 evaluation ISO (4.9 GiB)"
	wget --continue --progress=dot:giga -O "$iso_path.partial" "$iso_url"
	mv "$iso_path.partial" "$iso_path"
fi
verify_iso
chmod 0644 "$iso_path"
iso_path=$(readlink -f "$iso_path")

qemu_is_running() {
	[[ -n "$qemu_pid" ]] && kill -0 "$qemu_pid" >/dev/null 2>&1
}

wait_qemu_exit() {
	local timeout_seconds=$1
	local deadline=$((SECONDS + timeout_seconds))
	while qemu_is_running && (( SECONDS < deadline )); do sleep 3; done
	! qemu_is_running
}

stop_qemu() {
	if qemu_is_running; then
		kill "$qemu_pid" >/dev/null 2>&1 || true
		wait_qemu_exit 30 || kill -KILL "$qemu_pid" >/dev/null 2>&1 || true
	fi
	qemu_pid=
}

send_monitor_key() {
	local socket_path=$1 key=$2
	python3 - "$socket_path" "$key" <<'PY' >/dev/null 2>&1 || true
import socket
import sys
import time
path, key = sys.argv[1:]
for _ in range(20):
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
			client.settimeout(2)
            client.connect(path)
			try:
				client.recv(4096)
			except TimeoutError:
				pass
            client.sendall((f"sendkey {key}\n").encode())
        break
    except OSError:
        time.sleep(0.5)
PY
}

capture_qemu_screen() {
	local monitor="$work_dir/qemu-monitor.sock" output="$artifact_dir/screen-failure.png"
	[[ -S "$monitor" ]] || return
	python3 - "$monitor" "$output" <<'PY' >/dev/null 2>&1 || true
import socket
import sys
import time
with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
	client.settimeout(2)
	client.connect(sys.argv[1])
	try:
		client.recv(4096)
	except TimeoutError:
		pass
	client.sendall((f"screendump {sys.argv[2]} -f png\n").encode())
	time.sleep(1)
PY
}

launch_qemu() {
	local disk=$1 mode=$2
	local pid_file="$work_dir/qemu.pid" monitor="$work_dir/qemu-monitor.sock" qemu_log="$artifact_dir/qemu-$mode.log"
	local -a media_args boot_args
	rm -f "$pid_file" "$monitor"
	if [[ "$mode" == install ]]; then
		media_args=(
			-drive "file=$iso_path,if=ide,index=2,media=cdrom,readonly=on"
			-drive "file=$work_dir/config.iso,if=ide,index=3,media=cdrom,readonly=on"
		)
		boot_args=(-boot once=d,menu=off)
	else
		media_args=()
		boot_args=(-boot order=c,menu=off)
	fi
	"$qemu" \
		-name "daemon-windows-$mode" -machine pc,accel=tcg,usb=off -cpu max \
		-smp "$vm_vcpus" -m "$vm_memory_mib" -rtc base=localtime \
		-drive "file=$disk,if=ide,index=0,media=disk,format=qcow2,cache=writeback" \
		"${media_args[@]}" \
		-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$winrm_port-:5985,hostfwd=tcp:127.0.0.1:$app_host_port-:$app_port" \
		-device e1000,netdev=net0 \
		"${boot_args[@]}" -display none -vnc "127.0.0.1:$vnc_display" \
		-monitor "unix:$monitor,server=on,wait=off" -serial file:"$artifact_dir/serial-$mode.log" \
		-D "$qemu_log" -daemonize -pidfile "$pid_file"
	qemu_pid=$(cat "$pid_file")
	if [[ "$mode" == install ]]; then
		sleep 4
		send_monitor_key "$monitor" ret
		sleep 5
		send_monitor_key "$monitor" ret
	fi
}

xml_escape() {
	python3 -c 'import html,sys; print(html.escape(sys.stdin.read().rstrip("\n"), quote=True))'
}

prepare_unattended_media() {
	local answer_dir="$work_dir/answer" password_xml
	mkdir -p "$answer_dir"
	password_xml=$(printf '%s' "$admin_password" | xml_escape)
	cat >"$answer_dir/Autounattend.xml" <<EOF
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage><UILanguage>en-US</UILanguage></SetupUILanguage>
      <InputLocale>en-US</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID><WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add"><Order>1</Order><Type>Primary</Type><Size>500</Size></CreatePartition>
            <CreatePartition wcm:action="add"><Order>2</Order><Type>Primary</Type><Extend>true</Extend></CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add"><Order>1</Order><PartitionID>1</PartitionID><Active>true</Active><Format>NTFS</Format><Label>System</Label></ModifyPartition>
            <ModifyPartition wcm:action="add"><Order>2</Order><PartitionID>2</PartitionID><Format>NTFS</Format><Label>Windows</Label><Letter>C</Letter></ModifyPartition>
          </ModifyPartitions>
        </Disk>
        <WillShowUI>OnError</WillShowUI>
      </DiskConfiguration>
      <ImageInstall><OSImage><InstallFrom><MetaData wcm:action="add"><Key>/IMAGE/INDEX</Key><Value>1</Value></MetaData></InstallFrom><InstallTo><DiskID>0</DiskID><PartitionID>2</PartitionID></InstallTo><WillShowUI>OnError</WillShowUI></OSImage></ImageInstall>
      <UserData><AcceptEula>true</AcceptEula><FullName>daemon-itest</FullName><Organization>daemon-itest</Organization></UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>DAEMON-WIN2019</ComputerName><TimeZone>UTC</TimeZone><RegisteredOwner>daemon-itest</RegisteredOwner><RegisteredOrganization>daemon-itest</RegisteredOrganization>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>en-US</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE><HideEULAPage>true</HideEULAPage><HideLocalAccountScreen>true</HideLocalAccountScreen><HideOEMRegistrationScreen>true</HideOEMRegistrationScreen><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><NetworkLocation>Work</NetworkLocation><ProtectYourPC>3</ProtectYourPC><SkipMachineOOBE>true</SkipMachineOOBE><SkipUserOOBE>true</SkipUserOOBE></OOBE>
      <UserAccounts><AdministratorPassword><Value>$password_xml</Value><PlainText>true</PlainText></AdministratorPassword></UserAccounts>
      <AutoLogon><Password><Value>$password_xml</Value><PlainText>true</PlainText></Password><Enabled>true</Enabled><LogonCount>1</LogonCount><Username>$admin_user</Username></AutoLogon>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add"><Order>1</Order><Description>Configure WinRM</Description><RequiresUserInput>false</RequiresUserInput><CommandLine>powershell.exe -NoProfile -ExecutionPolicy Bypass -Command &quot;\$s=(Get-PSDrive -PSProvider FileSystem | Where-Object { Test-Path (\$_.Root + 'bootstrap.ps1') } | Select-Object -First 1).Root + 'bootstrap.ps1'; &amp; \$s&quot;</CommandLine></SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>
EOF
	cat >"$answer_dir/bootstrap.ps1" <<'EOF'
$ErrorActionPreference = "Stop"
try {
    Set-ExecutionPolicy -ExecutionPolicy Bypass -Scope LocalMachine -Force
    New-ItemProperty -Path "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System" -Name LocalAccountTokenFilterPolicy -PropertyType DWord -Value 1 -Force | Out-Null
    Set-Service -Name WinRM -StartupType Automatic
    & winrm.cmd quickconfig -quiet
    & winrm.cmd set winrm/config/service '@{AllowUnencrypted="true"}'
    & winrm.cmd set winrm/config/service/auth '@{Basic="true"}'
    & netsh.exe advfirewall firewall add rule name="daemon-itest WinRM" dir=in action=allow protocol=TCP localport=5985
    & netsh.exe advfirewall firewall add rule name="daemon-itest app" dir=in action=allow protocol=TCP localport=18080
	& netsh.exe interface portproxy delete v4tov4 listenaddress=10.0.2.15 listenport=18080 2>$null
	& netsh.exe interface portproxy add v4tov4 listenaddress=10.0.2.15 listenport=18080 connectaddress=127.0.0.1 connectport=18080
    Set-Service -Name iphlpsvc -StartupType Automatic
    Start-Service -Name iphlpsvc
    New-Item -ItemType File -Path C:\daemon-winrm-ready.txt -Force | Out-Null
} catch {
    $_ | Out-String | Out-File C:\daemon-bootstrap-error.txt
    throw
}
EOF
	genisoimage -quiet -J -r -V DAEMONCFG -o "$work_dir/config.iso" "$answer_dir"
	chmod 0644 "$work_dir/config.iso"
}

install_base_image() {
	[[ -z "${WINDOWS_BASE_IMAGE:-}" ]] || fail "WINDOWS_BASE_IMAGE is not prepared (missing $base_marker)"
	log 'creating Windows Server 2019 base disk'
	qemu-img create -q -f qcow2 "$base_disk" "${vm_disk_gib}G"
	chmod 0644 "$base_disk"
	prepare_unattended_media
	installation_active=1
	log "starting unattended Server Core installation; VNC is localhost:$((5900 + vnc_display))"
	launch_qemu "$base_disk" install
	if ! winrm wait --timeout "$vm_install_timeout"; then
		fail "Windows installation did not reach WinRM; inspect $artifact_dir/qemu-install.log or VNC localhost:$((5900 + vnc_display))"
	fi
	remote_ready=1
	winrm_ps "if (-not (Test-Path C:\\daemon-winrm-ready.txt)) { throw 'bootstrap marker is missing' }; (Get-CimInstance Win32_OperatingSystem).Caption"
	log 'Windows base installation completed; shutting down cleanly'
	winrm_ps 'Stop-Computer -Force' >/dev/null 2>&1 || true
	wait_qemu_exit 600 || fail 'Windows base VM did not power off'
	qemu_pid=
	installation_active=0
	touch "$base_marker"
	rm -rf "$work_dir/answer" "$work_dir/config.iso"
}

collect_artifacts() {
	[[ "$remote_ready" == 1 ]] || return
	local archive_b64="$artifact_dir/guest-artifacts.b64"
	winrm_ps "if (Test-Path C:\\daemon-itest\\artifacts) { Compress-Archive -Path C:\\daemon-itest\\artifacts\\* -DestinationPath C:\\daemon-itest\\artifacts.zip -Force; [Convert]::ToBase64String([IO.File]::ReadAllBytes('C:\\daemon-itest\\artifacts.zip')) }" >"$archive_b64" 2>/dev/null || true
	if [[ -s "$archive_b64" ]]; then
		tr -d '\r\n' <"$archive_b64" | base64 -d >"$artifact_dir/guest-artifacts.zip" 2>/dev/null || true
	fi
	rm -f "$archive_b64"
	winrm_ps 'Get-CimInstance Win32_OperatingSystem | Format-List *; Get-Service lz_lz_* -ErrorAction SilentlyContinue | Format-Table -AutoSize' >"$artifact_dir/guest-summary.txt" 2>&1 || true
}

cleanup() {
	local status_code=$?
	trap - EXIT
	if [[ -n "$payload_pid" ]]; then kill "$payload_pid" >/dev/null 2>&1 || true; fi
	if (( status_code != 0 )); then
		capture_qemu_screen
		if [[ "$installation_active" == 0 ]]; then collect_artifacts; fi
	fi
	if [[ "$keep_vm" == 1 ]]; then
		log "keeping QEMU process ${qemu_pid:-none} and work directory $work_dir"
	else
		stop_qemu
		rm -rf "$work_dir"
	fi
	if (( status_code == 0 )); then log "artifacts: $artifact_dir"; else log "test failed; artifacts: $artifact_dir" >&2; fi
	exit "$status_code"
}
trap cleanup EXIT

if [[ ! -f "$base_disk" || ! -f "$base_marker" ]]; then
	rm -f "$base_disk" "$base_marker"
	install_base_image
fi

log 'creating disposable Windows test overlay'
overlay="$work_dir/windows-test.qcow2"
qemu-img create -q -f qcow2 -F qcow2 -b "$(readlink -f "$base_disk")" "$overlay" "${vm_disk_gib}G"
chmod 0644 "$overlay"
launch_qemu "$overlay" test
winrm wait --timeout "$vm_boot_timeout"
remote_ready=1

build_dir="$work_dir/payload"
mkdir -p "$build_dir"
log 'building Windows/amd64 daemon and application binaries'
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o "$build_dir/daemon.exe" .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o "$build_dir/test-app.exe" ./test_app
)
printf '%s\n' 'daemon-util relative path test passed' >"$build_dir/relative-path-test.txt"
cp "$script_dir/guest-test.ps1" "$build_dir/guest-test.ps1"

python3 -m http.server "$payload_port" --bind 127.0.0.1 --directory "$build_dir" >"$artifact_dir/payload-server.log" 2>&1 &
payload_pid=$!
sleep 1
winrm_ps "New-Item -ItemType Directory -Path C:\\daemon-itest -Force | Out-Null; \$wc=New-Object Net.WebClient; \$wc.DownloadFile('http://10.0.2.2:$payload_port/daemon.exe','C:\\daemon-itest\\daemon.exe'); \$wc.DownloadFile('http://10.0.2.2:$payload_port/test-app.exe','C:\\daemon-itest\\test-app.exe'); \$wc.DownloadFile('http://10.0.2.2:$payload_port/relative-path-test.txt','C:\\daemon-itest\\relative-path-test.txt'); \$wc.DownloadFile('http://10.0.2.2:$payload_port/guest-test.ps1','C:\\daemon-itest\\guest-test.ps1')"

log 'running pre-reboot Windows application checks'
winrm_ps "& C:\\daemon-itest\\guest-test.ps1 -Phase pre-reboot -ServiceName '$service_name' -Port $app_port"

host_response=$(wget -q -O - --timeout=10 "http://127.0.0.1:$app_host_port/")
grep -Fq 'hello windows server 2019' <<<"$host_response" || fail 'host-visible Windows app response has the wrong message'
grep -Fq 'daemon-util relative path test passed' <<<"$host_response" || fail 'host-visible Windows app response is missing fixture content'
printf '%s\n' "$host_response" >"$artifact_dir/pre-reboot-host-http.json"

old_boot=$(winrm_ps '(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()' | tr -d '\r\n')
log 'rebooting Windows guest to verify SCM automatic start'
winrm_ps 'Restart-Computer -Force' >/dev/null 2>&1 || true
new_boot=
deadline=$((SECONDS + vm_boot_timeout))
while (( SECONDS < deadline )); do
	candidate=$(winrm_ps '(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()' 2>/dev/null | tr -d '\r\n' || true)
	if [[ -n "$candidate" && "$candidate" != "$old_boot" ]]; then new_boot=$candidate; break; fi
	sleep 10
done
[[ -n "$new_boot" ]] || fail 'timed out waiting for Windows reboot'

post_reboot_response=
deadline=$((SECONDS + 180))
while (( SECONDS < deadline )); do
	post_reboot_response=$(wget -q -O - --timeout=5 "http://127.0.0.1:$app_host_port/" 2>/dev/null || true)
	[[ -n "$post_reboot_response" ]] && break
	sleep 3
done
[[ -n "$post_reboot_response" ]] || fail 'Windows app was not externally reachable after reboot'
printf '%s\n' "$post_reboot_response" >"$artifact_dir/post-reboot-host-http.json"

old_pid=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["pid"])' <<<"$post_reboot_response")
log "forcing host-observed Windows app PID $old_pid to crash"
winrm_ps "Stop-Process -Id $old_pid -Force"
new_pid=
deadline=$((SECONDS + 150))
while (( SECONDS < deadline )); do
	response=$(wget -q -O - --timeout=5 "http://127.0.0.1:$app_host_port/" 2>/dev/null || true)
	if [[ -n "$response" ]]; then
		candidate=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["pid"])' <<<"$response" 2>/dev/null || true)
		if [[ -n "$candidate" && "$candidate" != "$old_pid" ]]; then new_pid=$candidate; break; fi
	fi
	sleep 3
done
[[ -n "$new_pid" ]] || fail "SCM did not recover host-observed app PID $old_pid"
log "host-observed hard crash recovered PID $old_pid as $new_pid"

log 'running post-reboot Windows lifecycle and recovery checks'
winrm_ps "& C:\\daemon-itest\\guest-test.ps1 -Phase post-reboot -ServiceName '$service_name' -Port $app_port"
collect_artifacts
remote_ready=0
log 'Windows Server 2019 real application-level test passed'
