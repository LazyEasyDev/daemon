#!/bin/sh

# Shared application-level coverage for Unix service-manager guests.
# The caller must define daemon_bin, install_dir, artifact_dir, service_name,
# fail(), and optional interpreted_definition_path()/interpreted_removed().

interpreted_assert_contains() {
	interpreted_value=$1
	interpreted_expected=$2
	interpreted_description=$3
	case "$interpreted_value" in
		*"$interpreted_expected"*) ;;
		*) fail "$interpreted_description: expected '$interpreted_expected', got '$interpreted_value'" ;;
	esac
}

interpreted_resolve_path() {
	interpreted_path=$(readlink -f "$1" 2>/dev/null || true)
	if [ -z "$interpreted_path" ] && command -v realpath >/dev/null 2>&1; then
		interpreted_path=$(realpath "$1" 2>/dev/null || true)
	fi
	[ -n "$interpreted_path" ] || fail "could not resolve native executable path: $1"
	printf '%s\n' "$interpreted_path"
}

interpreted_wait_for_pid() {
	interpreted_pid_file=$1
	interpreted_attempt=0
	while [ "$interpreted_attempt" -lt 30 ]; do
		interpreted_pid=$(cat "$interpreted_pid_file" 2>/dev/null || true)
		if [ -n "$interpreted_pid" ] && kill -0 "$interpreted_pid" 2>/dev/null; then
			printf '%s\n' "$interpreted_pid"
			return 0
		fi
		sleep 1
		interpreted_attempt=$((interpreted_attempt + 1))
	done
	fail "interpreted application did not write a live PID to $interpreted_pid_file"
}

interpreted_wait_for_pid_gone() {
	interpreted_pid=$1
	interpreted_attempt=0
	while kill -0 "$interpreted_pid" 2>/dev/null; do
		[ "$interpreted_attempt" -lt 30 ] || fail "interpreted application PID $interpreted_pid is still running"
		sleep 1
		interpreted_attempt=$((interpreted_attempt + 1))
	done
}

interpreted_http_response() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 2 "http://127.0.0.1:$port/"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- -T 2 "http://127.0.0.1:$port/"
	elif command -v uclient-fetch >/dev/null 2>&1; then
		uclient-fetch -qO- "http://127.0.0.1:$port/"
	elif command -v fetch >/dev/null 2>&1; then
		fetch -qo - "http://127.0.0.1:$port/"
	elif command -v bash >/dev/null 2>&1; then
		bash -c '
			exec 3<>"/dev/tcp/127.0.0.1/$1" || exit 1
			printf "GET / HTTP/1.0\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n" >&3
			while IFS= read -r line <&3; do
				[ "$line" = "$(printf "\r")" ] && break
			done
			cat <&3
		' bash "$port"
	else
		fail 'no supported HTTP client is installed in the guest'
	fi
}

interpreted_wait_for_native_app() {
	interpreted_attempt=0
	while [ "$interpreted_attempt" -lt 30 ]; do
		interpreted_response=$(interpreted_http_response 2>/dev/null || true)
		case "$interpreted_response" in
			*'"message": "hello symlink"'*)
				printf '%s\n' "$interpreted_response"
				return 0
				;;
		esac
		sleep 1
		interpreted_attempt=$((interpreted_attempt + 1))
	done
	fail 'symlinked native application did not become ready'
}

