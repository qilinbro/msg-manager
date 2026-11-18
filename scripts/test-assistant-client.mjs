import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';

// Execute the shipped clients, transport and React components with synthetic
// browser APIs. This is not a browser, real-model, real-account or Tauri test.
const TOKEN = 'synthetic-memory-only-business-token';
const SERVICE_URL = 'http://127.0.0.1:17890';
const FUTURE = '2099-12-31T23:59:59Z';
const consent = {
  model_config_id: 'synthetic-environment-config',
  model_config_revision: 0,
  confirmed: true,
};
class ServiceError extends Error {
  constructor(message, code, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
}
const status = () => ({
  configured: true,
  model: 'synthetic-model',
  endpoint_host: 'synthetic-model.invalid',
  external: true,
  model_config_id: consent.model_config_id,
  model_config_revision: 0,
  daily_limit: 50,
  used_today: 2,
  session_ttl_seconds: 1800,
  sending_enabled: false,
  operations: [
    {
      id: 'read_synthetic',
      summary: '读取合成记录',
      method: 'GET',
      path: '/api/v1/synthetic',
      effect: 'read',
      availability: 'available',
      reason: '合成读取范围',
    },
    {
      id: 'write_synthetic',
      summary: '修改合成记录',
      method: 'PATCH',
      path: '/api/v1/synthetic',
      effect: 'write',
      availability: 'confirmation_required',
      reason: '合成逐项确认',
    },
    {
      id: 'human_model_settings',
      summary: '人工模型设置',
      method: 'PATCH',
      path: '/api/v1/ai/config',
      effect: 'credential',
      availability: 'human_only',
      reason: '合成人工操作',
    },
  ],
});
const action = () => ({
  id: 'synthetic-action',
  operation_id: 'write_synthetic',
  method: 'PATCH',
  path: '/api/v1/synthetic',
  arguments: { id: 'synthetic-record', mode: 'paused' },
  effects: ['修改这条合成记录'],
  expires_at: FUTURE,
});
const tool = (state = 'succeeded') => ({
  call_id: 'synthetic-call',
  operation_id: 'write_synthetic',
  state,
  http_status: state === 'succeeded' ? 200 : null,
  data: { actual_synthetic_record: true },
  error_code: '',
});
const run = (state = 'completed', extra = {}) => ({
  session_id: 'synthetic-session',
  run_id: 'synthetic-run',
  state,
  assistant_message: '模型文字不是写入凭证。',
  tool_results: state === 'completed' ? [tool()] : [],
  pending_action: state === 'awaiting_confirmation' ? action() : null,
  model_config_id: consent.model_config_id,
  model_config_revision: 0,
  expires_at: FUTURE,
  error: null,
  ...extra,
});
const schedule = () => ({
  enabled: false,
  local_time: '18:30',
  timezone: 'Asia/Shanghai',
  revision: 1,
  last_run_day: '',
  model_config_id: '',
  model_config_revision: 0,
});
const report = (state = 'succeeded') => ({
  id: 'synthetic-report',
  day: '2099-12-31',
  state,
  summary: '合成摘要',
  reminders: [],
  evidence_ids: [],
  conversation_count: 0,
  generated_at: FUTURE,
});
const reply = (payload, code = 200) => ({
  ok: code >= 200 && code < 300,
  status: code,
  json: async () => structuredClone(payload),
});
const tick = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};
const event = () => ({ preventDefault() {} });
function sourceFor(file) {
  return readFileSync(
    new URL(`../apps/desktop/src/${file}`, import.meta.url),
    'utf8',
  )
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^export default /gm, '')
    .replace(/^export /gm, '');
}
function transpile(file, source) {
  const output = transformSync(file, source, {
    jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
  });
  assert.equal(
    output.errors.length,
    0,
    `${file} must compile for synthetic execution`,
  );
  return output.code;
}
function transport() {
  const requests = [],
    timers = new Map(),
    intervals = new Map();
  let responder = () => reply(status()),
    timerID = 0,
    storageReads = 0;
  const window = {
    setTimeout(callback, ms) {
      timers.set(++timerID, { callback, ms });
      return timerID;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    setInterval(callback, ms) {
      intervals.set(++timerID, { callback, ms });
      return timerID;
    },
    clearInterval(id) {
      intervals.delete(id);
    },
    get sessionStorage() {
      storageReads++;
      throw Error(
        'No synthetic credential or consent may enter sessionStorage',
      );
    },
    get localStorage() {
      storageReads++;
      throw Error('No synthetic credential or consent may enter localStorage');
    },
  };
  const globals = {
    SERVICE: { url: SERVICE_URL, error: null },
    ServiceError,
    AbortController,
    DOMException,
    TextEncoder,
    window,
    URL,
    Date,
    Map,
    Set,
    Error,
    setTimeout: window.setTimeout,
    clearTimeout: window.clearTimeout,
    fetch(url, options) {
      assert.ok(
        url.startsWith(`${SERVICE_URL}/api/v1/`),
        'only the configured local service is a transport target',
      );
      assert.equal(
        new URL(url).search,
        '',
        'business credential and arguments must not enter query strings',
      );
      assert.equal(options.credentials, 'omit');
      assert.equal(options.cache, 'no-store');
      assert.equal(options.headers.Authorization, `Bearer ${TOKEN}`);
      const request = { url, options };
      requests.push(request);
      return responder(request);
    },
  };
  Object.assign(
    globals,
    runInNewContext(
      `(function(){\n${transpile('assistant-help.ts', sourceFor('assistant-help.ts'))}\nreturn {ASSISTANT_MESSAGE_BYTE_LIMIT, inspectAssistantMessage, normalizeAssistantHelpRequest, prepareAssistantHelpDraft};\n})()`,
      globals,
      { filename: 'assistant-help.ts' },
    ),
  );
  return {
    requests,
    timers,
    intervals,
    window,
    get storageReads() {
      return storageReads;
    },
    respond(next) {
      responder = next;
    },
    load(file, names) {
      return runInNewContext(
        `(function(){\n${deviceAuthSource}\n${transpile(file, sourceFor(file))}\nreturn {${names.join(',')}};\n})()`,
        globals,
        { filename: file },
      );
    },
    fireTimeout(ms) {
      const pending = [...timers.values()].find((item) => item.ms === ms);
      assert.ok(pending, `missing ${ms}ms timeout`);
      pending.callback();
    },
  };
}
const assistantExports = [
  'AssistantRequestError',
  'getAssistantStatus',
  'createAssistantRun',
  'getAssistantRun',
  'confirmAssistantAction',
  'rejectAssistantAction',
  'isAssistantBusinessAuthFailure',
];
const signal = () => new AbortController().signal;
function waitForAbort(request) {
  return new Promise((_resolve, reject) => {
    const stop = () =>
      reject(new DOMException('synthetic local abort', 'AbortError'));
    if (request.options.signal.aborted) stop();
    else request.options.signal.addEventListener('abort', stop, { once: true });
  });
}

// Client boundary: real authorizedFetch passthrough, exact payload allowlists,
// revision zero, URI-encoded IDs, response validation and no mutation replay.
const network = transport();
const api = network.load('assistant-api.ts', assistantExports);
assert.equal(
  (await api.getAssistantStatus(TOKEN, signal())).model_config_revision,
  0,
  'environment model revision zero is valid',
);
assert.equal(network.requests.at(-1).options.method, 'GET');
assert.equal(network.requests.at(-1).options.body, undefined);
let unknownCatalogue = status();
unknownCatalogue.operations[0].availability = 'future_read_only_state';
network.respond(() => reply(unknownCatalogue));
assert.equal(
  (await api.getAssistantStatus(TOKEN, signal())).operations[0].availability,
  'future_read_only_state',
);
unknownCatalogue.operations.push(
  structuredClone(unknownCatalogue.operations[0]),
);
await assert.rejects(
  api.getAssistantStatus(TOKEN, signal()),
  (failure) => failure.code === 'INVALID_RESPONSE',
);
network.respond(() => reply(run('running'), 202));
assert.equal(
  (
    await api.createAssistantRun(TOKEN, signal(), {
      ...consent,
      session_id: '',
      message: '只读取合成状态',
      history: ['must-not-be-sent'],
      tools: ['must-not-be-sent'],
      system: 'must-not-be-sent',
      api_key: 'must-not-be-sent',
      url: 'https://must-not-be-called.invalid',
    })
  ).state,
  'running',
);
assert.deepEqual(JSON.parse(network.requests.at(-1).options.body), {
  session_id: '',
  message: '只读取合成状态',
  ...consent,
});
assert.equal(network.requests.at(-1).options.method, 'POST');
const unusualID = 'synthetic/run?not-a-query';
network.respond(() => reply(run('completed', { run_id: unusualID })));
await api.confirmAssistantAction(TOKEN, unusualID, signal(), {
  ...consent,
  action_id: action().id,
  arguments: { must_not_override: true },
  method: 'DELETE',
  confirmed_action: true,
});
assert.ok(
  network.requests
    .at(-1)
    .url.endsWith(`/runs/${encodeURIComponent(unusualID)}/confirm`),
);
assert.deepEqual(JSON.parse(network.requests.at(-1).options.body), {
  action_id: action().id,
  ...consent,
});
network.respond(() => reply(run('rejected')));
await api.rejectAssistantAction(TOKEN, 'synthetic-run', signal(), action().id);
assert.deepEqual(
  JSON.parse(network.requests.at(-1).options.body),
  { action_id: action().id },
  'reject must not add consent, model data or arguments',
);
network.respond(() => reply(run('running'), 202));
assert.equal(
  (await api.getAssistantRun(TOKEN, 'synthetic-run', signal())).state,
  'running',
);
assert.equal(network.requests.at(-1).options.method, 'GET');
assert.equal(network.requests.at(-1).options.body, undefined);
for (const message of ['中'.repeat(2666) + 'ab', '😀'.repeat(2000)]) {
  assert.equal(Buffer.byteLength(message, 'utf8'), 8000);
  assert.ok(message.length < 8000, 'UTF-16 length is not the service limit');
  network.respond(() => reply(run('completed')));
  const before = network.requests.length;
  await api.createAssistantRun(TOKEN, signal(), {
    ...consent,
    session_id: '',
    message,
  });
  assert.equal(network.requests.length, before + 1);
  assert.equal(
    JSON.parse(network.requests.at(-1).options.body).message,
    message,
  );
}
const beforeInvalid = network.requests.length;
for (const input of [
  { ...consent, confirmed: false },
  { ...consent, model_config_revision: -1 },
  { ...consent, model_config_revision: 0.5 },
  { ...consent, model_config_revision: Number.MAX_SAFE_INTEGER + 1 },
  { ...consent, model_config_id: '' },
]) {
  await assert.rejects(
    api.createAssistantRun(TOKEN, signal(), {
      ...input,
      session_id: '',
      message: '合成输入',
    }),
    (failure) => failure.code === 'CONSENT_REQUIRED' && !failure.uncertain,
  );
}
for (const message of [
  '',
  ' '.repeat(3),
  'x'.repeat(8001),
  '中'.repeat(2666) + 'abx',
  '😀'.repeat(2000) + 'x',
  '\0',
  '合成\0输入',
])
  await assert.rejects(
    api.createAssistantRun(TOKEN, signal(), {
      ...consent,
      session_id: '',
      message,
    }),
    (failure) => failure.code === 'INVALID_MESSAGE',
  );
await assert.rejects(
  api.getAssistantRun(TOKEN, ' invalid ', signal()),
  (failure) => failure.code === 'INVALID_RUN_ID',
);
assert.equal(
  network.requests.length,
  beforeInvalid,
  'invalid input must not reach the service',
);
for (const [payload, code] of [
  [{ run_id: 'recoverable-synthetic-run', state: 'completed' }, 200],
  [run('completed'), 202],
  [run('running'), 200],
  [run('made-up-state'), 200],
  [run('completed', { tool_results: [tool(), tool()] }), 200],
  [run('completed', { tool_results: [tool('made-up-tool-state')] }), 200],
  [run('awaiting_confirmation', { pending_action: null }), 202],
  [run('completed', { pending_action: action() }), 200],
  [run('completed', { model_config_revision: 1 }), 200],
  [run('completed', { model_config_id: 'different-model-target' }), 200],
]) {
  network.respond(() => reply(payload, code));
  const before = network.requests.length;
  await assert.rejects(
    api.createAssistantRun(TOKEN, signal(), {
      ...consent,
      session_id: '',
      message: '合成输入',
    }),
    (failure) =>
      failure.code === 'INVALID_RESPONSE' &&
      failure.uncertain &&
      Boolean(failure.runID),
  );
  assert.equal(
    network.requests.length,
    before + 1,
    'invalid snapshots never replay POST',
  );
}
network.respond(() => reply(run('completed', { session_id: 'wrong-session' })));
await assert.rejects(
  api.createAssistantRun(TOKEN, signal(), {
    ...consent,
    session_id: 'known-session',
    message: '合成输入',
  }),
  (failure) => failure.code === 'INVALID_RESPONSE' && failure.uncertain,
);
network.respond(() => reply(run('completed', { run_id: 'wrong-run' })));
await assert.rejects(
  api.confirmAssistantAction(TOKEN, 'known-run', signal(), {
    ...consent,
    action_id: action().id,
  }),
  (failure) => failure.uncertain && failure.runID === 'known-run',
);
await assert.rejects(
  api.getAssistantRun(TOKEN, 'known-run', signal()),
  (failure) => !failure.uncertain && failure.runID === 'known-run',
);
network.respond(() =>
  reply(run('unknown', { tool_results: [tool('unknown')] })),
);
assert.equal(
  (await api.getAssistantRun(TOKEN, 'synthetic-run', signal())).state,
  'unknown',
);
for (const code of [408, 500, 503]) {
  network.respond(() =>
    reply(
      {
        error: {
          code: 'ASSISTANT_UNAVAILABLE',
          message: 'private synthetic provider text',
        },
      },
      code,
    ),
  );
  const before = network.requests.length;
  await assert.rejects(
    api.createAssistantRun(TOKEN, signal(), {
      ...consent,
      session_id: '',
      message: '合成输入',
    }),
    (failure) =>
      failure.uncertain && !failure.message.includes('private synthetic'),
  );
  assert.equal(network.requests.length, before + 1);
}
network.respond(() => {
  throw new TypeError('private synthetic network details');
});
await assert.rejects(
  api.createAssistantRun(TOKEN, signal(), {
    ...consent,
    session_id: '',
    message: '合成输入',
  }),
  (failure) =>
    failure.uncertain &&
    failure.runID === null &&
    !failure.message.includes('private synthetic'),
);
network.respond(() =>
  reply(
    {
      error: {
        code: 'ASSISTANT_ACTION_CONSUMED',
        message: 'private synthetic upstream text',
      },
    },
    409,
  ),
);
await assert.rejects(
  api.confirmAssistantAction(TOKEN, 'synthetic-run', signal(), {
    ...consent,
    action_id: action().id,
  }),
  (failure) =>
    failure.code === 'ASSISTANT_ACTION_CONSUMED' &&
    !failure.message.includes('private synthetic'),
);
network.respond(() => reply({}, 401));
await assert.rejects(
  api.getAssistantStatus(TOKEN, signal()),
  (failure) => !api.isAssistantBusinessAuthFailure(failure),
);
for (const [code, httpStatus, expected] of [
  ['UNAUTHORIZED', 401, true],
  ['DEVICE_UNAUTHORIZED', 401, true],
  ['ASSISTANT_EXPIRED', 410, false],
  ['ASSISTANT_NOT_FOUND', 404, false],
  ['ASSISTANT_LIMIT', 429, false],
  ['DEVICE_AUTH_UNAVAILABLE', 503, false],
  ['PROVIDER_UNAUTHORIZED', 401, false],
]) {
  assert.equal(
    api.isAssistantBusinessAuthFailure(
      new api.AssistantRequestError('synthetic', code, httpStatus),
    ),
    expected,
  );
}
network.respond(waitForAbort);
let pending = api.createAssistantRun(TOKEN, signal(), {
  ...consent,
  session_id: '',
  message: '合成输入',
});
network.fireTimeout(120_000);
await assert.rejects(
  pending,
  (failure) => failure.code === 'TIMEOUT' && failure.uncertain,
);
pending = api.getAssistantRun(TOKEN, 'synthetic-run', signal());
network.fireTimeout(10_000);
await assert.rejects(
  pending,
  (failure) => failure.code === 'TIMEOUT' && !failure.uncertain,
);
const abort = new AbortController();
pending = api.confirmAssistantAction(TOKEN, 'synthetic-run', abort.signal, {
  ...consent,
  action_id: action().id,
});
abort.abort();
await assert.rejects(
  pending,
  (failure) => failure.code === 'LOCAL_ABORT' && failure.uncertain,
);
assert.equal(network.timers.size, 0, 'request timers must be released');
assert.equal(network.storageReads, 0);

// Minimal keyed hook renderer. It executes the actual nested ActionPreview
// component, including checkbox state and cleanup; it does not simulate layout,
// the DOM, assistive technologies, browser focus or native disabled behavior.
function componentHarness(file, name, bindings, props, browser) {
  const instances = new Map();
  let active,
    dirty = true,
    tree,
    mounted = true,
    effects = [],
    postUnmountWrites = 0;
  const sameDeps = (a, b) =>
    Array.isArray(a) &&
    Array.isArray(b) &&
    a.length === b.length &&
    a.every((item, i) => Object.is(item, b[i]));
  const hooks = {
    useState(initial) {
      const instance = active,
        i = instance.cursor++;
      if (!instance.slots[i])
        instance.slots[i] = {
          value: typeof initial === 'function' ? initial() : initial,
        };
      return [
        instance.slots[i].value,
        (value) => {
          if (!mounted || !instance.mounted) {
            postUnmountWrites++;
            return;
          }
          const next =
            typeof value === 'function'
              ? value(instance.slots[i].value)
              : value;
          if (!Object.is(next, instance.slots[i].value)) {
            instance.slots[i].value = next;
            dirty = true;
          }
        },
      ];
    },
    useRef(initial) {
      const instance = active,
        i = instance.cursor++;
      if (!instance.slots[i])
        instance.slots[i] = { value: { current: initial } };
      return instance.slots[i].value;
    },
    useEffect(callback, dependencies) {
      const instance = active,
        i = instance.cursor++,
        previous = instance.slots[i];
      if (!previous || !sameDeps(previous.dependencies, dependencies))
        effects.push(() => {
          previous?.cleanup?.();
          instance.slots[i] = { dependencies, callback, cleanup: callback() };
        });
    },
    useMemo(factory, dependencies) {
      const instance = active,
        i = instance.cursor++;
      if (
        !instance.slots[i] ||
        !sameDeps(instance.slots[i].dependencies, dependencies)
      )
        instance.slots[i] = { dependencies, value: factory() };
      return instance.slots[i].value;
    },
    useCallback(callback, dependencies) {
      return hooks.useMemo(() => callback, dependencies);
    },
  };
  const jsx = (type, supplied, ...children) => ({
    type,
    props: { ...(supplied ?? {}), children },
  });
  const component = runInNewContext(
    `(function(){${transpile(file, sourceFor(file))}; return ${name};})()`,
    {
      ...(file === 'AssistantPanel.tsx'
        ? browser.load('assistant-help.ts', [
            'ASSISTANT_MESSAGE_BYTE_LIMIT',
            'inspectAssistantMessage',
            'normalizeAssistantHelpRequest',
            'prepareAssistantHelpDraft',
          ])
        : {}),
      ...bindings,
      ...hooks,
      jsx,
      Fragment: 'fragment',
      ServiceError,
      AbortController,
      Date,
      Error,
      setTimeout: browser.window.setTimeout,
      clearTimeout: browser.window.clearTimeout,
      window: browser.window,
      crypto: { randomUUID },
    },
    { filename: file },
  );
  function expand(node, path, seen) {
    if (Array.isArray(node))
      return node.map((child, i) =>
        expand(child, `${path}/${child?.props?.key ?? i}`, seen),
      );
    if (!node || typeof node !== 'object') return node;
    if (typeof node.type === 'function')
      return render(node.type, node.props, `${path}/component`, seen);
    return {
      ...node,
      props: {
        ...node.props,
        children: expand(node.props.children, `${path}/children`, seen),
      },
    };
  }
  function render(type, supplied, id, seen) {
    let instance = instances.get(id);
    if (!instance) {
      instance = { slots: [], cursor: 0, mounted: true };
      instances.set(id, instance);
    }
    seen.add(id);
    active = instance;
    instance.cursor = 0;
    return expand(type(supplied), `${id}/output`, seen);
  }
  const cleanup = (instance) => {
    instance.mounted = false;
    for (const slot of instance.slots) slot?.cleanup?.();
  };
  return {
    get tree() {
      return tree;
    },
    get postUnmountWrites() {
      return postUnmountWrites;
    },
    async flush() {
      for (let pass = 0; pass < 70; pass++) {
        if (dirty && mounted) {
          dirty = false;
          effects = [];
          const seen = new Set();
          tree = render(component, props, 'root', seen);
          for (const [id, instance] of instances)
            if (!seen.has(id)) {
              cleanup(instance);
              instances.delete(id);
            }
          const queued = effects;
          effects = [];
          for (const effect of queued) effect();
        }
        await Promise.resolve();
      }
    },
    unmount() {
      mounted = false;
      for (const instance of [...instances.values()].reverse())
        cleanup(instance);
    },
  };
}
function nodes(value) {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value || typeof value !== 'object') return [];
  return [value, ...nodes(value.props.children)];
}
function text(value) {
  if (Array.isArray(value)) return value.map(text).join('');
  if (typeof value === 'string' || typeof value === 'number')
    return String(value);
  return value && typeof value === 'object' ? text(value.props.children) : '';
}
function button(panel, label) {
  const found = nodes(panel.tree).find(
    (node) => node.type === 'button' && text(node) === label,
  );
  assert.ok(found, `missing button ${label}`);
  return found;
}
function field(panel, id) {
  const found = nodes(panel.tree).find((node) => node.props.id === id);
  assert.ok(found, `missing field ${id}`);
  return found;
}
function checkbox(panel, label) {
  const wrapper = nodes(panel.tree).find(
    (node) => node.type === 'label' && text(node).includes(label),
  );
  const found =
    wrapper &&
    nodes(wrapper).find(
      (node) => node.type === 'input' && node.props.type === 'checkbox',
    );
  assert.ok(found, `missing checkbox ${label}`);
  return found;
}
async function check(panel, label) {
  const control = checkbox(panel, label);
  assert.equal(Boolean(control.props.disabled), false);
  control.props.onChange({ target: { checked: true } });
  await panel.flush();
}
async function click(panel, label) {
  const control = button(panel, label);
  assert.equal(Boolean(control.props.disabled), false, `${label} is disabled`);
  control.props.onClick();
  await panel.flush();
}
async function draft(panel, value) {
  field(panel, 'assistant-message').props.onChange({ target: { value } });
  await panel.flush();
}
async function submit(panel) {
  const form = nodes(panel.tree).find(
    (node) =>
      node.type === 'form' && node.props.className === 'assistant-composer',
  );
  assert.ok(form);
  form.props.onSubmit(event());
  await panel.flush();
}

