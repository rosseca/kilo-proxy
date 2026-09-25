import {contextLimits,resolveContextPolicy} from './context-policy.mjs';
import {claudeLaunch} from './claude-helper.mjs';
import {validModelID} from './model-helper.mjs';
export function clientConfig({client, baseURL, zedBaseURL, key, model, contextWindow=272000, language='es', catalogPath='', selectedModels=[], aliases={}, queueMode='queue'}) {
 if (client === 'codex' || client === 'codex-cli') return `# ~/.codex-kilo-${client === 'codex' ? 'desktop' : 'cli'}/config.toml · ${language === 'en' ? 'save in this isolated profile' : 'guardar en este perfil independiente'}\nmodel = ${JSON.stringify(model)}${catalogPath ? '\nmodel_catalog_json = ' + JSON.stringify(catalogPath) : ''}\nmodel_provider = "kilo-local"\ncli_auth_credentials_store = "file"${client === 'codex' ? `\n\n[desktop]\nfollowUpQueueMode = ${JSON.stringify(queueMode === 'steer' ? 'steer' : 'queue')}` : ''}\n\n[model_providers.kilo-local]\nname = "Kilo Proxy"\nbase_url = ${JSON.stringify(baseURL)}\n# ${language === 'en' ? 'Keep this variable name unchanged. The launch command supplies the local key.' : 'Conserva este nombre de variable. El comando de arranque carga la clave local.'}\nenv_key = "KILO_LOCAL_API_KEY"\nenv_key_instructions = ${JSON.stringify(language === 'en' ? 'Close the Kilo instance and launch it with the command from the Kilo Proxy Codex helper.' : 'Cierra la instancia Kilo y ábrela con el comando del helper de Codex en Kilo Proxy.')}\nwire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false`;
 const selected = [...new Map(selectedModels.filter(m => m && validModelID(m.id)).map(m => [m.id,m])).values()];
 if (!selected.length && validModelID(model)) selected.push({id:model,name:model});
 for (let i=0;i<selected.length;i++) {const m=selected[i];const limits=m.contextPreset ? contextLimits(m) : resolveContextPolicy('custom',Number.isSafeInteger(m.contextWindow)&&m.contextWindow>=1024?m.contextWindow:contextWindow,0,m.maxOutputTokens);selected[i]={...m,contextWindow:limits.contextWindow,maxOutputTokens:limits.maxOutputTokens};}
 const initial = selected.some(m=>m.id===model) ? model : selected[0]?.id || '';
 const alias = name => selected.some(m=>m.id===aliases[name]) ? aliases[name] : initial;
 if (client === 'claude') return JSON.stringify({env:{
  ANTHROPIC_BASE_URL:baseURL.replace(/\/v1\/?$/, ''),
  ANTHROPIC_AUTH_TOKEN:key,
  ANTHROPIC_MODEL:initial,
  ANTHROPIC_DEFAULT_SONNET_MODEL:alias('sonnet'),
  ANTHROPIC_DEFAULT_OPUS_MODEL:alias('opus'),
  ANTHROPIC_DEFAULT_HAIKU_MODEL:alias('haiku')
 }},null,2);
 if (client === 'zed') return JSON.stringify({agent:{default_model:{provider:'kilo-local',model:initial}},language_models:{openai_compatible:{'kilo-local':{api_url:zedBaseURL||baseURL,available_models:selected.map(m=>({name:m.id,display_name:m.name||m.id,max_tokens:m.contextWindow||contextWindow,...(m.maxOutputTokens>0?{max_output_tokens:m.maxOutputTokens}:{})}))}}}},null,2);
 if (client === 'opencode') return JSON.stringify({$schema:'https://opencode.ai/config.json',compaction:{auto:true},model:'kilo-local/'+initial,provider:{'kilo-local':{npm:'@ai-sdk/openai-compatible',name:'Kilo Proxy',options:{baseURL,...(key?{apiKey:key}:{})},models:Object.fromEntries(selected.map(m=>[m.id,{name:m.name || m.id,...(Number.isSafeInteger(m.contextWindow) && m.contextWindow>0 ? {limit:{context:m.contextWindow,output:m.maxOutputTokens}} : {})}]))}}},null,2);
 return `Base URL  ${baseURL}\nAPI key   ${key}\n${language === 'en' ? 'Model' : 'Modelo'}    ${model}`;
}
const shQuote = value => "'" + value.replaceAll("'", "'\\''") + "'";
const psQuote = value => "'" + value.replaceAll("'", "''") + "'";
export function launchCommand({client,key,shell='unix',platform='macos',appPath='',language='es', catalog=false}) {
 if(client==='claude') return claudeLaunch(shell,language);
 if(!['codex','codex-cli'].includes(client)) return '';
 const desktop = client === 'codex';
 const catalogCheck = catalog;
 const saveCatalog = language === 'en' ? 'Save models.json in the Kilo profile first.' : 'Guarda primero models.json en el perfil de Kilo.';
 const profile = desktop ? '.codex-kilo-desktop' : '.codex-kilo-cli';
 const saveFirst = language === 'en' ? 'Save the configuration to this profile first:' : 'Guarda primero la configuración en este perfil:';
 if(desktop && !appPath.trim()) return language === 'en' ? 'Enter the absolute path to the desktop application above.' : 'Introduce arriba la ruta absoluta de la aplicación de escritorio.';
 if(desktop ? platform === 'windows' : shell === 'powershell') {
  const names = desktop ? ['CODEX_HOME','KILO_LOCAL_API_KEY','CODEX_ELECTRON_USER_DATA_PATH'] : ['CODEX_HOME','KILO_LOCAL_API_KEY'];
  return `& {
  $kiloHome = Join-Path $env:USERPROFILE '${profile}'
  if (!(Test-Path (Join-Path $kiloHome 'config.toml'))) { throw (${psQuote(saveFirst+' ')} + $kiloHome + '\\config.toml') }
${catalogCheck ? `  if (!(Test-Path (Join-Path $kiloHome 'models.json'))) { throw ${psQuote(saveCatalog)} }\n` : ''}  $kiloPrevious = @{}
  $kiloNames = @(${names.map(psQuote).join(', ')})
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    $env:CODEX_HOME = $kiloHome
    $env:KILO_LOCAL_API_KEY = ${psQuote(key)}${desktop ? `
    $kiloUI = Join-Path $env:LOCALAPPDATA 'Codex Kilo'
    New-Item -ItemType Directory -Force -Path $kiloUI | Out-Null
    $env:CODEX_ELECTRON_USER_DATA_PATH = $kiloUI
    Start-Process -FilePath ${psQuote(appPath)} -ArgumentList @('--user-data-dir="' + $kiloUI + '"')` : '\n    codex'}
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
 }
 let intro = `(\n  kilo_home="$HOME/${profile}"\n  if [ ! -f "$kilo_home/config.toml" ]; then\n    printf '%s %s\\n' ${shQuote(saveFirst)} "$kilo_home/config.toml" >&2\n    exit 1\n  fi`;
 if(catalogCheck) intro += `
  if [ ! -f "$kilo_home/models.json" ]; then
    printf '%s\\n' ${shQuote(saveCatalog)} >&2
    exit 1
  fi`;
 if(!desktop) return `${intro}
  env CODEX_HOME="$kilo_home" KILO_LOCAL_API_KEY=${shQuote(key)} codex
)`;
 if(platform==='macos') return `${intro}
  kilo_ui="$HOME/Library/Application Support/Codex Kilo"
  mkdir -p "$kilo_ui" || exit 1
  open -n --env "CODEX_HOME=$kilo_home" \\
    --env "CODEX_ELECTRON_USER_DATA_PATH=$kilo_ui" \\
    --env ${shQuote('KILO_LOCAL_API_KEY='+key)} \\
    ${shQuote(appPath)} --args "--user-data-dir=$kilo_ui"
)`;
 return `${intro}
  kilo_ui="\${XDG_CONFIG_HOME:-$HOME/.config}/codex-kilo-desktop"
  mkdir -p "$kilo_ui" || exit 1
  env CODEX_HOME="$kilo_home" CODEX_ELECTRON_USER_DATA_PATH="$kilo_ui" \\
    KILO_LOCAL_API_KEY=${shQuote(key)} \\
    ${shQuote(appPath)} "--user-data-dir=$kilo_ui"
)`;
}
