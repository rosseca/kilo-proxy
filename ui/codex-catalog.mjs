import {validModelID} from './model-helper.mjs';
// Conservative, original instructions for external models. Do not copy the
// installed application's model prompts or claim provider-specific capabilities.
const instructions = 'You are a coding assistant working with the user in a shared workspace. Follow the user instructions and applicable repository guidance. Inspect relevant files before making changes. Use the available tools to complete the requested work, preserve unrelated user changes, and verify changes with appropriate checks. Explain results and limitations clearly. Do not claim actions or tests you did not perform.';

export const reasoningLevels = ['none','minimal','low','medium','high','xhigh','max','ultra'];
// Provider documentation for Claude; capability metadata from installed Codex
// for exact OpenAI IDs. Gateway support must still be checked at request time.
const knownReasoning = {
  // Exact gateway route, including its published no-thinking option.
  "openai/gpt-5.6-sol-discounted": {levels:['none','low','medium','high','xhigh','max'],initial:'low'},
  // https://docs.z.ai/guides/overview/concept-param#reasoning_effort
  "z-ai/glm-5.3": {levels:['low','high','max'],initial:'max'},
  "z-ai/glm-5.3-flash": {levels:['low','high','max'],initial:'max'},
  "openai/gpt-6-astra": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra"
    ],
    "initial": "low"
  },
  "openai/gpt-5.6-sol": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra"
    ],
    "initial": "low"
  },
  "openai/gpt-5.6-terra": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra"
    ],
    "initial": "medium"
  },
  "openai/gpt-5.6-luna": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "medium"
  },
  "openai/gpt-daybreak-blue-latest": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra"
    ],
    "initial": "low"
  },
  "openai/gpt-daybreak-red-latest": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra"
    ],
    "initial": "medium"
  },
  "openai/gpt-5.5": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh"
    ],
    "initial": "medium"
  },
  "openai/gpt-5.4": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh"
    ],
    "initial": "medium"
  },
  "openai/gpt-5.4-mini": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh"
    ],
    "initial": "medium"
  },
  "openai/gpt-5.2": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh"
    ],
    "initial": "medium"
  },
  "anthropic/claude-fable-5.1": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-fable-5": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-mythos-5.1": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-mythos-5": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-opus-5": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-opus-4.8": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-opus-4.7": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-sonnet-5": {
    "levels": [
      "low",
      "medium",
      "high",
      "xhigh",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-opus-4.6": {
    "levels": [
      "low",
      "medium",
      "high",
      "max"
    ],
    "initial": "high"
  },
  "anthropic/claude-sonnet-4.6": {
    "levels": [
      "low",
      "medium",
      "high",
      "max"
    ],
    "initial": "high"
  }
};
export function reasoningFor(model) {
 const preset=knownReasoning[model.id?.replace(/^~/,'')];
 // Manual choices (including an explicit empty selection) always win.
 const custom=Array.isArray(model.reasoningLevels);
 const declared=custom ? model.reasoningLevels : model.reasoningEfforts;
 if(Array.isArray(declared) && (custom || declared.length)) {
  const levels=reasoningLevels.filter(level=>declared.includes(level));
  const preferred=custom ? model.defaultReasoning : preset?.initial;
  return {levels,initial:levels.includes(preferred) ? preferred : levels.includes('medium') ? 'medium' : levels.find(level=>!['none','minimal'].includes(level)) || levels[0] || null};
 }
 return preset || {levels:[],initial:null};
}

export function codexDisplayName(model) {
  const custom=typeof model.displayName==='string' ? model.displayName.replace(/[\u0000-\u001f\u007f]/g,' ').trim().slice(0,80) : '';
  return custom || model.name || model.id;
}

export function codexCatalog(models, defaultModel) {
  const ordered = [...models].sort((a,b) => Number(b.id === defaultModel) - Number(a.id === defaultModel));
  const seen = new Set();
  return {models:ordered.filter(m => validModelID(m.id) && !seen.has(m.id) && seen.add(m.id)).map((m,index) => ({
    slug:m.id, display_name:codexDisplayName(m), description:'Kilo Gateway · ' + m.id,
    default_reasoning_level:reasoningFor(m).initial, supported_reasoning_levels:reasoningFor(m).levels.map(effort=>({effort,description:'Reasoning effort: '+effort})),
    shell_type:'default', visibility:'list', supported_in_api:true, priority:index,
    base_instructions:instructions,
    supports_reasoning_summaries:false, supports_reasoning_summary_parameter:false,
    support_verbosity:false, prefer_websockets:false, use_responses_lite:false,
    supports_parallel_tool_calls:false, experimental_supported_tools:[],
    truncation_policy:{mode:'tokens',limit:10000},
    input_modalities:m.inputModalities?.includes('image') ? ['text','image'] : ['text'],
    ...(Number.isInteger(m.contextWindow) && m.contextWindow >= 1024 && m.contextWindow <= 100000000 ? {context_window:m.contextWindow} : {})
  }))};
}
