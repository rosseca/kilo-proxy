#!/usr/bin/env python3
"""Check an installed Codex's dictation auth prerequisite and provider isolation.

Usage: python3 scripts/check-codex-dictation.py /absolute/path/to/codex
Uses temporary profiles, synthetic credentials and a loopback Responses server.
Does not access a real account, record audio or verify the ChatGPT voice service.
"""
import base64
import datetime
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
LOCAL_KEY = "synthetic-kilo-local-key"


def jwt_fixture():
    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value).encode()).decode().rstrip("=")
    claims = {"exp": int(time.time()) + 3600, "email": "test@example.invalid",
              "https://api.openai.com/auth": {"chatgpt_account_id": "dictation-test",
                                              "chatgpt_user_id": "dictation-test",
                                              "chatgpt_plan_type": "plus"}}
    return encode({"alg": "none"}) + "." + encode(claims) + ".synthetic"


def auth_status(binary, profile, env):
    process = subprocess.Popen([binary, "app-server", "--stdio"], cwd=profile, env=env,
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, text=True)
    messages = queue.Queue()
    def read():
        for line in process.stdout:
            try:
                messages.put(json.loads(line))
            except json.JSONDecodeError:
                continue
    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    def send(message):
        process.stdin.write(json.dumps(message) + "\n")
        process.stdin.flush()
    def response(request_id):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            message = messages.get(timeout=max(.01, deadline - time.monotonic()))
            if message.get("id") == request_id:
                assert "error" not in message, message.get("error")
                return message["result"]
        raise AssertionError("Codex app-server timed out")
    try:
        send({"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "kilo_dictation_test", "version": "1"}}})
        response(1)
        send({"method": "initialized"})
        send({"id": 2, "method": "getAuthStatus", "params": {"includeToken": True, "refreshToken": False}})
        return response(2)
    finally:
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        reader.join(timeout=3)
        process.stdin.close()
        process.stdout.close()


class ResponsesHandler(BaseHTTPRequestHandler):
    requests = []

    def log_message(self, *_args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        self.requests.append((self.path, dict(self.headers), body))
        if self.path != "/v1/responses":
            self.send_error(404)
            return
        message = {"id": "msg_test", "type": "message", "role": "assistant", "status": "completed",
                   "content": [{"type": "output_text", "text": "DICTATION_AUTH_OK", "annotations": []}]}
        response = {"id": "resp_test", "object": "response", "status": "completed", "output": [message],
                    "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
        events = [{"type": "response.created", "response": {"id": "resp_test", "status": "in_progress", "output": []}},
                  {"type": "response.output_item.done", "output_index": 0, "item": message},
                  {"type": "response.completed", "response": response}]
        data = "".join(f"event: {event['type']}\ndata: {json.dumps(event)}\n\n" for event in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main():
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    binary = str(Path(sys.argv[1]).resolve(strict=True))
    server = ThreadingHTTPServer(("127.0.0.1", 0), ResponsesHandler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    token = jwt_fixture()
    base = f"http://127.0.0.1:{server.server_port}/v1"
    try:
        for enabled in (False, True):
            with tempfile.TemporaryDirectory(prefix="kilo-dictation-test-") as directory:
                profile = Path(directory)
                env = {key: value for key, value in os.environ.items()
                       if not key.startswith(("OPENAI_", "CODEX_"))
                       and key.lower() not in {"http_proxy", "https_proxy", "all_proxy", "no_proxy"}}
                env.update(CODEX_HOME=directory, KILO_LOCAL_API_KEY=LOCAL_KEY,
                           HTTP_PROXY="http://127.0.0.1:1", HTTPS_PROXY="http://127.0.0.1:1",
                           ALL_PROXY="http://127.0.0.1:1", NO_PROXY="127.0.0.1,localhost")
                generated = subprocess.check_output(["node", "--input-type=module", "-e", """
import {clientConfig} from './ui/client-config.mjs';
import {codexCatalog} from './ui/codex-catalog.mjs';
const enabled=process.argv[1]==='true',baseURL=process.argv[2];
console.log(JSON.stringify({config:clientConfig({client:'codex',baseURL,model:'test/model',catalogPath:'models.json',chatgptDictation:enabled}),catalog:codexCatalog([{id:'test/model',name:'Test model',contextWindow:128000}],'test/model')}));
""", str(enabled).lower(), base], cwd=ROOT, text=True)
                generated = json.loads(generated)
                (profile / "config.toml").write_text('chatgpt_base_url = "http://127.0.0.1:1"\n' + generated["config"])
                (profile / "models.json").write_text(json.dumps(generated["catalog"]))
                auth = {"auth_mode": "chatgpt", "tokens": {"id_token": token, "access_token": token,
                        "refresh_token": "synthetic-refresh", "account_id": "dictation-test"},
                        "last_refresh": datetime.datetime.now(datetime.timezone.utc).isoformat()}
                (profile / "auth.json").write_text(json.dumps(auth))
                status = auth_status(binary, profile, env)
                assert status["requiresOpenaiAuth"] is enabled
                assert status["authMethod"] == ("chatgpt" if enabled else None)
                assert status["authToken"] == (token if enabled else None)
                ResponsesHandler.requests.clear()
                result = subprocess.run([binary, "exec", "--skip-git-repo-check", "--json",
                                         "Reply with DICTATION_AUTH_OK. Do not call tools."],
                                        cwd=profile, env=env, capture_output=True, text=True, timeout=30)
                assert result.returncode == 0, result.stderr[-2000:]
                assert "DICTATION_AUTH_OK" in result.stdout, "Missing mock response"
                requests = ResponsesHandler.requests
                assert requests, "No inference request received"
                for path, headers, body in requests:
                    assert path == "/v1/responses", path
                    headers = {key.lower(): value for key, value in headers.items()}
                    assert headers.get("authorization") == "Bearer " + LOCAL_KEY, "Wrong inference credential"
                    assert "chatgpt-account-id" not in headers, "ChatGPT account metadata reached provider"
                    assert token not in json.dumps(headers) and token.encode() not in body, "ChatGPT token reached provider"
                print(f"PASS dictation={enabled}: auth visibility correct; inference uses only the local provider key")
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=3)
    print("Not tested: actual sign-in, microphone UI, account entitlement, or live transcription.")


if __name__ == "__main__":
    main()
