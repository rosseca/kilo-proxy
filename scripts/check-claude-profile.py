#!/usr/bin/env python3
"""Verify the installed Claude Code against a disposable local Messages fixture.
Usage: python3 scripts/check-claude-profile.py /absolute/path/to/claude
Requires Claude Code 2.1.251+. No Kilo credentials or paid inference.
"""
import json,os,subprocess,tempfile,threading,sys
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
BINARY=sys.argv[1] if len(sys.argv)>1 else 'claude'
requests=[]
class Gateway(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_HEAD(self):self.send_response(200);self.end_headers()
 def do_GET(self):
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(b'{"data":[]}')
 def do_POST(self):
  data=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))));requests.append({'path':self.path,'headers':dict(self.headers),'body':data})
  if 'count_tokens' in self.path:
   self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(b'{"input_tokens":10}');return
  message={'id':'msg_local_fixture','type':'message','role':'assistant','model':data.get('model'),'content':[{'type':'text','text':'Local fixture OK'}],'stop_reason':'end_turn','stop_sequence':None,'usage':{'input_tokens':10,'output_tokens':4}}
  if data.get('stream'):
   self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
   for event,body in [
    ('message_start',{'type':'message_start','message':{**message,'content':[],'stop_reason':None,'usage':{'input_tokens':10,'output_tokens':0}}}),
    ('content_block_start',{'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}}),
    ('content_block_delta',{'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'Local fixture OK'}}),
    ('content_block_stop',{'type':'content_block_stop','index':0}),
    ('message_delta',{'type':'message_delta','delta':{'stop_reason':'end_turn','stop_sequence':None},'usage':{'output_tokens':4}}),
    ('message_stop',{'type':'message_stop'})]:
     self.wfile.write(('event: '+event+'\ndata: '+json.dumps(body)+'\n\n').encode());self.wfile.flush()
  else:
   self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps(message).encode())
server=ThreadingHTTPServer(('127.0.0.1',0),Gateway);threading.Thread(target=server.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='claude-runtime-') as directory:
 profile=Path(directory);settings=json.loads(subprocess.check_output(['node','--input-type=module','-e',"import {claudeSelection,claudeSettings,claudeCapabilities} from './ui/claude-helper.mjs';console.log(JSON.stringify(claudeSettings(claudeSelection([{id:'anthropic/claude-fable-5.1',displayName:'Fable',effort:'low'},{id:'anthropic/claude-opus-4.6',displayName:'Opus',effort:'medium'}],'anthropic/claude-fable-5.1',{},'modern'),claudeCapabilities('2.1.263'),'http://127.0.0.1:1','unused')));"],cwd=ROOT,text=True))
 settings['env']['ANTHROPIC_BASE_URL']='http://127.0.0.1:'+str(server.server_port)
 settings['env']['ANTHROPIC_AUTH_TOKEN']='local-runtime-fixture'
 (profile/'settings.json').write_text(json.dumps(settings))
 env={k:v for k,v in os.environ.items() if not k.startswith(('ANTHROPIC_','CLAUDE_'))}
 env.update(CLAUDE_CONFIG_DIR=str(profile),CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',DISABLE_AUTOUPDATER='1')
 result=subprocess.run([BINARY,'--settings',str(profile/'settings.json'),'--print','--output-format','json','--tools','','--max-turns','1','--no-session-persistence','Reply OK for a local mock test.'],cwd=directory,env=env,capture_output=True,text=True,timeout=45)
 print('exit:',result.returncode)
 print('stderr:',result.stderr[:500])
 try:out=json.loads(result.stdout);print('result:',out.get('result'),out.get('is_error'))
 except Exception:print(result.stdout[:500])
 messages=[r for r in requests if r['path'].split('?')[0]=='/v1/messages']
 for r in messages:
  print(json.dumps({'model':r['body'].get('model'),'effort':r['body'].get('output_config'),'thinking':r['body'].get('thinking'),'sessionHeader':next((v for k,v in r['headers'].items() if k.lower()=='x-claude-code-session-id'),None)}))
 assert result.returncode==0,result.stderr
 assert messages,'Claude did not reach mock gateway'
 assert any(r['body'].get('model')=='anthropic/claude-fable-5.1' for r in messages)
 assert all(next((v for k,v in r['headers'].items() if k.lower()=='authorization'),None)=='Bearer local-runtime-fixture' for r in messages)
 assert any(r['body'].get('output_config',{}).get('effort')=='low' for r in messages),'Selected per-model effort was not applied'
 print('PASS: actual Claude Code loaded the prepared profile, used the exact Kilo ID, local bearer credential and low effort against a local fake gateway. No paid inference.')
server.shutdown()
