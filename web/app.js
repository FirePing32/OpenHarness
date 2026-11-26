// OpenHarness builder UI.
// Plain ES module, no dependencies. All state lives in `state`; every form
// control is bound to a dotted path inside `state.spec` (e.g. "model.baseUrl",
// "tools.httpTools.0.name") through the declarative `field()` helper.

'use strict';

/* ------------------------------------------------------------------------ */
/* Offline fallback catalog (mirrors spec/catalog.go + spec.Default())       */
/* ------------------------------------------------------------------------ */

const FALLBACK_DEFAULTS = {
  version: '1',
  name: 'my-agent',
  description: 'A custom agent built with OpenHarness',
  server: { host: '127.0.0.1', port: 8080, modelId: 'my-agent', authMode: 'none', apiKeyEnv: 'HARNESS_API_KEYS', corsOrigins: [], streamToolEvents: false, requestTimeoutSec: 300 },
  model: {
    provider: 'openai', baseUrl: 'https://api.deepseek.com/v1', model: 'deepseek-chat', apiKeyEnv: 'DEEPSEEK_API_KEY',
    temperature: 0.3, maxTokens: 4096, timeoutSec: 120, retries: 2, fallbacks: [], clientParamPolicy: 'respect',
  },
  prompt: {
    system: 'You are a helpful, precise assistant. Use tools when they help, and say so when you are unsure.',
    sections: [], includeDateTime: true, includeToolSummary: false, clientSystemPolicy: 'append',
  },
  tools: {
    builtins: ['current_time', 'calculator'], httpTools: [], mcpServers: [], parallel: true, maxParallel: 4,
    resultMaxChars: 16000, resultTruncation: 'head_tail', timeoutSec: 60, clientToolsPolicy: 'passthrough',
    shell: { allowedCommands: ['ls', 'cat', 'grep', 'echo', 'wc'], timeoutSec: 30 },
    fetch: { allowedDomains: [], maxBytes: 200000 },
    files: { readOnly: true },
  },
  skills: { mode: 'progressive', items: [] },
  memory: { staticNotes: '', persistent: false, path: './harness-data/memory.json', scope: 'per_user', autoInject: 5, enableTools: true },
  sessions: { mode: 'stateless', dir: './harness-data/sessions', headerName: 'X-Session-Id', maxMessages: 200 },
  context: {
    maxContextTokens: 64000, strategy: 'sliding_window', triggerRatio: 0.8, keepRecentMessages: 12,
    summaryPrompt: 'Summarize the conversation so far for your own future reference. Keep facts, decisions, open tasks, file paths and tool results that still matter. Be concise.',
    pruneToolResults: true, pruneKeepLast: 6, pruneMaxChars: 2000,
  },
  loop: {
    strategy: 'react', maxSteps: 12, maxToolCalls: 40, timeBudgetSec: 240, onToolError: 'continue', onMaxSteps: 'summarize',
    planningPrompt: 'Before acting, write a short numbered plan for how you will answer the request. Do not call tools yet.',
    selfCheck: { enabled: false, maxRevisions: 1, prompt: "Review your draft answer against the user's request. If it is complete and correct reply with exactly APPROVED. Otherwise reply with the corrected final answer only." },
  },
  subagents: [],
  permissions: { defaultPolicy: 'allow', toolPolicies: { shell: 'deny', write_file: 'deny' }, approvalWebhook: '', workspaceRoot: './workspace' },
  guardrails: { inputRules: [], outputRules: [], redactPII: false, maxInputChars: 100000, blockedMessage: "Sorry, I can't help with that request." },
  hooks: [],
  observability: { logLevel: 'info', logFormat: 'text', traceFile: '', traceHeader: true, includeUsage: true },
};

const FALLBACK_CATALOG = {
  components: [
    { id: 'identity', title: 'Identity & API', summary: 'Name the agent and shape the OpenAI-compatible endpoint it serves.', seam: 'ctx.webServer · dsh profiles (web / headless / sdk / acp)', why: 'The harness is what clients talk to. Here the surface is /v1/chat/completions, so any OpenAI SDK, LangChain, or IDE plugin can use your agent unchanged.' },
    { id: 'model', title: 'Model adapter', summary: 'Pick the provider, model, sampling and fallback chain.', seam: 'ctx.llm · llm-deepseek · ctx.subagentModelSelection', why: 'The model is a swappable plugin. Fallbacks give you routing when a provider is down or rate-limited.' },
    { id: 'prompt', title: 'System prompt', summary: 'Compose the instructions the model sees on every request.', seam: 'ctx.systemPrompt · agent-instructions', why: "Prompt assembly is ordered sections: base instructions, date, tool summary, skills index, memory, then the client's own system message." },
    { id: 'tools', title: 'Tools', summary: 'Enable built-in tools, add HTTP tools and MCP servers.', seam: 'ctx.tools · tool-fs · tool-bash · tool-web · ctx.mcpManager', why: "Tools are how the agent acts. Results pass through a pipeline that truncates and prunes them so they don't swamp the context." },
    { id: 'skills', title: 'Skills', summary: 'Package reusable instructions the agent loads on demand.', seam: 'ctx.skills · skill-filesystem · tool-skill', why: 'Progressive disclosure keeps only names and descriptions in context; full instructions load when the model asks for them.' },
    { id: 'memory', title: 'Memory', summary: 'Static notes plus a persistent memory store the agent can write to.', seam: 'agent-instructions (AGENTS.md) · ctx.storage', why: 'Memory survives across conversations: either fixed notes you write, or facts the agent saves and recalls itself.' },
    { id: 'sessions', title: 'Sessions', summary: 'Stateless like OpenAI, or server-side conversation history.', seam: 'ctx.sessions · session-persistence-jsonl', why: 'In server mode the binary keeps an append-only log per session, so clients can send only the newest message.' },
    { id: 'context', title: 'Context management', summary: 'Compaction and tool-result pruning when the window fills up.', seam: 'ctx.compaction · compaction-basic · compaction-tool-result-pruner · ctx.tokenMeter', why: 'Long-running agents rot their own context. Choose between dropping old turns or summarizing them, and shrink stale tool output.' },
    { id: 'loop', title: 'Agent loop', summary: 'How the agent reasons and acts: ReAct, plan-then-execute, or one shot, plus self-checking.', seam: 'ctx.agentLoop · core/agent-loop · tool-ralph · plan-mode', why: 'The loop decides how many steps, what happens on errors, and whether the agent critiques its own answer before replying.' },
    { id: 'subagents', title: 'Sub-agents', summary: 'Specialist agents the main agent can delegate to with their own prompt and tools.', seam: 'ctx.subagents · subagent-spawn-in-process · tool-subagent', why: "Delegation keeps the parent's context clean: each sub-agent runs in its own context and returns only its result." },
    { id: 'permissions', title: 'Permissions & sandbox', summary: 'Which tools may run, inside which workspace, with optional webhook approval.', seam: 'ctx.approval · ctx.sandboxPolicy · ctx.permissionPresets', why: 'An API has no human to click Approve, so approvals are allow/deny rules or a webhook that decides. Anything unmatched falls back to the default.' },
    { id: 'guardrails', title: 'Guardrails', summary: 'Filter or redact what goes in and what comes out.', seam: 'tools/pre-execute waterfall · agent/pre-step listeners', why: 'Rules run before the model sees input and before the client sees output: block, redact, or just log.' },
    { id: 'hooks', title: 'Hooks', summary: 'Run webhooks, logs or context injections at lifecycle events.', seam: 'agent/pre-step · tools/pre-execute · tools/post-execute · agent/turn-stopping', why: 'Hooks are the general extension point: they can observe, inject context, or veto a step or tool call.' },
    { id: 'observability', title: 'Observability', summary: 'Logging, JSONL traces and usage reporting.', seam: 'ctx.sessionTelemetry · session-telemetry-otel', why: 'Every step, tool call and model request is written as an event, so you can replay and debug what the agent did.' },
  ],
  builtinTools: [
    { name: 'current_time', description: 'Current date and time, optionally in a given IANA time zone.', risk: 'low' },
    { name: 'calculator', description: 'Evaluate an arithmetic expression (+ - * / % ^, parentheses, sqrt, abs, round...).', risk: 'low' },
    { name: 'http_fetch', description: 'Fetch a URL over HTTP(S) and return text. Restricted by the domain allowlist.', risk: 'medium' },
    { name: 'read_file', description: 'Read a text file inside the workspace root.', risk: 'medium' },
    { name: 'list_dir', description: 'List a directory inside the workspace root.', risk: 'low' },
    { name: 'write_file', description: 'Create or overwrite a file inside the workspace root.', risk: 'high' },
    { name: 'shell', description: 'Run a command inside the workspace root. Restricted by the command allowlist.', risk: 'high' },
    { name: 'todo', description: 'Keep a per-turn task checklist (plan, check off, review).', risk: 'low' },
  ],
  hookEvents: ['turn_start', 'pre_step', 'pre_tool', 'post_tool', 'turn_end', 'on_error'],
  presets: [],
  targets: [
    { os: 'darwin', arch: 'arm64', label: 'macOS (Apple Silicon)' },
    { os: 'darwin', arch: 'amd64', label: 'macOS (Intel)' },
    { os: 'linux', arch: 'amd64', label: 'Linux (x86-64)' },
    { os: 'linux', arch: 'arm64', label: 'Linux (ARM64)' },
    { os: 'windows', arch: 'amd64', label: 'Windows (x86-64)' },
  ],
  defaults: FALLBACK_DEFAULTS,
  hostTarget: null,
};

/* ------------------------------------------------------------------------ */
/* Static UI knowledge                                                       */
/* ------------------------------------------------------------------------ */

const STORAGE_KEY = 'openharness.builder.v1';
const NAME_RE = /^[a-zA-Z0-9_-]{1,64}$/;

const PROVIDER_PRESETS = [
  { id: 'deepseek', label: 'DeepSeek', provider: 'openai', baseUrl: 'https://api.deepseek.com/v1', model: 'deepseek-chat', apiKeyEnv: 'DEEPSEEK_API_KEY' },
  { id: 'openai', label: 'OpenAI', provider: 'openai', baseUrl: 'https://api.openai.com/v1', model: 'gpt-4.1', apiKeyEnv: 'OPENAI_API_KEY' },
  { id: 'anthropic', label: 'Anthropic', provider: 'anthropic', baseUrl: 'https://api.anthropic.com', model: 'claude-sonnet-5-5', apiKeyEnv: 'ANTHROPIC_API_KEY' },
  { id: 'ollama', label: 'Ollama (local)', provider: 'openai', baseUrl: 'http://localhost:11434/v1', model: 'llama3.1', apiKeyEnv: '' },
  { id: 'openrouter', label: 'OpenRouter', provider: 'openai', baseUrl: 'https://openrouter.ai/api/v1', model: 'deepseek/deepseek-chat', apiKeyEnv: 'OPENROUTER_API_KEY' },
];

const HOOK_EVENT_INFO = {
  turn_start: 'Once per request, after input guardrails, before the first model call.',
  pre_step: 'Before every model call inside the loop. Good for injecting fresh context.',
  pre_tool: 'Before each tool executes. A blocking webhook can veto the call.',
  post_tool: 'After each tool returns. Inspect or annotate results.',
  turn_end: 'After the final answer is ready, before it is returned to the client.',
  on_error: 'Whenever a model or tool error occurs.',
};

const HTTP_TOOL_PARAMS_TEMPLATE = {
  type: 'object',
  properties: { query: { type: 'string', description: 'What to look up' } },
  required: ['query'],
};

const ERROR_PREFIX_TO_STEP = {
  version: 'identity', name: 'identity', description: 'identity', server: 'identity',
  model: 'model', prompt: 'prompt', tools: 'tools', skills: 'skills', memory: 'memory',
  sessions: 'sessions', context: 'context', loop: 'loop', subagents: 'subagents',
  permissions: 'permissions', guardrails: 'guardrails', hooks: 'hooks', observability: 'observability',
};

// Map-typed fields: replaced wholesale on import instead of deep-merged.
const MAP_KEYS = new Set(['toolPolicies', 'extraBody', 'extraHeaders', 'headers', 'env']);

/* ------------------------------------------------------------------------ */
/* State                                                                     */
/* ------------------------------------------------------------------------ */

const state = {
  spec: null,
  started: false,
  step: 'identity',
  visited: new Set(),
  target: '',
  showSpec: false,
};

let catalog = FALLBACK_CATALOG;
let online = false;
let lastErrors = null; // string[] from /api/validate, or null when unknown
let liveUpdaters = [];
let seq = 0;

const preview = {
  id: null, baseUrl: null, snapshot: null, sessionMode: 'stateless', headerName: 'X-Session-Id',
  modelId: '', sessionId: newId(), messages: [], busy: false, starting: false, abort: null,
  errors: [], error: '', traceId: '',
};

const buildState = { busy: false, startedAt: 0, timer: null, result: null, error: '', errors: [] };

/* ------------------------------------------------------------------------ */
/* Small utilities                                                           */
/* ------------------------------------------------------------------------ */

function newId() {
  try { if (crypto && crypto.randomUUID) return crypto.randomUUID(); } catch { /* ignore */ }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16);
  });
}

const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
const isPlainObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'html') el.innerHTML = v; // static, trusted markup only
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'value' || k === 'checked' || k === 'selected' || k === 'hidden') el[k] = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else el.setAttribute(k, v === true ? '' : String(v));
    }
  }
  appendKids(el, kids);
  return el;
}

// setKids replaces children, skipping null/false like h() does
// (native replaceChildren would render them as text).
function setKids(el, ...kids) {
  el.replaceChildren();
  appendKids(el, kids);
}

function appendKids(el, kids) {
  for (const k of kids) {
    if (k == null || k === false) continue;
    if (Array.isArray(k)) appendKids(el, k);
    else if (k instanceof Node) el.appendChild(k);
    else el.appendChild(document.createTextNode(String(k)));
  }
}

function parsePath(p) {
  return p.split('.').map((s) => (/^\d+$/.test(s) ? Number(s) : s));
}

function getPath(obj, p) {
  let o = obj;
  for (const k of parsePath(p)) {
    if (o == null) return undefined;
    o = o[k];
  }
  return o;
}

function setPath(obj, p, v) {
  const ks = parsePath(p);
  let o = obj;
  for (let i = 0; i < ks.length - 1; i++) {
    if (o[ks[i]] == null || typeof o[ks[i]] !== 'object') o[ks[i]] = typeof ks[i + 1] === 'number' ? [] : {};
    o = o[ks[i]];
  }
  const last = ks[ks.length - 1];
  if (v === undefined) delete o[last];
  else o[last] = v;
}

function arr(p) {
  let a = getPath(state.spec, p);
  if (!Array.isArray(a)) { a = []; setPath(state.spec, p, a); }
  return a;
}

function deepMerge(base, over) {
  if (!isPlainObject(base) || !isPlainObject(over)) return over === undefined ? base : clone(over);
  const out = { ...base };
  for (const [k, v] of Object.entries(over)) {
    if (MAP_KEYS.has(k)) out[k] = clone(v);
    else if (isPlainObject(v) && isPlainObject(base[k])) out[k] = deepMerge(base[k], v);
    else out[k] = clone(v);
  }
  return out;
}

