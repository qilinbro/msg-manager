import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import {
  ServiceError,
  componentHarness,
  nodes,
  text,
  button,
  checkbox,
  click,
  check,
  tick,
} from './feishu-test-harness.mjs';

// Synthetic only: execute the shipped TS/TSX and device-auth passthrough in a VM.
// Every fetch is intercepted, every timeout is manually fired, and there is no
// browser, DOM, platform IO, credential, storage, provider or model dependency.
// The shared hook renderer checks lifecycle/state contracts, NOT layout, native
// validation, focus, accessibility trees, real React scheduling or acceptance.
const SERVICE_URL = 'http://127.0.0.1:17890';
const TOKEN = 'synthetic-telegram-memory-only-business-token';
const OTHER_TOKEN = 'synthetic-telegram-second-business-token';
const DATE = '2026-11-19T12:00:00Z';
const RAW = 'RAW-PRIVATE-DETAIL https://api.telegram.org/botSECRET/chat?id=PRIVATE';
const EXPORTS = [
  'getTelegramBotStatus',
  'verifyTelegramBot',
  'syncTelegramBot',
  'telegramBotErrorMessage',
  'isTelegramBotAuthFailure',
  'telegramBotStateLabel',
];
const result = (extra = {}) => ({
  events: 5,
  imported: 2,
  duplicates: 1,
  unsupported: 1,
  not_authorized: 1,
  conversations: 2,
  next_offset: '9223372036854775807',
  partial: true,
  source_kind: 'telegram_bot',
  ai_queued: 0,
  ...extra,
});
const status = (extra = {}) => ({
  connection_id: 'telegram-bot',
  runtime_configured: true,
  configured: true,
  identity_verified: false,
  allowed_private_chat_count: 2,
  state: 'configured',
  checked_at: '',
  last_sync_at: '',
  manual_receive_only: true,
  background_receiving: false,
  sending_enabled: false,
  history_import: false,
  last_result: null,
  ...extra,
});
const verified = (extra = {}) => status({
  identity_verified: true,
  state: 'identity_verified',
  checked_at: DATE,
  ...extra,
});
const signal = () => new AbortController().signal;
const reply = (payload, code = 200, headers = {}) =>
  new Response(JSON.stringify(payload), {
    status: code,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
const errorReply = (code, http = 409) =>
  reply({ error: { code, message: RAW, details: { token: RAW } }, trace_id: RAW }, http);
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const abortedFetch = ({ options }) => new Promise((_, reject) => {
  const stop = () => reject(options.signal.reason);
  if (options.signal.aborted) stop();
  else options.signal.addEventListener('abort', stop, { once: true });
});
const sourceFor = (file) => readFileSync(
  new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8',
).replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export default /gm, '')
  .replace(/^export /gm, '');
function compiled(file) {
  const output = transformSync(file, sourceFor(file), {
    jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
  });
  assert.equal(output.errors.length, 0, `${file} must compile`);
  return output.code;
}
function transport() {
  const requests = [], timers = new Map();
  let timerID = 0, storageReads = 0;
  let responder = () => { throw Error('Unexpected synthetic request'); };
  const storage = () => { storageReads++; throw Error('Storage must not be accessed'); };
  const window = {
    setTimeout(callback, ms) { timers.set(++timerID, { callback, ms }); return timerID; },
    clearTimeout(id) { timers.delete(id); },
    setInterval() { throw Error('No background polling is authorized'); },
    clearInterval() {},
    get localStorage() { return storage(); },
    get sessionStorage() { return storage(); },
  };
  // Native signal composition, fake deterministic deadline. Never wait 10/45s.
  const fakeAbortSignal = {
    any: (signals) => AbortSignal.any(signals),
    timeout(ms) {
      const controller = new AbortController();
      window.setTimeout(() => controller.abort(new DOMException(RAW, 'TimeoutError')), ms);
      return controller.signal;
    },
  };
  const globals = {
    SERVICE: { url: SERVICE_URL, error: null }, ServiceError,
    AbortController, AbortSignal: fakeAbortSignal, DOMException,
    TextEncoder, TextDecoder, Uint8Array, URL, URLSearchParams, Date, Map, Set, Error,
    window, setTimeout: window.setTimeout, clearTimeout: window.clearTimeout,
    get localStorage() { return storage(); },
    get sessionStorage() { return storage(); },
    get indexedDB() { return storage(); },
    console: {
      log() { throw Error('Shipped client must not log data'); },
      warn() { throw Error('Shipped client must not log data'); },
      error() { throw Error('Shipped client must not log data'); },
    },
    fetch(url, options) {
      const parsed = new URL(url);
      assert.equal(parsed.origin, SERVICE_URL, 'only the synthetic local service is addressed');
      assert.ok(parsed.pathname.startsWith('/api/v1/'));
      assert.equal(parsed.hash, '');
      assert.equal(options.credentials, 'omit');
      assert.equal(options.redirect, 'error');
      assert.equal(options.cache, 'no-store');
      assert.equal(options.headers.Accept, 'application/json');
      assert.ok([`Bearer ${TOKEN}`, `Bearer ${OTHER_TOKEN}`].includes(options.headers.Authorization));
      const request = { url, options };
      requests.push(request);
      return responder(request);
    },
  };
  return {
    requests, timers, globals, window,
    get storageReads() { return storageReads; },
    respond(next) { responder = next; },
    fireTimeout(ms) {
      const entry = [...timers].find(([, value]) => value.ms === ms);
      assert.ok(entry, `missing ${ms}ms deadline`);
      timers.delete(entry[0]); entry[1].callback();
    },
    load(file = 'telegram-bot-api.ts', names = EXPORTS, bindings = {}) {
      Object.assign(globals, bindings);
      return runInNewContext(
        `(function(){${deviceAuthSource}\n${compiled(file)}\nreturn {${names.join(',')}};})()`,
        globals, { filename: file },
      );
    },
  };
}
const tests = [];
const test = (name, run) => tests.push({ name, run });
let assertions = 0;
async function expectCode(promise, code, api, http) {
  await assert.rejects(promise, (failure) => {
    assert.ok(failure instanceof ServiceError);
    assert.equal(failure.code, code);
    if (http !== undefined) assert.equal(failure.status, http);
    assert.match(failure.message, /[\u4e00-\u9fff]/);
    assert.ok(!JSON.stringify(failure).includes(RAW));
    assert.ok(!failure.message.includes('SECRET'));
    if (api) assert.equal(api.telegramBotErrorMessage(failure), failure.message);
    assertions++;
    return true;
  });
}
function panelFor(browser, props = {}, strict = false) {
  const api = browser.load();
  // useId is stable across this single-root harness's rerenders. No random IDs.
  const panel = componentHarness('TelegramBotPanel.tsx', 'TelegramBotPanel', {
    ...api, useId: () => 'telegram-synthetic-stable-id',
  }, { token: TOKEN, onAuthFailure() {}, ...props }, browser, strict);
  return { panel, api };
}
const consentLabel = '我同意本次手动接收授权私聊纯文本';
const checked = (panel) => checkbox(panel, consentLabel).props.checked;
const disabled = (panel, label) => button(panel, label)._disabled;

// Transport: exact endpoints, auth, HTTP options and consent bodies.
test('transport: GET status only, verify {}, sync explicit one-shot consent', async () => {
  const browser = transport(), api = browser.load();
  browser.respond(({ url }) => reply(url.endsWith('/sync') ? result() : verified()));
  await api.getTelegramBotStatus(TOKEN, signal());
  await api.verifyTelegramBot(TOKEN, signal());
  const received = await api.syncTelegramBot(TOKEN, signal(), true);
  assert.equal(received.next_offset, '9223372036854775807', 'no Number precision loss');
  assert.deepEqual(browser.requests.map(({ url, options }) => [url, options.method, options.body]), [
    [`${SERVICE_URL}/api/v1/connections/telegram-bot/status`, 'GET', undefined],
    [`${SERVICE_URL}/api/v1/connections/telegram-bot/verify`, 'POST', '{}'],
    [`${SERVICE_URL}/api/v1/connections/telegram-bot/sync`, 'POST', '{"consent_receive":true}'],
  ]);
  assert.equal(Object.hasOwn(browser.requests[0].options, 'body'), false);
  assert.equal(Object.hasOwn(browser.requests[0].options.headers, 'Content-Type'), false);
  for (const { options } of browser.requests.slice(1))
    assert.equal(options.headers['Content-Type'], 'application/json');
  assert.deepEqual([...browser.timers.values()].map(({ ms }) => ms), [10_000, 45_000, 45_000]);
  assert.equal(received.ai_queued, 0);
  assert.equal(browser.storageReads, 0);
});
test('transport: missing/false/truthy consent is rejected before fetch', async () => {
  const browser = transport(), api = browser.load();
  for (const value of [undefined, null, false, 1, 'true', {}, []])
    await expectCode(api.syncTelegramBot(TOKEN, signal(), value), 'TELEGRAM_BOT_INVALID_REQUEST', api);
  assert.equal(browser.requests.length, 0);
});
test('transport: status/verify/sync cancellation and deadlines are deterministic, no retry', async () => {
  for (const action of ['getTelegramBotStatus', 'verifyTelegramBot', 'syncTelegramBot']) {
    for (const kind of ['cancel', 'timeout']) {
      const browser = transport(), api = browser.load(), controller = new AbortController();
      browser.respond(abortedFetch);
      const pending = api[action](TOKEN, controller.signal, true);
      const rejection = expectCode(pending, kind === 'cancel' ? 'ABORTED' : 'TIMEOUT', api);
      if (kind === 'cancel') controller.abort(new Error(RAW));
      else browser.fireTimeout(action === 'getTelegramBotStatus' ? 10_000 : 45_000);
      await rejection;
      assert.equal(browser.requests.length, 1);
    }
  }
  const browser = transport(), api = browser.load(), controller = new AbortController();
  controller.abort();
  await expectCode(api.getTelegramBotStatus(TOKEN, controller.signal), 'ABORTED', api);
  assert.equal(browser.requests.length, 0);
});
test('transport: aborted late response/body cannot be accepted', async () => {
  const browser = transport(), api = browser.load(), controller = new AbortController();
  const late = deferred();
  browser.respond(() => late.promise);
  const pending = api.verifyTelegramBot(TOKEN, controller.signal);
  const rejection = expectCode(pending, 'ABORTED', api);
  controller.abort(); late.resolve(reply(verified())); await rejection;
  const body = deferred(), bodyController = new AbortController();
  browser.respond(() => ({
    ok: true, status: 200, headers: new Headers(),
    json: () => body.promise,
    body: new ReadableStream({ async start(output) {
      await body.promise;
      output.enqueue(new TextEncoder().encode(JSON.stringify(verified()))); output.close();
    } }),
  }));
  const reading = api.getTelegramBotStatus(TOKEN, bodyController.signal);
  const bodyRejection = expectCode(reading, 'ABORTED', api);
  await tick(); bodyController.abort(); body.resolve(verified()); await bodyRejection;
});
test('transport: safe Chinese errors, known scope/auth codes, unknown code normalization', async () => {
  const browser = transport(), api = browser.load();
  const codes = [
    'TELEGRAM_BOT_INVALID_REQUEST', 'TELEGRAM_BOT_HUMAN_REQUIRED',
    'TELEGRAM_BOT_PAYLOAD_TOO_LARGE', 'TELEGRAM_BOT_NOT_CONFIGURED',
    'TELEGRAM_BOT_RUNTIME_NOT_CONFIGURED', 'TELEGRAM_BOT_VERIFICATION_REQUIRED',
    'TELEGRAM_BOT_BUSY', 'TELEGRAM_BOT_CLOSED', 'TELEGRAM_BOT_CANCELED',
    'TELEGRAM_BOT_CURSOR_CHANGED', 'TELEGRAM_BOT_CONFLICT', 'TELEGRAM_BOT_STORE_UNAVAILABLE',
    'TELEGRAM_BOT_AUTH_FAILED', 'TELEGRAM_BOT_RATE_LIMITED', 'TELEGRAM_BOT_UNREACHABLE',
    'TELEGRAM_BOT_PROTOCOL_ERROR', 'TELEGRAM_BOT_IDENTITY_MISMATCH', 'TELEGRAM_BOT_WEBHOOK_PRESENT',
    'TELEGRAM_BOT_PERMISSION_DENIED', 'TELEGRAM_BOT_PLATFORM_REJECTED', 'TELEGRAM_BOT_UNAVAILABLE',
    'TELEGRAM_BOT_INVALID_CONFIG', 'STORAGE_DISABLED', 'FORBIDDEN_SCOPE', 'SCOPE_DENIED',
    'NOT_FOUND', 'METHOD_NOT_ALLOWED',
  ];
  for (const code of codes) {
    browser.respond(() => errorReply(code));
    await expectCode(api.verifyTelegramBot(TOKEN, signal()), code, api, 409);
  }
  for (const code of [RAW, '__proto__', 'constructor', '', null, 12]) {
    browser.respond(() => errorReply(code, 502));
    await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'TELEGRAM_BOT_ERROR', api, 502);
  }
  for (const code of ['UNAUTHORIZED', 'DEVICE_UNAUTHORIZED', RAW]) {
    browser.respond(() => errorReply(code, 401));
    await assert.rejects(api.getTelegramBotStatus(TOKEN, signal()), (failure) => {
      assert.equal(failure.code, code === 'DEVICE_UNAUTHORIZED' ? code : 'UNAUTHORIZED');
      assert.equal(api.isTelegramBotAuthFailure(failure), true);
      assert.ok(!failure.message.includes(RAW));
      return true;
    });
  }
  assert.equal(api.isTelegramBotAuthFailure(new ServiceError(RAW, 'TELEGRAM_BOT_AUTH_FAILED', 401)), false);
  assert.equal(api.isTelegramBotAuthFailure(new ServiceError(RAW, 'UNAUTHORIZED', 403)), false);
  assert.ok(!api.telegramBotErrorMessage(new Error(RAW)).includes(RAW));
  browser.respond(() => { throw new TypeError(RAW); });
  await expectCode(api.syncTelegramBot(TOKEN, signal(), true), 'NETWORK_ERROR', api);
  browser.globals.SERVICE.error = RAW;
  const before = browser.requests.length;
  await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'CONFIG_ERROR', api);
  assert.equal(browser.requests.length, before);
});

