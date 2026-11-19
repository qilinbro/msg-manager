import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { loadIcon } from './frontend-icon-test-loader.mjs';
import { loadHelpDetails } from './frontend-help-test-loader.mjs';

// Synthetic, in-memory execution of shipped code. Not a browser/AT/native-form,
// real-account, model, platform, scheduler-runtime or Tauri acceptance test.
export const TOKEN = 'synthetic-feishu-memory-only-business-token';
export const SERVICE_URL = 'http://127.0.0.1:17890';
export const NOW = Date.parse('2099-06-01T04:00:00Z');
export const START = '2099-05-31T12:00:00Z';
export const END = '2099-06-01T00:00:00Z';
export const NEXT = '2099-06-01T12:00:00Z';
export const REPORT_ID = `fr_${'1'.repeat(32)}`;
export const DELIVERY_ID = `fd_${'2'.repeat(32)}`;
export const TARGET = 'a'.repeat(64);
export const HASH = 'b'.repeat(64);
export const BINDING = {
  confirmed: true,
  model_config_id: 'c'.repeat(64),
  model_config_revision: 0,
  config_revision: 1,
  target_binding_id: TARGET,
};
const { confirmed: _confirmed, ...bindingDTO } = BINDING;
export const schedule = (extra = {}) => ({
  enabled: false,
  revision: 1,
  timezone: 'Asia/Shanghai',
  slots: ['08:00', '20:00'],
  model_config_id: '',
  model_config_revision: 0,
  config_revision: 0,
  target_binding_id: '',
  next_window_end: null,
  last_window_end: null,
  ...extra,
});
export const status = (extra = {}) => ({
  configured: true,
  key_configured: true,
  app_id: 'cli_synthetic',
  config_revision: 1,
  target_binding_id: TARGET,
  target_label_masked: '本人私聊（ou_***1234；归属尚未验证）',
  recipient_open_id_masked: 'ou_***1234',
  recipient_ownership_verified: false,
  schedule: schedule(),
  latest_report_id: '',
  latest_delivery: null,
  capabilities: { private_self_only: true, automatic_retry: false },
  timezone: 'Asia/Shanghai',
  slots: ['08:00', '20:00'],
  ...extra,
});
export const emptyStatus = (extra = {}) =>
  status({
    configured: false,
    key_configured: false,
    app_id: '',
    target_binding_id: '',
    target_label_masked: '',
    recipient_open_id_masked: '',
    ...extra,
  });