function normalizeSpec(raw) {
  const s = deepMerge(clone(catalog.defaults || FALLBACK_DEFAULTS), isPlainObject(raw) ? raw : {});
  const arrays = [
    'server.corsOrigins', 'model.fallbacks', 'prompt.sections', 'tools.builtins', 'tools.httpTools', 'tools.mcpServers',
    'tools.shell.allowedCommands', 'tools.fetch.allowedDomains', 'skills.items', 'subagents',
    'guardrails.inputRules', 'guardrails.outputRules', 'hooks',
  ];
  for (const p of arrays) if (!Array.isArray(getPath(s, p))) setPath(s, p, []);
  if (!isPlainObject(s.permissions.toolPolicies)) s.permissions.toolPolicies = {};
  for (const sa of s.subagents) if (!Array.isArray(sa.tools)) sa.tools = [];
  for (const m of s.tools.mcpServers) if (!Array.isArray(m.args)) m.args = [];
  for (const t of s.tools.httpTools) {
    if (typeof t.parameters === 'string') { try { t.parameters = JSON.parse(t.parameters); } catch { /* keep */ } }
  }
  if (!s.version) s.version = '1';
  return s;
}

function toast(msg, kind = '') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast show ' + kind;
  clearTimeout(toast.t);
  toast.t = setTimeout(() => { el.className = 'toast'; }, 2600);
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = h('a', { href: url, download: filename });
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30000);
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = h('textarea', { value: text, style: 'position:fixed;opacity:0' });
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch { /* ignore */ }
    ta.remove();
  }
  toast('Copied to clipboard');
}

function formatBytes(n) {
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

function specJSON() { return JSON.stringify(state.spec, null, 2); }

async function readJSON(res) {
  try { return await res.json(); } catch { return null; }
}

function errorMessage(j, res) {
  if (j) {
    if (typeof j.error === 'string') return j.error;
    if (j.error && j.error.message) return j.error.message;
    if (j.message) return j.message;
  }
  return res ? `HTTP ${res.status} ${res.statusText}` : 'Request failed';
}

/* ------------------------------------------------------------------------ */
/* Persistence                                                               */
/* ------------------------------------------------------------------------ */

function save() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      spec: state.spec, started: state.started, step: state.step,
      visited: [...state.visited], target: state.target, showSpec: state.showSpec,
    }));
  } catch { /* storage unavailable */ }
}

function loadSaved() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch { return null; }
}

function clearSaved() {
  try { localStorage.removeItem(STORAGE_KEY); } catch { /* ignore */ }
}

let saveTimer = null;
function scheduleSave() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(save, 250);
}

/* ------------------------------------------------------------------------ */
/* Change propagation                                                        */
/* ------------------------------------------------------------------------ */

function changed(rerender) {
  scheduleSave();
  scheduleValidate();
  if (rerender) renderStep({ keepScroll: true });
  else runLive();
}

let liveFrame = 0;
function runLive() {
  cancelAnimationFrame(liveFrame);
  liveFrame = requestAnimationFrame(() => {
    updateTopbar();
    updateSpecPanel();
    for (const fn of liveUpdaters) { try { fn(); } catch (e) { console.error(e); } }
  });
}

function live(fn, tag = 'span', cls = '') {
  const el = h(tag, { class: cls });
  const upd = () => { const v = fn(el); if (typeof v === 'string') el.textContent = v; };
  upd();
  liveUpdaters.push(upd);
  return el;
}

let validateTimer = null;
function scheduleValidate() {
  clearTimeout(validateTimer);
  validateTimer = setTimeout(validateNow, 700);
}

async function validateNow() {
  if (!online || !state.spec) return lastErrors;
  try {
    const res = await fetch('/api/validate', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ spec: state.spec }),
    });
    const j = await readJSON(res);
    if (!res.ok) {
      lastErrors = j && Array.isArray(j.errors) ? j.errors : [errorMessage(j, res)];
    } else {
      lastErrors = j && Array.isArray(j.errors) ? j.errors : [];
    }
  } catch {
    lastErrors = null;
  }
  updateSidebar();
  for (const fn of validationListeners) fn();
  return lastErrors;
}
let validationListeners = [];

function errorStep(msg) {
  const m = /^([a-zA-Z]+)/.exec(msg || '');
  return (m && ERROR_PREFIX_TO_STEP[m[1]]) || null;
}

function errorsByStep() {
  const out = {};
  for (const e of lastErrors || []) {
    const s = errorStep(e) || 'identity';
    (out[s] ||= []).push(e);
  }
  return out;
}

/* ------------------------------------------------------------------------ */
/* Declarative form helpers                                                  */
/* ------------------------------------------------------------------------ */

function normOptions(options) {
  return options.map((o) => (Array.isArray(o) ? { value: o[0], label: o[1] } : typeof o === 'string' ? { value: o, label: o } : o));
}

/**
 * field(path, opts) – a labelled control bound to state.spec at `path`.
 * types: text | password | url | number | textarea | select | toggle | csv |
 *        lines | args | json | slider
 * opts:  label, help, placeholder, wide, mono, rows, min, max, step, float,
 *        optional (empty number -> null), omitEmpty (empty -> key removed),
 *        options, rerender, onChange(new, old), validate(v) -> message,
 *        expect ('object' | 'stringMap' | 'any') for json, dim, dimNote.
 */
function field(path, o = {}) {
  const id = 'f' + (++seq);
  const type = o.type || 'text';
  const cur = getPath(state.spec, path);
  const helpId = id + '-help';
  const errId = id + '-err';
  const err = h('p', { class: 'field-error', id: errId, hidden: true });
  let control;

  const commit = (v, rerender) => {
    const old = clone(getPath(state.spec, path));
    setPath(state.spec, path, v);
    if (o.onChange) o.onChange(v, old);
    changed(rerender ?? o.rerender);
  };
  const setErr = (m) => {
    err.textContent = m || '';
    err.hidden = !m;
    if (control) control.setAttribute('aria-invalid', m ? 'true' : 'false');
  };
  const check = (v) => { if (o.validate) setErr(o.validate(v)); };

  const common = {
    id, 'data-path': path, 'aria-describedby': [o.help && helpId, errId].filter(Boolean).join(' '),
    placeholder: o.placeholder, class: o.mono ? 'mono' : null,
  };

  switch (type) {
    case 'text':
    case 'url':
    case 'password': {
      control = h('input', { ...common, type: type === 'url' ? 'url' : type, value: cur ?? '', autocomplete: 'off', spellcheck: 'false' });
      control.addEventListener('input', () => {
        const v = control.value;
        check(v);
        commit(o.omitEmpty && v === '' ? undefined : v);
      });
      check(cur ?? '');
      break;
    }
    case 'number': {
      control = h('input', {
        ...common, type: 'number', inputmode: o.float ? 'decimal' : 'numeric',
        value: cur == null ? '' : String(cur), min: o.min, max: o.max, step: o.step ?? (o.float ? 'any' : 1),
      });
      control.addEventListener('input', () => {
        const raw = control.value.trim();
        if (raw === '') {
          setErr('');
          commit(o.optional ? null : 0);
          return;
        }
        const n = o.float ? parseFloat(raw) : parseInt(raw, 10);
        if (Number.isNaN(n)) { setErr('Enter a number'); return; }
        if (o.min != null && n < o.min) setErr(`Minimum is ${o.min}`);
        else if (o.max != null && n > o.max) setErr(`Maximum is ${o.max}`);
        else setErr(o.validate ? o.validate(n) : '');
        commit(n);
      });
      break;
    }
    case 'textarea': {
      control = h('textarea', { ...common, rows: o.rows || 4, spellcheck: o.mono ? 'false' : 'true' });
      control.value = cur ?? '';
      control.addEventListener('input', () => { check(control.value); commit(o.omitEmpty && control.value === '' ? undefined : control.value); });
      check(cur ?? '');
      break;
    }
    case 'select': {
      const opts = normOptions(o.options || []);
      const curStr = cur == null ? '' : String(cur);
      if (!opts.some((x) => String(x.value) === curStr)) opts.push({ value: curStr, label: curStr === '' ? '(none)' : `${curStr} (custom)` });
      control = h('select', common, opts.map((x) => h('option', { value: String(x.value), selected: String(x.value) === curStr, text: x.label })));
      control.addEventListener('change', () => {
        const v = control.value;
        commit(o.omitEmpty && v === '' ? undefined : v);
      });
      break;
    }
    case 'toggle': {
      control = h('input', { ...common, class: null, type: 'checkbox', role: 'switch', checked: !!cur });
      control.addEventListener('change', () => commit(control.checked));
      const wrap = h('div', { class: cls(o, 'field toggle-field') },
        h('label', { class: 'switch', for: id }, control, h('span', { class: 'switch-track', 'aria-hidden': 'true' }), h('span', { class: 'switch-label', text: o.label })),
        o.help && h('p', { class: 'help', id: helpId, text: o.help }),
        o.dim && o.dimNote && h('p', { class: 'dim-note', text: o.dimNote }),
        err);
      return wrap;
    }
    case 'csv': {
      control = h('input', { ...common, type: 'text', value: (cur || []).join(', '), autocomplete: 'off', spellcheck: 'false' });
      control.addEventListener('input', () => commit(control.value.split(',').map((s) => s.trim()).filter(Boolean)));
      break;
    }
    case 'lines': {
      control = h('textarea', { ...common, rows: o.rows || 3, spellcheck: 'false' });
      control.value = (cur || []).join('\n');
      control.addEventListener('input', () => commit(control.value.split('\n').map((s) => s.trim()).filter(Boolean)));
      break;
    }
    case 'args': {
      const list = cur || [];
      control = h('textarea', { ...common, class: 'mono', rows: o.rows || 2, spellcheck: 'false' });
      control.value = list.some((a) => /\s/.test(a)) ? list.join('\n') : list.join(' ');
      control.addEventListener('input', () => {
        const t = control.value;
        const parts = t.includes('\n') ? t.split('\n').map((s) => s.trim()) : t.split(/\s+/);
        commit(parts.filter(Boolean));
      });
      break;
    }
    case 'json': {
      control = h('textarea', { ...common, class: 'mono', rows: o.rows || 4, spellcheck: 'false' });
      const empty = cur == null || (isPlainObject(cur) && Object.keys(cur).length === 0);
      control.value = empty ? '' : JSON.stringify(cur, null, 2);
      control.addEventListener('input', () => {
        const t = control.value.trim();
        if (t === '') { setErr(''); commit(o.emptyValue === undefined ? undefined : clone(o.emptyValue)); return; }
        let v;
        try { v = JSON.parse(t); } catch (e) { setErr('Invalid JSON: ' + e.message); return; }
        if ((o.expect === 'object' || o.expect === 'stringMap') && !isPlainObject(v)) { setErr('Must be a JSON object { ... }'); return; }
        if (o.expect === 'stringMap' && Object.values(v).some((x) => typeof x !== 'string')) { setErr('All values must be strings'); return; }
        setErr('');
        commit(v);
      });
      break;
    }
    case 'slider': {
      const out = h('output', { for: id, class: 'slider-value mono', text: String(cur ?? '') });
      control = h('input', { ...common, type: 'range', min: o.min ?? 0, max: o.max ?? 1, step: o.step ?? 0.05, value: String(cur ?? 0) });
      control.addEventListener('input', () => {
        const n = parseFloat(control.value);
        out.textContent = String(n);
        commit(n);
      });
      return h('div', { class: cls(o, 'field') },
        h('label', { for: id, class: 'label' }, o.label),
        h('div', { class: 'slider-row' }, control, out),
        o.help && h('p', { class: 'help', id: helpId, text: o.help }),
        err);
    }
    default:
      throw new Error('unknown field type ' + type);
  }

  let input = control;
  if (o.reveal && type === 'password') {
    const btn = h('button', { type: 'button', class: 'btn btn-ghost btn-sm', text: 'Show', 'aria-controls': id });
    btn.addEventListener('click', () => {
      const show = control.type === 'password';
      control.type = show ? 'text' : 'password';
      btn.textContent = show ? 'Hide' : 'Show';
    });
    input = h('div', { class: 'input-with-btn' }, control, btn);
  }
  if (o.suffix) input = h('div', { class: 'input-suffix' }, input, h('span', { class: 'suffix', text: o.suffix }));

  return h('div', { class: cls(o, 'field') },
    h('label', { for: id, class: 'label' }, o.label, o.optionalTag && h('span', { class: 'optional-tag', text: 'optional' })),
    input,
    o.help && h('p', { class: 'help', id: helpId, text: o.help }),
    o.dim && o.dimNote && h('p', { class: 'dim-note', text: o.dimNote }),
    err);
}

function cls(o, base) {
  return [base, (o.wide || o.type === 'textarea' || o.type === 'json' || o.type === 'lines') && o.wide !== false && 'wide', o.dim && 'dim', o.class].filter(Boolean).join(' ');
}

/** radioCards(path, opts) – mutually exclusive choice with explanations. */
function radioCards(path, o) {
  const name = 'r' + (++seq);
  const cur = getPath(state.spec, path);
  return h('fieldset', { class: 'field wide radio-cards' },
    h('legend', { class: 'label', text: o.label }),
    o.help && h('p', { class: 'help', text: o.help }),
    h('div', { class: 'rcards' + (o.options.length > 3 ? ' rcards-4' : '') }, o.options.map((opt) =>
      h('label', { class: 'rcard' + (cur === opt.value ? ' selected' : '') },
        h('input', {
          type: 'radio', name, value: opt.value, checked: cur === opt.value, 'data-path': path,
          onchange: () => { setPath(state.spec, path, opt.value); if (o.onChange) o.onChange(opt.value); changed(o.rerender ?? true); },
        }),
        h('span', { class: 'rcard-head' }, h('span', { class: 'rcard-title', text: opt.title }), opt.tag && h('code', { class: 'rcard-tag', text: opt.value })),
        h('span', { class: 'rcard-desc', text: opt.desc })))));
}

/** checkGroup(path, options) – array of strings edited as a set of checkboxes. */
function checkGroup(path, o) {
  const cur = new Set(getPath(state.spec, path) || []);
  const opts = normOptions(o.options);
  return h('fieldset', { class: 'field wide check-group' },
    h('legend', { class: 'label', text: o.label }),
    o.help && h('p', { class: 'help', text: o.help }),
    opts.length === 0 ? h('p', { class: 'muted small', text: o.emptyText || 'Nothing to choose from yet.' }) :
      h('div', { class: 'checks' }, opts.map((x) => h('label', { class: 'check' },
        h('input', {
          type: 'checkbox', checked: cur.has(x.value), 'data-path': path + ':' + x.value,
          onchange: (e) => {
            const list = (getPath(state.spec, path) || []).filter((v) => v !== x.value);
            if (e.target.checked) list.push(x.value);
            setPath(state.spec, path, list);
            changed(false);
          },
        }),
        h('span', { class: 'mono', text: x.label })))));
}

/** section – a titled card with a two-column field grid. */
function section(title, desc, ...children) {
  return h('section', { class: 'card' },
    title && h('h2', { class: 'card-title', text: title }),
    desc && (desc instanceof Node ? desc : h('p', { class: 'card-desc', text: desc })),
    h('div', { class: 'grid' }, children));
}

function callout(kind, ...children) {
  return h('div', { class: 'callout callout-' + kind, role: kind === 'danger' ? 'alert' : null }, children);
}

function codeBlock(text, lang) {
  const pre = h('pre', { class: 'code' }, h('code', { text }));
  return h('div', { class: 'code-wrap' },
    lang && h('span', { class: 'code-lang', text: lang }),
    h('button', { type: 'button', class: 'btn btn-ghost btn-xs code-copy', text: 'Copy', onclick: () => copyText(text) }),
    pre);
}

/**
 * listEditor – generic editor for arrays of objects at `path`.
 * renderItem(item, index, itemPath) returns the fields for one row.
 */
