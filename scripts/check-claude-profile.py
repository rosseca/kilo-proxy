#!/usr/bin/env python3
"""Check real Claude Code against disposable local Messages fixtures.
Usage: python3 scripts/check-claude-profile.py /path/to/claude [--report /path/report.json]
Requires --bare support (validated with Claude Code 2.1.288). No real credentials,
large prompt, public gateway or paid inference. macOS child traffic is loopback-only.
"""
import argparse, json, os, re, signal, subprocess, tempfile, threading, time, shutil, sys
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary', nargs='?', default='claude')
parser.add_argument('--report', type=Path)
args = parser.parse_args()
BINARY = shutil.which(args.binary)
if not BINARY:
    parser.error('Claude Code executable not found')
version = subprocess.check_output([BINARY, '--version'], text=True, timeout=10).strip().split(' ', 1)[0]
if '--bare' not in subprocess.check_output([BINARY, '--help'], text=True, timeout=10):
    parser.error('This fixture requires --bare support to avoid real authentication')


def run_client(command, profile, env):
    # Reap our own client before removing its temporary HOME, including on timeout.
    process = subprocess.Popen(command, cwd=profile, env=env, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, text=True, start_new_session=os.name != 'nt')
    try:
        stdout, stderr = process.communicate(timeout=45)
        return process.returncode, stdout, stderr
    except subprocess.TimeoutExpired:
        # communicate can time out on a pipe still held by an own descendant
        # even after the leader exits; kill this unreaped process group first.
        if os.name == 'nt':
            process.kill()
        else:
            os.killpg(process.pid, signal.SIGKILL)
        process.communicate(timeout=10)
        raise
    finally:
        if process.poll() is None:
            if os.name == 'nt':
                process.kill()
            else:
                os.killpg(process.pid, signal.SIGKILL)
            process.communicate(timeout=10)
requests = []