// Component: consent is separate per round and immutable action, 202 stays
// pending, local abort is not server cancellation, GET is the only recovery IO.
const uiNetwork = transport(),
  uiAPI = uiNetwork.load('assistant-api.ts', assistantExports),
  cache = new Map();
let current = run('awaiting_confirmation'),
  confirmHangs = false,
  authFailures = 0;
uiNetwork.respond((request) => {
  if (request.url.endsWith('/status')) return reply(status());
  if (request.url.endsWith('/confirm') && confirmHangs)
    return waitForAbort(request);
  if (request.options.method === 'GET')
    return reply(
      current,
      ['running', 'awaiting_confirmation'].includes(current.state) ? 202 : 200,
    );
  return reply(
    current,
    ['running', 'awaiting_confirmation'].includes(current.state) ? 202 : 200,
  );
});
const props = {
  token: TOKEN,
  cache,
  onAuthFailure() {
    authFailures++;
  },
  onSettings() {},
};
let panel = componentHarness(
  'AssistantPanel.tsx',
  'AssistantPanel',
  uiAPI,
  props,
  uiNetwork,
);
await panel.flush();
assert.equal(checkbox(panel, '我同意本轮调用').props.checked, false);
assert.equal(button(panel, '提交本轮请求').props.disabled, true);
const catalogue = nodes(panel.tree).find(
  (node) =>
    node.type === 'details' && node.props.className === 'assistant-scope',
);
assert.ok(catalogue);
assert.equal(catalogue.props.open, undefined);
assert.ok(text(catalogue).includes('human_model_settings'));
await draft(panel, '只处理这个合成记录');
await check(panel, '我同意本轮调用');
await draft(panel, '修改后的合成请求');
assert.equal(
  checkbox(panel, '我同意本轮调用').props.checked,
  false,
  'editing the request clears round consent',
);
await check(panel, '我同意本轮调用');
await click(panel, '刷新状态');
assert.equal(
  checkbox(panel, '我同意本轮调用').props.checked,
  false,
  'reading model status clears round consent even when the binding is unchanged',
);
await check(panel, '我同意本轮调用');
await submit(panel);
assert.ok(text(panel.tree).includes('等待逐项确认'));
assert.equal(
  checkbox(panel, '我已核对工具').props.checked,
  false,
  'round consent must not pre-authorize a write',
);
assert.equal(button(panel, '确认并继续').props.disabled, true);
const preview = nodes(panel.tree).find(
  (node) => node.props.className === 'assistant-confirmation',
);
assert.ok(text(preview).includes('"mode": "paused"'));
assert.equal(
  nodes(preview).filter(
    (node) =>
      ['textarea', 'select'].includes(node.type) ||
      (node.type === 'input' && node.props.type !== 'checkbox'),
  ).length,
  0,
  'server preview must not be editable',
);
const writesBeforeRevalidation = uiNetwork.requests.filter(
  (request) => request.options.method === 'POST',
).length;
await check(panel, '我已核对工具');
await click(panel, '读取运行结果');
assert.equal(
  checkbox(panel, '我已核对工具').props.checked,
  false,
  'reading the same pending action invalidates its previous checkbox',
);
assert.equal(button(panel, '确认并继续').props.disabled, true);
await check(panel, '我已核对工具');
await click(panel, '刷新状态');
assert.equal(
  checkbox(panel, '我已核对工具').props.checked,
  false,
  'refreshing an unchanged model binding invalidates action consent',
);
assert.equal(button(panel, '确认并继续').props.disabled, true);
assert.equal(
  uiNetwork.requests.filter((request) => request.options.method === 'POST')
    .length,
  writesBeforeRevalidation,
  'revalidation only reads; it does not confirm or replay',
);
await check(panel, '我已核对工具');
confirmHangs = true;
const beforeConfirm = uiNetwork.requests.length;
await click(panel, '确认并继续');
await click(panel, '停止等待');
assert.equal(
  uiNetwork.requests.length,
  beforeConfirm + 1,
  'stopping must not issue cancellation or replay',
);
assert.ok(text(panel.tree).includes('停止等待不等于后台取消'));
assert.ok(text(panel.tree).includes('后台可能已处理并产生费用'));
assert.equal(button(panel, '确认并继续').props.disabled, true);
assert.equal(button(panel, '拒绝此项').props.disabled, true);
assert.equal(button(panel, '提交本轮请求').props.disabled, true);
const writesBeforeRecovery = uiNetwork.requests.filter(
  (request) => request.options.method === 'POST',
).length;
current = run('unknown', { tool_results: [tool('unknown')] });
await click(panel, '读取运行结果');
assert.ok(text(panel.tree).includes('结果未知'));
assert.equal(button(panel, '提交本轮请求').props.disabled, true);
current = run('completed');
await click(panel, '读取运行结果');
assert.ok(text(panel.tree).includes('工具已完成'));
assert.ok(text(panel.tree).includes('actual_synthetic_record'));
assert.equal(
  uiNetwork.requests.filter((request) => request.options.method === 'POST')
    .length,
  writesBeforeRecovery,
  'GET recovery does not invoke a model or replay a tool',
);
await draft(panel, '跨页保留的合成草稿');
panel.unmount();
await tick();
assert.equal(panel.postUnmountWrites, 0);
panel = componentHarness(
  'AssistantPanel.tsx',
  'AssistantPanel',
  uiAPI,
  props,
  uiNetwork,
);
await panel.flush();
assert.equal(
  field(panel, 'assistant-message').props.value,
  '跨页保留的合成草稿',
);
assert.equal(
  checkbox(panel, '我同意本轮调用').props.checked,
  false,
  'consent must not survive navigation',
);
uiNetwork.respond((request) =>
  request.url.endsWith('/status')
    ? reply(status())
    : reply(
        { error: { code: 'ASSISTANT_EXPIRED', message: 'synthetic expiry' } },
        410,
      ),
);
await click(panel, '读取运行结果');
assert.equal(
  authFailures,
  0,
  'assistant expiry must not clear business auth or shared AI drafts',
);
assert.equal(
  field(panel, 'assistant-message').props.value,
  '跨页保留的合成草稿',
);
assert.equal(button(panel, '新建助手会话').props.disabled, true);
await check(panel, '我已记录必要的运行编号');
await click(panel, '新建助手会话');
assert.equal(cache.get(TOKEN).sessionID, '');
panel.unmount();
await tick();
assert.equal(panel.postUnmountWrites, 0);
assert.equal(uiNetwork.storageReads, 0);
assert.equal(uiNetwork.intervals.size, 0);