function listEditor(o) {
  const items = arr(o.path);
  const list = h('div', { class: 'list' });
  if (items.length === 0) list.append(h('p', { class: 'list-empty', text: o.empty || 'Nothing here yet.' }));
  items.forEach((item, i) => {
    const p = `${o.path}.${i}`;
    const title = h('span', { class: 'list-item-title', text: o.itemLabel(item, i) });
    const move = (d) => {
      const j = i + d;
      if (j < 0 || j >= items.length) return;
      [items[i], items[j]] = [items[j], items[i]];
      changed(true);
    };
    const actions = h('div', { class: 'list-item-actions' },
      o.reorder && h('button', { type: 'button', class: 'btn btn-ghost btn-xs', 'aria-label': `Move ${o.noun || 'item'} ${i + 1} up`, disabled: i === 0, text: '↑', onclick: () => move(-1) }),
      o.reorder && h('button', { type: 'button', class: 'btn btn-ghost btn-xs', 'aria-label': `Move ${o.noun || 'item'} ${i + 1} down`, disabled: i === items.length - 1, text: '↓', onclick: () => move(1) }),
      h('button', {
        type: 'button', class: 'btn btn-ghost btn-xs btn-danger-text', text: 'Remove', 'aria-label': `Remove ${o.noun || 'item'} ${i + 1}`,
        onclick: () => { items.splice(i, 1); if (o.onRemove) o.onRemove(item); changed(true); },
      }));
    const body = h('div', { class: 'grid list-item-body' }, o.renderItem(item, i, p));
    const row = h('div', { class: 'list-item' }, h('div', { class: 'list-item-head' }, h('span', { class: 'list-item-index mono', text: String(i + 1).padStart(2, '0') }), title, actions), body);
    row.addEventListener('input', () => { title.textContent = o.itemLabel(items[i], i); });
    row.addEventListener('change', () => { title.textContent = o.itemLabel(items[i], i); });
    list.append(row);
  });
  const add = h('button', {
    type: 'button', class: 'btn btn-secondary btn-sm', text: '+ ' + (o.addLabel || 'Add'),
    onclick: () => { items.push(o.newItem()); changed(true); focusLater(`${o.path}.${items.length - 1}.${o.focusKey || 'name'}`); },
  });
  return h('div', { class: 'field wide list-editor' },
    o.title && h('div', { class: 'list-editor-head' }, h('h3', { class: 'subhead', text: o.title }), h('span', { class: 'count-badge', text: String(items.length) })),
    o.help && h('p', { class: 'help', text: o.help }),
    list,
    h('div', { class: 'list-add' }, add, o.extraActions));
}

function focusLater(path) {
  setTimeout(() => {
    const el = findControl(path);
    if (el) el.focus();
  }, 30);
}

function findControl(path) {
  const sel = `[data-path="${CSS.escape(path)}"]`;
  const all = document.querySelectorAll(sel);
  if (all.length === 0) return null;
  for (const el of all) if (el.type === 'radio' && el.checked) return el;
  return all[0];
}

/* ------------------------------------------------------------------------ */
/* Steps                                                                     */
/* ------------------------------------------------------------------------ */

function steps() {
  const comps = (catalog.components || []).map((c) => ({ id: c.id, title: c.title, comp: c }));
  return [
    ...comps,
    { id: 'test', title: 'Test', comp: { id: 'test', title: 'Test in the playground', summary: 'Spin up an in-process instance of this exact spec and chat with it.' } },
    { id: 'build', title: 'Build & download', comp: { id: 'build', title: 'Build & download', summary: 'Validate the spec, compile a self-contained binary for your target, and run it anywhere.' } },
  ];
}

function stepIndex(id) { return steps().findIndex((s) => s.id === id); }

function goTo(id, focusField) {
  if (stepIndex(id) < 0) return;
  state.step = id;
  state.visited.add(id);
  save();
  renderStep();
  updateSidebar();
  const content = document.getElementById('content');
  if (content) content.scrollTop = 0;
  window.scrollTo(0, 0);
  const heading = document.getElementById('step-heading');
  if (focusField) focusLater(focusField);
  else if (heading) heading.focus({ preventScroll: true });
}

/* ------------------------------------------------------------------------ */
/* Top-level rendering                                                       */
/* ------------------------------------------------------------------------ */

function render() {
  const app = document.getElementById('app');
  app.replaceChildren();
  updateTopbar();
  if (!state.started) {
    app.append(renderStart());
    return;
  }
  const select = h('select', { id: 'step-select', class: 'step-select', 'aria-label': 'Jump to step' });
  select.addEventListener('change', () => goTo(select.value));
  const layout = h('div', { class: 'layout' + (state.showSpec ? ' with-spec' : '') },
    h('nav', { class: 'sidebar', id: 'sidebar', 'aria-label': 'Builder steps' }),
    h('main', { class: 'content', id: 'content' },
      h('div', { class: 'step-select-wrap' }, h('label', { class: 'label', for: 'step-select', text: 'Step' }), select),
      h('div', { id: 'step' })),
    h('aside', { class: 'spec-panel', id: 'spec-panel', 'aria-label': 'Spec JSON', hidden: !state.showSpec }));
  app.append(layout);
  renderSidebar();
  renderSpecPanel();
  renderStep();
}

function updateTopbar() {
  const nameEl = document.getElementById('agent-name');
  nameEl.textContent = state.started && state.spec ? state.spec.name || '(unnamed)' : '';
  document.getElementById('btn-spec').hidden = !state.started;
  document.getElementById('btn-spec').setAttribute('aria-pressed', String(state.showSpec));
  document.getElementById('btn-export').hidden = !state.started;
  document.getElementById('btn-reset').hidden = !state.started;
  const pill = document.getElementById('status-pill');
  pill.hidden = false;
  pill.className = 'status-pill ' + (online ? 'ok' : 'off');
  pill.textContent = online ? 'builder connected' : 'offline';
  pill.title = online ? 'Connected to the OpenHarness builder backend' : 'The builder backend is not reachable. Editing works; test and build need the backend.';
}

/* ---------- Start screen ---------- */

function renderStart() {
  const saved = loadSaved();
  const comps = catalog.components || [];
  const presetMeta = (s) => {
    const sp = normalizeSpec(s);
    return [
      `${sp.loop.strategy.replace('_', ' ')}`,
      `${sp.tools.builtins.length} tool${sp.tools.builtins.length === 1 ? '' : 's'}`,
      `${sp.sessions.mode} sessions`,
      sp.subagents.length ? `${sp.subagents.length} sub-agent` : null,
      sp.skills.items.length ? `${sp.skills.items.length} skill` : null,
    ].filter(Boolean);
  };

  const importBtn = h('button', { type: 'button', class: 'preset-card preset-alt', onclick: () => document.getElementById('input-import').click() },
    h('span', { class: 'preset-kicker', text: 'Bring your own' }),
    h('span', { class: 'preset-title', text: 'Import spec JSON' }),
    h('span', { class: 'preset-desc', text: 'Load a harness spec exported earlier or written by hand. Missing fields fall back to defaults.' }));

  const blank = h('button', { type: 'button', class: 'preset-card preset-alt', onclick: () => startWith(catalog.defaults) },
    h('span', { class: 'preset-kicker', text: 'From scratch' }),
    h('span', { class: 'preset-title', text: 'Start from defaults' }),
    h('span', { class: 'preset-desc', text: 'DeepSeek model, ReAct loop, a clock and a calculator. Change everything from there.' }));

  return h('div', { class: 'start' },
    h('section', { class: 'hero' },
      h('div', { class: 'hero-text' },
        h('p', { class: 'eyebrow mono', text: 'agent harness builder' }),
        h('h1', { class: 'hero-title' }, 'Every component of the harness is ', h('em', { text: 'yours' }), '.'),
        h('p', { class: 'hero-sub', text: 'Pick the model, write the prompt, wire up tools, skills, memory, the loop, sub-agents, permissions, guardrails and hooks. Test it here, then download a single binary that serves an OpenAI-compatible API.' }),
        h('div', { class: 'hero-flow mono', 'aria-hidden': 'true' },
          h('span', { text: 'spec.json' }), h('span', { class: 'arrow', text: '→' }), h('span', { text: 'go build' }), h('span', { class: 'arrow', text: '→' }), h('span', { text: './agent' }), h('span', { class: 'arrow', text: '→' }), h('span', { text: 'POST /v1/chat/completions' })),
        h('div', { class: 'hero-cta' },
          h('button', { type: 'button', class: 'btn btn-primary btn-lg btn-arrow', onclick: () => document.getElementById('presets').scrollIntoView({ behavior: 'smooth', block: 'start' }) }, 'Start building'))),
      h('ol', { class: 'anatomy', 'aria-label': 'Harness components' }, comps.map((c, i) =>
        h('li', { class: 'anatomy-item' }, h('span', { class: 'mono anatomy-num', text: String(i + 1).padStart(2, '0') }), h('span', { text: c.title }))))),

    h('div', { class: 'hero-mark', 'aria-hidden': 'true', text: 'OpenHarness' }),

    saved && saved.started && saved.spec && h('div', { class: 'resume' },
      h('div', null,
        h('p', { class: 'resume-title' }, 'Continue your draft ', h('code', { text: saved.spec.name || '(unnamed)' })),
        h('p', { class: 'muted small', text: 'Saved in this browser. Starting something new replaces it.' })),
      h('button', { type: 'button', class: 'btn btn-primary', text: 'Resume', onclick: () => resumeDraft(saved) })),

    h('section', { class: 'presets', id: 'presets' },
      h('h2', { class: 'section-title', text: 'Start from a preset' }),
      h('p', { class: 'muted', text: online ? 'Each preset is a complete spec. You will still walk through every component and can change anything.' : 'The builder backend is offline, so presets are unavailable. You can still start from defaults or import a spec.' }),
      h('div', { class: 'preset-grid' },
        (catalog.presets || []).map((p) => h('button', { type: 'button', class: 'preset-card', onclick: () => startWith(p.spec) },
          h('span', { class: 'preset-kicker mono', text: p.id }),
          h('span', { class: 'preset-title', text: p.title }),
          h('span', { class: 'preset-desc', text: p.description }),
          h('span', { class: 'preset-meta' }, presetMeta(p.spec).map((m) => h('span', { class: 'chip', text: m }))))),
        blank,
        importBtn)));
}

function startWith(spec) {
  state.spec = normalizeSpec(clone(spec));
  state.started = true;
  state.step = steps()[0].id;
  state.visited = new Set([state.step]);
  resetPreviewState();
  buildState.result = null;
  save();
  render();
  validateNow();
}

function resumeDraft(saved) {
  state.spec = normalizeSpec(saved.spec);
  state.started = true;
  state.step = stepIndex(saved.step) >= 0 ? saved.step : steps()[0].id;
  state.visited = new Set(saved.visited || [state.step]);
  state.showSpec = !!saved.showSpec;
  if (saved.target) state.target = saved.target;
  save();
  render();
  validateNow();
}

/* ---------- Sidebar ---------- */

function renderSidebar() {
  const nav = document.getElementById('sidebar');
  if (!nav) return;
  const all = steps();
  const comps = all.filter((s) => s.id !== 'test' && s.id !== 'build');
  const ship = all.filter((s) => s.id === 'test' || s.id === 'build');
  const item = (s, i) => {
    const btn = h('button', { type: 'button', class: 'step-btn', 'data-step': s.id, onclick: () => goTo(s.id) },
      h('span', { class: 'step-num mono', text: String(i + 1).padStart(2, '0') }),
      h('span', { class: 'step-title', text: s.title }),
      h('span', { class: 'step-state', 'aria-hidden': 'true' }));
    return h('li', null, btn);
  };
  setKids(nav,
    h('p', { class: 'side-label', text: 'Components' }),
    h('ol', { class: 'steps' }, comps.map((s, i) => item(s, i))),
    h('p', { class: 'side-label', text: 'Ship' }),
    h('ol', { class: 'steps', start: comps.length + 1 }, ship.map((s, i) => item(s, comps.length + i))),
    h('div', { class: 'side-foot' }, h('p', { class: 'muted small', id: 'side-foot' })));
  const select = document.getElementById('step-select');
  if (select) {
    select.replaceChildren(...all.map((s, i) => h('option', { value: s.id, text: `${String(i + 1).padStart(2, '0')}  ${s.title}` })));
  }
  updateSidebar();
}

function updateSidebar() {
  const byStep = errorsByStep();
  document.querySelectorAll('.step-btn').forEach((btn) => {
    const id = btn.dataset.step;
    const current = id === state.step;
    const errs = (byStep[id] || []).length;
    btn.classList.toggle('current', current);
    btn.classList.toggle('visited', state.visited.has(id));
    btn.classList.toggle('has-errors', errs > 0);
    if (current) btn.setAttribute('aria-current', 'step'); else btn.removeAttribute('aria-current');
    const st = btn.querySelector('.step-state');
    st.replaceChildren();
    if (errs > 0) {
      st.append(h('span', { class: 'err-badge', text: String(errs) }));
      btn.setAttribute('aria-label', `${btn.querySelector('.step-title').textContent}, ${errs} problem${errs === 1 ? '' : 's'}`);
    } else {
      btn.removeAttribute('aria-label');
      if (state.visited.has(id) && !current) st.append(h('span', { class: 'check-mark', html: '<svg viewBox="0 0 16 16" width="12" height="12"><path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>' }));
    }
  });
  const select = document.getElementById('step-select');
  if (select) select.value = state.step;
  const foot = document.getElementById('side-foot');
  if (foot) {
    const n = (lastErrors || []).length;
    foot.className = 'small ' + (lastErrors == null ? 'muted' : n === 0 ? 'ok-text' : 'err-text');
    if (lastErrors == null) foot.textContent = online ? 'Checking spec…' : 'Validation needs the builder backend.';
    else foot.textContent = n === 0 ? 'Spec is valid.' : `${n} problem${n === 1 ? '' : 's'} to fix before building.`;
  }
}

/* ---------- Spec panel ---------- */

function renderSpecPanel() {
  const panel = document.getElementById('spec-panel');
  if (!panel) return;
  panel.hidden = !state.showSpec;
  setKids(panel,
    h('div', { class: 'spec-panel-head' },
      h('h2', { class: 'spec-panel-title', text: 'Spec JSON' }),
      h('span', { class: 'muted small', text: 'live · read-only' }),
      h('div', { class: 'spec-panel-actions' },
        h('button', { type: 'button', class: 'btn btn-ghost btn-xs', text: 'Copy', onclick: () => copyText(specJSON()) }),
        h('button', { type: 'button', class: 'btn btn-ghost btn-xs', text: 'Close', 'aria-label': 'Close spec panel', onclick: toggleSpec }))),
    h('pre', { class: 'spec-json mono', id: 'spec-json', tabindex: '0' }));
  updateSpecPanel();
}

function updateSpecPanel() {
  const pre = document.getElementById('spec-json');
  if (pre && state.showSpec) pre.textContent = specJSON();
}

function toggleSpec() {
  state.showSpec = !state.showSpec;
  const layout = document.querySelector('.layout');
  if (layout) layout.classList.toggle('with-spec', state.showSpec);
  renderSpecPanel();
  updateTopbar();
  save();
}

/* ---------- Step view ---------- */

