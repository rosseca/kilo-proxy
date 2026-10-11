#!/usr/bin/env python3
"""Validate Codex V1 collaboration's exact argument repair against local SSE.

Usage: python3 scripts/check-codex-collab.py /absolute/path/to/codex
The synthetic gateway checks both conflicts (message + [] and items + ""),
then the corresponding single-input calls in fresh profiles. This checks the
installed Codex runtime, not the Kilo HTTP bridge: Go collab_input tests
separately verify that bridge performs these exact repairs.
No existing profile, existing agent or paid API is used.
"""
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]


def run_case(binary, input_kind, include_empty_other):
    records, outputs, failures = [], [], []
    lock = threading.Lock()

    class Gateway(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            try:
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                assert self.headers.get('Authorization') == 'Bearer collab-fixture'
                with lock:
                    records.append(body)
                    number = len(records)
                for item in body.get('input', []):
                    if (
                        item.get('type') == 'function_call_output'
                        and item.get('call_id') == 'spawn_fixture'
                    ):
                        outputs.append(item.get('output'))
                if number == 1:
                    assert any(
                        tool.get('name') == 'multi_agent_v1'
                        and any(
                            inner.get('name') == 'spawn_agent'
                            for inner in tool.get('tools', [])
                        )
                        for tool in body.get('tools', [])
                    ), 'Installed Codex did not expose multi_agent_v1.spawn_agent'
                    task = (
                        'Reply with a short synthetic acknowledgement. '
                        'No tools or external requests are needed.'
                    )
                    if input_kind == 'message':
                        args = {'message': task}
                        if include_empty_other:
                            args['items'] = []
                    else:
                        args = {'items': [{'type': 'text', 'text': task}]}
                        if include_empty_other:
                            args['message'] = ''
                    item = {
                        'id': 'fc_fixture',
                        'type': 'function_call',
                        'call_id': 'spawn_fixture',
                        'name': 'spawn_agent',
                        'namespace': 'multi_agent_v1',
                        'arguments': json.dumps(args),
                        'status': 'completed',
                    }
                else:
                    item = {
                        'id': 'msg_' + str(number),
                        'type': 'message',
                        'role': 'assistant',
                        'status': 'completed',
                        'content': [{
                            'type': 'output_text',
                            'text': 'Synthetic acknowledgement.',
                            'annotations': [],
                        }],
                    }
                response = {
                    'id': 'resp_' + str(number),
                    'object': 'response',
                    'status': 'completed',
                    'model': body.get('model'),
                    'output': [item],
                    'usage': {
                        'input_tokens': 100,
                        'output_tokens': 5,
                        'total_tokens': 105,
                        'input_tokens_details': {'cached_tokens': 0},
                    },
                }
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.end_headers()
                for event in [
                    {
                        'type': 'response.created',
                        'response': {**response, 'status': 'in_progress', 'output': []},
                    },
                    {'type': 'response.output_item.added', 'output_index': 0, 'item': item},
                    {'type': 'response.output_item.done', 'output_index': 0, 'item': item},
                    {'type': 'response.completed', 'response': response},
                ]:
                    self.wfile.write(('data: ' + json.dumps(event) + '\n\n').encode())
                    self.wfile.flush()
            except Exception as error:
                failures.append(str(error))
                self.send_error(500, 'Synthetic collaboration fixture failed')

    with ThreadingHTTPServer(('127.0.0.1', 0), Gateway) as server, \
            tempfile.TemporaryDirectory(prefix='kilo-collab-runtime-') as directory:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        profile = Path(directory)
        generated = subprocess.check_output(
            ['node', '--input-type=module', '-e', """
import {codexCatalog} from './ui/codex-catalog.mjs';
const catalog=codexCatalog([{id:'fixture/collab',contextWindow:128000,contextPreset:'low'}]);
catalog.models[0].multi_agent_version='v1';
console.log(JSON.stringify(catalog));
"""],
            cwd=ROOT,
            text=True,
            timeout=15,
        )
        (profile / 'models.json').write_text(generated)
        (profile / 'config.toml').write_text(f'''model = "fixture/collab"
model_provider = "kilo-local"
model_catalog_json = "models.json"
cli_auth_credentials_store = "file"
[features]
multi_agent = true
multi_agent_v2 = false
[model_providers.kilo-local]
name = "Synthetic local collaboration fixture"
base_url = "http://127.0.0.1:{server.server_port}/v1"
env_key = "KILO_LOCAL_API_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
[analytics]
enabled = false
''')
        env = {
            k: v for k, v in os.environ.items()
            if not k.startswith(('CODEX_', 'OPENAI_', 'KILO_'))
        }
        env.update(CODEX_HOME=directory, KILO_LOCAL_API_KEY='collab-fixture')
        process = subprocess.Popen(
            [binary, 'app-server', '--stdio'],
            cwd=directory,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        events, errors, observed = queue.Queue(), [], []

        def read():
            for line in process.stdout:
                events.put(json.loads(line))

        def read_errors():
            for line in process.stderr:
                errors.append(line)

        threading.Thread(target=read, daemon=True).start()
        threading.Thread(target=read_errors, daemon=True).start()

        def send(message):
            process.stdin.write(json.dumps(message) + '\n')
            process.stdin.flush()

        def wait_for(predicate):
            deadline = time.monotonic() + 35
            while time.monotonic() < deadline:
                assert not failures, failures
                try:
                    event = events.get(timeout=.2)
                except queue.Empty:
                    assert process.poll() is None, ''.join(errors[-8:])
                    continue
                observed.append(event)
                assert 'error' not in event, event
                assert event.get('method') != 'configWarning', event
                if predicate(event):
                    return event
            raise AssertionError(
                'Codex event timed out: ' + ''.join(errors[-5:])
                + '; outputs=' + json.dumps(outputs)
            )

        try:
            send({
                'id': 1,
                'method': 'initialize',
                'params': {
                    'clientInfo': {'name': 'collab_fixture', 'version': '1'},
                    'capabilities': {'experimentalApi': True},
                },
            })
            wait_for(lambda event: event.get('id') == 1)
            send({'method': 'initialized'})
            send({
                'id': 2,
                'method': 'thread/start',
                'params': {
                    'cwd': directory,
                    'ephemeral': True,
                    'approvalPolicy': 'never',
                    'sandbox': 'read-only',
                    'model': 'fixture/collab',
                },
            })
            thread = wait_for(lambda event: event.get('id') == 2)['result']['thread']['id']
            send({
                'id': 3,
                'method': 'turn/start',
                'params': {
                    'threadId': thread,
                    'input': [{
                        'type': 'text',
                        'text': 'Run the synthetic collaboration check.',
                    }],
                },
            })
            wait_for(
                lambda event: event.get('method') == 'turn/completed'
                and event.get('params', {}).get('threadId') == thread
            )
            assert outputs, 'Codex did not return a collaboration tool result'
            joined = json.dumps(outputs)
            if include_empty_other:
                assert 'Provide either message or items, but not both' in joined, outputs
            else:
                agent_id = json.loads(outputs[0])['agent_id']
                completed = [
                    event.get('params', {}).get('item', {})
                    for event in observed
                    if event.get('method') == 'item/completed'
                ]
                assert any(
                    item.get('type') == 'collabAgentToolCall'
                    and item.get('tool') == 'spawnAgent'
                    and item.get('status') == 'completed'
                    and agent_id in item.get('receiverThreadIds', [])
                    for item in completed
                ), completed
                assert 'Provide either message or items, but not both' not in joined, outputs
            return outputs
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            server.shutdown()


def main():
    if len(sys.argv) != 2 or not Path(sys.argv[1]).is_file():
        raise SystemExit(
            'Usage: python3 scripts/check-codex-collab.py /absolute/path/to/codex'
        )
    for input_kind in ('message', 'items'):
        run_case(sys.argv[1], input_kind, True)
        run_case(sys.argv[1], input_kind, False)
    print(
        'PASS: installed Codex rejects both empty-sibling conflicts and creates a '
        'subagent when each empty field is omitted. All gateway responses are '
        'synthetic; no paid requests.'
    )


if __name__ == '__main__':
    main()
