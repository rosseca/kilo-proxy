import test from 'node:test';
import assert from 'node:assert/strict';
import {imageGenerationModels,imageGenerationSelection,imageGenerationValid,codexImageMCPConfig} from '../ui/model-helper.mjs';

const catalog=[
  {id:'vendor/vision',name:'Vision input',inputModalities:['text','image'],outputModalities:['text'],tools:true},
  {id:'vendor/z',name:'Zulu image',outputModalities:['image'],tools:false},
  {id:'vendor/a',name:'Alpha image',outputModalities:['text','image']},
  {id:'vendor/unknown',name:'Unknown'}
];

test('image models require image output independently from coding tools and do not mutate the catalog',()=>{
  const before=structuredClone(catalog);
  assert.deepEqual(imageGenerationModels(catalog).map(model=>model.id),['vendor/a','vendor/z']);
  assert.deepEqual(catalog,before);
});

test('image drafts clone saved values, wait for initial state, and can disable a corrupt saved ID',()=>{
  for(const value of [null,undefined,{},true,{enabled:'false',model:''},{enabled:false}])assert.equal(imageGenerationSelection(value),null);
  const saved={enabled:true,model:'vendor/z'},draft=imageGenerationSelection(saved);
  draft.model='vendor/a';assert.equal(saved.model,'vendor/z');
  assert.deepEqual(imageGenerationSelection({enabled:false,model:'bad id'}),{enabled:false,model:''});
  assert.deepEqual(imageGenerationSelection({enabled:false,model:'vendor/missing'}),{enabled:false,model:'vendor/missing'});
  assert.deepEqual(imageGenerationSelection({enabled:true,model:'bad id'}),{enabled:true,model:'bad id'});
});

test('enabled image tools need an available image-output model, while disabled tools can be prepared',()=>{
  for(const model of ['', 'bad id', 'vendor/vision', 'vendor/missing', '../model', 'a'.repeat(201)])assert.equal(imageGenerationValid({enabled:true,model},catalog),false,model);
  for(const model of ['vendor/a','vendor/z'])assert.equal(imageGenerationValid({enabled:true,model},catalog),true);
  assert.equal(imageGenerationValid({enabled:false,model:'vendor/missing'},catalog),true);
  assert.equal(imageGenerationValid(null,catalog),true);
});

test('Codex config exports use the local image MCP and an environment reference without embedding credentials or image IDs',()=>{
  const base='model = "vendor/code"\n',images={enabled:true,model:'vendor/z'};
  const actual=codexImageMCPConfig(base,images,'http://127.0.0.1:8877/v1');
  assert.equal(actual,base+'\n[mcp_servers.kilo_images]\nurl = "http://127.0.0.1:8877/mcp/images"\nbearer_token_env_var = "KILO_LOCAL_API_KEY"\nstartup_timeout_sec = 15\ntool_timeout_sec = 360\n');
  assert.ok(!actual.includes(images.model));
  assert.equal(codexImageMCPConfig(base,{enabled:false,model:''},'invalid'),base);
  for(const url of ['https://example.test:8877/v1','http://localhost:8877/v1','http://127.0.0.1/v1','http://127.0.0.1:99/v1','http://secret@127.0.0.1:8877/v1','file:///tmp/profile'])assert.throws(()=>codexImageMCPConfig(base,images,url));
});