function renderStep({ keepScroll = false } = {}) {
  const host = document.getElementById('step');
  if (!host) return;
  const content = document.getElementById('content');
  const scroll = keepScroll ? [content ? content.scrollTop : 0, window.scrollY] : null;
  const active = document.activeElement;
  const activePath = keepScroll && active && active.dataset ? active.dataset.path : null;
  const sel = keepScroll && active && typeof active.selectionStart === 'number' ? [active.selectionStart, active.selectionEnd] : null;

  liveUpdaters = [];
  validationListeners = [];
  const all = steps();
  const idx = stepIndex(state.step);
  const s = all[idx] || all[0];
  const c = s.comp;
  const renderer = RENDERERS[s.id];

  const prev = all[idx - 1];
  const next = all[idx + 1];
  const errs = errorsByStep()[s.id] || [];

  setKids(host,
    h('header', { class: 'step-header' },
      h('p', { class: 'step-count mono', text: `Step ${idx + 1} of ${all.length}` }),
      h('h1', { class: 'step-title-h', id: 'step-heading', tabindex: '-1', text: c.title }),
      c.summary && h('p', { class: 'step-summary', text: c.summary }),
      c.seam && h('p', { class: 'seam-chip' }, h('span', { class: 'seam-label', text: 'Equivalent in DeepSeek Harness' }), h('code', { text: c.seam })),
      c.why && h('div', { class: 'why' }, h('span', { class: 'why-label', text: 'Why it matters' }), h('p', { text: c.why }))),
    errs.length > 0 && s.id !== 'build' && callout('danger',
      h('strong', { text: `${errs.length} problem${errs.length === 1 ? '' : 's'} in this component` }),
      h('ul', { class: 'err-list' }, errs.map((e) => h('li', { class: 'mono small', text: e })))),
    h('div', { class: 'step-body' }, renderer ? renderer() : h('p', { text: 'This component has no editor yet.' })),
    h('footer', { class: 'step-nav' },
      prev ? h('button', { type: 'button', class: 'btn btn-secondary', onclick: () => goTo(prev.id) }, '← ', prev.title) : h('span'),
      next ? h('button', { type: 'button', class: 'btn btn-primary', onclick: () => goTo(next.id) }, 'Next: ', next.title, ' →') : h('span')));

  updateSidebar();
  updateSpecPanel();
  updateTopbar();

  if (scroll) {
    if (content) content.scrollTop = scroll[0];
    window.scrollTo(0, scroll[1]);
    if (activePath) {
      const el = findControl(activePath);
      if (el) {
        el.focus({ preventScroll: true });
        if (sel && typeof el.setSelectionRange === 'function') { try { el.setSelectionRange(sel[0], sel[1]); } catch { /* ignore */ } }
      }
    }
  }
}

/* ------------------------------------------------------------------------ */
/* Component editors                                                         */
/* ------------------------------------------------------------------------ */

function builtinNames() { return (catalog.builtinTools || []).map((t) => t.name); }
function httpToolNames() { return (state.spec.tools.httpTools || []).map((t) => t.name).filter(Boolean); }
function stepLink(id, text) {
  return h('button', { type: 'button', class: 'link-btn', text: text || (steps().find((s) => s.id === id) || {}).title, onclick: () => goTo(id) });
}

function providerButtons(applyTo) {
  return h('div', { class: 'quickfill', role: 'group', 'aria-label': 'Quick-fill provider' },
    h('span', { class: 'quickfill-label', text: 'Quick fill' }),
    PROVIDER_PRESETS.map((p) => h('button', {
      type: 'button', class: 'btn btn-chip', text: p.label,
      onclick: () => {
        const target = getPath(state.spec, applyTo);
        Object.assign(target, { provider: p.provider, baseUrl: p.baseUrl, model: p.model, apiKeyEnv: p.apiKeyEnv });
        changed(true);
        toast(`Filled ${p.label} settings`);
      },
    })));
}

function modelRefFields(p, { inline = true } = {}) {
  return [
    field(`${p}.provider`, {
      type: 'select', label: 'Provider', rerender: true,
      options: [['openai', 'OpenAI-compatible (chat/completions)'], ['anthropic', 'Anthropic (Messages API)']],
      help: 'DeepSeek, OpenAI, Ollama, OpenRouter, vLLM, Groq… all speak the OpenAI-compatible protocol.',
    }),
    field(`${p}.model`, { label: 'Model', mono: true, placeholder: 'deepseek-chat', validate: (v) => (v ? '' : 'Required') }),
    field(`${p}.baseUrl`, { type: 'url', label: 'Base URL', mono: true, wide: true, placeholder: 'https://api.deepseek.com/v1', validate: (v) => (v ? '' : 'Required') }),
    field(`${p}.apiKeyEnv`, { label: 'API key env var', mono: true, placeholder: 'DEEPSEEK_API_KEY', help: 'Read at runtime from the environment of the binary. Leave empty for keyless local servers.' }),
    inline && field(`${p}.apiKey`, {
      type: 'password', label: 'Inline API key', optionalTag: true, omitEmpty: true, mono: true, reveal: true,
      help: 'Baked into the binary in plain text. Prefer the env var.', rerender: true,
    }),
    inline && getPath(state.spec, `${p}.apiKey`) && callout('danger', h('strong', { text: 'This key will be embedded in the binary. ' }), 'Anyone who has the file (or runs ', h('code', { text: '--print-spec' }), ') can read it. Use the env var unless the binary never leaves your machine.'),
  ];
}