export const model = (extra = {}) => ({
  configured: true,
  model: 'synthetic-model',
  endpoint_host: 'synthetic-model.invalid',
  ...bindingDTO,
  daily_limit: 50,
  used_today: 2,
  sending_enabled: false,
  ...extra,
});
export const report = (state = 'succeeded', extra = {}) => ({
  id: REPORT_ID,
  window_start: START,
  window_end: END,
  state,
  ...bindingDTO,
  schedule_revision: 0,
  trigger: 'manual',
  summary: state === 'succeeded' ? '合成摘要；不是待发送正文。' : '',
  todos:
    state === 'succeeded'
      ? [
          {
            text: '合成证据候选，须人工核对',
            evidence_ids: ['synthetic-message'],
            needs_review: true,
          },
        ]
      : [],
  excerpts:
    state === 'succeeded'
      ? [
          {
            message_id: 'synthetic-message',
            conversation_id: 'synthetic-conversation',
            text: '<script>synthetic text only</script>',
            occurred_at: START,
          },
        ]
      : [],
  conversation_ids: ['synthetic-conversation'],
  source_refs: [
    { conversation_id: 'synthetic-conversation', settings_revision: 0 },
  ],
  conversation_count: 1,
  text:
    state === 'succeeded'
      ? '合成冻结正文：此处是服务器将发送的完整原文。\n<script>not executable</script>'
      : '',
  content_hash: state === 'succeeded' ? HASH : '',
  truncated: false,
  error_code:
    state === 'failed'
      ? 'FEISHU_MODEL_FAILED'
      : state === 'unknown'
        ? 'FEISHU_INTERRUPTED'
        : '',
  created_at: END,
  generated_at: state === 'running' ? null : '2099-06-01T00:00:01Z',
  delivery: null,
  ...extra,
});
export const delivery = (state = 'succeeded', extra = {}) => ({
  id: DELIVERY_ID,
  report_id: REPORT_ID,
  window_start: START,
  window_end: END,
  state,
  ...bindingDTO,
  content_hash: HASH,
  message_id: state === 'succeeded' ? 'synthetic-platform-message' : '',
  error_code:
    state === 'failed'
      ? 'FEISHU_PLATFORM_REJECTED'
      : state === 'unknown'
        ? 'FEISHU_INTERRUPTED'
        : '',
  created_at: '2099-06-01T00:00:02Z',
  completed_at: state === 'running' ? null : '2099-06-01T00:00:03Z',
  ...extra,
});
export class ServiceError extends Error {
  constructor(message, code, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
}
export const reply = (payload, code = 200) => ({
  ok: code >= 200 && code < 300,
  status: code,
  json: async () => structuredClone(payload),
});
export const errorReply = (code, httpStatus = 409) =>
  reply(
    {
      error: {
        code,
        message: 'RAW-PRIVATE-PROVIDER-DETAIL-MUST-NOT-BE-DISPLAYED',
      },
    },
    httpStatus,
  );
export const signal = () => new AbortController().signal;
export const tick = async () => {
  for (let i = 0; i < 60; i++) await Promise.resolve();
};
export const event = () => ({ preventDefault() {} });
export const waitForAbort = ({ options }) =>
  new Promise((_, reject) => {
    const fail = () =>
      reject(new DOMException('synthetic local stop', 'AbortError'));
    if (options.signal.aborted) fail();
    else options.signal.addEventListener('abort', fail, { once: true });
  });
const sourceFor = (file) =>
  readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8')
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^export default /gm, '')
    .replace(/^export /gm, '');
function transpile(file) {
  const output = transformSync(file, sourceFor(file), {
    jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
  });
  assert.equal(output.errors.length, 0, `${file} must compile`);
  return output.code;
}
export const FEISHU_EXPORTS = [
  'FeishuRequestError',
  'isFeishuBusinessAuthFailure',
  'getFeishuStatus',
  'saveFeishuConfig',
  'clearFeishuConfig',
  'getFeishuSchedule',
  'saveFeishuSchedule',
  'generateFeishuPreview',
  'getLatestFeishuReport',
  'getFeishuReport',
  'sendFeishuReport',
  'getFeishuDelivery',
  'getFeishuModelStatus',
];