interpreted_verify_native_symlink() {
	interpreted_auxiliary_name="${service_name}symlinkapp"
	interpreted_app_link="$install_dir/test-app-symlink"
	interpreted_events="$artifact_dir/symlink-application.events.jsonl"
	interpreted_response_file="$artifact_dir/symlink-application-http.json"

	ln -sfn "$app_bin" "$interpreted_app_link"
	rm -f "$interpreted_events"
	"$daemon_bin" install --ignore-warnings "$interpreted_auxiliary_name" "$interpreted_app_link" \
		--enabled=true --message 'hello symlink' --count 7 --port "$port" \
		--file-path relative-path-test.txt --event-path "$interpreted_events"

	if command -v interpreted_definition_path >/dev/null 2>&1; then
		interpreted_definition=$(interpreted_definition_path "$interpreted_auxiliary_name")
		[ -e "$interpreted_definition" ] || fail "symlink application definition was not created: $interpreted_definition"
		grep -Fq "$app_bin" "$interpreted_definition" || fail "symlink definition does not contain resolved application $app_bin"
	fi

	"$daemon_bin" start "$interpreted_auxiliary_name"
	interpreted_wait_for_native_app >"$interpreted_response_file"
	interpreted_pid=$(sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' "$interpreted_response_file")
	[ -n "$interpreted_pid" ] || fail 'symlinked native application response has no PID'
	interpreted_output=$("$daemon_bin" status "$interpreted_auxiliary_name")
	interpreted_assert_contains "$interpreted_output" running 'symlink application status'
	interpreted_output=$("$daemon_bin" list)
	interpreted_assert_contains "$interpreted_output" "$app_bin" 'symlink application resolved path listing'
	"$daemon_bin" stop "$interpreted_auxiliary_name"
	interpreted_wait_for_pid_gone "$interpreted_pid"
	"$daemon_bin" remove "$interpreted_auxiliary_name"
	if command -v interpreted_removed >/dev/null 2>&1; then
		interpreted_removed "$interpreted_auxiliary_name"
	fi
	rm -f "$interpreted_app_link"
}

interpreted_verify_one() {
	interpreted_label=$1
	interpreted_interpreter=$2
	interpreted_script=$3
	interpreted_auxiliary_name="${service_name}${interpreted_label}app"
	interpreted_interpreter_link="$install_dir/${interpreted_label}-interpreter"
	interpreted_state="$artifact_dir/${interpreted_label}-application"
	interpreted_resolved=$(interpreted_resolve_path "$interpreted_interpreter")
	interpreted_applet=
	case "${interpreted_resolved##*/}" in
		busybox|busybox.*|toybox|toybox.*) interpreted_applet=${interpreted_interpreter##*/} ;;
	esac

	ln -sfn "$interpreted_resolved" "$interpreted_interpreter_link"
	rm -f "$interpreted_state.pid" "$interpreted_state.events"
	"$daemon_bin" install --ignore-warnings "$interpreted_auxiliary_name" "$interpreted_interpreter_link" \
		${interpreted_applet:+"$interpreted_applet"} "$interpreted_script" "$interpreted_state"

	if command -v interpreted_definition_path >/dev/null 2>&1; then
		interpreted_definition=$(interpreted_definition_path "$interpreted_auxiliary_name")
		[ -e "$interpreted_definition" ] || fail "$interpreted_label application definition was not created: $interpreted_definition"
		grep -Fq "$interpreted_resolved" "$interpreted_definition" || fail "$interpreted_label definition does not contain resolved interpreter $interpreted_resolved"
		grep -Fq "$interpreted_script" "$interpreted_definition" || fail "$interpreted_label definition does not contain script argument $interpreted_script"
	fi

	"$daemon_bin" start "$interpreted_auxiliary_name"
	interpreted_pid=$(interpreted_wait_for_pid "$interpreted_state.pid")
	interpreted_output=$("$daemon_bin" status "$interpreted_auxiliary_name")
	interpreted_assert_contains "$interpreted_output" running "$interpreted_label application status"
	interpreted_output=$("$daemon_bin" list -l)
	interpreted_assert_contains "$interpreted_output" "$interpreted_script" "$interpreted_label application argument listing"
	"$daemon_bin" stop "$interpreted_auxiliary_name"
	interpreted_wait_for_pid_gone "$interpreted_pid"
	grep -Fq stopped "$interpreted_state.events" || fail "$interpreted_label application did not record graceful stop"
	"$daemon_bin" remove "$interpreted_auxiliary_name"
	if command -v interpreted_removed >/dev/null 2>&1; then
		interpreted_removed "$interpreted_auxiliary_name"
	fi
	rm -f "$interpreted_interpreter_link"
}

verify_interpreted_applications() {
	interpreted_shell_script="$install_dir/shell-application.sh"
	interpreted_python_script="$install_dir/python-application.py"
	interpreted_rejected_name="${service_name}directscript"
	interpreted_rejection="$artifact_dir/direct-script-rejection.txt"

	cat >"$interpreted_shell_script" <<'INTERPRETED_SHELL_EOF'
#!/bin/sh
state=$1
printf '%s\n' "$$" >"$state.pid"
printf '%s\n' started >>"$state.events"
trap 'printf "%s\n" stopped >>"$state.events"; exit 0' TERM INT
while :; do sleep 1; done
INTERPRETED_SHELL_EOF
	chmod 0755 "$interpreted_shell_script"

	set +e
	"$daemon_bin" install --ignore-warnings "$interpreted_rejected_name" "$interpreted_shell_script" >"$interpreted_rejection" 2>&1
	interpreted_status=$?
	set -e
	[ "$interpreted_status" -ne 0 ] || fail 'direct shell script was accepted as a native executable'
	if command -v interpreted_removed >/dev/null 2>&1; then
		interpreted_removed "$interpreted_rejected_name"
	fi

	interpreted_verify_native_symlink
	interpreted_verify_one shell /bin/sh "$interpreted_shell_script"

	if command -v python3 >/dev/null 2>&1; then
		cat >"$interpreted_python_script" <<'INTERPRETED_PYTHON_EOF'
import os
import signal
import sys
import time
state = sys.argv[1]
with open(state + ".pid", "w", encoding="utf-8") as output:
    output.write(str(os.getpid()))
with open(state + ".events", "a", encoding="utf-8") as output:
    output.write("started\n")
def stop(_signal, _frame):
    with open(state + ".events", "a", encoding="utf-8") as output:
        output.write("stopped\n")
    raise SystemExit(0)
signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
while True:
    time.sleep(1)
INTERPRETED_PYTHON_EOF
		chmod 0644 "$interpreted_python_script"
		if [ "$(uname -s)" = Linux ]; then
			interpreted_python_executable=$(python3 -c 'import os; print(os.readlink("/proc/self/exe"))')
		else
			interpreted_python_executable=$(python3 -c 'import os, sys; print(os.path.realpath(sys.executable))')
		fi
		[ -x "$interpreted_python_executable" ] || fail "Python did not report a native executable: $interpreted_python_executable"
		interpreted_verify_one python "$interpreted_python_executable" "$interpreted_python_script"
	else
		printf '%s\n' 'SKIP: python3 is not installed in this guest' >"$artifact_dir/python-application-skip.txt"
	fi
}