const RENDERERS = {
  /* ---------- identity ---------- */
  identity() {
    const sv = state.spec.server;
    return [
      section('Agent', null,
        field('name', {
          label: 'Name', mono: true, help: 'Letters, digits, - and _. Used for the binary filename, logs and traces.',
          validate: (v) => (NAME_RE.test(v) ? '' : 'Use 1–64 letters, digits, - or _'),
          onChange: (v, old) => {
            if (state.spec.server.modelId === old) {
              state.spec.server.modelId = v;
              const el = findControl('server.modelId');
              if (el) el.value = v;
            }
          },
        }),
        field('server.modelId', { label: 'Advertised model id', mono: true, help: 'What clients pass as "model" and what GET /v1/models lists. Follows the name until you change it.' }),
        field('description', { label: 'Description', wide: true, help: 'Shown in --help and on GET /v1/models.' })),
      section('Endpoint', 'The binary serves an OpenAI-compatible API. Clients point their SDK at the base URL below.',
        field('server.host', { label: 'Host', mono: true, help: '127.0.0.1 = this machine only. 0.0.0.0 = reachable on the network.' }),
        field('server.port', { type: 'number', label: 'Port', min: 1, max: 65535 }),
        field('server.requestTimeoutSec', { type: 'number', label: 'Request timeout', suffix: 'sec', min: 0, help: 'Hard limit for one chat completion request, tool calls included.' }),
        h('div', { class: 'field' }, h('span', { class: 'label', text: 'Base URL' }),
          live(() => `http://${sv.host === '0.0.0.0' ? '127.0.0.1' : sv.host || '127.0.0.1'}:${sv.port || 8080}/v1`, 'code', 'endpoint-preview'),
          h('p', { class: 'help', text: 'Routes: POST /chat/completions, GET /models, GET /health.' }))),
      section('Authentication', null,
        radioCards('server.authMode', {
          label: 'Auth mode', options: [
            { value: 'none', title: 'None', desc: 'Anyone who can reach the port can use the agent. Fine on localhost.' },
            { value: 'bearer', title: 'Bearer token', desc: 'Clients send Authorization: Bearer <key>. Accepted keys come from an env var.' },
          ],
        }),
        field('server.apiKeyEnv', {
          label: 'Accepted keys env var', mono: true, dim: sv.authMode !== 'bearer', dimNote: 'Only used when auth mode is Bearer token.',
          help: 'Comma-separated list of valid keys, e.g. HARNESS_API_KEYS="key1,key2".',
          validate: (v) => (sv.authMode === 'bearer' && !v ? 'Required for bearer auth' : ''),
        })),
      section('Clients', null,
        field('server.corsOrigins', { type: 'csv', label: 'CORS origins', mono: true, placeholder: 'https://app.example.com, http://localhost:3000', help: 'Comma-separated. Empty disables CORS; * allows any origin (browser apps).' }),
        field('server.streamToolEvents', { type: 'toggle', label: 'Stream tool activity', help: 'Surface tool calls and results as reasoning_content deltas so chat UIs can show what the agent is doing.' })),
    ];
  },

  /* ---------- model ---------- */
  model() {
    const m = state.spec.model;
    return [
      section('Primary model', null,
        h('div', { class: 'field wide' }, providerButtons('model')),
        modelRefFields('model')),
      section('Sampling', 'Leave temperature or top-p empty to use the provider default.',
        field('model.temperature', { type: 'number', float: true, optional: true, label: 'Temperature', min: 0, max: 2, step: 0.1, placeholder: 'provider default', optionalTag: true }),
        field('model.topP', { type: 'number', float: true, optional: true, label: 'Top-p', min: 0, max: 1, step: 0.05, placeholder: 'provider default', optionalTag: true }),
        field('model.maxTokens', { type: 'number', label: 'Max output tokens', min: 0, help: '0 lets the provider decide.' }),
        field('model.reasoningEffort', {
          type: 'select', label: 'Reasoning effort', omitEmpty: true,
          options: [['', 'Not set'], ['low', 'low'], ['medium', 'medium'], ['high', 'high']],
          help: 'Sent to models that support it (o-series, reasoning models). Ignored otherwise.',
        })),
      section('Reliability', null,
        field('model.timeoutSec', { type: 'number', label: 'Timeout per model call', suffix: 'sec', min: 0 }),
        field('model.retries', { type: 'number', label: 'Retries', min: 0, max: 10, help: 'On 429/5xx and network errors, with backoff, before trying a fallback.' }),
        field('model.clientParamPolicy', {
          type: 'select', label: 'Client sampling params', wide: true,
          options: [['respect', 'respect — use temperature / max_tokens sent by the client'], ['ignore', 'ignore — always use the values configured here']],
        })),
      section('Advanced request shaping', 'Merged into every upstream request. Useful for provider-specific switches.',
        field('model.extraHeaders', { type: 'json', label: 'Extra headers', expect: 'stringMap', placeholder: '{\n  "HTTP-Referer": "https://example.com"\n}', help: 'JSON object of string values.' }),
        field('model.extraBody', { type: 'json', label: 'Extra body fields', expect: 'object', placeholder: '{\n  "thinking": { "type": "enabled" }\n}', help: 'JSON object merged into the request body.' })),
      section('Fallback chain', 'Tried in order when the primary model fails after its retries.',
        listEditor({
          path: 'model.fallbacks', noun: 'fallback', reorder: true, addLabel: 'Add fallback model', focusKey: 'model',
          empty: 'No fallbacks. If the primary fails, the request fails.',
          itemLabel: (f) => (f.model ? `${f.model}` : 'New fallback') + (f.baseUrl ? `  ·  ${f.baseUrl.replace(/^https?:\/\//, '')}` : ''),
          newItem: () => ({ provider: 'openai', baseUrl: '', model: '', apiKeyEnv: '' }),
          renderItem: (_f, _i, p) => [h('div', { class: 'field wide' }, providerButtons(p)), ...modelRefFields(p)],
        })),
      m.provider === 'anthropic' && callout('info', 'Anthropic uses its own Messages API. The harness translates OpenAI-style requests, tools and streaming for you.'),
    ];
  },

  /* ---------- prompt ---------- */
  prompt() {
    const promptPreview = live((el) => { el.textContent = assemblePrompt(); }, 'pre', 'prompt-preview mono');
    const stats = live(() => { const t = assemblePrompt(); return `${t.length.toLocaleString()} chars · ~${Math.ceil(t.length / 4).toLocaleString()} tokens`; }, 'span', 'muted small mono');
    return [
      section('Base instructions', null,
        field('prompt.system', { type: 'textarea', label: 'System prompt', rows: 9, help: 'The core persona and rules. Sent first on every request.' })),
      section('Sections', 'Optional named blocks appended after the base prompt, in order. Toggle them off to keep them around without sending them.',
        listEditor({
          path: 'prompt.sections', noun: 'section', reorder: true, addLabel: 'Add section', focusKey: 'title',
          empty: 'No extra sections.',
          itemLabel: (s) => (s.title || 'Untitled section') + (s.enabled ? '' : '  (off)'),
          newItem: () => ({ title: '', content: '', enabled: true }),
          renderItem: (_s, _i, p) => [
            field(`${p}.title`, { label: 'Title', placeholder: 'Style guide' }),
            field(`${p}.enabled`, { type: 'toggle', label: 'Enabled' }),
            field(`${p}.content`, { type: 'textarea', label: 'Content', rows: 4 }),
          ],
        })),
      section('Assembly', null,
        field('prompt.includeDateTime', { type: 'toggle', label: 'Include current date & time', help: 'Models do not know today\'s date. This line fixes "what day is it" mistakes.' }),
        field('prompt.includeToolSummary', { type: 'toggle', label: 'Include tool summary', help: 'A short list of enabled tools in prose. Usually unnecessary: tools are also sent as schemas.' }),
        radioCards('prompt.clientSystemPolicy', {
          label: 'When the client sends its own system message', rerender: false, options: [
            { value: 'append', title: 'Append', desc: 'Your prompt first, then the client\'s system message.' },
            { value: 'replace', title: 'Replace', desc: 'The client\'s system message replaces yours entirely.' },
            { value: 'ignore', title: 'Ignore', desc: 'Drop client system messages. Your prompt always wins.' },
          ],
        })),
      h('section', { class: 'card' },
        h('div', { class: 'card-title-row' }, h('h2', { class: 'card-title', text: 'Assembled prompt preview' }), stats),
        h('p', { class: 'card-desc', text: 'Approximation of what the model receives. The runtime fills in real dates, memories and the client\'s message.' }),
        promptPreview),
    ];
  },

  /* ---------- tools ---------- */
  tools() {
    const t = state.spec.tools;
    const enabled = new Set(t.builtins);
    const pol = state.spec.permissions.toolPolicies || {};
    const effective = (n) => pol[n] || state.spec.permissions.defaultPolicy;

    const toolRow = (bt) => {
      const on = enabled.has(bt.name);
      const id = 'tool-' + bt.name;
      let extra = null;
      if (on && bt.name === 'shell') {
        extra = h('div', { class: 'grid nested' },
          field('tools.shell.allowedCommands', { type: 'csv', label: 'Allowed commands', mono: true, help: 'First word of the command must be in this list. Empty = any command (only if permissions allow shell).' }),
          field('tools.shell.timeoutSec', { type: 'number', label: 'Shell timeout', suffix: 'sec', min: 0 }));
      } else if (on && bt.name === 'http_fetch') {
        extra = h('div', { class: 'grid nested' },
          field('tools.fetch.allowedDomains', { type: 'csv', label: 'Allowed domains', mono: true, placeholder: 'en.wikipedia.org, docs.python.org', help: 'Empty = any domain. Subdomains of listed domains are allowed.' }),
          field('tools.fetch.maxBytes', { type: 'number', label: 'Max response size', suffix: 'bytes', min: 0 }));
      } else if (on && bt.name === 'write_file') {
        extra = h('div', { class: 'grid nested' },
          field('tools.files.readOnly', { type: 'toggle', label: 'Workspace is read-only', help: 'When on, write_file refuses every write even though it is enabled.' }));
      }
      const denied = on && effective(bt.name) !== 'allow';
      return h('div', { class: 'tool-row' + (on ? ' on' : '') },
        h('div', { class: 'tool-row-main' },
          h('input', {
            type: 'checkbox', id, checked: on, 'data-path': 'tools.builtins:' + bt.name,
            onchange: (e) => {
              const list = t.builtins.filter((x) => x !== bt.name);
              if (e.target.checked) list.push(bt.name);
              t.builtins = builtinNames().filter((n) => list.includes(n)).concat(list.filter((n) => !builtinNames().includes(n)));
              changed(true);
            },
          }),
          h('label', { for: id, class: 'tool-label' },
            h('span', { class: 'tool-name mono', text: bt.name }),
            h('span', { class: 'risk risk-' + bt.risk, text: bt.risk + ' risk' }),
            h('span', { class: 'tool-desc', text: bt.description })),
          denied && h('span', { class: 'tool-policy' }, `${effective(bt.name)} by `, stepLink('permissions', 'permissions'))),
        extra);
    };

    return [
      section('Built-in tools', 'Shipped inside the runtime. Risky tools are confined to the workspace root set in Permissions.',
        h('div', { class: 'field wide tool-list' }, (catalog.builtinTools || []).map(toolRow)),
        !enabled.has('write_file') && (enabled.has('read_file') || enabled.has('list_dir')) &&
          field('tools.files.readOnly', { type: 'toggle', label: 'Workspace is read-only', help: 'Applies to file tools. Only matters once write_file is enabled.' })),
      section('Execution pipeline', 'Every tool result goes through this pipeline before the model sees it.',
        field('tools.parallel', { type: 'toggle', label: 'Run tool calls in parallel', rerender: true, help: 'When the model requests several tools in one step.' }),
        field('tools.maxParallel', { type: 'number', label: 'Max parallel calls', min: 1, dim: !t.parallel, dimNote: 'Only used when parallel is on.' }),
        field('tools.timeoutSec', { type: 'number', label: 'Timeout per tool call', suffix: 'sec', min: 0 }),
        field('tools.resultMaxChars', { type: 'number', label: 'Max result size', suffix: 'chars', min: 0, help: 'Longer results are truncated.' }),
        field('tools.resultTruncation', { type: 'select', label: 'Truncation', options: [['head_tail', 'head_tail — keep start and end'], ['head', 'head — keep the start']] }),
        field('tools.clientToolsPolicy', {
          type: 'select', label: 'Tools sent by the client', wide: true,
          options: [['passthrough', 'passthrough — offer them to the model; return their calls to the client (standard OpenAI behavior)'], ['ignore', 'ignore — only the tools configured here exist']],
        })),
      section('HTTP tools', 'Turn any HTTP endpoint into a tool. The model fills in the arguments described by the JSON Schema.',
        listEditor({
          path: 'tools.httpTools', noun: 'HTTP tool', addLabel: 'Add HTTP tool',
          empty: 'No HTTP tools.',
          itemLabel: (x) => x.name ? `${x.name}  ·  ${x.method || 'GET'} ${x.url || ''}` : 'New HTTP tool',
          newItem: () => ({ name: '', description: '', method: 'GET', url: '', headers: {}, parameters: clone(HTTP_TOOL_PARAMS_TEMPLATE) }),
          renderItem: (_x, _i, p) => [
            field(`${p}.name`, { label: 'Name', mono: true, placeholder: 'search_docs', validate: (v) => (NAME_RE.test(v) ? '' : 'Use letters, digits, - or _'), rerender: false }),
            field(`${p}.method`, { type: 'select', label: 'Method', options: ['GET', 'POST'] }),
            field(`${p}.description`, { label: 'Description', wide: true, help: 'The model reads this to decide when to call the tool. Be specific.' }),
            field(`${p}.url`, {
              type: 'text', label: 'URL', mono: true, wide: true, placeholder: 'https://api.example.com/search?q={{query}}',
              help: 'Use {{arg}} placeholders for arguments. Remaining arguments go in the query string (GET) or JSON body (POST).',
              validate: (v) => (v ? '' : 'Required'),
            }),
            field(`${p}.headers`, { type: 'json', label: 'Headers', expect: 'stringMap', emptyValue: {}, rows: 3, placeholder: '{\n  "Authorization": "Bearer ${MY_TOKEN}"\n}', help: 'JSON object of strings. Avoid pasting secrets; they are baked into the binary.' }),
            field(`${p}.parameters`, { type: 'json', label: 'Parameters (JSON Schema)', expect: 'object', rows: 8, help: 'Describes the arguments. Must be an object schema.' }),
          ],
        })),
      section('MCP servers', 'Model Context Protocol servers. Their tools join the registry, named by the server.',
        listEditor({
          path: 'tools.mcpServers', noun: 'MCP server', addLabel: 'Add MCP server',
          empty: 'No MCP servers.',
          itemLabel: (x) => x.name ? `${x.name}  ·  ${x.transport}` : 'New MCP server',
          newItem: () => ({ name: '', transport: 'stdio', command: '', args: [], env: {}, url: '', headers: {} }),
          renderItem: (x, _i, p) => [
            field(`${p}.name`, { label: 'Name', mono: true, placeholder: 'github', validate: (v) => (NAME_RE.test(v) ? '' : 'Use letters, digits, - or _') }),
            field(`${p}.transport`, { type: 'select', label: 'Transport', rerender: true, options: [['stdio', 'stdio — launch a local process'], ['http', 'http — connect to a URL']] }),
            x.transport === 'stdio' ? [
              field(`${p}.command`, { label: 'Command', mono: true, placeholder: 'npx', validate: (v) => (v ? '' : 'Required for stdio') }),
              field(`${p}.args`, { type: 'args', label: 'Arguments', help: 'Space-separated, or one per line if an argument contains spaces.', placeholder: '-y @modelcontextprotocol/server-github' }),
              field(`${p}.env`, { type: 'json', label: 'Environment', expect: 'stringMap', emptyValue: {}, rows: 3, placeholder: '{\n  "GITHUB_TOKEN": "..."\n}', help: 'Extra env vars for the process. The binary\'s own environment is inherited.' }),
              h('p', { class: 'help field wide', text: 'The command must exist on the machine where the binary runs.' }),
            ] : [
              field(`${p}.url`, { type: 'url', label: 'URL', mono: true, wide: true, placeholder: 'https://mcp.example.com/mcp', validate: (v) => (v ? '' : 'Required for http') }),
              field(`${p}.headers`, { type: 'json', label: 'Headers', expect: 'stringMap', emptyValue: {}, rows: 3 }),
            ],
          ],
        })),
    ];
  },

  /* ---------- skills ---------- */
  skills() {
    return [
      section('Loading strategy', null,
        radioCards('skills.mode', {
          label: 'Mode', rerender: false, options: [
            { value: 'progressive', title: 'Progressive disclosure', desc: 'Only names and descriptions sit in the prompt. The agent loads full instructions with a tool call when it needs them. Scales to many skills.' },
            { value: 'eager', title: 'Eager', desc: 'All instructions go into the system prompt up front. Simple, but every skill costs tokens on every request.' },
          ],
        })),
      section('Skills', 'A skill is a named playbook: when to use it (description) and how (instructions).',
        listEditor({
          path: 'skills.items', noun: 'skill', addLabel: 'Add skill',
          empty: 'No skills yet.',
          itemLabel: (s) => s.name || 'New skill',
          newItem: () => ({ name: '', description: '', instructions: '' }),
          renderItem: (_s, _i, p) => [
            field(`${p}.name`, { label: 'Name', mono: true, placeholder: 'refund_policy', validate: (v) => (NAME_RE.test(v) ? '' : 'Use letters, digits, - or _') }),
            field(`${p}.description`, { label: 'Description', help: 'When should the agent use this skill?' }),
            field(`${p}.instructions`, { type: 'textarea', label: 'Instructions', rows: 6, help: 'Markdown is fine. Steps, rules, examples.' }),
          ],
        })),
    ];
  },

  /* ---------- memory ---------- */
  memory() {
    const m = state.spec.memory;
    return [
      section('Static notes', 'Like an AGENTS.md file: facts and conventions the agent should always know.',
        field('memory.staticNotes', { type: 'textarea', label: 'Notes', rows: 8, mono: true, placeholder: '- The production database is read-only.\n- Prefer metric units.\n- Our support hours are 9–17 CET.' })),
      section('Persistent memory', 'A store the agent writes to and recalls from across conversations.',
        field('memory.persistent', { type: 'toggle', label: 'Enable persistent memory', rerender: true }),
        field('memory.path', { label: 'Store path', mono: true, dim: !m.persistent, dimNote: 'Only used when persistent memory is on.', help: 'JSON file, relative to where the binary runs.' }),
        field('memory.scope', {
          type: 'select', label: 'Scope', dim: !m.persistent,
          options: [['per_user', 'per_user — separate memories per session/user'], ['global', 'global — one shared memory for everyone']],
        }),
        field('memory.autoInject', { type: 'number', label: 'Auto-inject recent memories', min: 0, dim: !m.persistent, help: 'Put the N most recent memories into the prompt. 0 = none.' }),
        field('memory.enableTools', { type: 'toggle', label: 'Give the agent memory tools', dim: !m.persistent, help: 'Lets the model save and search memories itself.' })),
    ];
  },

  /* ---------- sessions ---------- */
  sessions() {
    const s = state.spec.sessions;
    return [
      section('Conversation state', null,
        radioCards('sessions.mode', {
          label: 'Mode', options: [
            { value: 'stateless', title: 'Stateless', desc: 'Exactly like the OpenAI API: the client sends the full message history every time. Nothing is stored.' },
            { value: 'server', title: 'Server-side sessions', desc: 'The binary keeps an append-only JSONL log per session id. Clients send a session header and only the newest message.' },
          ],
        }),
        field('sessions.headerName', { label: 'Session header', mono: true, dim: s.mode !== 'server', dimNote: 'Only used in server mode.', help: 'Clients send their session id in this header.' }),
        field('sessions.dir', { label: 'Session directory', mono: true, dim: s.mode !== 'server' }),
        field('sessions.maxMessages', { type: 'number', label: 'Max messages kept per session', min: 0, dim: s.mode !== 'server' })),
      s.mode === 'server' && callout('info', 'Clients send ', h('code', { text: `${s.headerName || 'X-Session-Id'}: <any id>` }), ' and a messages array with just the new user turn. Without the header a request behaves statelessly.'),
    ];
  },

  /* ---------- context ---------- */
  context() {
    const c = state.spec.context;
    return [
      section('Window', null,
        field('context.maxContextTokens', { type: 'number', label: 'Max context tokens', min: 0, help: 'Your budget, usually a bit below the model\'s real limit.' }),
        h('div', { class: 'field' }, h('span', { class: 'label', text: 'Compaction triggers at' }),
          live(() => c.strategy === 'none' ? 'never (strategy: none)' : `~${Math.round((c.maxContextTokens || 0) * (c.triggerRatio || 0)).toLocaleString()} tokens`, 'code', 'endpoint-preview'))),
      section('Compaction strategy', null,
        radioCards('context.strategy', {
          label: 'When the window fills up', options: [
            { value: 'none', title: 'None', desc: 'Send everything. Requests fail once the model\'s limit is exceeded.' },
            { value: 'sliding_window', title: 'Sliding window', desc: 'Drop the oldest turns, keep the system prompt and the most recent messages. Cheap and predictable.' },
            { value: 'summarize', title: 'Summarize', desc: 'Replace older turns with a model-written summary. Keeps long-range facts at the cost of an extra call.' },
          ],
        }),
        field('context.triggerRatio', { type: 'slider', label: 'Trigger ratio', min: 0.1, max: 1, step: 0.05, help: 'Compact when usage passes this fraction of max context tokens.', dim: c.strategy === 'none' }),
        field('context.keepRecentMessages', { type: 'number', label: 'Keep recent messages', min: 0, dim: c.strategy === 'none', help: 'Always kept verbatim.' }),
        field('context.summaryPrompt', { type: 'textarea', label: 'Summary prompt', rows: 4, dim: c.strategy !== 'summarize', dimNote: 'Only used by the summarize strategy.' })),
      section('Tool-result pruning', 'Old tool output is the fastest way to fill a context window.',
        field('context.pruneToolResults', { type: 'toggle', label: 'Prune old tool results', rerender: true }),
        field('context.pruneKeepLast', { type: 'number', label: 'Keep last N results intact', min: 0, dim: !c.pruneToolResults }),
        field('context.pruneMaxChars', { type: 'number', label: 'Shrink older results to', suffix: 'chars', min: 0, dim: !c.pruneToolResults })),
    ];
  },

  /* ---------- loop ---------- */
  loop() {
    const l = state.spec.loop;
    return [
      h('section', { class: 'card' },
        h('h2', { class: 'card-title', text: 'Lifecycle of one request' }),
        h('p', { class: 'card-desc', text: 'Highlighted events are where hooks fire. A number shows how many hooks you have configured there.' }),
        live((el) => { el.replaceChildren(loopDiagram()); }, 'div', 'loop-diagram-host')),
      section('Strategy', null,
        radioCards('loop.strategy', {
          label: 'Loop driver', options: [
            { value: 'react', title: 'ReAct', desc: 'Think, call tools, observe, repeat until the model answers. The default for tool-using agents.' },
            { value: 'plan_execute', title: 'Plan, then execute', desc: 'One planning call writes a numbered plan first; the ReAct loop then follows it.' },
            { value: 'single_shot', title: 'Single shot', desc: 'One model call, no tool loop. Lowest latency; a smarter prompt proxy.' },
          ],
        }),
        field('loop.planningPrompt', { type: 'textarea', label: 'Planning prompt', rows: 3, dim: l.strategy !== 'plan_execute', dimNote: 'Only used by plan, then execute.' })),
      section('Limits', null,
        field('loop.maxSteps', { type: 'number', label: 'Max steps', min: 1, help: 'Model calls per request.' }),
        field('loop.maxToolCalls', { type: 'number', label: 'Max tool calls', min: 0, help: 'Across all steps in a request.' }),
        field('loop.timeBudgetSec', { type: 'number', label: 'Time budget', suffix: 'sec', min: 0, help: 'Wall clock for the whole loop.' }),
        field('loop.onToolError', { type: 'select', label: 'On tool error', options: [['continue', 'continue — show the error to the model'], ['stop', 'stop — end the turn with an error']] }),
        field('loop.onMaxSteps', { type: 'select', label: 'On hitting a limit', options: [['summarize', 'summarize — final answer from what it has'], ['error', 'error — return an error to the client']] })),
      section('Self-check', 'Before replying, the agent reviews its draft and may revise it.',
        field('loop.selfCheck.enabled', { type: 'toggle', label: 'Enable self-check', rerender: true }),
        field('loop.selfCheck.maxRevisions', { type: 'number', label: 'Max revisions', min: 0, dim: !l.selfCheck.enabled }),
        field('loop.selfCheck.prompt', { type: 'textarea', label: 'Review prompt', rows: 4, dim: !l.selfCheck.enabled, help: 'Reply APPROVED to keep the draft, or return the corrected answer.' })),
    ];
  },

  /* ---------- subagents ---------- */
  subagents() {
    const toolOpts = [...builtinNames(), ...httpToolNames()];
    return [
      callout('info', 'Each sub-agent appears to the main agent as a tool named ', h('code', { text: 'agent_<name>' }), '. The main agent sends it a task; the sub-agent runs its own loop in a fresh context and returns only its final answer.'),
      section('Sub-agents', null,
        listEditor({
          path: 'subagents', noun: 'sub-agent', addLabel: 'Add sub-agent',
          empty: 'No sub-agents. The main agent does everything itself.',
          itemLabel: (s) => s.name ? `agent_${s.name}` : 'New sub-agent',
          newItem: () => ({ name: '', description: '', systemPrompt: '', tools: [], maxSteps: 6 }),
          renderItem: (_s, _i, p) => [
            field(`${p}.name`, { label: 'Name', mono: true, placeholder: 'researcher', validate: (v) => (NAME_RE.test(v) ? '' : 'Use letters, digits, - or _') }),
            field(`${p}.maxSteps`, { type: 'number', label: 'Max steps', min: 1 }),
            field(`${p}.description`, { label: 'Description', wide: true, help: 'Becomes the tool description. Tell the main agent when to delegate.' }),
            field(`${p}.systemPrompt`, { type: 'textarea', label: 'System prompt', rows: 4, validate: (v) => (v ? '' : 'Required') }),
            field(`${p}.model`, { label: 'Model override', mono: true, omitEmpty: true, optionalTag: true, placeholder: state.spec.model.model, help: 'Another model name on the same provider. Empty = same as the main agent.' }),
            checkGroup(`${p}.tools`, { label: 'Tools', options: toolOpts, help: 'Any built-in or HTTP tool, whether or not the main agent has it.' }),
          ],
        })),
    ];
  },

  /* ---------- permissions ---------- */
  permissions() {
    const pm = state.spec.permissions;
    const pol = pm.toolPolicies || (pm.toolPolicies = {});
    const enabled = new Set(state.spec.tools.builtins);
    const rows = [
      ...builtinNames().map((n) => ({ name: n, kind: 'built-in', active: enabled.has(n) })),
      ...httpToolNames().map((n) => ({ name: n, kind: 'http', active: true })),
      ...state.spec.subagents.filter((s) => s.name).map((s) => ({ name: 'agent_' + s.name, kind: 'sub-agent', active: true })),
    ];
    const known = new Set(rows.map((r) => r.name));
    for (const k of Object.keys(pol)) if (!known.has(k)) rows.push({ name: k, kind: 'custom', active: true, custom: true });

    const policySelect = (name) => {
      const id = 'pol-' + (++seq);
      const sel = h('select', { id, 'data-path': 'permissions.toolPolicies:' + name, 'aria-label': `Policy for ${name}` },
        h('option', { value: '', text: `default (${pm.defaultPolicy})`, selected: !pol[name] }),
        ['allow', 'deny', 'webhook'].map((v) => h('option', { value: v, text: v, selected: pol[name] === v })));
      sel.addEventListener('change', () => {
        if (sel.value) pol[name] = sel.value; else delete pol[name];
        changed(true);
      });
      return sel;
    };

    const customInput = h('input', { type: 'text', class: 'mono', placeholder: 'mcp:github or a tool name', 'aria-label': 'Tool name for a custom policy' });
    const addCustom = () => {
      const n = customInput.value.trim();
      if (!n) return;
      if (!pol[n]) pol[n] = 'deny';
      changed(true);
    };
    customInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addCustom(); } });

    return [
      section('Default', null,
        radioCards('permissions.defaultPolicy', {
          label: 'Tools without a rule', options: [
            { value: 'allow', title: 'Allow', desc: 'Every tool may run unless denied below. Convenient; rely on per-tool deny rules.' },
            { value: 'deny', title: 'Deny', desc: 'Nothing runs unless explicitly allowed below. Safest for exposed deployments.' },
          ],
        })),
      h('section', { class: 'card' },
        h('h2', { class: 'card-title', text: 'Per-tool policy' }),
        h('p', { class: 'card-desc', text: 'webhook = ask your approval endpoint before each call. MCP tools can be matched as mcp:<server> or by their tool name.' }),
        h('div', { class: 'table-wrap' },
          h('table', { class: 'policy-table' },
            h('thead', null, h('tr', null, h('th', { text: 'Tool' }), h('th', { text: 'Kind' }), h('th', { text: 'Policy' }), h('th', { text: '' }))),
            h('tbody', null, rows.map((r) => h('tr', { class: r.active ? '' : 'inactive' },
              h('td', null, h('code', { text: r.name }), !r.active && h('span', { class: 'muted small', text: '  not enabled' })),
              h('td', { class: 'muted small', text: r.kind }),
              h('td', null, policySelect(r.name)),
              h('td', null, r.custom && h('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-danger-text', text: 'Remove', onclick: () => { delete pol[r.name]; changed(true); } }))))))),
        h('div', { class: 'inline-add' }, customInput, h('button', { type: 'button', class: 'btn btn-secondary btn-sm', text: 'Add rule', onclick: addCustom }))),
      section('Approval & sandbox', null,
        field('permissions.approvalWebhook', {
          type: 'url', label: 'Approval webhook', mono: true, wide: true, placeholder: 'https://example.com/approve',
          help: 'Receives POST {tool, arguments, session}. Respond {"allow": true} or {"allow": false, "reason": "..."}.',
          validate: (v) => (!v && Object.values(pol).includes('webhook') ? 'Required because a tool uses the webhook policy' : ''),
        }),
        field('permissions.workspaceRoot', { label: 'Workspace root', mono: true, help: 'File and shell tools cannot escape this directory. Relative to where the binary runs.' })),
    ];
  },

  /* ---------- guardrails ---------- */
  guardrails() {
    const ruleEditor = (path, title, help) => listEditor({
      path, title, help, noun: 'rule', addLabel: 'Add rule',
      empty: 'No rules.',
      itemLabel: (r) => `${r.name || 'Unnamed rule'}  ·  ${r.action || ''}`,
      newItem: () => ({ name: '', type: 'keyword', pattern: '', action: 'block' }),
      renderItem: (r, _i, p) => [
        field(`${p}.name`, { label: 'Name', placeholder: 'no-secrets' }),
        field(`${p}.type`, { type: 'select', label: 'Match type', rerender: true, options: [['keyword', 'keyword — case-insensitive substring'], ['regex', 'regex — RE2 syntax']] }),
        field(`${p}.pattern`, {
          label: 'Pattern', mono: true, placeholder: r.type === 'regex' ? '(?i)sk-[a-z0-9]{20,}' : 'internal only',
          validate: (v) => {
            if (!v) return 'Required';
            if (r.type !== 'regex') return '';
            try { new RegExp(v.replace(/^\(\?[a-z]+\)/, '')); return ''; } catch (e) { return 'Invalid regex: ' + e.message; }
          },
        }),
        field(`${p}.action`, { type: 'select', label: 'Action', options: [['block', 'block — reject with the blocked message'], ['redact', 'redact — replace the match with [REDACTED]'], ['warn', 'warn — log and continue']] }),
      ],
    });
    return [
      section('Input rules', null, ruleEditor('guardrails.inputRules', null, 'Checked against the user\'s messages before the model sees them.')),
      section('Output rules', null, ruleEditor('guardrails.outputRules', null, 'Checked against the final answer before the client sees it.')),
      section('General', null,
        field('guardrails.redactPII', { type: 'toggle', label: 'Redact PII', help: 'Masks emails, phone numbers, card numbers and similar in input and output.' }),
        field('guardrails.maxInputChars', { type: 'number', label: 'Max input size', suffix: 'chars', min: 0, help: '0 = no limit.' }),
        field('guardrails.blockedMessage', { label: 'Blocked message', wide: true, help: 'Returned as the assistant reply when a block rule matches.' })),
    ];
  },

  /* ---------- hooks ---------- */
  hooks() {
    const events = catalog.hookEvents || FALLBACK_CATALOG.hookEvents;
    return [
      h('section', { class: 'card' },
        h('h2', { class: 'card-title', text: 'Webhook contract' }),
        h('p', { class: 'card-desc', text: 'Webhook hooks receive a POST with JSON. Blocking hooks wait for the reply and can veto or inject; non-blocking hooks are fire-and-forget.' }),
        h('div', { class: 'two-col' },
          codeBlock('POST <your url>\nContent-Type: application/json\n\n{\n  "event": "pre_tool",\n  "data": { "tool": "shell", "arguments": { ... }, "session": "..." }\n}', 'request'),
          codeBlock('// veto the step or tool call\n{ "action": "block", "message": "Not allowed" }\n\n// add text to the model\'s context\n{ "inject": "Remember: prices are in EUR." }\n\n// or anything else / empty body: continue', 'response')),
        h('dl', { class: 'event-list' }, events.map((e) => [h('dt', null, h('code', { text: e })), h('dd', { text: HOOK_EVENT_INFO[e] || '' })]))),
      section('Hooks', null,
        listEditor({
          path: 'hooks', noun: 'hook', addLabel: 'Add hook', focusKey: 'event',
          empty: 'No hooks.',
          itemLabel: (x) => `${x.event || '?'}  →  ${x.type || '?'}${x.type === 'webhook' && x.url ? '  ' + x.url : ''}`,
          newItem: () => ({ event: 'pre_tool', type: 'webhook', url: '', content: '', blocking: false, timeoutMs: 3000 }),
          renderItem: (x, _i, p) => [
            field(`${p}.event`, { type: 'select', label: 'Event', options: events, help: HOOK_EVENT_INFO[x.event], rerender: true }),
            field(`${p}.type`, {
              type: 'select', label: 'Type', rerender: true,
              options: [['webhook', 'webhook — POST to a URL'], ['log', 'log — write a log line'], ['inject', 'inject — add fixed text to context']],
            }),
            x.type === 'webhook' && field(`${p}.url`, { type: 'url', label: 'URL', mono: true, wide: true, omitEmpty: true, placeholder: 'https://example.com/hooks/agent', validate: (v) => (v ? '' : 'Required for webhook hooks') }),
            x.type !== 'webhook' && field(`${p}.content`, {
              type: 'textarea', label: x.type === 'inject' ? 'Text to inject' : 'Log message', rows: 3, omitEmpty: true,
              help: x.type === 'inject' ? 'Added to the model\'s context when the event fires.' : 'Optional label written with the event data.',
            }),
            field(`${p}.blocking`, { type: 'toggle', label: 'Blocking', help: 'Wait for the result. Required for a webhook to block or inject.' }),
            field(`${p}.timeoutMs`, { type: 'number', label: 'Timeout', suffix: 'ms', min: 0 }),
          ],
        })),
    ];
  },

  /* ---------- observability ---------- */
  observability() {
    return [
      section('Logging', null,
        field('observability.logLevel', { type: 'select', label: 'Log level', options: ['debug', 'info', 'warn', 'error'] }),
        field('observability.logFormat', { type: 'select', label: 'Log format', options: [['text', 'text — human readable'], ['json', 'json — for log pipelines']] })),
      section('Tracing', null,
        field('observability.traceFile', { label: 'Trace file', mono: true, wide: true, placeholder: './harness-data/trace.jsonl', help: 'JSONL file with every step, tool call and model request. Empty = no trace file.' }),
        field('observability.traceHeader', { type: 'toggle', label: 'Return X-Harness-Trace-Id header', help: 'Lets clients correlate a response with its trace events.' }),
        field('observability.includeUsage', { type: 'toggle', label: 'Include token usage in responses', help: 'Aggregated over every model call in the loop, including sub-agents.' })),
    ];
  },

  test: renderTest,
  build: renderBuild,
};

