#!/usr/bin/env python3
"""Recover an OOBE-blocked tray only inside a disposable GitHub ARM64 runner."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

from probe_windows_tray import registration_matches, registration_succeeded

OOBE_HOSTS = {'msoobe.exe', 'cloudexperiencehost.exe',
              'cloudexperiencehostbroker.exe', 'wwahost.exe'}


def recovery_required(report, environment, current_session):
    if registration_succeeded(report):
        return False
    if not registration_matches(report, False):
        raise RuntimeError('A failed production-equivalent notification probe is required before recovery')
    required = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted',
                'RUNNER_OS': 'Windows', 'RUNNER_ARCH': 'ARM64'}
    if any(environment.get(key) != value for key, value in required.items()):
        raise RuntimeError('Shell recovery is restricted to hosted GitHub Windows ARM64 runners')
    if (not isinstance(current_session, int) or current_session <= 0
            or report.get('desktop', {}).get('session_id') != current_session
            or report.get('process_machine') != '0xaa64'
            or report.get('native_machine') != '0xaa64'
            or report.get('emulated') is not False):
        raise RuntimeError('The notification probe does not belong to this native interactive session')
    if not any(row.get('name', '').lower() in OOBE_HOSTS
               and str(row.get('session')) == str(current_session)
               for row in report.get('shell_processes', [])):
        raise RuntimeError('Notification registration failed without a known OOBE host in this session')
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before-report', type=Path, required=True)
    parser.add_argument('--after-report', type=Path, required=True)
    args = parser.parse_args()
    report = json.loads(args.before_report.read_text(encoding='utf-8'))
    if registration_succeeded(report):
        args.after_report.parent.mkdir(parents=True, exist_ok=True)
        args.after_report.write_text(json.dumps(report, indent=2), encoding='utf-8')
        print('Native notification registration already works; shell unchanged.')
        return
    from smoke_desktop import windows_desktop_context
    session = windows_desktop_context()['session_id']
    recovery_required(report, os.environ, session)
    print('Recovering the OOBE-blocked notification area in this disposable runner session.', flush=True)
    subprocess.run(['pwsh', '-NoProfile', '-NonInteractive', '-File',
                    str(Path(__file__).with_suffix('.ps1')), '-SessionId', str(session)],
                   check=True, timeout=45)
    subprocess.run([sys.executable, str(Path(__file__).with_name('probe_windows_tray.py')),
                    '--report', str(args.after_report), '--require-success'],
                   check=True, timeout=90)


if __name__ == '__main__':
    main()