class Gateway(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_HEAD(self):
        self.send_response(200); self.end_headers()
    def do_GET(self):
        self.send_response(200); self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(b'{"data":[]}')
    def do_POST(self):
        data=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))))
        requests.append({'path':self.path,'headers':dict(self.headers),'body':data})
        if 'count_tokens' in self.path:
            self.send_response(200); self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(b'{"input_tokens":10}'); return
        message={'id':'msg_local_fixture','type':'message','role':'assistant','model':data.get('model'),'content':[{'type':'text','text':'Local fixture OK'}],'stop_reason':'end_turn','stop_sequence':None,'usage':{'input_tokens':10,'output_tokens':4}}
        self.send_response(200)
        if data.get('stream'):
            self.send_header('Content-Type','text/event-stream'); self.end_headers()
            for event,body in [
                ('message_start',{'type':'message_start','message':{**message,'content':[],'stop_reason':None,'usage':{'input_tokens':10,'output_tokens':0}}}),
                ('content_block_start',{'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}}),
                ('content_block_delta',{'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'Local fixture OK'}}),
                ('content_block_stop',{'type':'content_block_stop','index':0}),
                ('message_delta',{'type':'message_delta','delta':{'stop_reason':'end_turn','stop_sequence':None},'usage':{'output_tokens':4}}),
                ('message_stop',{'type':'message_stop'})]:
                self.wfile.write(('event: '+event+'\ndata: '+json.dumps(body)+'\n\n').encode()); self.wfile.flush()
        else:
            self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(json.dumps(message).encode())

server=ThreadingHTTPServer(('127.0.0.1',0),Gateway)
server_thread=threading.Thread(target=server.serve_forever,daemon=True)
server_thread.start()
scenarios=[
    {'name':'unknown-untagged-control','models':[{'id':'vendor/test-context-large','contextWindow':200000}],'expected':'vendor/test-context-large','budget':200000,'ceiling':200000},
    {'name':'unknown-recommended-tagged','models':[{'id':'vendor/test-context-large','contextWindow':1048576}],'expected':'vendor/test-context-large','budget':272000,'ceiling':1000000},
    {'name':'picker-maximum-tagged','models':[{'id':'anthropic/claude-fable-5.1','displayName':'Fable','effort':'low','contextWindow':1048576,'contextPreset':'maximum'}],'expected':'anthropic/claude-fable-5.1','budget':1000000,'ceiling':1000000},
    {'name':'picker-low-tagged','models':[{'id':'anthropic/claude-fable-5.1','displayName':'Fable','effort':'low','contextWindow':1048576,'contextPreset':'low'}],'expected':'anthropic/claude-fable-5.1','budget':128000,'ceiling':1000000},
    {'name':'mixed-minimum-tagged','models':[{'id':'anthropic/claude-fable-5.1','displayName':'Fable','effort':'low','contextWindow':1048576,'contextPreset':'maximum'},{'id':'anthropic/claude-opus-4.6','contextWindow':200000}],'expected':'anthropic/claude-fable-5.1','budget':200000,'ceiling':1000000},
]
summary=[]
profiles_root=Path(tempfile.mkdtemp(prefix='claude-runtime-'))
try:
    for scenario in scenarios:
        profile=profiles_root/scenario['name']; profile.mkdir()
        expression="import {claudeSelection,claudeSettings,claudeCapabilities} from './ui/claude-helper.mjs'; const models="+json.dumps(scenario['models'])+"; console.log(JSON.stringify(claudeSettings(claudeSelection(models,models[0].id,{},'modern'),claudeCapabilities("+json.dumps(version)+"),'http://127.0.0.1:"+str(server.server_port)+"','local-runtime-fixture')));"
        settings=json.loads(subprocess.check_output(['node','--input-type=module','-e',expression],cwd=ROOT,text=True,timeout=10))
        (profile/'settings.json').write_text(json.dumps(settings,indent=2))
        assert settings['autoCompactWindow']==scenario['budget']
        assert settings['env']['CLAUDE_CODE_AUTO_COMPACT_WINDOW']==str(scenario['budget'])
        env={k:v for k,v in os.environ.items() if not k.startswith(('ANTHROPIC_','CLAUDE_','CODEX_'))}
        env.update(HOME=str(profile),XDG_CONFIG_HOME=str(profile/'config'),XDG_CACHE_HOME=str(profile/'cache'),XDG_DATA_HOME=str(profile/'data'),CLAUDE_CONFIG_DIR=str(profile/'claude'),CLAUDE_SECURESTORAGE_CONFIG_DIR=str(profile/'secure'),CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',DISABLE_AUTOUPDATER='1',ANTHROPIC_API_KEY='local-runtime-fixture',ANTHROPIC_BASE_URL='http://127.0.0.1:'+str(server.server_port))
        request_start=len(requests)
        started=time.monotonic()
        command=[BINARY,'--bare','--settings',str(profile/'settings.json'),'--print','--output-format','json','--tools','','--max-turns','1','--no-session-persistence','--debug-file',str(profile/'debug.log'),'Reply OK for a local mock test.']
        loopback_sandbox=sys.platform=='darwin' and Path('/usr/bin/sandbox-exec').is_file()
        if loopback_sandbox:
            policy='(version 1) (allow default) (deny network*) (allow network-outbound (remote ip "localhost:*"))'
            command=['/usr/bin/sandbox-exec','-p',policy]+command
        code,stdout,stderr=run_client(command,profile,env)
        (profile/'stdout.json').write_text(stdout); (profile/'stderr.log').write_text(stderr)
        assert code==0,stderr
        messages=[r for r in requests[request_start:] if r['path'].split('?')[0]=='/v1/messages']
        (profile/'requests.json').write_text(json.dumps(requests[request_start:],indent=2))
        out=json.loads(stdout)
        # Read the ceiling and effective compaction window from the installed client,
        # rather than treating generated settings as evidence that Claude consumed them.
        usage=out.get('modelUsage',{}).get(settings['model'],{})
        windows=[int(n) for n in re.findall(r'autocompact:[^\n]*effectiveWindow=(\d+)',(profile/'debug.log').read_text())]
        assert usage.get('contextWindow')==scenario['ceiling'],out.get('modelUsage')
        assert windows,'Claude did not report an effective auto-compaction window'
        reserved=int(settings['env']['CLAUDE_CODE_MAX_OUTPUT_TOKENS'])
        assert all(scenario['budget']-reserved<=window<=scenario['budget'] for window in windows),(scenario,windows)
        tagged=scenario['ceiling']==1000000
        assert settings['model'].endswith('[1m]')==tagged,settings
        assert all(not key.endswith('[1m]') for key in settings.get('modelOverrides',{})),settings
        row={'scenario':scenario['name'],'elapsed':round(time.monotonic()-started,3),'exit':code,'is_error':out.get('is_error'),'settings_model':settings['model'],'budget':settings['autoCompactWindow'],'overrides':settings.get('modelOverrides'),'requests':[{'path':r['path'],'model':r['body'].get('model'),'effort':r['body'].get('output_config'),'beta':next((v for k,v in r['headers'].items() if k.lower()=='anthropic-beta'),None)} for r in messages],'contextCeiling':usage['contextWindow'],'effectiveCompactionWindows':windows,'loopbackSandbox':loopback_sandbox}
        assert out.get('is_error') is False,out
        assert messages,'Claude did not reach local gateway'
        assert all(r['body'].get('model')==scenario['expected'] for r in messages),row
        assert all(next((v for k,v in r['headers'].items() if k.lower()=='authorization'),None)=='Bearer local-runtime-fixture' for r in messages),row
        assert all(('context-1m-' in (next((v for k,v in r['headers'].items() if k.lower()=='anthropic-beta'),None) or ''))==tagged for r in messages),row
        if scenario['models'][0].get('effort'):
            assert all(r['body'].get('output_config',{}).get('effort')=='low' for r in messages),row
        summary.append(row); print(json.dumps(row),flush=True)
finally:
    server.shutdown(); server.server_close()
    server_thread.join(timeout=5)
    shutil.rmtree(profiles_root)
    if args.report:
        args.report.write_text(json.dumps({'claudeVersion':version,'complete':len(summary)==len(scenarios),'scenarios':summary,'limits':'Local synthetic replies only; no large prompt, public gateway or paid inference.'},indent=2)+'\n')
print('PASS: real Claude Code accepted [1m], resolved exact IDs, reported the 1M ceiling and retained smaller compaction budgets. Temporary profiles removed; no paid inference.')