/* ------------------------------------------------------------------------ */
/* Prompt assembly preview                                                   */
/* ------------------------------------------------------------------------ */

function assemblePrompt() {
  const s = state.spec;
  const parts = [];
  if (s.prompt.system) parts.push(s.prompt.system.trim());
  for (const sec of s.prompt.sections || []) {
    if (sec.enabled && (sec.title || sec.content)) parts.push(`## ${sec.title || 'Untitled'}\n${(sec.content || '').trim()}`);
  }
  if (s.prompt.includeDateTime) parts.push(`Current date and time: ${new Date().toString().replace(/ \(.*\)$/, '')}`);
  if (s.prompt.includeToolSummary) {
    const tools = [
      ...(catalog.builtinTools || []).filter((t) => s.tools.builtins.includes(t.name)).map((t) => `- ${t.name}: ${t.description}`),
      ...s.tools.httpTools.filter((t) => t.name).map((t) => `- ${t.name}: ${t.description || ''}`),
      ...s.subagents.filter((a) => a.name).map((a) => `- agent_${a.name}: ${a.description || ''}`),
    ];
    if (tools.length) parts.push(`## Available tools\n${tools.join('\n')}`);
  }
  const skills = s.skills.items.filter((k) => k.name);
  if (skills.length) {
    if (s.skills.mode === 'eager') {
      parts.push(`## Skills\n${skills.map((k) => `### ${k.name}\n${k.description || ''}\n\n${(k.instructions || '').trim()}`).join('\n\n')}`);
    } else {
      parts.push(`## Skills\nLoad a skill's full instructions with the load_skill tool before using it.\n${skills.map((k) => `- ${k.name}: ${k.description || ''}`).join('\n')}`);
    }
  }
  const mem = [];
  if (s.memory.staticNotes && s.memory.staticNotes.trim()) mem.push(s.memory.staticNotes.trim());
  if (s.memory.persistent && s.memory.autoInject > 0) mem.push(`[up to ${s.memory.autoInject} most recent saved memories]`);
  if (mem.length) parts.push(`## Memory\n${mem.join('\n\n')}`);
  const policy = s.prompt.clientSystemPolicy;
  if (policy === 'append') parts.push('[+ client system message, if any]');
  if (policy === 'replace') return '[client system message, if any, replaces everything below]\n\n' + parts.join('\n\n');
  return parts.join('\n\n');
}

/* ------------------------------------------------------------------------ */
/* Loop lifecycle diagram                                                    */
/* ------------------------------------------------------------------------ */

function loopDiagram() {
  const s = state.spec;
  const l = s.loop;
  const counts = {};
  for (const hk of s.hooks) counts[hk.event] = (counts[hk.event] || 0) + 1;
  const guardIn = s.guardrails.inputRules.length + (s.guardrails.redactPII ? 1 : 0);
  const guardOut = s.guardrails.outputRules.length + (s.guardrails.redactPII ? 1 : 0);

  const hook = (ev) => h('span', { class: 'ld-node ld-hook' + (counts[ev] ? ' active' : ''), title: HOOK_EVENT_INFO[ev] },
    h('span', { class: 'mono', text: ev }), counts[ev] ? h('span', { class: 'ld-count', text: String(counts[ev]) }) : null);
  const node = (label, sub, extra = '') => h('span', { class: 'ld-node ' + extra }, h('span', { text: label }), sub && h('span', { class: 'ld-sub', text: sub }));
  const arrow = () => h('span', { class: 'ld-arrow', 'aria-hidden': 'true', text: '→' });
  const flow = (...items) => {
    const out = [];
    items.filter(Boolean).forEach((it, i) => { if (i) out.push(arrow()); out.push(it); });
    return out;
  };

  const toolChain = l.strategy !== 'single_shot' && h('div', { class: 'ld-tools' },
    h('span', { class: 'ld-caption', text: 'for each tool call' + (s.tools.parallel ? ` (≤${s.tools.maxParallel} in parallel)` : '') }),
    h('div', { class: 'ld-row' }, flow(hook('pre_tool'), node('permission check', s.permissions.defaultPolicy + ' by default'), node('execute'), hook('post_tool'))));

  const loopBox = h('div', { class: 'ld-loop' + (l.strategy === 'single_shot' ? ' single' : '') },
    h('span', { class: 'ld-caption', text: l.strategy === 'single_shot' ? 'one model call' : `repeat ≤ ${l.maxSteps} steps · ≤ ${l.maxToolCalls} tool calls · ${l.timeBudgetSec}s` }),
    h('div', { class: 'ld-row' }, flow(hook('pre_step'), node('model call', s.model.model || 'model', 'ld-model'))),
    toolChain);

  return h('div', { class: 'loop-diagram', role: 'img', 'aria-label': 'Agent loop lifecycle diagram' },
    h('div', { class: 'ld-row ld-main' }, flow(
      node('request'),
      node('input guardrails', guardIn ? `${guardIn} rule${guardIn === 1 ? '' : 's'}` : 'none'),
      hook('turn_start'),
      l.strategy === 'plan_execute' && node('plan', 'planning call', 'ld-plan'),
      loopBox,
      l.selfCheck.enabled && node('self-check', `≤ ${l.selfCheck.maxRevisions} revision${l.selfCheck.maxRevisions === 1 ? '' : 's'}`, 'ld-plan'),
      node('output guardrails', guardOut ? `${guardOut} rule${guardOut === 1 ? '' : 's'}` : 'none'),
      hook('turn_end'),
      node('response'))),
    h('div', { class: 'ld-error' }, hook('on_error'), h('span', { class: 'muted small', text: `fires on any model or tool error · on tool error: ${l.onToolError} · at limits: ${l.onMaxSteps}` })));
}

/* ------------------------------------------------------------------------ */
/* Test step (playground)                                                    */
/* ------------------------------------------------------------------------ */

function resetPreviewState() {
  if (preview.abort) preview.abort.abort();
  Object.assign(preview, { id: null, baseUrl: null, snapshot: null, messages: [], busy: false, starting: false, abort: null, errors: [], error: '', traceId: '', notes: [], sessionId: newId() });
}

function renderTest() {
  const s = state.spec;
  const envs = envVarsUsed();
  const stale = preview.baseUrl && preview.snapshot !== JSON.stringify(s);

  const status = preview.baseUrl
    ? h('div', { class: 'preview-status ok' },
      h('span', { class: 'dot', 'aria-hidden': 'true' }),
      h('span', null, 'Test instance running at ', h('code', { text: preview.baseUrl })),
      stale && h('span', { class: 'stale', text: 'Spec changed since start — restart to apply.' }))
    : h('div', { class: 'preview-status' }, h('span', { class: 'dot', 'aria-hidden': 'true' }), h('span', { text: 'No test instance running.' }));

  const startBtn = h('button', {
    type: 'button', class: 'btn btn-primary', disabled: preview.starting || !online,
    text: preview.starting ? 'Starting…' : preview.baseUrl ? 'Restart test instance' : 'Start test instance',
    onclick: startPreview,
  });

  const errBox = (preview.errors.length || preview.error) && callout('danger',
    h('strong', { text: preview.errors.length ? 'The spec has problems:' : 'Could not start the test instance' }),
    preview.error && h('p', { class: 'mono small', text: preview.error }),
    preview.errors.length > 0 && errorList(preview.errors));

  const intro = catalog.public
    ? callout('info',
      h('p', { text: 'This test runs on a shared server. Paste your own model API key below: it stays in this browser tab\'s memory and on the server only while the test instance runs. It is never saved, and server environment variables are never read.' }),
      h('p', { text: 'For safety, shell commands and stdio MCP servers are disabled here, and tools cannot reach private networks. They all work in the binary you download.' }))
    : callout('info',
      h('p', null, 'The test instance runs inside the builder process on this machine, so it uses the builder\'s environment variables. ',
        envs.length ? ['Make sure ', envs.map((e, i) => [i ? ', ' : '', h('code', { text: e.name })]), ' ', envs.length === 1 ? 'is' : 'are', ' set where you started OpenHarness.'] : 'This spec does not reference any API key env vars.'),
      s.tools.builtins.some((t) => ['shell', 'write_file'].includes(t)) && h('p', { text: 'File and shell tools act on the real workspace root relative to the builder\'s working directory.' }));

  // Deliberately not bound to the spec: the key must never be saved or built in.
  const keyField = catalog.public && h('div', { class: 'card preview-key field' },
    h('label', { class: 'label', for: 'preview-api-key', text: 'Your model API key for this test' }),
    h('input', {
      id: 'preview-api-key', type: 'password', class: 'mono', autocomplete: 'off', spellcheck: 'false',
      placeholder: s.model.provider === 'anthropic' ? 'sk-ant-…' : 'sk-…', value: preview.apiKey || '',
      oninput: (e) => { preview.apiKey = e.target.value; },
    }),
    h('p', { class: 'help', text: 'Used for the main model and any fallbacks without their own inline key. Leave empty for keyless endpoints.' }));

  const notes = preview.baseUrl && preview.notes && preview.notes.length > 0 && callout('warn',
    h('strong', { text: 'Adjusted for the shared test server:' }),
    h('ul', null, preview.notes.map((n) => h('li', { text: n }))));

  return [
    intro,
    keyField,
    !online && callout('warn', 'The builder backend is not reachable, so the playground is unavailable.'),
    h('div', { class: 'card preview-bar' }, status, h('div', { class: 'preview-actions' }, startBtn)),
    errBox,
    notes,
    preview.baseUrl && chatPanel(),
  ];
}

async function startPreview() {
  if (preview.abort) preview.abort.abort();
  preview.starting = true;
  preview.errors = [];
  preview.error = '';
  renderStep({ keepScroll: true });
  try {
    const res = await fetch('/api/preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ spec: state.spec, apiKey: catalog.public ? (preview.apiKey || '') : undefined }) });
    const j = await readJSON(res);
    if (!res.ok || !j || !j.baseUrl) {
      preview.baseUrl = null;
      if (j && Array.isArray(j.errors) && j.errors.length) preview.errors = j.errors;
      else preview.error = errorMessage(j, res);
    } else {
      preview.id = j.id;
      preview.baseUrl = j.baseUrl.replace(/\/$/, '');
      preview.snapshot = JSON.stringify(state.spec);
      preview.sessionMode = state.spec.sessions.mode;
      preview.headerName = state.spec.sessions.headerName || 'X-Session-Id';
      preview.modelId = state.spec.server.modelId || state.spec.name;
      preview.messages = [];
      preview.sessionId = newId();
      preview.traceId = '';
      preview.notes = Array.isArray(j.notes) ? j.notes : [];
    }
  } catch (e) {
    preview.error = 'Network error: ' + e.message;
  }
  preview.starting = false;
  renderStep({ keepScroll: true });
  if (preview.baseUrl) setTimeout(() => { const ta = document.getElementById('chat-input'); if (ta) ta.focus(); }, 30);
}

function chatPanel() {
  const log = h('div', { class: 'chat-log', id: 'chat-log', 'aria-live': 'polite' });
  renderChatLog(log);
  const input = h('textarea', { id: 'chat-input', rows: 2, placeholder: 'Message your agent…  (Enter to send, Shift+Enter for a new line)', 'aria-label': 'Message' });
  const send = h('button', { type: 'button', class: 'btn btn-primary', id: 'chat-send', text: preview.busy ? 'Stop' : 'Send' });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); if (!preview.busy) sendMessage(input); }
  });
  send.addEventListener('click', () => { if (preview.busy) { if (preview.abort) preview.abort.abort(); } else sendMessage(input); });
  return h('section', { class: 'card chat' },
    h('div', { class: 'chat-head' },
      h('div', null,
        h('h2', { class: 'card-title', text: 'Playground' }),
        h('p', { class: 'muted small' }, 'model ', h('code', { text: preview.modelId }), ' · session ', h('code', { text: preview.sessionId.slice(0, 8) }),
          preview.sessionMode === 'server' ? ' · server sessions: only the newest message is sent' : ' · stateless: full history is sent')),
      h('button', {
        type: 'button', class: 'btn btn-secondary btn-sm', text: 'Reset conversation',
        onclick: () => { if (preview.abort) preview.abort.abort(); preview.messages = []; preview.sessionId = newId(); preview.traceId = ''; renderStep({ keepScroll: true }); },
      })),
    log,
    h('div', { class: 'chat-input-row' }, input, send));
}