test('DTO: strict status keys, enums, flags, nulls, identity and timestamps', async () => {
  const browser = transport(), api = browser.load();
  const invalid = [null, [], {}, status({ extra: RAW }), status({ connection_id: 'other' }),
    status({ state: 'online' }), status({ state: '__proto__' }), status({ state: null }),
    status({ runtime_configured: null }), status({ configured: 'true' }),
    status({ identity_verified: null }), status({ allowed_private_chat_count: -1 }),
    status({ allowed_private_chat_count: 0.5 }), status({ allowed_private_chat_count: Number.MAX_SAFE_INTEGER + 1 }),
    status({ checked_at: RAW }), status({ checked_at: null }), status({ last_sync_at: 1 }),
    status({ manual_receive_only: false }), status({ manual_receive_only: null }),
    status({ background_receiving: true }), status({ background_receiving: null }),
    status({ sending_enabled: true }), status({ sending_enabled: null }),
    status({ history_import: true }), status({ history_import: null }),
    verified({ configured: false }), verified({ runtime_configured: false }),
    verified({ allowed_private_chat_count: 0 }), status({ last_result: {} }),
  ];
  for (const key of Object.keys(status())) {
    const missing = status(); delete missing[key]; invalid.push(missing);
  }
  for (const value of invalid) {
    browser.respond(() => reply(value));
    await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  }
  for (const value of [status(), verified(), verified({ last_result: result(), last_sync_at: DATE }),
    status({ configured: false, runtime_configured: false, allowed_private_chat_count: 0, state: 'not_configured' }),
    status({ runtime_configured: false, state: 'runtime_not_configured' })]) {
    browser.respond(() => reply(value));
    assert.deepEqual(JSON.parse(JSON.stringify(await api.getTelegramBotStatus(TOKEN, signal()))), value);
  }
  browser.respond(() => reply(status()));
  await expectCode(api.verifyTelegramBot(TOKEN, signal()), 'INVALID_RESPONSE', api);
  browser.respond(() => reply(verified({ state: 'receiving' })));
  await expectCode(api.verifyTelegramBot(TOKEN, signal()), 'INVALID_RESPONSE', api);
});
test('DTO: result exact fields, <=100 events, disjoint counts, partial and no AI', async () => {
  const browser = transport(), api = browser.load();
  const invalid = [null, [], {}, result({ extra: true }), result({ source_kind: 'telegram' }),
    result({ source_kind: null }), result({ ai_queued: 1 }), result({ ai_queued: false }),
    result({ partial: null }), result({ partial: 'true' }),
    result({ events: 101, imported: 98 }), result({ imported: 3 }),
    result({ conversations: 4 }), result({ events: 0 }),
    result({ partial: false }), result({ unsupported: 0, not_authorized: 0, imported: 4, partial: true }),
  ];
  for (const key of ['events', 'imported', 'duplicates', 'unsupported', 'not_authorized', 'conversations'])
    for (const bad of [-1, 0.5, null, false, '1', Number.MAX_SAFE_INTEGER + 1])
      invalid.push(result({ [key]: bad }));
  for (const key of Object.keys(result())) {
    const missing = result(); delete missing[key]; invalid.push(missing);
  }
  for (const value of invalid) {
    browser.respond(() => reply(value));
    await expectCode(api.syncTelegramBot(TOKEN, signal(), true), 'INVALID_RESPONSE', api);
    browser.respond(() => reply(verified({ last_result: value, last_sync_at: DATE })));
    if (value !== null) await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  }
  for (const value of [result(), result({ events: 0, imported: 0, duplicates: 0, unsupported: 0,
    not_authorized: 0, conversations: 0, next_offset: '0', partial: false }),
    result({ events: 100, imported: 0, duplicates: 100, unsupported: 0,
      not_authorized: 0, conversations: 0, partial: false }),
    result({ events: 1, imported: 0, duplicates: 1, unsupported: 0,
      not_authorized: 0, conversations: 1, partial: false })]) {
    browser.respond(() => reply(value));
    assert.deepEqual(JSON.parse(JSON.stringify(await api.syncTelegramBot(TOKEN, signal(), true))), value);
  }
});
test('DTO: canonical decimal offset preserves 0..MaxInt64 exactly', async () => {
  const browser = transport(), api = browser.load();
  for (const next_offset of ['0', '1', '9007199254740993', '9223372036854775807']) {
    browser.respond(() => reply(result({ next_offset })));
    assert.equal((await api.syncTelegramBot(TOKEN, signal(), true)).next_offset, next_offset);
  }
  for (const next_offset of [0, 123, null, '', '-1', '+1', '01', ' 1', '1 ', '1e3', '1.0',
    '9223372036854775808', '9999999999999999999', '10000000000000000000', '１', RAW]) {
    browser.respond(() => reply(result({ next_offset })));
    await expectCode(api.syncTelegramBot(TOKEN, signal(), true), 'INVALID_RESPONSE', api);
  }
});
test('DTO: malformed, non-200 success and oversized response bodies fail closed', async () => {
  const browser = transport(), api = browser.load();
  for (const response of [new Response('{bad-json'), new Response('null'),
    new Response(JSON.stringify(verified()), { status: 201 }),
    new Response(null, { status: 204 })]) {
    browser.respond(() => response);
    await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  }
  // Valid JSON with excessive whitespace must not bypass the body budget.
  browser.respond(() => new Response(' '.repeat(70_000) + JSON.stringify(verified())));
  await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  browser.respond(() => reply(verified(), 200, { 'Content-Length': '99999999' }));
  await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  let canceled = false, chunks = 0;
  browser.respond(() => new Response(new ReadableStream({
    pull(output) {
      if (++chunks > 10) output.close();
      else output.enqueue(new Uint8Array(8192).fill(32));
    },
    cancel() { canceled = true; },
  })));
  await expectCode(api.getTelegramBotStatus(TOKEN, signal()), 'INVALID_RESPONSE', api);
  assert.equal(canceled, true, 'oversized stream is canceled rather than drained');
});