// A lost initial POST has no discoverable run ID. Navigation must preserve the
// unknown marker; the user must verify business state before a new session.
const lostNetwork = transport(),
  lostAPI = lostNetwork.load('assistant-api.ts', assistantExports),
  lostCache = new Map();
lostNetwork.respond((request) =>
  request.url.endsWith('/status')
    ? reply(status())
    : Promise.reject(new TypeError('synthetic lost response')),
);
const lostProps = {
  token: TOKEN,
  cache: lostCache,
  onAuthFailure() {
    throw Error('unexpected business auth reset');
  },
  onSettings() {},
};
let lostPanel = componentHarness(
  'AssistantPanel.tsx',
  'AssistantPanel',
  lostAPI,
  lostProps,
  lostNetwork,
);
await lostPanel.flush();
await draft(lostPanel, '合成未知请求');
await check(lostPanel, '我同意本轮调用');
await submit(lostPanel);
assert.equal(lostCache.get(TOKEN).turns.at(-1).uncertain, true);
assert.equal(lostCache.get(TOKEN).turns.at(-1).runID, null);
assert.equal(button(lostPanel, '提交本轮请求').props.disabled, true);
assert.equal(button(lostPanel, '新建助手会话').props.disabled, true);
lostPanel.unmount();
await tick();
lostPanel = componentHarness(
  'AssistantPanel.tsx',
  'AssistantPanel',
  lostAPI,
  lostProps,
  lostNetwork,
);
await lostPanel.flush();
assert.equal(button(lostPanel, '新建助手会话').props.disabled, true);
assert.equal(
  lostNetwork.requests.filter((request) => request.options.method === 'POST')
    .length,
  1,
  'remount never retries an unknown POST',
);
lostPanel.unmount();
await tick();
assert.equal(lostPanel.postUnmountWrites, 0);