function renderChatLog(log = document.getElementById('chat-log')) {
  if (!log) return;
  log.replaceChildren();
  if (preview.messages.length === 0) {
    log.append(h('p', { class: 'chat-empty muted', text: 'Say hello. Agent activity (plans, tool calls, compaction) appears above each answer.' }));
    return;
  }
  for (const m of preview.messages) log.append(messageEl(m));
  log.scrollTop = log.scrollHeight;
}

function messageEl(m) {
  if (m.role === 'user') return h('div', { class: 'msg msg-user' }, h('div', { class: 'msg-body', text: m.content }));
  const body = h('div', { class: 'msg-body md' });
  body.append(renderMarkdown(m.content || ''));
  if (!m.content && m.pending) body.append(h('span', { class: 'typing', 'aria-label': 'Waiting for response' }, h('i'), h('i'), h('i')));
  return h('div', { class: 'msg msg-assistant' + (m.error ? ' msg-error' : '') },
    m.activity && h('details', { class: 'activity', open: m.activityOpen ?? (!!m.pending && !m.content), ontoggle: (e) => { m.activityOpen = e.target.open; } },
      h('summary', null, 'Agent activity', h('span', { class: 'muted small', text: `  ${m.activity.split('\n').filter(Boolean).length} lines` })),
      h('pre', { class: 'mono', text: m.activity })),
    (m.content || m.pending || !m.error) && body,
    m.error && h('p', { class: 'msg-err mono small', text: m.error }),
    (m.traceId || m.usage) && h('p', { class: 'msg-meta mono' },
      m.traceId && `trace ${m.traceId}`,
      m.traceId && m.usage && '  ·  ',
      m.usage && `${m.usage.prompt_tokens ?? '?'} in / ${m.usage.completion_tokens ?? '?'} out tokens`));
}

async function sendMessage(input) {
  const text = input.value.trim();
  if (!text || !preview.baseUrl) return;
  input.value = '';
  preview.messages.push({ role: 'user', content: text });
  const reply = { role: 'assistant', content: '', activity: '', pending: true, error: '', traceId: '', usage: null };
  preview.messages.push(reply);
  preview.busy = true;
  const ctrl = new AbortController();
  preview.abort = ctrl;
  updateChatControls();
  renderChatLog();

  const history = preview.messages.filter((m) => m !== reply && !m.error).map((m) => ({ role: m.role, content: m.content }));
  const messages = preview.sessionMode === 'server' ? [{ role: 'user', content: text }] : history;
  const headers = { 'Content-Type': 'application/json', Accept: 'text/event-stream', 'X-Session-Id': preview.sessionId };
  if (preview.headerName && preview.headerName.toLowerCase() !== 'x-session-id') headers[preview.headerName] = preview.sessionId;

  let frame = 0;
  const repaint = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(() => renderChatLog()); };

  try {
    const res = await fetch(preview.baseUrl + '/chat/completions', {
      method: 'POST', headers, signal: ctrl.signal,
      body: JSON.stringify({ model: preview.modelId, messages, stream: true }),
    });
    reply.traceId = res.headers.get('X-Harness-Trace-Id') || '';
    if (!res.ok) {
      const j = await readJSON(res);
      throw new Error(errorMessage(j, res));
    }
    const ct = res.headers.get('Content-Type') || '';
    if (ct.includes('application/json')) {
      const j = await res.json();
      const msg = j.choices && j.choices[0] && j.choices[0].message;
      reply.content = (msg && msg.content) || '';
      if (msg && msg.reasoning_content) reply.activity = msg.reasoning_content;
      reply.usage = j.usage || null;
    } else {
      await readSSE(res, (chunk) => {
        if (chunk.error) throw new Error(chunk.error.message || String(chunk.error));
        const d = (chunk.choices && chunk.choices[0] && chunk.choices[0].delta) || {};
        if (d.reasoning_content) reply.activity += d.reasoning_content;
        if (d.content) reply.content += d.content;
        if (chunk.usage) reply.usage = chunk.usage;
        repaint();
      });
    }
  } catch (e) {
    if (e.name === 'AbortError') reply.error = 'Stopped.';
    else reply.error = e.message || String(e);
  }
  reply.pending = false;
  if (reply.traceId) preview.traceId = reply.traceId;
  preview.busy = false;
  preview.abort = null;
  cancelAnimationFrame(frame);
  renderChatLog();
  updateChatControls();
}

async function readSSE(res, onChunk) {
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let idx;
    while ((idx = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, idx).replace(/\r$/, '');
      buf = buf.slice(idx + 1);
      if (!line.startsWith('data:')) continue;
      const data = line.slice(5).trim();
      if (!data) continue;
      if (data === '[DONE]') { try { await reader.cancel(); } catch { /* ignore */ } return; }
      let chunk;
      try { chunk = JSON.parse(data); } catch { continue; }
      onChunk(chunk);
    }
  }
}

function updateChatControls() {
  const send = document.getElementById('chat-send');
  if (send) send.textContent = preview.busy ? 'Stop' : 'Send';
}

/** Minimal, safe markdown: fenced code, inline code, bold, paragraphs, line breaks. */
function renderMarkdown(text) {
  const frag = document.createDocumentFragment();
  const re = /```([\w+-]*)[^\n]*\n?([\s\S]*?)(?:```|$)/g;
  let last = 0;
  let m;
  const addText = (t) => {
    for (const para of t.split(/\n{2,}/)) {
      if (!para.trim()) continue;
      const p = document.createElement('p');
      p.innerHTML = inlineMd(para.replace(/^\n+|\n+$/g, ''));
      frag.append(p);
    }
  };
  while ((m = re.exec(text)) !== null) {
    if (m.index === re.lastIndex) { re.lastIndex++; continue; }
    addText(text.slice(last, m.index));
    const pre = h('pre', { class: 'code' }, h('code', { text: m[2].replace(/\n$/, '') }));
    frag.append(m[1] ? h('div', { class: 'code-wrap' }, h('span', { class: 'code-lang', text: m[1] }), pre) : pre);
    last = re.lastIndex;
  }
  addText(text.slice(last));
  return frag;
}

function escapeHTML(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function inlineMd(s) {
  // Escape first; the replacements below only add our own tags.
  const codes = [];
  let out = escapeHTML(s).replace(/`([^`\n]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000`; });
  out = out
    .replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>')
    .replace(/^#{1,6}\s+(.*)$/gm, '<strong>$1</strong>')
    .replace(/\n/g, '<br>');
  return out.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[Number(i)]}</code>`);
}

/* ------------------------------------------------------------------------ */
/* Build step                                                                */
/* ------------------------------------------------------------------------ */

function errorList(errors) {
  return h('ul', { class: 'err-list' }, errors.map((e) => {
    const stepId = errorStep(e);
    const title = stepId && (steps().find((s) => s.id === stepId) || {}).title;
    const fieldPath = (/^([a-zA-Z0-9_.[\]]+):/.exec(e) || [])[1];
    const focusPath = fieldPath ? fieldPath.replace(/\[(\d+)\]/g, '.$1') : null;
    return h('li', null,
      h('span', { class: 'mono small', text: e }),
      stepId && h('button', { type: 'button', class: 'link-btn small', text: `Fix in ${title} →`, onclick: () => goTo(stepId, focusPath) }));
  }));
}

function envVarsUsed() {
  const s = state.spec;
  const out = [];
  const seen = new Set();
  const add = (name, why) => { if (name && !seen.has(name)) { seen.add(name); out.push({ name, why }); } };
  if (!s.model.apiKey) add(s.model.apiKeyEnv, `API key for ${s.model.model || 'the primary model'}`);
  for (const f of s.model.fallbacks) if (!f.apiKey) add(f.apiKeyEnv, `API key for fallback ${f.model || ''}`.trim());
  if (s.server.authMode === 'bearer') add(s.server.apiKeyEnv, 'comma-separated keys clients may use');
  return out;
}

function targetList() { return catalog.targets && catalog.targets.length ? catalog.targets : FALLBACK_CATALOG.targets; }