// Panel lifecycle using the real client and fixed local responses.
test('panel: mount is one status read, honest manual-only copy and stable associations', async () => {
  const browser = transport(); browser.respond(() => reply(status()));
  const { panel } = panelFor(browser);
  await panel.flush();
  assert.deepEqual(browser.requests.map(({ url }) => new URL(url).pathname), [
    '/api/v1/connections/telegram-bot/status',
  ]);
  const ids = nodes(panel.tree).filter((node) => node.props.id).map((node) => node.props.id);
  assert.equal(new Set(ids).size, ids.length);
  assert.ok(nodes(panel.tree).some((node) => node.props['aria-labelledby'] &&
    ids.includes(node.props['aria-labelledby'])));
  assert.match(text(panel.tree), /手动收件/);
  assert.match(text(panel.tree), /不会.*(?:后台|自动)|不.*(?:后台|持续)/);
  assert.equal(disabled(panel, '核验 Bot 身份'), false);
  assert.equal(disabled(panel, '手动收件一次'), true);
  assert.equal(checked(panel), false);
  await panel.flush(); assert.equal(browser.requests.length, 1);
  assert.equal(nodes(panel.tree).some((node) => node.type === 'input' && node.props.type === 'password'), false);
  panel.unmount(); assert.equal(panel.postUnmountWrites, 0);
});
test('panel: verify never receives; sync consumes consent before IO and blocks stale double-click', async () => {
  const browser = transport();
  browser.respond(({ url }) => reply(url.endsWith('/status') ? status() : verified()));
  const { panel } = panelFor(browser); await panel.flush();
  await click(panel, '核验 Bot 身份');
  assert.equal(browser.requests.at(-1).options.body, '{}');
  assert.equal(browser.requests.length, 2);
  assert.equal(disabled(panel, '手动收件一次'), true);
  await check(panel, consentLabel);
  assert.equal(disabled(panel, '手动收件一次'), false);
  const late = deferred(); browser.respond(() => late.promise);
  const staleClick = button(panel, '手动收件一次').props.onClick;
  staleClick(); staleClick(); await panel.flush();
  assert.equal(browser.requests.length, 3);
  assert.equal(browser.requests.at(-1).options.body, '{"consent_receive":true}');
  assert.equal(checked(panel), false);
  for (const label of ['刷新状态', '核验 Bot 身份']) assert.equal(disabled(panel, label), true);
  late.resolve(reply(result())); await panel.flush();
  assert.match(text(panel.tree), /新增 2 条、重复 1 条/);
  assert.match(text(panel.tree), /未创建 AI 任务/);
  assert.equal(checked(panel), false);
  assert.equal(disabled(panel, '手动收件一次'), true);
  staleClick(); await panel.flush(); assert.equal(browser.requests.length, 3);
  assert.ok(browser.requests.every(({ url }) => url.includes('/connections/telegram-bot/')),
    'neither verify nor sync requests AI or messages from another page');
  panel.unmount();
});
test('panel: every failed/uncertain sync resets consent and stale identity, no retries', async () => {
  for (const kind of ['server', 'network', 'invalid', 'timeout']) {
    const browser = transport(); browser.respond(() => reply(verified()));
    const { panel } = panelFor(browser); await panel.flush(); await check(panel, consentLabel);
    browser.respond(kind === 'server' ? () => errorReply('TELEGRAM_BOT_RATE_LIMITED', 429)
      : kind === 'network' ? () => { throw new Error(RAW); }
      : kind === 'invalid' ? () => reply(result({ ai_queued: 1 })) : abortedFetch);
    await click(panel, '手动收件一次');
    if (kind === 'timeout') { browser.fireTimeout(45_000); await panel.flush(); }
    assert.equal(checked(panel), false);
    assert.equal(disabled(panel, '手动收件一次'), true);
    assert.ok(nodes(panel.tree).some((node) => node.props.role === 'alert'));
    assert.ok(!text(panel.tree).includes(RAW));
    assert.equal(browser.requests.length, 2);
    await panel.flush(); assert.equal(browser.requests.length, 2);
    panel.unmount(); assert.equal(panel.postUnmountWrites, 0);
  }
});
test('panel: refreshing/reverifying also revokes previous one-use consent', async () => {
  const browser = transport(); browser.respond(() => reply(verified()));
  const { panel } = panelFor(browser); await panel.flush();
  for (const action of ['刷新状态', '核验 Bot 身份']) {
    await check(panel, consentLabel); await click(panel, action);
    assert.equal(checked(panel), false);
    assert.equal(disabled(panel, '手动收件一次'), true);
  }
  assert.equal(browser.requests.filter(({ url }) => url.endsWith('/sync')).length, 0);
  panel.unmount();
});
test('panel: unavailable states never allow receive or manufacture a verification', async () => {
  for (const next of [status({ configured: false, state: 'not_configured' }),
    status({ runtime_configured: false, state: 'runtime_not_configured' }),
    status({ state: 'closed' }), status({ state: 'verifying' }),
    verified({ state: 'receiving' }), status({ allowed_private_chat_count: 0 }),
    status({ state: 'identity_mismatch' }), status({ state: 'webhook_present' })]) {
    const browser = transport(); browser.respond(() => reply(next));
    const { panel } = panelFor(browser); await panel.flush();
    assert.equal(disabled(panel, '手动收件一次'), true);
    assert.equal(checkbox(panel, consentLabel)._disabled, true);
    assert.equal(browser.requests.length, 1);
    panel.unmount();
  }
  const browser = transport(), { panel } = panelFor(browser, { token: '' });
  await panel.flush(); assert.equal(browser.requests.length, 0);
  assert.equal(disabled(panel, '刷新状态'), true); panel.unmount();
});
test('panel: token switch aborts pending status/verify/sync; late success or auth failure is ignored', async () => {
  for (const action of ['status', 'verify', 'sync']) for (const lateKind of ['success', 'auth']) {
    const browser = transport(), late = deferred(); let authFailures = 0;
    browser.respond(() => action === 'status' ? late.promise : reply(verified()));
    const { panel } = panelFor(browser, { onAuthFailure() { authFailures++; } });
    await panel.flush();
    if (action !== 'status') {
      browser.respond(() => late.promise);
      if (action === 'sync') await check(panel, consentLabel);
      await click(panel, action === 'sync' ? '手动收件一次' : '核验 Bot 身份');
    }
    const oldRequest = browser.requests.at(-1);
    browser.respond(() => reply(status({ allowed_private_chat_count: 7 })));
    panel.updateProps({ token: OTHER_TOKEN }); await panel.flush();
    assert.equal(oldRequest.options.signal.aborted, true);
    assert.equal(browser.requests.at(-1).options.headers.Authorization, `Bearer ${OTHER_TOKEN}`);
    const before = text(panel.tree), requests = browser.requests.length;
    late.resolve(lateKind === 'auth' ? errorReply('UNAUTHORIZED', 401)
      : reply(action === 'sync' ? result() : verified()));
    await panel.flush();
    assert.equal(text(panel.tree), before);
    assert.equal(authFailures, 0);
    assert.equal(checked(panel), false);
    assert.equal(browser.requests.length, requests);
    panel.unmount(); assert.equal(panel.postUnmountWrites, 0);
  }
});
test('panel: unmount aborts any operation; late success/failure never writes state', async () => {
  for (const action of ['status', 'verify', 'sync']) for (const lateKind of ['success', 'failure']) {
    const browser = transport(), late = deferred(); let authFailures = 0;
    browser.respond(() => action === 'status' ? late.promise : reply(verified()));
    const { panel } = panelFor(browser, { onAuthFailure() { authFailures++; } });
    await panel.flush();
    if (action !== 'status') {
      browser.respond(() => late.promise);
      if (action === 'sync') await check(panel, consentLabel);
      await click(panel, action === 'sync' ? '手动收件一次' : '核验 Bot 身份');
    }
    panel.unmount(); assert.equal(browser.requests.at(-1).options.signal.aborted, true);
    if (lateKind === 'failure') late.reject(new Error(RAW));
    else late.resolve(reply(action === 'sync' ? result() : verified()));
    await panel.flush();
    assert.equal(panel.postUnmountWrites, 0); assert.equal(authFailures, 0);
  }
});
test('panel: current service 401 clears auth; platform auth failure does not; strict replay only reads status', async () => {
  for (const [code, http, expected] of [['UNAUTHORIZED', 401, 1], ['DEVICE_UNAUTHORIZED', 401, 1],
    ['TELEGRAM_BOT_AUTH_FAILED', 502, 0], ['SCOPE_DENIED', 403, 0]]) {
    const browser = transport(); let authFailures = 0;
    browser.respond(() => reply(verified()));
    const { panel } = panelFor(browser, { onAuthFailure() { authFailures++; } }); await panel.flush();
    browser.respond(() => errorReply(code, http)); await click(panel, '核验 Bot 身份');
    assert.equal(authFailures, expected);
    assert.equal(disabled(panel, '手动收件一次'), true);
    assert.ok(!text(panel.tree).includes(RAW)); panel.unmount();
  }
  const browser = transport(); browser.respond(() => reply(verified()));
  const { panel } = panelFor(browser, {}, true); await panel.flush();
  assert.equal(browser.requests.length, 2, 'development strict replay is two abortable status reads, not a mutation');
  assert.equal(browser.requests[0].options.signal.aborted, true);
  assert.ok(browser.requests.every(({ options }) => options.method === 'GET'));
  panel.unmount(); assert.equal(panel.postUnmountWrites, 0);
});

