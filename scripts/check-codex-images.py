#!/usr/bin/env python3
"""Verify installed Codex discovers and calls Kilo's image MCP without inference.

Usage: python3 scripts/check-codex-images.py /absolute/path/to/codex
Requires Go, Node.js, and an installed Codex binary. All profiles, image outputs,
credentials, and gateway responses are synthetic and temporary. No turn/start.
"""
import base64
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def wait_json(file, process, seconds=20):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise AssertionError('Synthetic Go fixture exited before readiness')
        try:
            return json.loads(file.read_text())
        except (FileNotFoundError, json.JSONDecodeError):
            time.sleep(.05)
    raise AssertionError('Synthetic Go fixture readiness timed out')


def stop(process):
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def main():
    if len(sys.argv) != 2 or not Path(sys.argv[1]).is_absolute():
        raise SystemExit('Usage: python3 scripts/check-codex-images.py /absolute/path/to/codex')
    binary = Path(sys.argv[1])
    assert binary.is_file(), 'Codex binary does not exist'
    fixture = codex = None
    with tempfile.TemporaryDirectory(prefix='kilo-images-codex-test-') as directory:
        temp = Path(directory)
        compiled = temp / ('kilo-e2e.exe' if os.name == 'nt' else 'kilo-e2e')
        subprocess.run(['go', 'test', '-c', '-o', str(compiled)], cwd=ROOT, check=True, timeout=180)
        manifest, stop_file = temp / 'manifest.json', temp / 'stop'
        fixture_log = (temp / 'fixture.log').open('w+')
        try:
            fixture = subprocess.Popen([str(compiled), '-test.run=^TestE2EServer$', '-test.timeout=4m'], cwd=temp,
                env={**os.environ, 'KILO_E2E_MANIFEST': str(manifest), 'KILO_E2E_STOP': str(stop_file)},
                stdout=fixture_log, stderr=subprocess.STDOUT)
            gateway = wait_json(manifest, fixture)
            Path(gateway['launchControl']).write_text(json.dumps({'imageModels': True}))
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            admin = gateway['url'].split('/#')[0]

            def api(endpoint, body=None):
                data = None if body is None else json.dumps(body).encode()
                req = urllib.request.Request(admin + '/api/' + endpoint, data=data,
                    headers={'Authorization': 'Bearer ' + gateway['token'], 'Content-Type': 'application/json'})
                with opener.open(req, timeout=15) as response:
                    return json.load(response)

            api('config', {'apiKey': 'synthetic-kilo-personal-key', 'orgId': 'e2e-team',
                           'port': gateway['proxyPort'], 'remember': True})
            api('start', {})
            generated = subprocess.check_output(['node', '--input-type=module', '-e',
                "import {codexCatalog} from './ui/codex-catalog.mjs'; console.log(JSON.stringify(codexCatalog([{id:'vendor/one',name:'Synthetic coding model'}],'vendor/one')));"],
                cwd=ROOT, text=True, timeout=15)
            api('codex/catalog', {'catalog': json.loads(generated),
                'imageGeneration': {'enabled': True, 'model': 'image-lab/painter'}})
            local_key = api('state')['localKey']
            profile = Path(gateway['profiles']['codex'])
            # This disposable profile must never inherit user billing or plugins.
            with (profile / 'config.toml').open('a') as config:
                config.write('\n[analytics]\nenabled = false\n')
            env = {k: v for k, v in os.environ.items() if k not in {'OPENAI_API_KEY', 'OPENAI_BASE_URL', 'CODEX_API_KEY'}}
            env.update(CODEX_HOME=str(profile), KILO_LOCAL_API_KEY=local_key)
            codex = subprocess.Popen([str(binary), 'app-server', '--stdio'], cwd=temp, env=env,
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
            messages = queue.Queue()
            stderr = []

            def read_stdout():
                for line in codex.stdout:
                    try:
                        messages.put(json.loads(line))
                    except json.JSONDecodeError:
                        messages.put({'invalidLine': line})

            def read_stderr():
                for line in codex.stderr:
                    stderr.append(line)

            threading.Thread(target=read_stdout, daemon=True).start()
            threading.Thread(target=read_stderr, daemon=True).start()
            sequence = 0

            def send(message):
                codex.stdin.write(json.dumps(message) + '\n')
                codex.stdin.flush()

            def rpc(method, params):
                nonlocal sequence
                sequence += 1
                send({'id': sequence, 'method': method, 'params': params})
                deadline = time.monotonic() + 25
                while time.monotonic() < deadline:
                    try:
                        message = messages.get(timeout=.2)
                    except queue.Empty:
                        if codex.poll() is not None:
                            raise AssertionError('Codex app-server exited: ' + ''.join(stderr[-6:]))
                        continue
                    assert 'invalidLine' not in message, message
                    assert message.get('method') != 'configWarning', message
                    if message.get('id') == sequence:
                        assert 'error' not in message, message
                        return message['result']
                raise AssertionError(method + ' timed out')

            rpc('initialize', {'clientInfo': {'name': 'kilo_images_test', 'version': '1'}, 'capabilities': {'experimentalApi': True}})
            send({'method': 'initialized'})
            thread = rpc('thread/start', {'cwd': str(temp), 'ephemeral': True, 'approvalPolicy': 'never',
                'approvalsReviewer': 'user', 'sandbox': 'read-only', 'model': 'vendor/one', 'modelProvider': 'kilo-local'})
            thread_id = thread['thread']['id']
            deadline = time.monotonic() + 20
            server = None
            while time.monotonic() < deadline:
                inventory = rpc('mcpServerStatus/list', {'threadId': thread_id, 'detail': 'toolsAndAuthOnly', 'limit': 100})
                server = next((item for item in inventory['data'] if item['name'] == 'kilo_images'), None)
                if server and any(tool.get('name') == 'generate_image' for tool in server['tools'].values()):
                    break
                time.sleep(.2)
            assert server and any(tool.get('name') == 'generate_image' for tool in server['tools'].values()), inventory
            result = rpc('mcpServer/tool/call', {'threadId': thread_id, 'server': 'kilo_images', 'tool': 'generate_image',
                'arguments': {'prompt': 'SYNTHETIC_CODEX_IMAGE_PROBE'}})
            assert not result.get('isError'), result
            info = result.get('structuredContent') or json.loads(next(item['text'] for item in result['content'] if item['type'] == 'text'))
            assert info['model'] == 'image-lab/painter', info
            image = info['images'][0]
            assert image['width'] == image['height'] == 1 and image['mimeType'] == 'image/png', image
            assert Path(image['path']).parent == Path(gateway['root']) / 'app' / 'generated-images', image
            assert base64.b64encode(Path(image['path']).read_bytes()).decode() == gateway['imageBase64']
            requests = json.loads(Path(gateway['imageRecords']).read_text())
            assert len(requests) == 1 and requests[0]['model'] == 'image-lab/painter', requests
            print('PASS: installed Codex discovered kilo_images/generate_image and invoked the real Go MCP through a synthetic organization gateway; PNG output verified. No model turn or paid request.')
        finally:
            stop(codex)
            if fixture is not None and fixture.poll() is None:
                stop_file.write_text('stop')
                try:
                    fixture.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    stop(fixture)
            fixture_log.close()


if __name__ == '__main__':
    main()