function hostTargetKey() {
  const ht = catalog.hostTarget;
  if (ht && ht.os) return `${ht.os}/${ht.arch}`;
  const ua = navigator.userAgent || '';
  if (/Mac/.test(ua)) return 'darwin/arm64';
  if (/Win/.test(ua)) return 'windows/amd64';
  return 'linux/amd64';
}

function currentTarget() {
  const key = state.target || hostTargetKey();
  const [os, arch] = key.split('/');
  const t = targetList().find((x) => x.os === os && x.arch === arch);
  return t || targetList()[0];
}

function renderBuild() {
  const t = currentTarget();
  const errsHost = h('div', { class: 'preflight' });
  const paintErrors = () => {
    errsHost.replaceChildren();
    if (!online) { errsHost.append(callout('warn', 'The builder backend is not reachable. Validation and builds need it.')); return; }
    if (lastErrors == null) { errsHost.append(h('p', { class: 'muted', text: 'Checking spec…' })); return; }
    if (lastErrors.length === 0) {
      errsHost.append(h('div', { class: 'preflight-ok' }, h('span', { class: 'ok-mark', 'aria-hidden': 'true', text: '✓' }), h('span', { text: 'All checks passed. Ready to build.' })));
      return;
    }
    errsHost.append(callout('danger', h('strong', { text: `${lastErrors.length} problem${lastErrors.length === 1 ? '' : 's'} to fix before building` }), errorList(lastErrors)));
  };
  paintErrors();
  validationListeners.push(paintErrors);
  validationListeners.push(() => {
    const b = document.getElementById('build-btn');
    if (b && !buildState.busy) b.disabled = !online || (lastErrors && lastErrors.length > 0);
  });
  if (online) validateNow();

  const targetSel = h('select', { id: 'target-select' }, targetList().map((x) => h('option', { value: `${x.os}/${x.arch}`, selected: x === t, text: x.label + (catalog.hostTarget && x.os === catalog.hostTarget.os && x.arch === catalog.hostTarget.arch ? '  (this machine)' : '') })));
  targetSel.addEventListener('change', () => { state.target = targetSel.value; save(); renderStep({ keepScroll: true }); });

  const elapsed = live(() => buildState.busy ? `${Math.floor((Date.now() - buildState.startedAt) / 1000)}s elapsed` : '', 'span', 'muted small mono');

  const buildBtn = h('button', {
    type: 'button', id: 'build-btn', class: 'btn btn-primary btn-lg', disabled: buildState.busy || !online || (lastErrors && lastErrors.length > 0),
    text: buildState.busy ? 'Building…' : `Build binary for ${t.label}`,
    onclick: runBuild,
  });

  return [
    section('Pre-flight check', null, h('div', { class: 'field wide' }, errsHost,
      h('button', { type: 'button', class: 'btn btn-ghost btn-sm', text: 'Re-check', disabled: !online, onclick: validateNow }))),
    h('section', { class: 'card build-card' },
      h('h2', { class: 'card-title', text: 'Compile' }),
      h('div', { class: 'grid' },
        h('div', { class: 'field' }, h('label', { class: 'label', for: 'target-select', text: 'Target platform' }), targetSel,
          h('p', { class: 'help', text: 'Cross-compiled with Go. No runtime dependencies on the target.' })),
        h('div', { class: 'field' }, h('span', { class: 'label', text: 'What gets built' }),
          h('p', { class: 'help' }, 'A single executable with this spec embedded. It serves ', h('code', { text: `/v1/chat/completions` }), ' as ', h('code', { text: state.spec.server.modelId }), '.'))),
      h('div', { class: 'build-actions' }, buildBtn, elapsed),
      buildState.busy && h('div', { class: 'progress', role: 'progressbar', 'aria-label': 'Building binary', 'aria-busy': 'true' }, h('span')),
      buildState.busy && h('p', { class: 'muted small', text: 'Compiling the runtime with your spec. This usually takes 5–30 seconds.' }),
      buildState.error && callout('danger', h('strong', { text: 'Build failed' }), buildState.errors.length ? errorList(buildState.errors) : h('pre', { class: 'mono small pre-wrap', text: buildState.error })),
      buildState.result && h('div', { class: 'build-result' },
        h('span', { class: 'ok-mark', 'aria-hidden': 'true', text: '✓' }),
        h('span', null, 'Downloaded ', h('code', { text: buildState.result.filename }), ` · ${formatBytes(buildState.result.size)} · ${buildState.result.label}`))),
    howToRun(t),
    section('Spec', 'Keep the spec next to your code. Any binary can run a different spec with --spec.',
      h('div', { class: 'field wide button-row' },
        h('button', { type: 'button', class: 'btn btn-secondary', text: 'Export spec JSON', onclick: exportSpec }),
        h('button', { type: 'button', class: 'btn btn-secondary', text: 'Copy spec', onclick: () => copyText(specJSON()) }))),
  ];
}

async function runBuild() {
  const t = currentTarget();
  const errs = await validateNow();
  if (errs && errs.length) { renderStep({ keepScroll: true }); return; }
  buildState.busy = true;
  buildState.error = '';
  buildState.errors = [];
  buildState.result = null;
  buildState.startedAt = Date.now();
  clearInterval(buildState.timer);
  buildState.timer = setInterval(runLive, 1000);
  renderStep({ keepScroll: true });
  try {
    const res = await fetch('/api/build', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ spec: state.spec, os: t.os, arch: t.arch }),
    });
    if (!res.ok) {
      const j = await readJSON(res);
      if (j && Array.isArray(j.errors) && j.errors.length) buildState.errors = j.errors;
      throw new Error(errorMessage(j, res));
    }
    const blob = await res.blob();
    const filename = filenameFrom(res.headers.get('Content-Disposition')) || defaultBinaryName(t);
    downloadBlob(blob, filename);
    buildState.result = { filename, size: blob.size, label: t.label, os: t.os };
    toast('Binary downloaded');
  } catch (e) {
    buildState.error = e.message || String(e);
  }
  buildState.busy = false;
  clearInterval(buildState.timer);
  renderStep({ keepScroll: true });
}

function filenameFrom(cd) {
  if (!cd) return '';
  const star = /filename\*\s*=\s*(?:UTF-8'')?([^;]+)/i.exec(cd);
  if (star) { try { return decodeURIComponent(star[1].trim().replace(/^"|"$/g, '')); } catch { /* fall through */ } }
  const plain = /filename\s*=\s*"?([^";]+)"?/i.exec(cd);
  return plain ? plain[1].trim() : '';
}

function defaultBinaryName(t) {
  return `${state.spec.name || 'agent'}${t.os === 'windows' ? '.exe' : ''}`;
}

function howToRun(t) {
  const s = state.spec;
  const file = buildState.result && buildState.result.os === t.os ? buildState.result.filename : defaultBinaryName(t);
  const win = t.os === 'windows';
  const host = s.server.host === '0.0.0.0' || !s.server.host ? '127.0.0.1' : s.server.host;
  const base = `http://${host}:${s.server.port}/v1`;
  const envs = envVarsUsed();
  const bearer = s.server.authMode === 'bearer';
  const serverSess = s.sessions.mode === 'server';
  const sessHeader = s.sessions.headerName || 'X-Session-Id';

  let shell;
  if (win) {
    shell = [
      '# PowerShell',
      `Unblock-File .\\${file}   # if Windows SmartScreen blocks it`,
      ...envs.map((e) => `$env:${e.name} = "<${e.why}>"`),
      `.\\${file}`,
      '',
      '# flags',
      `.\\${file} --port 9000 --host 0.0.0.0   # override listen address`,
      `.\\${file} --print-spec                 # print the embedded spec`,
      `.\\${file} --spec my-spec.json          # run with a different spec`,
    ].join('\n');
  } else {
    shell = [
      `chmod +x ./${file}`,
      t.os === 'darwin' ? `xattr -d com.apple.quarantine ./${file}   # only if macOS Gatekeeper blocks it` : null,
      ...envs.map((e) => `export ${e.name}="<${e.why}>"`),
      `./${file}`,
      '',
      '# flags',
      `./${file} --port 9000 --host 0.0.0.0   # override listen address`,
      `./${file} --print-spec                 # print the embedded spec`,
      `./${file} --spec my-spec.json          # run with a different spec`,
    ].filter((x) => x !== null).join('\n');
  }

  const body = JSON.stringify({ model: s.server.modelId, messages: [{ role: 'user', content: 'Hello! What can you do?' }] });
  const curlLines = [
    `curl ${base}/chat/completions`,
    `  -H "Content-Type: application/json"`,
    bearer ? (win ? `  -H "Authorization: Bearer $env:${s.server.apiKeyEnv}"` : `  -H "Authorization: Bearer $${s.server.apiKeyEnv}"`) : null,
    serverSess ? `  -H "${sessHeader}: demo-1"` : null,
    win ? `  -d '${body.replace(/"/g, '\\"')}'` : `  -d '${body}'`,
  ].filter(Boolean);
  const curl = (win ? curlLines.join(' `\n').replace(/^curl/, 'curl.exe') : curlLines.join(' \\\n'));

  const py = [
    'from openai import OpenAI',
    bearer ? 'import os' : null,
    '',
    'client = OpenAI(',
    `    base_url="${base}",`,
    bearer ? `    api_key=os.environ["${s.server.apiKeyEnv}"].split(",")[0],` : '    api_key="not-needed",  # auth mode is none',
    ')',
    '',
    'resp = client.chat.completions.create(',
    `    model="${s.server.modelId}",`,
    '    messages=[{"role": "user", "content": "Hello! What can you do?"}],',
    serverSess ? `    extra_headers={"${sessHeader}": "demo-1"},` : null,
    ')',
    'print(resp.choices[0].message.content)',
  ].filter((x) => x !== null).join('\n');

  return h('details', { class: 'card how-to', open: !!buildState.result },
    h('summary', { class: 'card-title' }, 'How to run', h('span', { class: 'muted small', text: `  ·  ${t.label}` })),
    envs.length > 0 && h('div', { class: 'env-table' },
      h('p', { class: 'label', text: 'Environment variables this binary reads' }),
      h('ul', null, envs.map((e) => h('li', null, h('code', { text: e.name }), h('span', { class: 'muted small', text: '  ' + e.why }))))),
    h('h3', { class: 'subhead', text: '1. Start the server' }),
    codeBlock(shell, win ? 'powershell' : 'shell'),
    h('h3', { class: 'subhead', text: '2. Call it with curl' }),
    codeBlock(curl, win ? 'powershell' : 'shell'),
    h('h3', { class: 'subhead', text: '3. Or with the OpenAI Python SDK' }),
    codeBlock(py, 'python'),
    h('p', { class: 'help' }, 'Any OpenAI-compatible client works: set the base URL to ', h('code', { text: base }), ' and the model to ', h('code', { text: s.server.modelId }), '.'));
}

/* ------------------------------------------------------------------------ */
/* Import / export / reset                                                   */
/* ------------------------------------------------------------------------ */

function exportSpec() {
  const blob = new Blob([specJSON() + '\n'], { type: 'application/json' });
  downloadBlob(blob, `${state.spec.name || 'agent'}.harness.json`);
}

async function importFile(file) {
  try {
    const text = await file.text();
    const j = JSON.parse(text);
    const raw = isPlainObject(j) && isPlainObject(j.spec) ? j.spec : j;
    if (!isPlainObject(raw)) throw new Error('Expected a JSON object');
    startWith(raw);
    toast(`Imported ${state.spec.name}`);
  } catch (e) {
    toast('Import failed: ' + e.message, 'error');
  }
}

function resetAll() {
  if (!window.confirm('Discard this agent and start over? Export the spec first if you want to keep it.')) return;
  clearSaved();
  resetPreviewState();
  buildState.result = null;
  buildState.error = '';
  state.spec = normalizeSpec(catalog.defaults);
  state.started = false;
  state.visited = new Set();
  state.step = steps()[0].id;
  lastErrors = null;
  render();
}

/* ------------------------------------------------------------------------ */
/* Boot                                                                      */
/* ------------------------------------------------------------------------ */

async function loadCatalog() {
  try {
    const res = await fetch('/api/catalog', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const j = await res.json();
    catalog = {
      ...FALLBACK_CATALOG,
      ...j,
      components: j.components && j.components.length ? j.components : FALLBACK_CATALOG.components,
      builtinTools: j.builtinTools && j.builtinTools.length ? j.builtinTools : FALLBACK_CATALOG.builtinTools,
      hookEvents: j.hookEvents && j.hookEvents.length ? j.hookEvents : FALLBACK_CATALOG.hookEvents,
      targets: j.targets && j.targets.length ? j.targets : FALLBACK_CATALOG.targets,
      defaults: j.defaults || FALLBACK_DEFAULTS,
      presets: j.presets || [],
    };
    online = true;
  } catch {
    catalog = FALLBACK_CATALOG;
    online = false;
  }
}

/* ------------------------------------------------------------------------ */
/* Theme toggle                                                              */
/* ------------------------------------------------------------------------ */

const THEME_KEY = 'openharness.theme';
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');
const ICON_SUN = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="square"><rect x="5" y="5" width="6" height="6"/><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3 3l1.4 1.4M11.6 11.6L13 13M3 13l1.4-1.4M11.6 4.4L13 3"/></svg>';
const ICON_MOON = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="miter"><path d="M13.5 9.8A6 6 0 0 1 6.2 2.5a6 6 0 1 0 7.3 7.3z"/></svg>';

function effectiveTheme() {
  return document.documentElement.dataset.theme || (darkQuery.matches ? 'dark' : 'light');
}

function updateThemeButton() {
  const btn = document.getElementById('btn-theme');
  if (!btn) return;
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  btn.innerHTML = next === 'light' ? ICON_SUN : ICON_MOON; // static, trusted markup
  btn.setAttribute('aria-label', `Switch to ${next} theme`);
  btn.title = `Switch to ${next} theme`;
}

function wireTheme() {
  document.getElementById('btn-theme').addEventListener('click', () => {
    const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem(THEME_KEY, next); } catch { /* private mode: choice lasts for this page only */ }
    updateThemeButton();
  });
  // Without an explicit choice the page follows the system; keep the icon in sync.
  darkQuery.addEventListener('change', updateThemeButton);
  updateThemeButton();
}

function wireTopbar() {
  wireTheme();
  document.getElementById('brand').addEventListener('click', () => {
    if (!state.started) return;
    state.started = false;
    // Keep the draft marked as started so the start screen offers "Resume".
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ spec: state.spec, started: true, step: state.step, visited: [...state.visited], target: state.target, showSpec: state.showSpec }));
    } catch { /* ignore */ }
    render();
  });
  document.getElementById('btn-spec').addEventListener('click', toggleSpec);
  document.getElementById('btn-export').addEventListener('click', exportSpec);
  document.getElementById('btn-reset').addEventListener('click', resetAll);
  const fileInput = document.getElementById('input-import');
  document.getElementById('btn-import').addEventListener('click', () => {
    if (state.started && !window.confirm('Importing replaces the current agent. Continue?')) return;
    fileInput.click();
  });
  fileInput.addEventListener('change', () => {
    const f = fileInput.files && fileInput.files[0];
    if (f) importFile(f);
    fileInput.value = '';
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && state.showSpec && window.innerWidth < 1200) toggleSpec();
  });
}

async function init() {
  wireTopbar();
  await loadCatalog();
  const saved = loadSaved();
  if (saved && saved.spec && saved.started) {
    resumeDraft(saved);
  } else {
    state.spec = normalizeSpec(catalog.defaults);
    state.step = steps()[0].id;
    state.target = (saved && saved.target) || hostTargetKey();
    render();
  }
  if (!state.target) state.target = hostTargetKey();
}

init();
