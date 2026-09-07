import {validModelID} from './model-helper.mjs';
export function claudeCapabilities(version='') {
 const match=String(version).match(/^(\d+)\.(\d+)\.(\d+)(?:\s|$)/);
 const parts=match?.slice(1,4).map(Number);
 const atLeast=patch=>!!parts && (parts[0]>2 || parts[0]===2 && (parts[1]>1 || parts[1]===1 && parts[2]>=patch));
 return {version:match?.slice(1,4).join('.') || '',picker:atLeast(242),perModelEffort:atLeast(251)};
}
export function claudeEffortKey(id) {
 const match=String(id).match(/^(?:anthropic\/)?(claude-(?:fable-5(?:[.-]1)?|opus-(?:5|4[.-][678])|sonnet-(?:5|4[.-]6)))(?:-\d{8})?$/i);
 return match?.[1].toLowerCase().replaceAll('.','-') || '';
}
export function claudeEfforts(id,caps={}) {
 const key=claudeEffortKey(id);
 if(!key)return [];
 return ['low','medium','high',...(caps.perModelEffort && !['claude-opus-4-6','claude-sonnet-4-6'].includes(key) ? ['xhigh'] : [])];
}
export function claudeSelection(models,initial,aliases={},mode='installed') {
 const selected=[...new Map(models.filter(m=>m && validModelID(m.id)).map(m=>[m.id,m])).values()].slice(0,50);
 const ids=new Set(selected.map(m=>m.id));
 return {models:selected.map(m=>({id:m.id,displayName:[...(m.displayName || m.name || m.id).replace(/[\x00-\x1f\x7f]/g,'')].slice(0,80).join(''),...(m.effort ? {effort:m.effort} : {})})),initial:ids.has(initial)?initial:selected[0]?.id || '',aliases:Object.fromEntries(['sonnet','opus','haiku'].map(a=>[a,ids.has(aliases[a])?aliases[a]:''])),mode};
}
export function claudeSettings(selection,caps,baseURL,key) {
 const {models,initial,aliases}=selection;
 const nativeID=id=>caps.picker ? claudeEffortKey(id) || id : id;
 const env={ANTHROPIC_BASE_URL:baseURL.replace(/\/v1\/?$/,''),ANTHROPIC_AUTH_TOKEN:key,ANTHROPIC_MODEL:nativeID(initial)};
 for(const alias of ['sonnet','opus','haiku']){
  const id=aliases[alias] || initial,prefix='ANTHROPIC_DEFAULT_'+alias.toUpperCase()+'_MODEL';
  env[prefix]=nativeID(id);
  if(caps.picker)env[prefix+'_NAME']=models.find(m=>m.id===id)?.displayName || id;
 }
 const settings={env,model:nativeID(initial)};
 if(caps.picker){env.ANTHROPIC_DEFAULT_FABLE_MODEL=nativeID(initial);env.ANTHROPIC_DEFAULT_FABLE_MODEL_NAME=models.find(m=>m.id===initial)?.displayName || initial;}
 if(caps.picker)settings.modelPicker={options:models.map(m=>({model:nativeID(m.id),label:m.displayName || m.id})),replaceBuiltInOptions:true};
 if(caps.picker){const overrides=Object.fromEntries(models.filter(m=>nativeID(m.id)!==m.id).map(m=>[nativeID(m.id),m.id]));if(Object.keys(overrides).length)settings.modelOverrides=overrides;}
 if(caps.perModelEffort)settings.modelSettings=Object.fromEntries(models.filter(m=>m.effort).map(m=>[claudeEffortKey(m.id),{effortLevel:m.effort}]));
 else {
  const effort=models.find(m=>m.id===initial)?.effort;
  if(effort && effort!=='xhigh')settings.effortLevel=effort;
 }
 return settings;
}
const shQuote=value=>"'"+value.replaceAll("'","'\\''")+"'";
const psQuote=value=>"'"+value.replaceAll("'","''")+"'";
export const claudeResetEnv=['ANTHROPIC_API_KEY','ANTHROPIC_AUTH_TOKEN','ANTHROPIC_BASE_URL','ANTHROPIC_MODEL','CLAUDE_CODE_OAUTH_TOKEN','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_EFFORT_LEVEL','ANTHROPIC_CUSTOM_HEADERS','CLAUDE_CODE_SUBAGENT_MODEL','ANTHROPIC_SMALL_FAST_MODEL','CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY',...['SONNET','OPUS','HAIKU','FABLE'].flatMap(a=>['','_NAME','_DESCRIPTION','_SUPPORTED_CAPABILITIES'].map(s=>'ANTHROPIC_DEFAULT_'+a+'_MODEL'+s))];
export function claudeLaunch(shell='unix',language='en') {
 const message=language==='en'?'Prepare Claude Code in Kilo Local first.':'Prepara Claude Code desde Kilo Local primero.';
 if(shell==='powershell')return `& {
  $kiloHome = Join-Path $env:USERPROFILE '.claude-kilo'
  $kiloSettings = Join-Path $kiloHome 'settings.json'
  if (!(Test-Path -PathType Leaf $kiloSettings)) { throw ${psQuote(message)} }
  $kiloNames = @(${['CLAUDE_CONFIG_DIR',...claudeResetEnv].map(psQuote).join(', ')})
  $kiloPrevious = @{}
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    foreach ($kiloName in $kiloNames) { [Environment]::SetEnvironmentVariable($kiloName, $null, 'Process') }
    $env:CLAUDE_CONFIG_DIR = $kiloHome
    claude --settings $kiloSettings
  } finally {
    foreach ($kiloName in $kiloNames) { [Environment]::SetEnvironmentVariable($kiloName, $kiloPrevious[$kiloName], 'Process') }
  }
}`;
 return `(
  kilo_home="$HOME/.claude-kilo"
  if [ ! -f "$kilo_home/settings.json" ]; then
    printf '%s\\n' ${shQuote(message)} >&2
    exit 1
  fi
  unset ${claudeResetEnv.join(' ')}
  CLAUDE_CONFIG_DIR="$kilo_home" claude --settings "$kilo_home/settings.json"
)`;
}