// Digest uses the same nonnegative model revision binding, separate daily and
// immediate consent, schedule CAS, and memory-only unknown recovery markers.
const digestNetwork = transport();
const digestAPI = digestNetwork.load('digest-api.ts', [
  'DigestRequestError',
  'getDigestSchedule',
  'getLatestDigest',
  'runDigestNow',
  'saveDigestSchedule',
]);
const aiAPI = digestNetwork.load('ai-api.ts', ['getAIStatus']);
let plan = schedule(),
  latest = null,
  digestHangs = false;
let digestModel = { ...status(), provider: 'synthetic-provider' };
digestNetwork.respond((request) => {
  if (request.url.endsWith('/ai/status')) return reply(digestModel);
  if (request.url.endsWith('/digest/latest'))
    return reply({ report: latest, sending_enabled: false });
  if (
    request.url.endsWith('/digest/schedule') &&
    request.options.method === 'GET'
  )
    return reply(plan);
  if (request.url.endsWith('/digest/schedule')) {
    const input = JSON.parse(request.options.body);
    plan = { ...plan, ...input, revision: plan.revision + 1 };
    delete plan.confirmed;
    delete plan.expected_revision;
    return reply(plan);
  }
  if (digestHangs) return waitForAbort(request);
  latest = report();
  return reply({ report: latest, sending_enabled: false });
});
assert.equal(
  (await aiAPI.getAIStatus(TOKEN, signal())).model_config_revision,
  0,
);
let beforeDigest = digestNetwork.requests.length;
await assert.rejects(
  digestAPI.saveDigestSchedule(TOKEN, {
    enabled: true,
    local_time: '18:30',
    timezone: 'Asia/Shanghai',
    expected_revision: 0,
    ...consent,
  }),
  (failure) => failure.code === 'INVALID_REQUEST',
);
assert.equal(
  digestNetwork.requests.length,
  beforeDigest,
  'schedule CAS revision still starts at one',
);
const unresolvedIdentities = new Set();
const digestProps = {
  token: TOKEN,
  unresolvedIdentities,
  onAuthFailure() {
    throw Error('unexpected digest auth reset');
  },
};
digestModel = {
  ...digestModel,
  configured: false,
  model: null,
  endpoint_host: null,
  model_config_id: '',
  daily_limit: 0,
  used_today: 0,
};
latest = {
  ...report(),
  reminders: Array.from({ length: 2 }, () => ({
    conversation_id: 'synthetic-chat',
    title: '合成提醒',
    platform: 'qq',
    conversation_kind: 'private',
    priority: 'normal',
    reason: '相同原因',
    evidence_ids: ['synthetic-evidence'],
  })),
};
let digestPanel = componentHarness(
  'DigestPanel.tsx',
  'DigestPanel',
  { ...digestAPI, ...aiAPI },
  digestProps,
  digestNetwork,
);
await digestPanel.flush();
assert.ok(
  !text(digestPanel.tree).includes('今日共享额度已达限制'),
  'unconfigured 0/0 is not exhausted quota',
);
assert.equal(button(digestPanel, '生成 / 读取今日摘要').props.disabled, true);
const reminderNodes = nodes(digestPanel.tree).filter(
  (node) => node.type === 'li',
);
assert.equal(reminderNodes.length, 2);
assert.equal(
  new Set(reminderNodes.map((node) => node.props.key)).size,
  2,
  'identical reminders retain distinct snapshot keys',
);
digestModel = { ...status(), provider: 'synthetic-provider' };
await click(digestPanel, '读取实际计划与最近摘要');
assert.equal(
  checkbox(digestPanel, '我同意启用 / 更新每日计划').props.checked,
  false,
);
assert.equal(checkbox(digestPanel, '我同意仅本次把').props.checked, false);
assert.equal(button(digestPanel, '启用每日计划').props.disabled, true);
assert.equal(button(digestPanel, '生成 / 读取今日摘要').props.disabled, true);
await check(digestPanel, '我同意启用 / 更新每日计划');
await check(digestPanel, '我同意仅本次把');
await click(digestPanel, '读取实际计划与最近摘要');
assert.equal(
  checkbox(digestPanel, '我同意启用 / 更新每日计划').props.checked,
  false,
  'rereading the plan clears recurring consent',
);
assert.equal(
  checkbox(digestPanel, '我同意仅本次把').props.checked,
  false,
  'rereading the plan clears immediate consent independently',
);
await check(digestPanel, '我同意启用 / 更新每日计划');
await click(digestPanel, '启用每日计划');
assert.equal(plan.enabled, true);
assert.equal(plan.model_config_revision, 0);
assert.deepEqual(
  JSON.parse(
    digestNetwork.requests.findLast(
      (request) => request.options.method === 'PATCH',
    ).options.body,
  ),
  {
    enabled: true,
    local_time: '18:30',
    timezone: 'Asia/Shanghai',
    expected_revision: 1,
    ...consent,
  },
);
assert.equal(
  checkbox(digestPanel, '我同意启用 / 更新每日计划').props.checked,
  false,
);
assert.equal(checkbox(digestPanel, '我同意仅本次把').props.checked, false);
nodes(digestPanel.tree)
  .find((node) => node.type === 'input' && node.props.type === 'time')
  .props.onChange({ target: { value: '17:55' } });