// Inbox validators run with the actual service transport. Existing message
// sources stay accepted; platform alone must never manufacture bot provenance.
const inboxInfo = (live_status = 'manual_receive_only') => ({
  storage_enabled: true, source_mode: 'mixed', import_limit: 1000,
  platforms: [{ id: 'telegram', name: 'Telegram', live_status, message_count: 1 }],
});
const conversation = {
  id: 'synthetic-conversation', conversation_kind: 'private', connection_id: 'synthetic-telegram',
  platform: 'telegram', account_id: 'synthetic-account', peer_id: 'synthetic-private',
  title: '合成 Telegram 私聊', last_message: '合成正文', last_message_at: DATE,
  unread_count: 0, pinned: false, archived: false,
};
const message = (extra = {}) => ({
  id: 'synthetic-message', conversation_id: conversation.id, external_id: 'synthetic-external',
  direction: 'incoming', body: '合成正文，不含真实消息', occurred_at: DATE, read: true, ...extra,
});
function inboxAPI(browser) {
  const { isProfileCategory } = browser.load('profile-api.ts', ['isProfileCategory']);
  return browser.load('inbox-api.ts', ['getInboxInfo', 'getConversations', 'getConversationMessages',
    'updateConversation', 'PLATFORM_NAMES'], { isProfileCategory });
}
test('Inbox transport: exact bot source and manual-only status accepted; unknown/null rejected', async () => {
  const browser = transport(), api = inboxAPI(browser);
  for (const source of ['telegram_bot', 'import', 'imap', 'wechatdb', undefined]) {
    browser.respond(() => reply({ items: [message(source === undefined ? {} : { source })], next_cursor: null }));
    const page = await api.getConversationMessages(TOKEN, signal(), conversation.id);
    assert.equal(page.items[0].source, source);
  }
  for (const source of ['telegram', 'telegram_bot ', 'Telegram_Bot', '', '__proto__', null, false, 1, {}]) {
    browser.respond(() => reply({ items: [message({ source })], next_cursor: null }));
    await expectCode(api.getConversationMessages(TOKEN, signal(), conversation.id), 'INVALID_RESPONSE');
  }
  for (const live of ['not_connected', 'probe_only', 'unsupported', 'configured', 'manual_receive_only']) {
    browser.respond(() => reply(inboxInfo(live)));
    assert.equal((await api.getInboxInfo(TOKEN, signal())).platforms[0].live_status, live);
  }
  for (const live of ['online', 'manual_receive_only ', 'telegram_bot', null, false, 1, {}, [], ['manual_receive_only']]) {
    browser.respond(() => reply(inboxInfo(live)));
    await expectCode(api.getInboxInfo(TOKEN, signal()), 'INVALID_RESPONSE');
  }
});
async function inboxFor(source) {
  const browser = transport(), api = inboxAPI(browser);
  browser.respond(({ url }) => {
    const path = new URL(url).pathname;
    if (path === '/api/v1/inbox') return reply(inboxInfo());
    if (path === '/api/v1/conversations') return reply({ items: [conversation], next_cursor: null });
    if (path.endsWith('/messages')) return reply({ items: [message(source === undefined ? {} : { source })], next_cursor: null });
    throw Error(`Unexpected synthetic Inbox request ${path}`);
  });
  const panel = componentHarness('Inbox.tsx', 'Inbox', {
    ...api,
    // Layout/focus and unrelated integrations are outside this synthetic check.
    useLayoutEffect() {}, WechatPanel: () => null, EmailPanel: () => null,
    ProfilePanel: () => null, AIPanel: () => null, ConversationKindSelect: () => null,
    KeywordMatch: ({ text: value }) => value, keywordError: () => null,
    PROFILE_CATEGORIES: ['unclassified'], CATEGORY_NAMES: { unclassified: '未分类' },
  }, { token: TOKEN, aiDrafts: new Map(), onSettings() {}, onAuthFailure() {}, onAssistantHelp() {} }, browser);
  await panel.flush();
  const control = nodes(panel.tree).find((node) => node.type === 'button' &&
    node.props.className?.startsWith('inbox-conversation '));
  assert.ok(control, 'shipped Inbox renders the synthetic conversation');
  control.props.onClick({ currentTarget: control.element }); await panel.flush();
  return { browser, panel };
}
test('Inbox render: manual-only is not realtime-disconnected and exact provenance only', async () => {
  for (const source of ['telegram_bot', 'import', 'imap', 'wechatdb', undefined]) {
    const { browser, panel } = await inboxFor(source);
    assert.match(text(panel.tree), /仅手动收件/);
    assert.ok(!text(panel.tree).includes('实时未连接'));
    const badges = nodes(panel.tree).filter((node) => node.type === 'span' &&
      text(node) === 'Telegram Bot · 手动收件');
    assert.equal(badges.length, source === 'telegram_bot' ? 1 : 0,
      'Telegram platform/imported history must not get a fabricated Bot badge');
    assert.ok(browser.requests.every(({ options }) => options.method === 'GET'));
    assert.ok(browser.requests.every(({ url }) => !/\/ai|\/sync|\/verify/.test(url)));
    panel.unmount(); assert.equal(panel.postUnmountWrites, 0);
  }
});
test('App integration: token-keyed Settings panel, Inbox mounts and reloads after leaving Settings', async () => {
  // Read actual App functions rather than asserting only source substrings.
  const jsx = (type, props, ...children) => ({ type, props: { ...props, children } });
  const Stub = () => null;
  const globals = {
    jsx, Fragment: 'fragment', SERVICE: { url: SERVICE_URL, error: null },
    Icon: Stub, HelpDetails: 'details', MilkyPanel: Stub, TelegramBotPanel: 'TelegramPanelStub',
    AIModelPanel: Stub, DigestPanel: Stub,
  };
  const { Settings } = runInNewContext(`(function(){${compiled('App.tsx')}; return {Settings};})()`, globals);
  const base = { session: { status: 'idle' }, activeToken: TOKEN, digestUnresolved: new Set(),
    tokenDraft: '', hasToken: true, storageAvailable: false, rememberDevice: false,
    remembered: false, deviceExpiresAt: null, persistenceNotice: null, forgetting: false,
    onRememberChange() {}, onDraftChange() {}, onConnect() {}, onClear() {}, onCancel() {},
    onSystemUpdate() {}, onAuthFailure() {} };
  for (const token of [TOKEN, OTHER_TOKEN]) {
    const tree = Settings({ ...base, activeToken: token });
    const panels = nodes(tree).filter((node) => node.type === 'TelegramPanelStub');
    assert.equal(panels.length, 1);
    assert.equal(panels[0].props.key, `telegram-bot:${token}`);
    assert.equal(panels[0].props.token, token);
  }
  // Settings and Inbox are mutually exclusive in App; there is no always-mounted
  // Inbox to invalidate, no extra sync callback/event/navigation to fabricate.
  // A fresh Inbox mount reads fresh service data. Verify/sync were covered above.
  const first = await inboxFor('import'); first.panel.unmount();
  const next = await inboxFor('telegram_bot');
  assert.ok(next.browser.requests.some(({ url }) => new URL(url).pathname === '/api/v1/inbox'));
  assert.ok(next.browser.requests.some(({ url }) => new URL(url).pathname.endsWith('/messages')));
  assert.ok(text(next.panel.tree).includes('Telegram Bot · 手动收件'));
  next.panel.unmount();
});

let failures = 0;
for (const { name, run } of tests) {
  try { await run(); console.log(`PASS ${name}`); }
  catch (failure) { failures++; console.error(`FAIL ${name}\n${failure.stack ?? failure}`); }
}
console.log(`Telegram Bot synthetic checks: ${tests.length - failures}/${tests.length} groups passed; ${assertions} rejection assertions; no real DOM/network/platform IO.`);
if (failures) process.exitCode = 1;
