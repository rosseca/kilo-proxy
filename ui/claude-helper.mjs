import {contextLimits} from './context-policy.mjs';
import {validModelID} from './model-helper.mjs';
export function claudeCapabilities(version='') {
 const match=String(version).match(/^(\d+)\.(\d+)\.(\d+)(?:\s|$)/);
 const parts=match?.slice(1,4).map(Number);
 const atLeast=patch=>!!parts && (parts[0]>2 || parts[0]===2 && (parts[1]>1 || parts[1]===1 && parts[2]>=patch));
 return {version:match?.slice(1,4).join('.') || '',picker:atLeast(242),perModelEffort:atLeast(251),perModelEffort55:atLeast(267)};
}
const claude55ID=/^(?:anthropic\/)?claude-(?:opus|sonnet)-5[.-]5(?:-\d{8})?$/i;
const has55Defaults=caps=>caps.perModelEffort && claudeCapabilities(caps.version).perModelEffort55;
export function claudePickerKey(id) {
 const match=String(id).match(/^(?:anthropic\/)?(claude-(?:fable-5(?:[.-]1)?|opus-(?:5|4[.-][678])|sonnet-(?:5|4[.-]6)))(?:-\d{8})?$/i);
 return match?.[1].toLowerCase().replaceAll('.','-') || '';
}
export function claudeEffortKey(id) {
 const match=String(id).match(/^(?:anthropic\/)?(claude-(?:fable-5(?:[.-]1)?|opus-(?:5(?:[.-]5)?|4[.-][678])|sonnet-(?:5(?:[.-]5)?|4[.-]6)))(?:-\d{8})?$/i);
 const family=match?.[1].toLowerCase() || '';
 return family.endsWith('-5.5') ? family.slice(0,-2) : family.replaceAll('.','-');
}
export function claudeEfforts(id,caps={}) {
 const key=claudeEffortKey(id);
 if(!key || claude55ID.test(id) && !has55Defaults(caps))return [];
 return ['low','medium','high',...(caps.perModelEffort && !['claude-opus-4-6','claude-sonnet-4-6'].includes(key) ? ['xhigh'] : [])];
}
export function claudeSelection(models,initial,aliases={},mode='installed') {
 const selected=[...new Map(models.filter(m=>m && validModelID(m.id)).map(m=>[m.id,m])).values()].slice(0,50);
 const ids=new Set(selected.map(m=>m.id));
 return {models:selected.map(m=>({id:m.id,contextWindow:contextLimits(m).contextWindow,maxOutputTokens:contextLimits(m).maxOutputTokens,displayName:[...(m.displayName || m.name || m.id).replace(/[\x00-\x1f\x7f]/g,'')].slice(0,80).join(''),...(m.effort ? {effort:m.effort} : {})})),initial:ids.has(initial)?initial:selected[0]?.id || '',aliases:Object.fromEntries(['sonnet','opus','haiku'].map(a=>[a,ids.has(aliases[a])?aliases[a]:''])),mode};
}
export function claudeSettings(selection,caps,baseURL,key) {
 const {models,initial,aliases}=selection;
 const nativeID=id=>caps.picker ? claudePickerKey(id) || id : id;
 const efforts=new Map();
 for(const model of models){
  if(model.effort && claude55ID.test(model.id) && !has55Defaults(caps))throw new Error('Claude Code 2.1.267 or newer is required to apply saved per-model reasoning for Claude 5.5. Update Claude Code and prepare again.');
  const native=claudeEffortKey(model.id);
  if(native){
   if(efforts.has(native) && efforts.get(native)!==(model.effort || ''))throw new Error('Choose compatible reasoning defaults for gateway IDs that share a native Claude effort key');
   efforts.set(native,model.effort || '');
  }
 }
 const env={ANTHROPIC_BASE_URL:baseURL.replace(/\/v1\/?$/,''),ANTHROPIC_AUTH_TOKEN:key,ANTHROPIC_MODEL:nativeID(initial)};
 for(const alias of ['sonnet','opus','haiku']){
  const id=aliases[alias] || initial,prefix='ANTHROPIC_DEFAULT_'+alias.toUpperCase()+'_MODEL';
  env[prefix]=nativeID(id);
  if(caps.picker)env[prefix+'_NAME']=models.find(m=>m.id===id)?.displayName || id;
 }
 const windows=models.map(m=>m.contextWindow).filter(n=>Number.isSafeInteger(n)&&n>0),outputs=models.map(m=>m.maxOutputTokens).filter(n=>Number.isSafeInteger(n)&&n>0);
 const budget=Math.min(1000000,...windows);
 if(windows.length&&budget<100000)throw new Error('Claude Code requires a session context window of at least 100,000 tokens. Remove models with smaller windows or choose another agent.');
 const settings={env,model:nativeID(initial),...(windows.length?{autoCompactWindow:budget,autoCompactEnabled:true}:{})};
 if(windows.length)env.CLAUDE_CODE_AUTO_COMPACT_WINDOW=String(budget);
 if(outputs.length)env.CLAUDE_CODE_MAX_OUTPUT_TOKENS=String(Math.min(...outputs));
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
export const claudeResetEnv=['CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT','CLAUDE_CODE_DISABLE_1M_CONTEXT','CLAUDE_CODE_AUTO_COMPACT_WINDOW','CLAUDE_CODE_MAX_OUTPUT_TOKENS','CLAUDE_AUTOCOMPACT_PCT_OVERRIDE','DISABLE_AUTO_COMPACT','DISABLE_COMPACT','CLAUDE_CODE_MAX_CONTEXT_TOKENS','ANTHROPIC_API_KEY','ANTHROPIC_AUTH_TOKEN','ANTHROPIC_BASE_URL','ANTHROPIC_MODEL','CLAUDE_CODE_OAUTH_TOKEN','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_EFFORT_LEVEL','ANTHROPIC_CUSTOM_HEADERS','CLAUDE_CODE_SUBAGENT_MODEL','ANTHROPIC_SMALL_FAST_MODEL','CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY',...['SONNET','OPUS','HAIKU','FABLE'].flatMap(a=>['','_NAME','_DESCRIPTION','_SUPPORTED_CAPABILITIES'].map(s=>'ANTHROPIC_DEFAULT_'+a+'_MODEL'+s))];
export function claudeLaunch(shell='unix',language='en') {
 const message=language==='en'?'Prepare Claude Code in Kilo Proxy first.':'Prepara Claude Code desde Kilo Proxy primero.';
 if(shell==='powershell')return `& {
  $kiloHome = Join-Path $env:USERPROFILE '.claude-kilo'
  $kiloSettings = Join-Path $kiloHome 'settings.json'
  if (!(Test-Path -PathType Leaf $kiloSettings)) { throw ${psQuote(message)} }
  $kiloNames = @(${['CLAUDE_CONFIG_DIR',...claudeResetEnv].map(psQuote).join(', ')})
  $kiloPrevious = @{}
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    foreach ($kiloName in $kiloNames) { Remove-Item -LiteralPath "Env:$kiloName" -ErrorAction SilentlyContinue }
    $env:CLAUDE_CONFIG_DIR = $kiloHome
    claude --settings $kiloSettings
  } finally {
    foreach ($kiloName in $kiloNames) {
      if ($null -eq $kiloPrevious[$kiloName]) {
        Remove-Item -LiteralPath "Env:$kiloName" -ErrorAction SilentlyContinue
      } else {
        Set-Item -LiteralPath "Env:$kiloName" -Value $kiloPrevious[$kiloName]
      }
    }
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
