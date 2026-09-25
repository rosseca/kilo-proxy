const otherModel = /ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk/i;
// Unlike $, this end assertion also rejects a final newline in JavaScript.
const realModelID = /^[A-Za-z0-9~][A-Za-z0-9._:/~+-]{0,199}(?![\s\S])/;
const reservedModelID = /^(?:anthropic\/)?claude-kilo-v1-/;

// Claude Desktop validates model families before sending inference requests.
// Filtering a client selection must never rewrite the shared model library.
export function claudeDesktopModelSupported(id) {
 return typeof id==='string' && realModelID.test(id) && !reservedModelID.test(id) && /^(?:anthropic\/)?claude-[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(id) && !otherModel.test(id);
}

export function claudeDesktopModelAllowed(id, experimental=false) {
 return experimental?typeof id==='string'&&realModelID.test(id)&&!reservedModelID.test(id):claudeDesktopModelSupported(id);
}

export function claudeDesktopSelection(models, initial, experimental=false) {
 const compatible=models.filter(model=>claudeDesktopModelAllowed(model.id,experimental));
 const fallback=!!initial&&!compatible.some(model=>model.id===initial);
 return {
  selection:{models:compatible.map(model=>({id:model.id,name:Array.from(model.displayName||model.name||model.id).slice(0,80).join('')})),initial:fallback?compatible[0]?.id||'':initial||compatible[0]?.id||''},
  omitted:models.length-compatible.length,
  fallback,
 };
}