export function transport() {
  const requests = [],
    timers = new Map();
  let responder = () => {
      throw Error('No synthetic response supplied');
    },
    timerID = 0,
    storageReads = 0,
    now = NOW;
  class Clock extends Date {
    constructor(...args) {
      super(...(args.length ? args : [now]));
    }
    static now() {
      return now;
    }
  }
  const forbiddenStorage = () => {
    storageReads++;
    throw Error(
      'No credential, report, consent or recovery may enter persistent browser storage',
    );
  };
  const window = {
    setTimeout(callback, ms) {
      timers.set(++timerID, { callback, ms });
      return timerID;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    setInterval() {
      throw Error('No background polling is authorized by this component');
    },
    clearInterval() {},
    get localStorage() {
      return forbiddenStorage();
    },
    get sessionStorage() {
      return forbiddenStorage();
    },
  };
  const globals = {
    SERVICE: { url: SERVICE_URL, error: null },
    ServiceError,
    AbortController,
    DOMException,
    TextEncoder,
    URL,
    Map,
    Set,
    Error,
    Date: Clock,
    window,
    setTimeout: window.setTimeout,
    clearTimeout: window.clearTimeout,
    crypto: { randomUUID },
    get localStorage() {
      return forbiddenStorage();
    },
    get sessionStorage() {
      return forbiddenStorage();
    },
    get indexedDB() {
      return forbiddenStorage();
    },
    console: {
      log() {
        throw Error('Shipped code must not log sensitive data');
      },
      warn() {
        throw Error('Unexpected diagnostic');
      },
      error() {
        throw Error('Unexpected diagnostic');
      },
    },
    fetch(url, options) {
      assert.equal(
        new URL(url).origin,
        SERVICE_URL,
        'only the fixed local service is addressed; all IO is mocked',
      );
      assert.ok(new URL(url).pathname.startsWith('/api/v1/'));
      assert.equal(new URL(url).search, '');
      assert.equal(new URL(url).hash, '');
      assert.equal(options.headers.Authorization, `Bearer ${TOKEN}`);
      assert.equal(options.credentials, 'omit');
      assert.equal(options.redirect, 'error');
      assert.equal(options.cache, 'no-store');
      const request = { url, options };
      requests.push(request);
      return responder(request);
    },
  };
  Object.assign(
    globals,
    runInNewContext(
      `(function(){\n${transpile('assistant-help.ts')}\nreturn {ASSISTANT_MESSAGE_BYTE_LIMIT, inspectAssistantMessage, normalizeAssistantHelpRequest, prepareAssistantHelpDraft};\n})()`,
      globals,
      { filename: 'assistant-help.ts' },
    ),
  );
  return {
    requests,
    timers,
    window,
    globals,
    Date: Clock,
    get storageReads() {
      return storageReads;
    },
    respond(next) {
      responder = next;
    },
    setTime(value) {
      now = value;
    },
    load(file = 'feishu-api.ts', names = FEISHU_EXPORTS) {
      return runInNewContext(
        `(function(){\n${deviceAuthSource}\n${transpile(file)}\nreturn {${names.join(',')}};\n})()`,
        globals,
        { filename: file },
      );
    },
    fireTimeout(ms) {
      const found = [...timers.values()].find((item) => item.ms === ms);
      assert.ok(found, `missing ${ms}ms timeout`);
      found.callback();
    },
    writes() {
      return requests.filter((item) => item.options.method !== 'GET');
    },
  };
}

// Keyed hook renderer with synthetic input refs and disabled-fieldset tracking.
// This verifies state/IO contracts, not actual DOM layout, browser native form
// validation, focus, hit targets, assistive technologies or accessibility trees.
export function componentHarness(
  file,
  name,
  bindings,
  props,
  browser,
  strictEffects = false,
) {
  const instances = new Map(),
    elements = new Map();
  let active,
    dirty = true,
    tree,
    mounted = true,
    effects = [],
    postUnmountWrites = 0,
    firstEffects = true;
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
          kind: 'state',
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
        instance.slots[i] = { kind: 'ref', value: { current: initial } };
      return instance.slots[i].value;
    },
    useEffect(callback, dependencies) {
      const instance = active,
        i = instance.cursor++,
        previous = instance.slots[i];
      if (!previous || !sameDeps(previous.dependencies, dependencies))
        effects.push(() => {
          previous?.cleanup?.();
          instance.slots[i] = {
            kind: 'effect',
            dependencies,
            callback,
            cleanup: callback(),
          };
        });
    },
    useMemo(factory, dependencies) {
      const instance = active,
        i = instance.cursor++;
      if (
        !instance.slots[i] ||
        !sameDeps(instance.slots[i].dependencies, dependencies)
      )
        instance.slots[i] = { kind: 'memo', dependencies, value: factory() };
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
  const componentGlobals = Object.assign(
    Object.defineProperties(
      {},
      Object.getOwnPropertyDescriptors(browser.globals),
    ),
    bindings,
    hooks,
    { jsx, Fragment: 'fragment', Icon: loadIcon(jsx), HelpDetails: loadHelpDetails(jsx) },
  );
  const component = runInNewContext(
    `(function(){${transpile(file)}; return ${name};})()`,
    componentGlobals,
    { filename: file },
  );
  function expand(node, path, seen, inheritedDisabled = false) {
    if (Array.isArray(node))
      return node.map((child, i) =>
        expand(
          child,
          `${path}/${child?.props?.key ?? i}`,
          seen,
          inheritedDisabled,
        ),
      );
    if (!node || typeof node !== 'object') return node;
    if (typeof node.type === 'function')
      return render(
        node.type,
        node.props,
        `${path}/component:${node.props.key ?? ''}`,
        seen,
        inheritedDisabled,
      );
    const disabled = inheritedDisabled || Boolean(node.props.disabled);
    let element = elements.get(path);
    if (!element) {
      element = { value: node.props.defaultValue ?? '', checked: false };
      elements.set(path, element);
    }
    if (node.props.value !== undefined) element.value = node.props.value;
    if (node.props.checked !== undefined) element.checked = node.props.checked;
    element.disabled = disabled;
    if (node.props.ref && typeof node.props.ref === 'object')
      node.props.ref.current = element;
    return {
      ...node,
      element,
      _disabled: disabled,
      props: {
        ...node.props,
        children: expand(
          node.props.children,
          `${path}/children`,
          seen,
          inheritedDisabled ||
            (node.type === 'fieldset' && Boolean(node.props.disabled)),
        ),
      },
    };
  }
  function render(type, supplied, id, seen, inheritedDisabled = false) {
    let instance = instances.get(id);
    if (!instance) {
      instance = { slots: [], cursor: 0, mounted: true };
      instances.set(id, instance);
    }
    seen.add(id);
    active = instance;
    instance.cursor = 0;
    return expand(type(supplied), `${id}/output`, seen, inheritedDisabled);
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
    states() {
      return [...instances.values()].flatMap((instance) =>
        instance.slots
          .filter((slot) => slot?.kind === 'state')
          .map((slot) => slot.value),
      );
    },
    updateProps(next) {
      props = { ...props, ...next };
      dirty = true;
    },
    async flush() {
      for (let pass = 0; pass < 90; pass++) {
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
          const queue = effects;
          effects = [];
          for (const effect of queue) effect();
          if (strictEffects && firstEffects) {
            firstEffects = false;
            for (const instance of instances.values())
              for (const slot of instance.slots)
                if (slot?.kind === 'effect') slot.cleanup?.();
            for (const instance of instances.values())
              for (const slot of instance.slots)
                if (slot?.kind === 'effect') slot.cleanup = slot.callback();
          }
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
export function nodes(value) {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value || typeof value !== 'object') return [];
  return [value, ...nodes(value.props.children)];
}
export function text(value) {
  if (Array.isArray(value)) return value.map(text).join('');
  if (typeof value === 'string' || typeof value === 'number')
    return String(value);
  return value && typeof value === 'object' ? text(value.props.children) : '';
}
export function field(panel, id) {
  const found = nodes(panel.tree).find((node) => node.props.id === id);
  assert.ok(found, `missing field ${id}`);
  return found;
}
export function button(panel, label) {
  const found = nodes(panel.tree).find(
    (node) =>
      node.type === 'button' &&
      (node.props['aria-label'] ?? text(node)).trim() === label,
  );
  assert.ok(found, `missing button ${label}`);
  return found;
}
export function checkbox(panel, label) {
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
export async function check(panel, label, checked = true) {
  const control = checkbox(panel, label);
  assert.equal(control._disabled, false, `${label} is disabled`);
  control.element.checked = checked;
  control.props.onChange({
    target: control.element,
    currentTarget: control.element,
  });
  await panel.flush();
}
export async function click(panel, label) {
  const control = button(panel, label);
  assert.equal(control._disabled, false, `${label} is disabled`);
  control.props.onClick();
  await panel.flush();
}
export async function fill(panel, id, value) {
  const control = field(panel, id);
  assert.equal(control._disabled, false, `${id} is disabled`);
  control.element.value = value;
  control.props.onChange({
    target: control.element,
    currentTarget: control.element,
  });
  await panel.flush();
}
export async function submit(panel, name) {
  const form = nodes(panel.tree).find(
    (node) =>
      node.type === 'form' &&
      (node.props.id === name || node.props['aria-labelledby'] === name),
  );
  assert.ok(form, `missing form ${name}`);
  form.props.onSubmit(event());
  await panel.flush();
}
