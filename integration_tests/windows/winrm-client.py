#!/usr/bin/env python3

import argparse
import os
import sys
import time

import winrm


def make_session(args: argparse.Namespace) -> winrm.Session:
    return winrm.Session(
        args.endpoint,
        auth=(args.user, args.password),
        transport="ntlm",
        server_cert_validation="ignore",
        operation_timeout_sec=args.operation_timeout,
        read_timeout_sec=args.operation_timeout + 10,
    )


def run_script(session: winrm.Session, script: str) -> int:
    result = session.run_ps(script)
    if result.std_out:
        sys.stdout.buffer.write(result.std_out)
    if result.std_err:
        sys.stderr.buffer.write(result.std_err)
    return result.status_code


def main() -> int:
    parser = argparse.ArgumentParser(description="Minimal pywinrm command helper")
    parser.add_argument("--endpoint", default=os.environ.get("WINDOWS_WINRM_ENDPOINT", "http://127.0.0.1:55985/wsman"))
    parser.add_argument("--user", default=os.environ.get("WINDOWS_ADMIN_USER", "Administrator"))
    parser.add_argument("--password", default=os.environ.get("WINDOWS_ADMIN_PASSWORD", "DaemonTest!2026"))
    parser.add_argument("--operation-timeout", type=int, default=60)
    subparsers = parser.add_subparsers(dest="action", required=True)

    wait_parser = subparsers.add_parser("wait", help="wait until WinRM accepts commands")
    wait_parser.add_argument("--timeout", type=int, default=7200)

    run_parser = subparsers.add_parser("run", help="execute a PowerShell command")
    run_parser.add_argument("script")

    file_parser = subparsers.add_parser("run-file", help="execute a local PowerShell script through WinRM")
    file_parser.add_argument("path")

    args = parser.parse_args()
    if args.action == "wait":
        deadline = time.monotonic() + args.timeout
        last_error = None
        while time.monotonic() < deadline:
            try:
                session = make_session(args)
                result = session.run_ps("$PSVersionTable.PSVersion.ToString()")
                if result.status_code == 0:
                    if result.std_out:
                        sys.stdout.buffer.write(result.std_out)
                    return 0
                last_error = result.std_err.decode("utf-8", "replace")
            except Exception as error:  # WinRM raises several transport-specific errors.
                last_error = str(error)
            time.sleep(10)
        print(f"timed out waiting for WinRM: {last_error}", file=sys.stderr)
        return 1

    session = make_session(args)
    if args.action == "run":
        return run_script(session, args.script)
    with open(args.path, "r", encoding="utf-8-sig") as script_file:
        return run_script(session, script_file.read())


if __name__ == "__main__":
    raise SystemExit(main())
