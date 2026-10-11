#!/usr/bin/env python3
"""Exercise a production console binary in a disposable home without desktop services."""
import argparse
import copy
import json
import os
from pathlib import Path
import platform
import shlex
import socket
import struct
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request


def extract(archive, directory):
    if not archive.name.endswith('.tar.gz'):
        raise ValueError('Headless releases must be TAR.GZ archives')
    with tarfile.open(archive) as tar:
        for member in tar.getmembers():
            target = (directory / member.name).resolve()
            if (directory not in target.parents or member.name.startswith('/') or
                    not (member.isfile() or member.isdir())):
                raise ValueError('Unsafe headless archive entry')
        executables = [member for member in tar.getmembers()
                       if member.isfile() and member.name.rsplit('/', 1)[-1] == 'kilo-proxy-headless']
        if len(executables) != 1:
            raise ValueError('Archive must contain exactly one console executable')
        if executables[0].mode & 0o111 == 0:
            raise ValueError('Headless archive executable lacks executable permissions')
        if hasattr(tarfile, 'data_filter'):
            # Do not restore build-machine uid/gid. Besides being unnecessary,
            # a root container without CAP_CHOWN can otherwise skip chmod
            # after a nonfatal ownership error and lose the executable bit.
            tar.extractall(directory, filter=tarfile.data_filter)
        else:
            # Python 3.9 compatibility: every path/type was prevalidated above.
            # -1 means retain the extracting user's ownership on Unix.
            members = []
            for member in tar.getmembers():
                member = copy.copy(member)
                member.uid = member.gid = -1
                member.uname = member.gname = None
                member.mode &= 0o777
                members.append(member)
            tar.extractall(directory, members=members)
    candidates = [path for path in directory.rglob('kilo-proxy-headless') if path.is_file()]
    if len(candidates) != 1:
        raise ValueError('Archive must contain exactly one console executable')
    if os.name == 'posix' and candidates[0].stat().st_mode & 0o111 == 0:
        raise ValueError('Headless executable lost its executable permissions')
    return candidates[0]


def verify_native_header(binary):
    data = binary.read_bytes()
    expected_os = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
    expected_arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
    if expected_os is None or expected_arch is None:
        raise RuntimeError('Headless smoke requires a supported macOS/Linux native host')
    actual = None
    if data[:6] == b'\x7fELF\x02\x01':
        actual = ('linux', {62: 'amd64', 183: 'arm64'}.get(struct.unpack_from('<H', data, 18)[0]))
        offset, entry_size, count = struct.unpack_from('<Q', data, 32)[0], struct.unpack_from('<H', data, 54)[0], struct.unpack_from('<H', data, 56)[0]
        if entry_size < 56 or offset + count * entry_size > len(data):
            raise RuntimeError('Malformed ELF program header')
        if any(struct.unpack_from('<I', data, offset + i * entry_size)[0] == 3 for i in range(count)):
            raise RuntimeError('Headless Linux binary must be static: found a dynamic interpreter')
    elif data[:4] == b'\xcf\xfa\xed\xfe':
        actual = ('darwin', {0x01000007: 'amd64', 0x0100000c: 'arm64'}.get(struct.unpack_from('<I', data, 4)[0]))
        # Pure-Go Mach-O still uses libSystem; reject native UI/graphics libraries.
        dependencies = subprocess.run(['/usr/bin/otool', '-L', str(binary)], capture_output=True, text=True, check=True).stdout
        if any(name in dependencies for name in ('AppKit', 'Cocoa', 'Metal', 'QuartzCore', 'OpenGL', 'CoreGraphics')):
            raise RuntimeError('Headless macOS binary links a desktop/graphics framework')
    if actual != (expected_os, expected_arch):
        raise RuntimeError('Headless smoke ran the wrong CPU/OS executable')
    return actual


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def request_status(url, key='wrong-local-key'):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + key})
    try:
        with opener.open(request, timeout=2) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code


def wait(predicate, message, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.1)
    raise RuntimeError(message)


