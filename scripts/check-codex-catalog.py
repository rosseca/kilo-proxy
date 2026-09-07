#!/usr/bin/env python3
"""Read-only model/list compatibility test against an installed Codex binary.
Uses an isolated temporary profile and a non-listening localhost provider. No inference.
Usage: python3 scripts/check-codex-catalog.py /absolute/path/to/codex
"""
import json,os,selectors,subprocess,sys,tempfile,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix='kilo-catalog-test-') as directory:
    profile=Path(directory)
    generated=subprocess.check_output(['node','--input-type=module','-e', """
import {codexCatalog} from './ui/codex-catalog.mjs';
import {clientConfig} from './ui/client-config.mjs';
console.log(JSON.stringify({catalog:codexCatalog([{id:'openai/gpt-5.6-sol-discounted',name:'Long original Sol name',displayName:'Sol',contextWindow:1050000},{id:'z-ai/glm-5.3',name:'Long original GLM name',displayName:'GLM',contextWindow:1048576}],'z-ai/glm-5.3'),config:clientConfig({client:'codex',baseURL:'http://127.0.0.1:1/v1',key:'unused',model:'z-ai/glm-5.3',catalogPath:'models.json'})}));
"""],cwd=ROOT,text=True)
    data=json.loads(generated)
    (profile/'models.json').write_text(json.dumps(data['catalog']))
    (profile/'config.toml').write_text(data['config'])
    process=subprocess.Popen([sys.argv[1],'app-server','--stdio'],cwd=ROOT,env={**os.environ,'CODEX_HOME':directory,'KILO_LOCAL_API_KEY':'unused-local-test'},stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    def send(data):
        process.stdin.write(json.dumps(data)+'\n');process.stdin.flush()
    try:
        send({'id':1,'method':'initialize','params':{'clientInfo':{'name':'kilo_catalog_test','version':'1'},'capabilities':{'experimentalApi':True}}})
        selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ);selector.register(process.stderr,selectors.EVENT_READ)
        deadline=time.monotonic()+15;result=None
        while time.monotonic()<deadline and result is None:
            for key,_ in selector.select(timeout=.2):
                line=key.fileobj.readline()
                if not line:selector.unregister(key.fileobj);continue
                if 'Invalid configuration' in line:raise AssertionError(line)
                if key.fileobj!=process.stdout:continue
                message=json.loads(line)
                if message.get('method')=='configWarning':raise AssertionError(message)
                if message.get('id')==1:
                    assert 'result' in message,message
                    send({'method':'initialized'})
                    send({'id':2,'method':'model/list','params':{'includeHidden':False}})
                if message.get('id')==2:result=message
            if process.poll() is not None:break
        assert result is not None,'model/list timed out'
        models=result['result']['data']
        assert {m['model'] for m in models}=={'openai/gpt-5.6-sol-discounted','z-ai/glm-5.3'},result
        assert next(m for m in models if m['isDefault'])['model']=='z-ai/glm-5.3',result
        chosen=next(m for m in models if m['model']=='z-ai/glm-5.3')
        assert chosen['defaultReasoningEffort']=='max',chosen
        assert [r['reasoningEffort'] for r in chosen['supportedReasoningEfforts']]==['low','high','max'],chosen
        sol=next(m for m in models if m['model']=='openai/gpt-5.6-sol-discounted')
        assert [r['reasoningEffort'] for r in sol['supportedReasoningEfforts']]==['none','low','medium','high','xhigh','max'],sol
        assert sol['defaultReasoningEffort']=='low',sol
        assert sol['displayName']=='Sol',sol
        assert chosen['displayName']=='GLM',chosen
        print('PASS: custom display names, exact model IDs, native reasoning levels and default verified;  installed Codex returns both catalog models and the chosen default; relative catalog path resolved from isolated CODEX_HOME.')
    finally:
        process.terminate()
        try:process.wait(timeout=3)
        except subprocess.TimeoutExpired:process.kill();process.wait()
