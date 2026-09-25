import {validModelID} from './model-helper.mjs';

const otherModel = /ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk/i;

// Claude Desktop validates model families before sending inference requests.
// Filtering a client selection must never rewrite the shared model library.
export function claudeDesktopModelSupported(id) {
 return validModelID(id) && id.length <= 200 && /^(?:anthropic\/)?claude-[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(id) && !otherModel.test(id);
}

export function claudeDesktopSelection(models, initial) {
 const compatible=models.filter(model=>claudeDesktopModelSupported(model.id));
 const fallback=!!initial&&!compatible.some(model=>model.id===initial);
 return {
  selection:{models:compatible.map(model=>({id:model.id,name:Array.from(model.displayName||model.name||model.id).slice(0,80).join('')})),initial:fallback?compatible[0]?.id||'':initial||compatible[0]?.id||''},
  omitted:models.length-compatible.length,
  fallback,
 };
}