def smoke(binary, root, expected_version):
    system, arch = verify_native_header(binary)
    binary = binary.resolve()
    home = root / "home with spaces ' literal"
    profile = home / 'headless-profile'
    project = home / 'project with spaces'
    fake_bin = home / 'fake-bin'
    for path in (home, profile, project, fake_bin):
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
    env = dict(os.environ)
    for name in list(env):
        if name in ('DISPLAY', 'WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS', 'DBUS_SYSTEM_BUS_ADDRESS',
                    'SSH_AUTH_SOCK', 'ZDOTDIR', 'NODE_OPTIONS', 'ELECTRON_RUN_AS_NODE') or name.startswith(('CODEX_', 'CLAUDE_', 'ANTHROPIC_', 'OPENAI_', 'OMP_', 'PI_', 'OPENCODE_', 'KILO_')):
            env.pop(name, None)
    # A deliberately invalid bus address proves the runtime never requires it.
    env.update(HOME=str(home), XDG_CONFIG_HOME=str(home / '.config'),
               XDG_DATA_HOME=str(home / '.local/share'), XDG_CACHE_HOME=str(home / '.cache'),
               XDG_STATE_HOME=str(home / '.local/state'), DBUS_SESSION_BUS_ADDRESS='unix:path=/nonexistent-kilo-smoke-bus',
               SHELL='/bin/bash', PATH=str(fake_bin) + os.pathsep + env.get('PATH', '/usr/local/bin:/usr/bin:/bin'))
    token = 'synthetic-headless-upstream-credential'
    checks = []

    def run(*args, input='', expected=0, cwd=None, timeout=30):
        result = subprocess.run([str(binary), '--config-dir', str(profile), *args], input=input,
                                capture_output=True, text=True, env=env, cwd=cwd or project, timeout=timeout)
        if token in result.stdout + result.stderr:
            raise RuntimeError('Headless CLI disclosed its synthetic upstream credential')
        if result.returncode != expected:
            raise RuntimeError(f'Headless command {args[0] if args else "default"} failed (exit {result.returncode}): ' + result.stderr[-2000:])
        return result

    if run('--version').stdout.strip() != expected_version:
        raise RuntimeError('Headless smoke ran a stale version')
    checks.append('pure-go-native-header-and-version')
    run('status', '--json')
    port = free_port()
    configured = run('configure', '--org', 'synthetic-headless-org', '--port', str(port), '--key-stdin', input=token + '\n')
    commands = ('kilo-codex', 'kilo-claude', 'kilo-omp', 'kilo-opencode')
    suggestions = [shlex.split(line.strip()) for line in configured.stdout.splitlines() if line.strip().endswith('commands install')]
    if suggestions != [[str(binary), '--config-dir', str(profile), 'commands', 'install']] or not all(command in configured.stdout for command in commands):
        raise RuntimeError('Setup did not suggest the four commands with a correctly quoted custom profile')
    if any((home / '.local/bin' / command).exists() for command in commands):
        raise RuntimeError('Setup installed terminal commands without an explicit install request')
    library = {'schemaVersion': 1, 'defaultModel': 'openai/gpt-synthetic', 'models': [
        {'id': 'openai/gpt-synthetic', 'displayName': 'Synthetic High', 'contextPreset': 'custom', 'contextWindow': 128000,
         'maxOutputTokens': 1024, 'reasoningEffort': 'high', 'reasoningLevels': ['low', 'high'], 'reasoningCustom': True}]}
    run('models', 'import', '-', input=json.dumps(library))
    exported = json.loads(run('models', 'export', '-').stdout)
    if exported != library:
        raise RuntimeError('Headless model library round-trip changed its reasoning/context metadata')
    checks.append('stdin-credentials-and-offline-models')
    settings = json.loads((profile / 'settings.json').read_text())
    local_key = settings['localKey']
    if (profile.stat().st_mode & 0o777) != 0o700:
        raise RuntimeError('Headless profile is not private')
    for path in (profile / 'headless-secrets').iterdir():
        if not path.is_file() or path.is_symlink() or path.stat().st_mode & 0o077:
            raise RuntimeError('Headless credential vault file is unsafe')
    checks.append('private-file-vault-without-desktop-keyring')

    fake_script = '''#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
if sys.argv[1:] == ['--version']:
    print({'codex': 'codex-cli 0.159.0', 'claude': '2.1.251 (Claude Code)', 'omp': '18.2.9', 'opencode': '1.3.18'}[name])
    sys.exit(0)
record = {'name': name, 'args': sys.argv[1:], 'cwd': os.getcwd(), 'stdin': sys.stdin.read(),
          'codexHome': os.environ.get('CODEX_HOME'), 'claudeHome': os.environ.get('CLAUDE_CONFIG_DIR'),
          'localKey': os.environ.get('KILO_LOCAL_API_KEY'), 'openCodeConfig': os.environ.get('OPENCODE_CONFIG')}
pathlib.Path(os.environ['KILO_HEADLESS_SMOKE_RECORD']).write_text(json.dumps(record))
print('synthetic agent stdout')
print('synthetic agent stderr', file=sys.stderr)
sys.exit(23)
'''
    for name in ('codex', 'claude', 'omp', 'opencode'):
        path = fake_bin / name
        path.write_text(fake_script)
        path.chmod(0o700)
    for normal in ('.codex', '.claude'):
        normal_dir = home / normal
        normal_dir.mkdir(mode=0o700)
        (normal_dir / 'auth.json').write_text('synthetic normal sign-in remains untouched')
    bashrc = home / '.bashrc'
    bashrc.write_text('# existing synthetic shell preferences\n')
    installed = run('commands', 'install')
    activation = [shlex.split(line.strip()) for line in installed.stdout.splitlines() if line.strip().startswith('source ')]
    if activation != [['source', str(bashrc)]] or 'new bash terminal' not in installed.stdout:
        raise RuntimeError('Terminal command installation did not explain its startup file and current-shell activation')
    for command in commands:
        wrapper = home / '.local/bin' / command
        if not wrapper.is_file() or token in wrapper.read_text() or local_key in wrapper.read_text():
            raise RuntimeError('Installed terminal wrapper is missing or contains a credential')
    if not bashrc.read_text().startswith('# existing synthetic shell preferences\n'):
        raise RuntimeError('Terminal installation replaced unrelated shell preferences')
    checks.append('terminal-command-installation-without-gui')

    processes = []

    def start():
        out_path, err_path = root / f'serve-{len(processes)}.out', root / f'serve-{len(processes)}.err'
        out, err = out_path.open('w'), err_path.open('w')
        process = subprocess.Popen([str(binary), '--config-dir', str(profile), 'serve'],
                                   env=env, cwd=project, stdout=out, stderr=err)
        out.close()
        err.close()
        processes.append(process)

        def diagnostic():
            value = '\n'.join(path.read_text(errors='replace') for path in (out_path, err_path))
            for credential in (token, local_key):
                value = value.replace(credential, '[redacted]')
            return ''.join(character for character in value[-4000:]
                           if character in '\n\t' or ord(character) >= 32 and ord(character) != 127)

        def ready():
            if process.poll() is not None:
                raise RuntimeError(f'Headless controller exited before becoming ready (exit {process.returncode}):\n' + diagnostic())
            # Status legitimately owns an offline profile until the controller
            # has published its live endpoint. Do not race daemon startup by
            # opening an offline status client before that file exists.
            if not (profile / 'terminal-runtime.json').exists():
                return False
            try:
                return json.loads(run('status', '--json', timeout=5).stdout)['running']
            except (RuntimeError, subprocess.TimeoutExpired):
                return False
        try:
            wait(ready, 'Headless controller did not become ready')
        except RuntimeError as error:
            if process.poll() is None:
                raise RuntimeError(str(error) + '\n' + diagnostic()) from error
            raise
        return process

    try:
        process = start()
        state = json.loads(run('status', '--json').stdout)
        if state.get('version') != expected_version or not state.get('connectionReady') or state.get('port') != port or token in json.dumps(state) or local_key in json.dumps(state):
            raise RuntimeError('Headless status was invalid or disclosed credentials')
        if request_status(f'http://127.0.0.1:{port}/v1/models') != 401:
            raise RuntimeError('Inference listener accepted an incorrect local key')
        duplicate = run('serve', expected=1)
        if 'already running' not in duplicate.stderr.lower():
            raise RuntimeError('Second controller did not report exclusive profile ownership')
        checks.append('foreground-runtime-auth-and-duplicate-guard')
        run('proxy', 'stop')
        suffix = ['--synthetic-test', 'literal spaces', '', "single'quote", 'double"quote', '日本語']
        for command in commands:
            record = root / (command + '.json')
            child_env = dict(env, KILO_HEADLESS_SMOKE_RECORD=str(record))
            result = subprocess.run([str(home / '.local/bin' / command), *suffix], input='synthetic agent stdin\n',
                                    capture_output=True, text=True, env=child_env, cwd=project, timeout=30)
            if result.returncode != 23 or result.stdout != 'synthetic agent stdout\n' or 'synthetic agent stderr' not in result.stderr:
                raise RuntimeError('Terminal wrapper did not preserve native streams and exit code: ' + command + ' ' + result.stderr[-2000:])
            value = json.loads(record.read_text())
            if value['args'][-len(suffix):] != suffix or value['cwd'] != str(project) or value['stdin'] != 'synthetic agent stdin\n':
                raise RuntimeError('Terminal wrapper changed arguments, cwd or stdin: ' + command)
            if token in json.dumps(value):
                raise RuntimeError('Terminal agent received the upstream credential')
        if not json.loads(run('status', '--json').stdout)['running']:
            raise RuntimeError('Terminal wrapper did not restart the saved proxy connection')
        for normal in ('.codex', '.claude'):
            if (home / normal / 'auth.json').read_text() != 'synthetic normal sign-in remains untouched':
                raise RuntimeError('Terminal agent modified normal authentication')
        checks.append('four-terminal-agents-args-cwd-stdin-exit-and-isolation')
        process.terminate()
        process.wait(timeout=25)
        if process.returncode != 0 or (profile / 'terminal-runtime.json').exists():
            raise RuntimeError('SIGTERM failed to cleanly release the controller runtime')
        with socket.socket() as connection:
            if connection.connect_ex(('127.0.0.1', port)) == 0:
                raise RuntimeError('SIGTERM left the inference listener open')
        checks.append('sigterm-cleans-runtime-and-listener')
        new_port = free_port()
        run('configure', '--port', str(new_port))
        process = start()
        if json.loads(run('status', '--json').stdout)['port'] != new_port:
            raise RuntimeError('Restart ignored the saved profile configuration')
        run('stop')
        process.wait(timeout=25)
        if process.returncode != 0 or (profile / 'terminal-runtime.json').exists():
            raise RuntimeError('CLI stop failed to cleanly release the controller runtime')
        checks.append('saved-config-restart-and-cli-stop')
        for log in root.glob('serve-*.out'):
            if token in log.read_text() or local_key in log.read_text():
                raise RuntimeError('Foreground log disclosed a credential')
    finally:
        for process in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=25)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
    print(f'Passed headless {system}/{arch} {expected_version}: ' + ', '.join(checks))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--binary', type=Path)
    group.add_argument('--archive', type=Path)
    parser.add_argument('--version', default=None)
    args = parser.parse_args()
    expected = args.version
    if expected is None:
        expected = (Path(__file__).resolve().parents[1] / 'VERSION').read_text().strip()
    with tempfile.TemporaryDirectory(prefix='kilo-headless-smoke-') as temporary:
        root = Path(temporary).resolve()
        binary = extract(args.archive.resolve(), root) if args.archive else args.binary
        smoke(binary, root, expected)


if __name__ == '__main__':
    main()