await digestPanel.flush();
await click(digestPanel, '暂停当前计划');
assert.deepEqual(
  JSON.parse(
    digestNetwork.requests.findLast(
      (request) => request.options.method === 'PATCH',
    ).options.body,
  ),
  {
    enabled: false,
    local_time: '18:30',
    timezone: 'Asia/Shanghai',
    expected_revision: 2,
    confirmed: true,
  },
  'pause preserves actual saved time and carries no model binding',
);
assert.ok(text(digestPanel.tree).includes('失败或未知记录也不会被强制重跑'));
await check(digestPanel, '我同意仅本次把');
digestHangs = true;
await click(digestPanel, '生成 / 读取今日摘要');
assert.equal(unresolvedIdentities.has(TOKEN), true);
await click(digestPanel, '停止等待');
assert.equal(button(digestPanel, '生成 / 读取今日摘要').props.disabled, true);
beforeDigest = digestNetwork.requests.filter(
  (request) => request.options.method !== 'GET',
).length;
digestPanel.unmount();
await tick();
assert.equal(digestPanel.postUnmountWrites, 0);
digestPanel = componentHarness(
  'DigestPanel.tsx',
  'DigestPanel',
  { ...digestAPI, ...aiAPI },
  digestProps,
  digestNetwork,
);
await digestPanel.flush();
assert.equal(
  button(digestPanel, '生成 / 读取今日摘要').props.disabled,
  true,
  'navigation must not silently forget an interrupted digest mutation',
);
assert.equal(
  button(digestPanel, '允许重新核对新的摘要操作').props.disabled,
  true,
);
await check(digestPanel, '我已重新读取并人工核实');
await click(digestPanel, '允许重新核对新的摘要操作');
assert.equal(unresolvedIdentities.has(TOKEN), false);
assert.equal(
  checkbox(digestPanel, '我同意仅本次把').props.checked,
  false,
  'manual recovery does not grant fresh model consent',
);
assert.equal(
  digestNetwork.requests.filter((request) => request.options.method !== 'GET')
    .length,
  beforeDigest,
  'digest recovery only reads, never replays',
);
digestPanel.unmount();
await tick();
assert.equal(digestPanel.postUnmountWrites, 0);
assert.equal(digestNetwork.timers.size, 0);
assert.equal(digestNetwork.storageReads, 0);
console.log(
  'Assistant and digest synthetic client/component checks passed: real authorized transport, revision-zero environment binding, request allowlists, nonterminal 202, immutable per-action consent, actual tool audit, no mutation replay, local-stop/unknown recovery, isolated assistant expiry, memory-only navigation state, digest CAS and separate daily/immediate consent. No real service, model, account, browser or Tauri was used.',
);
