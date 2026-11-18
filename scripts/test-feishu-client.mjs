import assert from 'node:assert/strict';
import {
  BINDING,
  DELIVERY_ID,
  END,
  FEISHU_EXPORTS,
  HASH,
  NEXT,
  NOW,
  REPORT_ID,
  START,
  TARGET,
  TOKEN,
  ServiceError,
  button,
  check,
  checkbox,
  click,
  componentHarness,
  delivery,
  emptyStatus,
  errorReply,
  field,
  fill,
  model,
  nodes,
  reply,
  report,
  schedule,
  signal,
  status,
  submit,
  text,
  tick,
  transport,
  waitForAbort,
} from './feishu-test-harness.mjs';

// All responses, credentials, messages, models, clocks and timers are synthetic.
// This script never starts a server, calls a provider/platform, or enables a real schedule.
const network = transport(),
  api = network.load('feishu-api.ts', FEISHU_EXPORTS);
const configInput = {
  expected_revision: 1,
  app_id: 'cli_synthetic',
  app_secret: 'synthetic-form-only-secret',
  retain_secret: false,
  recipient_open_id: 'ou_synthetic_form_only_1234',
  confirmed: true,
};
const plain = (value) => JSON.parse(JSON.stringify(value));
const lastRequest = (method, path, body) => {
  const request = network.requests.at(-1);
  assert.equal(request.options.method, method);
  assert.equal(new URL(request.url).pathname, `/api/v1/feishu/${path}`);
  assert.deepEqual(
    request.options.body === undefined
      ? undefined
      : JSON.parse(request.options.body),
    body,
  );
};
network.respond(() => reply(status()));
assert.equal((await api.getFeishuStatus(TOKEN, signal())).config_revision, 1);
lastRequest('GET', 'status');
network.respond(() => reply(status({ config_revision: 2 })));
await api.saveFeishuConfig(TOKEN, signal(), {
  ...configInput,
  webhook: 'must-not-be-serialized',
  token: 'must-not-be-serialized',
});
lastRequest('PATCH', 'config', configInput);
network.respond(() => reply(emptyStatus({ config_revision: 3 })));
await api.clearFeishuConfig(TOKEN, signal(), {
  expected_revision: 2,
  confirmed: true,
  app_secret: 'must-not-be-serialized',
});
lastRequest('DELETE', 'config', { expected_revision: 2, confirmed: true });
network.respond(() => reply(schedule()));
assert.equal((await api.getFeishuSchedule(TOKEN, signal())).enabled, false);
lastRequest('GET', 'schedule');
const enabledSchedule = schedule({
  enabled: true,
  revision: 2,
  model_config_id: BINDING.model_config_id,
  model_config_revision: 0,
  config_revision: 1,
  target_binding_id: TARGET,
  next_window_end: NEXT,
});
network.respond(() => reply(enabledSchedule));
await api.saveFeishuSchedule(TOKEN, signal(), {
  enabled: true,
  expected_revision: 1,
  ...BINDING,
  text: 'must-not-be-serialized',
});
lastRequest('PATCH', 'schedule', {
  enabled: true,
  expected_revision: 1,
  ...BINDING,
});
network.respond(() => reply(schedule({ revision: 3 })));
await api.saveFeishuSchedule(TOKEN, signal(), {
  enabled: false,
  expected_revision: 2,
  confirmed: true,
  model_config_id: 'ignored-on-disable',
  model_config_revision: -99,
  target_binding_id: 'ignored-on-disable',
});
lastRequest('PATCH', 'schedule', {
  enabled: false,
  expected_revision: 2,
  confirmed: true,
});
network.respond(() => reply({ report: report('running') }, 202));
assert.equal(
  (
    await api.generateFeishuPreview(TOKEN, signal(), {
      ...BINDING,
      recipient_open_id: 'must-not-be-serialized',
    })
  ).state,
  'running',
);
lastRequest('POST', 'preview', BINDING);
network.respond(() => reply({ report: null }));
assert.equal(await api.getLatestFeishuReport(TOKEN, signal()), null);
lastRequest('GET', 'reports/latest');
network.respond(() => reply({ report: report() }));
assert.equal(
  (await api.getFeishuReport(TOKEN, signal(), REPORT_ID)).id,
  REPORT_ID,
);
lastRequest('GET', `reports/${REPORT_ID}`);
network.respond(() => reply({ delivery: delivery('running') }, 202));
assert.equal(
  (
    await api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
      ...BINDING,
      content_hash: HASH,
      text: 'must-not-be-serialized',
      recipient_open_id: 'must-not-be-serialized',
    })
  ).state,
  'running',
);
lastRequest('POST', `reports/${REPORT_ID}/send`, {
  ...BINDING,
  content_hash: HASH,
});
network.respond(() => reply({ delivery: delivery() }));
assert.equal(
  (await api.getFeishuDelivery(TOKEN, signal(), DELIVERY_ID)).message_id,
  'synthetic-platform-message',
);
lastRequest('GET', `deliveries/${DELIVERY_ID}`);
network.respond(() => reply(model()));
assert.equal(
  (await api.getFeishuModelStatus(TOKEN, signal())).model_config_revision,
  0,
  'environment model revision zero is valid',
);
// HTTP 200 may be an existing immutable ledger in any state. It is a receipt,
// not an invitation for the client to repeat generation or sending.
for (const state of ['running', 'succeeded', 'failed', 'unknown']) {
  network.respond(() => reply({ report: report(state) }));
  const previewsBefore = network.requests.length;
  assert.equal(
    (await api.generateFeishuPreview(TOKEN, signal(), BINDING)).state,
    state,
  );
  assert.equal(network.requests.length, previewsBefore + 1);
  network.respond(() => reply({ delivery: delivery(state) }));
  const sendsBefore = network.requests.length;
  assert.equal(
    (
      await api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
        ...BINDING,
        content_hash: HASH,
      })
    ).state,
    state,
  );
  assert.equal(network.requests.length, sendsBefore + 1);
}

const invalidResponse = async (
  payload,
  call = () => api.getFeishuStatus(TOKEN, signal()),
  uncertain = false,
  httpStatus = 200,
) => {
  network.respond(() => reply(payload, httpStatus));
  const before = network.requests.length;
  await assert.rejects(
    call(),
    (failure) =>
      failure.code === 'INVALID_RESPONSE' && failure.uncertain === uncertain,
  );
  assert.equal(
    network.requests.length - before,
    1,
    'invalid response must not cause an implicit retry',
  );
};
// Feishu model identity is a stable 64-lowerhex binding, including revision-zero
// environment models. Bad DTOs stay unreadable; bad input must not reach fetch.
for (const modelID of [
  'synthetic-environment',
  'c'.repeat(63),
  'c'.repeat(65),
  'C'.repeat(64),
  'g'.repeat(64),
]) {
  await invalidResponse({ ...enabledSchedule, model_config_id: modelID }, () =>
    api.getFeishuSchedule(TOKEN, signal()),
  );
  await invalidResponse(
    { delivery: delivery('succeeded', { model_config_id: modelID }) },
    () => api.getFeishuDelivery(TOKEN, signal(), DELIVERY_ID),
  );
  await invalidResponse(
    { report: report('succeeded', { model_config_id: modelID }) },
    () => api.getLatestFeishuReport(TOKEN, signal()),
  );
  await invalidResponse(model({ model_config_id: modelID }), () =>
    api.getFeishuModelStatus(TOKEN, signal()),
  );
  const badBinding = { ...BINDING, model_config_id: modelID };
  for (const call of [
    () => api.generateFeishuPreview(TOKEN, signal(), badBinding),
    () =>
      api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
        ...badBinding,
        content_hash: HASH,
      }),
    () =>
      api.saveFeishuSchedule(TOKEN, signal(), {
        enabled: true,
        expected_revision: 1,
        ...badBinding,
      }),
  ]) {
    const before = network.requests.length;
    await assert.rejects(
      call(),
      (failure) => failure.code === 'INVALID_REQUEST',
    );
    assert.equal(
      network.requests.length,
      before,
      'invalid model binding must be rejected before fetch',
    );
  }
}
const boundedConversations = Array.from(
  { length: 4 },
  (_, index) => `synthetic-conversation-${index}`,
);
const boundedReport = report('succeeded', {
  conversation_ids: boundedConversations,
  conversation_count: 4,
  source_refs: boundedConversations.map((conversation_id) => ({
    conversation_id,
    settings_revision: 0,
  })),
  todos: Array.from({ length: 8 }, (_, index) => ({
    ...report().todos[0],
    text: `synthetic bounded todo ${index}`,
  })),
  excerpts: Array.from({ length: 3 }, (_, index) => ({
    ...report().excerpts[0],
    message_id:
      index === 0 ? 'synthetic-message' : `synthetic-message-${index}`,
    conversation_id: boundedConversations[index],
  })),
});
network.respond(() => reply({ report: boundedReport }));
assert.equal(
  (await api.getLatestFeishuReport(TOKEN, signal())).todos.length,
  8,
  'the exact frozen limits remain valid',
);
for (const patch of [
  {
    todos: [
      ...boundedReport.todos,
      { ...report().todos[0], text: 'synthetic over-limit todo' },
    ],
  },
  {
    excerpts: [
      ...boundedReport.excerpts,
      { ...report().excerpts[0], message_id: 'synthetic-message-over-limit' },
    ],
  },
  {
    conversation_ids: [
      ...boundedConversations,
      'synthetic-conversation-over-limit',
    ],
    conversation_count: 5,
  },
  {
    source_refs: [
      ...boundedReport.source_refs,
      {
        conversation_id: 'synthetic-conversation-over-limit',
        settings_revision: 0,
      },
    ],
  },
])
  await invalidResponse({ report: { ...boundedReport, ...patch } }, () =>
    api.getLatestFeishuReport(TOKEN, signal()),
  );

for (const patch of [
  { config_revision: 0 },
  { config_revision: null },
  { config_revision: 1.5 },
  { config_revision: Number.MAX_SAFE_INTEGER + 1 },
  { configured: 'true' },
  { configured: null },
  { recipient_ownership_verified: true },
  { recipient_ownership_verified: undefined },
  { latest_report_id: null },
  { latest_report_id: 'fr_bad' },
  { latest_delivery: {} },
  { timezone: 'UTC' },
  { slots: ['20:00', '08:00'] },
  { slots: ['08:00', '21:00'] },
  { recipient_open_id_masked: 'ou_unmasked_raw_id' },
  { app_secret: 'synthetic-leak' },
  { recipient_open_id: 'synthetic-leak' },
  { token: 'synthetic-leak' },
  {
    capabilities: {
      private_self_only: true,
      automatic_retry: false,
      app_secret: 'synthetic-leak',
    },
  },
  { capabilities: { private_self_only: true, automatic_retry: true } },
  { capabilities: { private_self_only: false, automatic_retry: false } },
  { schedule: schedule({ enabled: true }) },
  { schedule: schedule({ next_window_end: NEXT }) },
  { schedule: { ...enabledSchedule, config_revision: 2 } },
  { schedule: { ...enabledSchedule, target_binding_id: 'c'.repeat(64) } },
])
  await invalidResponse(status(patch));
for (const fieldName of [
  'configured',
  'app_id',
  'target_binding_id',
  'target_label_masked',
  'recipient_open_id_masked',
  'schedule',
  'capabilities',
  'timezone',
  'slots',
]) {
  const malformed = status();
  delete malformed[fieldName];
  await invalidResponse(malformed);
}
for (const malformed of [
  [],
  null,
  {},
  { status: status() },
  { ...status(), error: {} },
])
  await invalidResponse(malformed);
network.respond(() => reply(emptyStatus()));
assert.equal(
  (await api.getFeishuStatus(TOKEN, signal())).configured,
  false,
  'initial unconfigured revision is one, not zero',
);
for (const patch of [
  { id: 'fr_BAD' },
  { state: 'complete' },
  { window_start: '2099-05-31T00:00:00Z' },
  { window_end: '2099-06-01T01:00:00Z' },
  { window_end: '2099-06-01T00:00:00.000000001Z' },
  { created_at: '2099-02-31T00:00:00Z' },
  { created_at: 'not-a-date' },
  { generated_at: null },
  { created_at: undefined },
  { schedule_revision: -1 },
  { schedule_revision: 1 },
  { trigger: 'other' },
  { content_hash: '' },
  { content_hash: 'B'.repeat(64) },
  { text: '' },
  { model_config_revision: -1 },
  { config_revision: 0 },
  { conversation_count: 2 },
  {
    source_refs: [
      { conversation_id: 'synthetic-conversation', settings_revision: null },
    ],
  },
  { todos: [{ text: 'synthetic', evidence_ids: [], needs_review: false }] },
  { todos: [{ text: 'synthetic', evidence_ids: [] }] },
  {
    todos: [
      {
        text: 'synthetic',
        evidence_ids: [],
        needs_review: true,
        app_secret: 'synthetic-leak',
      },
    ],
  },
  {
    excerpts: [
      { ...report().excerpts[0], recipient_open_id: 'synthetic-leak' },
    ],
  },
  { source_refs: [{ ...report().source_refs[0], token: 'synthetic-leak' }] },
  { delivery: delivery('succeeded', { report_id: `fr_${'3'.repeat(32)}` }) },
  { delivery: delivery('succeeded', { content_hash: 'c'.repeat(64) }) },
])
  await invalidResponse({ report: report('succeeded', patch) }, () =>
    api.getLatestFeishuReport(TOKEN, signal()),
  );
for (const state of ['running', 'failed', 'unknown']) {
  network.respond(() => reply({ report: report(state) }));
  assert.equal((await api.getLatestFeishuReport(TOKEN, signal())).state, state);
}
await invalidResponse(
  { report: report('running', { generated_at: END }) },
  () => api.getLatestFeishuReport(TOKEN, signal()),
);
await invalidResponse(
  { report: report('unknown', { generated_at: null }) },
  () => api.getLatestFeishuReport(TOKEN, signal()),
);
network.respond(() =>
  reply({
    report: report('succeeded', { trigger: 'schedule', schedule_revision: 4 }),
  }),
);
assert.equal(
  (await api.getLatestFeishuReport(TOKEN, signal())).schedule_revision,
  4,
);
for (const state of ['succeeded', 'failed', 'unknown']) {
  network.respond(() => reply({ delivery: delivery(state) }));
  assert.equal(
    (await api.getFeishuDelivery(TOKEN, signal(), DELIVERY_ID)).state,
    state,
  );
  await invalidResponse(
    { delivery: delivery(state, { completed_at: null }) },
    () => api.getFeishuDelivery(TOKEN, signal(), DELIVERY_ID),
  );
}
for (const patch of [
  { state: 'succeeded', message_id: '' },
  { state: 'succeeded', error_code: 'FEISHU_FAILED' },
  { state: 'failed', error_code: '' },
  { state: 'running', completed_at: END },
  { state: 'running', message_id: 'not-yet-accepted' },
  { id: `fd_${'3'.repeat(32)}` },
  { content_hash: '' },
]) {
  await invalidResponse({ delivery: delivery('running', patch) }, () =>
    api.getFeishuDelivery(TOKEN, signal(), DELIVERY_ID),
  );
}
await invalidResponse(
  { report: report() },
  () => api.generateFeishuPreview(TOKEN, signal(), BINDING),
  true,
  202,
);
await invalidResponse(
  { report: report('running', { target_binding_id: 'c'.repeat(64) }) },
  () => api.generateFeishuPreview(TOKEN, signal(), BINDING),
  true,
  202,
);
await invalidResponse(
  { delivery: delivery() },
  () =>
    api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
      ...BINDING,
      content_hash: HASH,
    }),
  true,
  202,
);
await invalidResponse(
  { delivery: delivery('running', { config_revision: 2 }) },
  () =>
    api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
      ...BINDING,
      content_hash: HASH,
    }),
  true,
  202,
);
await invalidResponse(
  status({ config_revision: 1 }),
  () => api.saveFeishuConfig(TOKEN, signal(), configInput),
  true,
);
await invalidResponse(
  status({
    config_revision: 2,
    schedule: { ...enabledSchedule, config_revision: 2 },
  }),
  () => api.saveFeishuConfig(TOKEN, signal(), configInput),
  true,
);
await invalidResponse(
  schedule({ revision: 1 }),
  () =>
    api.saveFeishuSchedule(TOKEN, signal(), {
      enabled: false,
      expected_revision: 1,
      confirmed: true,
    }),
  true,
);
for (const patch of [
  { sending_enabled: true },
  { model_config_revision: -1 },
  { daily_limit: NaN },
  { api_key: 'synthetic-leak' },
]) {
  await invalidResponse(model(patch), () =>
    api.getFeishuModelStatus(TOKEN, signal()),
  );
}
for (const call of [
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      expected_revision: 0,
    }),
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      app_id: `cli_${'x'.repeat(125)}`,
    }),
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      recipient_open_id: `ou_${'x'.repeat(126)}`,
    }),
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      app_secret: 'x'.repeat(4097),
    }),
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      retain_secret: true,
    }),
  () =>
    api.saveFeishuConfig(TOKEN, signal(), {
      ...configInput,
      app_secret: '',
      retain_secret: false,
    }),
  () =>
    api.clearFeishuConfig(TOKEN, signal(), {
      expected_revision: null,
      confirmed: true,
    }),
  () =>
    api.saveFeishuSchedule(TOKEN, signal(), {
      enabled: true,
      expected_revision: 1,
      ...BINDING,
      confirmed: false,
    }),
  () =>
    api.generateFeishuPreview(TOKEN, signal(), {
      ...BINDING,
      model_config_revision: -1,
    }),
  () =>
    api.sendFeishuReport(TOKEN, signal(), REPORT_ID, {
      ...BINDING,
      content_hash: 'invalid',
    }),
  () => api.getFeishuReport(TOKEN, signal(), '../other?token=secret'),
  () => api.getFeishuDelivery(TOKEN, signal(), `${DELIVERY_ID}/send`),
]) {
  const before = network.requests.length;
  await assert.rejects(
    call(),
    (failure) => failure.code === 'INVALID_REQUEST' && !failure.uncertain,
  );
  assert.equal(network.requests.length, before);
}

// Never replay a mutation after network/abort/timeout/5xx/bad response/auth recovery.
const generate = (requestSignal = signal()) =>
  api.generateFeishuPreview(TOKEN, requestSignal, BINDING);
for (const [replyCode, httpStatus, uncertain] of [
  ['FEISHU_UNAVAILABLE', 503, true],
  ['AUTH_RETRY_REQUIRED', 409, true],
  ['FEISHU_TOKEN_EXPIRED', 401, false],
  ['DEVICE_AUTH_UNAVAILABLE', 503, true],
  ['UNAUTHORIZED', 401, false],
  ['DEVICE_UNAUTHORIZED', 401, false],
  ['AI_QUOTA_EXCEEDED', 429, false],
]) {
  network.respond(() => errorReply(replyCode, httpStatus));
  const before = network.requests.length;
  await assert.rejects(
    generate(),
    (failure) =>
      failure.code === replyCode &&
      failure.uncertain === uncertain &&
      !failure.message.includes('RAW-PRIVATE'),
  );
  assert.equal(network.requests.length, before + 1);
}
network.respond(() => {
  throw Error('RAW-PRIVATE-TRANSPORT-DETAIL');
});
let before = network.requests.length;
await assert.rejects(
  generate(),
  (failure) =>
    failure.code === 'NETWORK_ERROR' &&
    failure.uncertain &&
    !failure.message.includes('RAW-PRIVATE'),
);
assert.equal(network.requests.length, before + 1);
network.respond(() => reply({}));
await assert.rejects(
  generate(),
  (failure) => failure.code === 'INVALID_RESPONSE' && failure.uncertain,
);
network.respond(waitForAbort);
let pending = generate();
network.fireTimeout(40_000);
await assert.rejects(
  pending,
  (failure) => failure.code === 'TIMEOUT' && failure.uncertain,
);
pending = api.getFeishuStatus(TOKEN, signal());
network.fireTimeout(10_000);
await assert.rejects(
  pending,
  (failure) => failure.code === 'TIMEOUT' && !failure.uncertain,
);
const abort = new AbortController();
pending = generate(abort.signal);
abort.abort();
await assert.rejects(
  pending,
  (failure) => failure.code === 'ABORTED' && failure.uncertain,
);
for (const code of ['UNAUTHORIZED', 'DEVICE_UNAUTHORIZED'])
  assert.equal(
    api.isFeishuBusinessAuthFailure(new ServiceError('synthetic', code, 401)),
    true,
  );
for (const code of [
  'FEISHU_UNAUTHORIZED',
  'FEISHU_TOKEN_EXPIRED',
  'DEVICE_AUTH_UNAVAILABLE',
  'AUTH_RETRY_REQUIRED',
  'AI_PROVIDER_UNAUTHORIZED',
])
  assert.equal(
    api.isFeishuBusinessAuthFailure(new ServiceError('synthetic', code, 401)),
    false,
  );
assert.equal(
  api.isFeishuBusinessAuthFailure(
    new ServiceError('synthetic', 'UNAUTHORIZED', 503),
  ),
  false,
);
assert.equal(
  api.isFeishuBusinessAuthFailure({ code: 'UNAUTHORIZED', status: 401 }),
  false,
);
assert.equal(network.timers.size, 0);
assert.equal(network.storageReads, 0);

function setup(initial = {}, strictEffects = false) {
  const browser = transport(),
    client = browser.load(),
    cache = new Map();
  const server = {
    status: status(),
    model: model(),
    report: null,
    delivery: null,
    authFailures: 0,
    ...initial,
  };
  browser.respond((request) => {
    const path = new URL(request.url).pathname;
    if (request.options.method !== 'GET')
      return server.write?.(request) ?? errorReply('FEISHU_UNAVAILABLE', 503);
    if (path === '/api/v1/feishu/status')
      return server.statusFailure?.(request) ?? reply(server.status);
    if (path === '/api/v1/ai/status')
      return server.modelFailure?.(request) ?? reply(server.model);
    if (path.startsWith('/api/v1/feishu/reports/'))
      return (
        server.reportFailure?.(request) ??
        (path.endsWith('/latest') || server.report
          ? reply({ report: server.report })
          : errorReply('FEISHU_NOT_FOUND', 404))
      );
    if (path.startsWith('/api/v1/feishu/deliveries/'))
      return (
        server.deliveryFailure?.(request) ??
        (server.delivery
          ? reply({ delivery: server.delivery })
          : errorReply('FEISHU_NOT_FOUND', 404))
      );
    throw Error(`Unexpected synthetic route ${path}`);
  });
  const props = {
    token: TOKEN,
    recoveryCache: cache,
    onAuthFailure() {
      server.authFailures++;
    },
    onSettings() {},
  };
  return {
    browser,
    client,
    cache,
    server,
    props,
    mount() {
      return componentHarness(
        'FeishuPanel.tsx',
        'FeishuPanel',
        client,
        props,
        browser,
        strictEffects,
      );
    },
  };
}
const pendingEntries = (test) =>
  (test.cache.get(TOKEN) ?? []).filter((item) => !item.settled);
const previewLabel = '我同意仅本次把该完整窗口内';
const sendLabel = '我已核对以上完整正文';
const modelLabel = '我同意后续每个固定窗口';
const feishuLabel = '我另行同意持续向已保存的本人飞书私聊';
const saveLabel = '我已确认 Open ID 属于本人';
const clearLabel = '我确认清除本机飞书配置';
const disableLabel = '我确认关闭早晚报计划';
const previewButton = '生成最近完整窗口预览';
const sendButton = '发送这份预览至本人飞书';
const refreshButton = '重新读取';

// Exact slot calculations, including midnight/year rollover and boundary instants.
const windowFunction = network.load('FeishuPanel.tsx', [
  'latestFeishuWindow',
]).latestFeishuWindow;
for (const [time, expectedStart, expectedEnd] of [
  [
    '2099-06-01T07:59:59+08:00',
    '2099-05-31T00:00:00.000Z',
    '2099-05-31T12:00:00.000Z',
  ],
  [
    '2099-06-01T08:00:00+08:00',
    '2099-05-31T12:00:00.000Z',
    '2099-06-01T00:00:00.000Z',
  ],
  [
    '2099-06-01T19:59:59+08:00',
    '2099-05-31T12:00:00.000Z',
    '2099-06-01T00:00:00.000Z',
  ],
  [
    '2099-06-01T20:00:00+08:00',
    '2099-06-01T00:00:00.000Z',
    '2099-06-01T12:00:00.000Z',
  ],
  [
    '2100-01-01T00:00:00+08:00',
    '2099-12-31T00:00:00.000Z',
    '2099-12-31T12:00:00.000Z',
  ],
])
  assert.deepEqual(plain(windowFunction(Date.parse(time))), {
    start: expectedStart,
    end: expectedEnd,
  });

// Form memory only, native constraints, separate consents and StrictMode cleanup.
let test = setup({}, true),
  panel = test.mount();
await panel.flush();
assert.equal(test.browser.writes().length, 0);
const refreshControl = button(panel, refreshButton);
assert.equal(refreshControl.props.title, refreshButton);
assert.equal(text(refreshControl).trim(), '');
assert.ok(
  nodes(refreshControl).some(
    (node) =>
      node.type === 'svg' &&
      node.props['data-icon'] === 'refresh' &&
      node.props['aria-hidden'] === 'true' &&
      node.props.focusable === 'false',
  ),
  'reread control must render the shipped decorative SVG',
);
assert.ok(text(panel.tree).includes('未通过平台核验'));
assert.equal(field(panel, 'feishu-app-id').props.maxLength, 128);
assert.equal(field(panel, 'feishu-open-id').props.maxLength, 128);
assert.equal(field(panel, 'feishu-app-secret').props.maxLength, 4096);
assert.equal(field(panel, 'feishu-app-secret').props.type, 'password');
for (const id of ['feishu-app-secret', 'feishu-open-id'])
  assert.equal(
    field(panel, id).props.value,
    undefined,
    'secret and raw OpenID must not be React state snapshots',
  );
assert.ok(
  nodes(panel.tree)
    .filter((node) => node.type === 'input' && node.props.type === 'checkbox')
    .every((node) => node.props.checked === false),
);
const configForm = nodes(panel.tree).find(
  (node) =>
    node.type === 'form' &&
    node.props['aria-labelledby'] === 'feishu-config-form-title',
);
assert.equal(configForm.props.method, 'post');
await fill(panel, 'feishu-app-secret', configInput.app_secret);
await fill(panel, 'feishu-open-id', configInput.recipient_open_id);
await check(panel, saveLabel);
await fill(panel, 'feishu-open-id', `${configInput.recipient_open_id}x`);
assert.equal(
  checkbox(panel, saveLabel).props.checked,
  false,
  'target edits invalidate consent',
);
assert.ok(!JSON.stringify(panel.states()).includes(configInput.app_secret));
assert.ok(
  !JSON.stringify(panel.states()).includes(configInput.recipient_open_id),
);
await fill(panel, 'feishu-open-id', configInput.recipient_open_id);
await check(panel, saveLabel);
test.server.write = (request) => {
  assert.equal(request.options.method, 'PATCH');
  test.server.status = status({
    config_revision: 2,
    schedule: schedule({ revision: 2 }),
  });
  return reply(test.server.status);
};
await submit(panel, 'feishu-config-form-title');
assert.deepEqual(
  JSON.parse(test.browser.writes().at(-1).options.body),
  configInput,
);
assert.equal(field(panel, 'feishu-app-secret').element.value, '');
assert.equal(field(panel, 'feishu-open-id').element.value, '');
assert.ok(text(panel.tree).includes('尚未联网验证'));
assert.equal(checkbox(panel, saveLabel).props.checked, false);
await fill(panel, 'feishu-app-secret', 'synthetic-refresh-clear');
await fill(panel, 'feishu-open-id', 'ou_synthetic_refresh_clear');
await click(panel, refreshButton);
assert.equal(field(panel, 'feishu-app-secret').element.value, '');
assert.equal(field(panel, 'feishu-open-id').element.value, '');
await fill(panel, 'feishu-app-secret', 'synthetic-leave-clear');
await fill(panel, 'feishu-open-id', 'ou_synthetic_leave_clear');
const secretElement = field(panel, 'feishu-app-secret').element,
  openElement = field(panel, 'feishu-open-id').element;
panel.unmount();
await tick();
assert.equal(secretElement.value, '');
assert.equal(openElement.value, '');
assert.equal(panel.postUnmountWrites, 0);
assert.equal(test.browser.storageReads, 0);
assert.ok(!JSON.stringify([...test.cache]).includes('synthetic-leave'));

// Every completed save attempt clears write-only values, including definite
// failures and uncertain responses. Unknown operations stay GET-only afterward.
for (const failure of [
  'definite',
  'network',
  'invalid-response',
  'timeout',
  'abort',
]) {
  test = setup();
  panel = test.mount();
  await panel.flush();
  await fill(panel, 'feishu-app-secret', configInput.app_secret);
  await fill(panel, 'feishu-open-id', configInput.recipient_open_id);
  await check(panel, saveLabel);
  test.server.write =
    failure === 'definite'
      ? () => errorReply('FEISHU_CONFIG_CHANGED')
      : failure === 'network'
        ? () => {
            throw Error('RAW-PRIVATE-SYNTHETIC-TRANSPORT');
          }
        : failure === 'invalid-response'
          ? () => reply({})
          : waitForAbort;
  await submit(panel, 'feishu-config-form-title');
  if (failure === 'timeout') {
    test.browser.fireTimeout(40_000);
    await panel.flush();
  }
  if (failure === 'abort') await click(panel, '停止等待');
  assert.equal(field(panel, 'feishu-app-secret').element.value, '');
  assert.equal(field(panel, 'feishu-open-id').element.value, '');
  assert.equal(checkbox(panel, saveLabel).props.checked, false);
  assert.ok(!text(panel.tree).includes('RAW-PRIVATE'));
  assert.ok(!JSON.stringify([...test.cache]).includes(configInput.app_secret));
  assert.ok(
    !JSON.stringify([...test.cache]).includes(configInput.recipient_open_id),
  );
  assert.equal(pendingEntries(test).length, failure === 'definite' ? 0 : 1);
  await click(panel, refreshButton);
  assert.equal(test.browser.writes().length, 1);
  assert.equal(pendingEntries(test).length, failure === 'definite' ? 0 : 1);
  assert.equal(test.browser.storageReads, 0);
  panel.unmount();
}
// Retaining a secret is explicit, only for the same saved App ID, never a GET
// response or copied DOM value. Changing App ID removes this option/consent.
test = setup();
panel = test.mount();
await panel.flush();
const retainLabel = '仅沿用该 App ID 已保存在本机的密钥';
await fill(panel, 'feishu-app-secret', configInput.app_secret);
await check(panel, saveLabel);
await check(panel, retainLabel);
assert.equal(field(panel, 'feishu-app-secret').element.value, '');
assert.equal(field(panel, 'feishu-app-secret')._disabled, true);
assert.equal(checkbox(panel, saveLabel).props.checked, false);
await fill(panel, 'feishu-app-id', 'cli_synthetic_other');
assert.equal(checkbox(panel, retainLabel).props.checked, false);
assert.equal(checkbox(panel, retainLabel)._disabled, true);
await fill(panel, 'feishu-app-id', configInput.app_id);
await check(panel, retainLabel);
await fill(panel, 'feishu-open-id', configInput.recipient_open_id);
await check(panel, saveLabel);
test.server.write = () => reply(status({ config_revision: 2 }));
await submit(panel, 'feishu-config-form-title');
assert.deepEqual(JSON.parse(test.browser.writes()[0].options.body), {
  ...configInput,
  app_secret: '',
  retain_secret: true,
});
panel.unmount();

// Ongoing model sharing and Feishu sending must be separately checked; disable
// and clear remain usable without a configured/available model or quota.
test = setup();
panel = test.mount();
await panel.flush();
await check(panel, modelLabel);
assert.equal(button(panel, '启用固定早晚报')._disabled, true);
await check(panel, feishuLabel);
assert.equal(button(panel, '启用固定早晚报')._disabled, false);
await click(panel, refreshButton);
assert.equal(checkbox(panel, modelLabel).props.checked, false);
assert.equal(checkbox(panel, feishuLabel).props.checked, false);
await check(panel, modelLabel);
await check(panel, feishuLabel);
test.server.write = (request) => {
  const body = JSON.parse(request.options.body);
  test.server.status.schedule = body.enabled
    ? enabledSchedule
    : schedule({ revision: 3 });
  return reply(test.server.status.schedule);
};
await click(panel, '启用固定早晚报');
assert.deepEqual(JSON.parse(test.browser.writes().at(-1).options.body), {
  enabled: true,
  expected_revision: 1,
  ...BINDING,
});
test.server.modelFailure = () => errorReply('AI_NOT_CONFIGURED', 503);
await click(panel, refreshButton);
await check(panel, disableLabel);
await click(panel, '关闭后续计划');
assert.deepEqual(JSON.parse(test.browser.writes().at(-1).options.body), {
  enabled: false,
  expected_revision: 2,
  confirmed: true,
});
test.server.write = () => {
  test.server.status = emptyStatus({
    config_revision: 2,
    schedule: schedule({ revision: 4 }),
  });
  return reply(test.server.status);
};
await check(panel, clearLabel);
await click(panel, '清除配置并关闭计划');
assert.equal(test.browser.writes().at(-1).options.method, 'DELETE');
assert.deepEqual(JSON.parse(test.browser.writes().at(-1).options.body), {
  expected_revision: 1,
  confirmed: true,
});
panel.unmount();
test = setup({ model: model({ used_today: 50 }) });
panel = test.mount();
await panel.flush();
assert.equal(checkbox(panel, previewLabel)._disabled, true);
assert.equal(button(panel, previewButton)._disabled, true);
await check(panel, disableLabel);
assert.equal(button(panel, '关闭后续计划')._disabled, false);
panel.unmount();

// 202 generation/send are unresolved, freeze full outgoing text, then recover
// only with GET. A failed/unknown/accepted receipt never opens another send.
test = setup();
panel = test.mount();
await panel.flush();
test.server.write = (request) => {
  if (request.url.endsWith('/preview')) {
    test.server.report = report('running');
    test.server.status.latest_report_id = REPORT_ID;
    return reply({ report: test.server.report }, 202);
  }
  test.server.delivery = delivery('running');
  test.server.report.delivery = test.server.delivery;
  test.server.status.latest_delivery = test.server.delivery;
  return reply({ delivery: test.server.delivery }, 202);
};
await check(panel, previewLabel);
await click(panel, previewButton);
assert.equal(test.browser.writes().length, 1);
assert.deepEqual(JSON.parse(test.browser.writes()[0].options.body), BINDING);
assert.equal(pendingEntries(test).length, 1);
assert.equal(button(panel, previewButton)._disabled, true);
assert.equal(
  nodes(panel.tree).some(
    (node) => node.props.className === 'feishu-frozen-text',
  ),
  false,
);
const previewWrites = test.browser.writes().length;
button(panel, previewButton).props.onClick();
await panel.flush();
assert.equal(test.browser.writes().length, previewWrites);
test.server.report = report();
await click(panel, refreshButton);
assert.equal(pendingEntries(test).length, 0);
assert.ok(
  test.cache
    .get(TOKEN)
    .some((item) => item.settled && item.reportId === REPORT_ID),
);
const frozen = nodes(panel.tree).find(
  (node) => node.props.className === 'feishu-frozen-text',
);
assert.ok(frozen);
assert.equal(text(frozen), report().text);
assert.equal(frozen.props.dangerouslySetInnerHTML, undefined);
assert.ok(text(panel.tree).includes('待人工核对：'));
assert.ok(text(panel.tree).includes('冻结报告 SHA-256（含窗口与绑定）'));
assert.equal(checkbox(panel, sendLabel).props.checked, false);
assert.equal(button(panel, sendButton)._disabled, true);
await check(panel, sendLabel);
await click(panel, sendButton);
assert.equal(test.browser.writes().length, 2);
assert.deepEqual(JSON.parse(test.browser.writes().at(-1).options.body), {
  ...BINDING,
  content_hash: HASH,
});
assert.equal(pendingEntries(test).length, 1);
assert.equal(checkbox(panel, sendLabel).props.checked, false);
assert.equal(button(panel, sendButton)._disabled, true);
let receipt = nodes(panel.tree).find(
  (node) => node.props.className === 'feishu-receipt',
);
assert.ok(text(receipt).includes('后台处理中'));
assert.ok(!text(receipt).includes('飞书平台已接受'));
assert.ok(!JSON.stringify([...test.cache]).includes(report().text));
assert.ok(!JSON.stringify([...test.cache]).includes(report().summary));
assert.ok(!JSON.stringify([...test.cache]).includes('synthetic-message'));
panel.unmount();
await tick();
before = test.browser.writes().length;
panel = test.mount();
await panel.flush();
assert.equal(
  test.browser.writes().length,
  before,
  'remount is GET-only and never restores consent',
);
assert.ok(
  test.browser.requests.some(
    (request) =>
      request.options.method === 'GET' &&
      request.url.endsWith(`/deliveries/${DELIVERY_ID}`),
  ),
);
test.server.delivery = delivery('unknown');
test.server.report.delivery = test.server.delivery;
test.server.status.latest_delivery = test.server.delivery;
await click(panel, refreshButton);
receipt = nodes(panel.tree).find(
  (node) => node.props.className === 'feishu-receipt',
);
assert.ok(text(receipt).includes('人工核实，不要再次发送'));
assert.equal(pendingEntries(test).length, 1);
assert.equal(button(panel, sendButton)._disabled, true);
test.server.delivery = delivery('succeeded');
test.server.report.delivery = test.server.delivery;
test.server.status.latest_delivery = test.server.delivery;
await click(panel, refreshButton);
receipt = nodes(panel.tree).find(
  (node) => node.props.className === 'feishu-receipt',
);
assert.ok(text(receipt).includes('飞书平台已接受'));
assert.ok(text(receipt).includes('不代表手机系统已通知或已读'));
assert.equal(pendingEntries(test).length, 0);
assert.equal(button(panel, sendButton)._disabled, true);
assert.equal(test.browser.writes().length, 2);
panel.unmount();

for (const state of ['running', 'failed', 'unknown']) {
  test = setup({
    report: report(state),
    status: status({ latest_report_id: REPORT_ID }),
  });
  panel = test.mount();
  await panel.flush();
  assert.equal(button(panel, previewButton)._disabled, true);
  assert.equal(
    nodes(panel.tree).some(
      (node) => node.props.className === 'feishu-frozen-text',
    ),
    false,
  );
  await click(panel, refreshButton);
  assert.equal(test.browser.writes().length, 0);
  panel.unmount();
}
test = setup({
  report: report('succeeded', { delivery: delivery('failed') }),
  delivery: delivery('failed'),
  status: status({
    latest_report_id: REPORT_ID,
    latest_delivery: delivery('failed'),
  }),
});
panel = test.mount();
await panel.flush();
assert.equal(button(panel, sendButton)._disabled, true);
assert.ok(text(panel.tree).includes('失败（不重试）'));
assert.equal(test.browser.writes().length, 0);
panel.unmount();

// A terminal delivery alone proves this window already has a frozen report,
// even on a fresh mount whose report GET is null, denied or times out. Persist
// only settled identity metadata; a later GET can restore the body, never POST.
for (const deliveryState of ['succeeded', 'failed']) {
  for (const reportRead of ['null', 'denied', 'timeout']) {
    const receipt = delivery(deliveryState);
    test = setup({
      status: status({ latest_report_id: REPORT_ID, latest_delivery: receipt }),
      report: null,
      reportFailure:
        reportRead === 'denied'
          ? () => errorReply('FEISHU_SOURCE_CHANGED')
          : reportRead === 'timeout'
            ? waitForAbort
            : undefined,
    });
    assert.equal(test.cache.size, 0);
    panel = test.mount();
    await panel.flush();
    if (reportRead === 'timeout') {
      test.browser.fireTimeout(10_000);
      await panel.flush();
    }
    assert.equal(checkbox(panel, previewLabel)._disabled, true);
    assert.equal(checkbox(panel, previewLabel).props.checked, false);
    assert.equal(
      button(panel, previewButton)._disabled,
      true,
      `${deliveryState}/${reportRead}: a terminal receipt must keep the window spent`,
    );
    assert.equal(
      nodes(panel.tree).some(
        (node) => node.props.className === 'feishu-frozen-text',
      ),
      false,
    );
    assert.equal(pendingEntries(test).length, 0);
    const identities = test.cache.get(TOKEN) ?? [];
    assert.equal(identities.length, 1);
    const { key, ...identity } = plain(identities[0]);
    assert.equal(typeof key, 'string');
    assert.deepEqual(identity, {
      kind: 'preview',
      configRevision: 1,
      scheduleRevision: 1,
      windowEnd: END,
      reportId: REPORT_ID,
      deliveryId: '',
      settled: true,
    });
    const cachedIdentity = JSON.stringify([...test.cache]);
    for (const sensitive of [
      '合成冻结正文',
      '合成摘要',
      HASH,
      TARGET,
      configInput.app_secret,
      configInput.recipient_open_id,
    ])
      assert.ok(!cachedIdentity.includes(sensitive));
    assert.equal(test.browser.writes().length, 0);
    test.server.status = status();
    test.server.reportFailure = () => errorReply('FEISHU_SOURCE_CHANGED');
    panel.unmount();
    panel = test.mount();
    await panel.flush();
    assert.equal(button(panel, previewButton)._disabled, true);
    assert.equal(checkbox(panel, previewLabel)._disabled, true);
    assert.equal(test.cache.get(TOKEN)[0].reportId, REPORT_ID);
    assert.equal(pendingEntries(test).length, 0);
    delete test.server.reportFailure;
    test.server.report = report('succeeded', { delivery: receipt });
    await fill(panel, 'feishu-report-id', REPORT_ID);
    await submit(panel, 'feishu-read-report-label');
    assert.ok(
      test.browser.requests.some(
        (request) =>
          request.options.method === 'GET' &&
          new URL(request.url).pathname.endsWith(`/reports/${REPORT_ID}`),
      ),
    );
    assert.ok(text(panel.tree).includes(report().text));
    assert.equal(button(panel, previewButton)._disabled, true);
    assert.equal(button(panel, sendButton)._disabled, true);
    assert.equal(test.browser.writes().length, 0);
    assert.equal(test.browser.storageReads, 0);
    panel.unmount();
  }
}

// Stop waiting and lost responses cannot unlock another mutation. GET null or a
// redacted report is not proof of failure; state survives leave/remount as IDs only.
test = setup({
  report: report(),
  status: status({ latest_report_id: REPORT_ID }),
});
panel = test.mount();
await panel.flush();
test.server.write = waitForAbort;
await check(panel, sendLabel);
await click(panel, sendButton);
assert.equal(
  pendingEntries(test).length,
  1,
  'safe recovery identity is recorded before waiting for IO',
);
await click(panel, '停止等待');
assert.ok(text(panel.tree).includes('未取消后台'));
assert.equal(test.browser.writes().length, 1);
assert.equal(
  nodes(panel.tree).some(
    (node) => node.props.className === 'feishu-frozen-text',
  ),
  false,
);
test.server.report = null;
test.server.status.latest_report_id = '';
await click(panel, refreshButton);
assert.equal(pendingEntries(test).length, 1);
assert.equal(button(panel, previewButton)._disabled, true);
panel.unmount();
await tick();
panel = test.mount();
await panel.flush();
assert.equal(test.browser.writes().length, 1);
assert.equal(button(panel, previewButton)._disabled, true);
await check(panel, clearLabel);
assert.equal(button(panel, '清除配置并关闭计划')._disabled, false);
panel.unmount();
test = setup();
panel = test.mount();
await panel.flush();
test.server.write = waitForAbort;
await check(panel, previewLabel);
await click(panel, previewButton);
const originalPanel = panel;
panel.unmount();
await tick();
assert.equal(originalPanel.postUnmountWrites, 0);
panel = test.mount();
await panel.flush();
assert.equal(test.browser.writes().length, 1);
assert.equal(pendingEntries(test).length, 1);
assert.equal(button(panel, previewButton)._disabled, true);
panel.unmount();

// Withdraw old body/consent after pause/redaction/model/target change. Keep only
// the known immutable window identity so denied re-reads cannot offer regeneration.
for (const code of [
  'FEISHU_SOURCE_CHANGED',
  'AI_PAUSED',
  'FEISHU_MODEL_CHANGED',
  'FEISHU_CONFIG_CHANGED',
]) {
  test = setup({
    report: report(),
    status: status({ latest_report_id: REPORT_ID }),
  });
  panel = test.mount();
  await panel.flush();
  await check(panel, sendLabel);
  test.server.reportFailure = () => errorReply(code);
  await click(panel, refreshButton);
  assert.ok(!text(panel.tree).includes(report().text));
  assert.equal(
    nodes(panel.tree).some(
      (node) => node.props.className === 'feishu-frozen-text',
    ),
    false,
  );
  assert.equal(button(panel, previewButton)._disabled, true);
  assert.ok(!text(panel.tree).includes('RAW-PRIVATE'));
  assert.equal(test.browser.writes().length, 0);
  panel.unmount();
  panel = test.mount();
  await panel.flush();
  assert.equal(button(panel, previewButton)._disabled, true);
  assert.equal(test.browser.writes().length, 0);
  panel.unmount();
}
// Boundary rollover invalidates send consent and expires the old preview even
// without polling, but does not block a genuinely future fixed window.
test = setup({
  report: report(),
  status: status({ latest_report_id: REPORT_ID }),
});
panel = test.mount();
await panel.flush();
await check(panel, sendLabel);
test.browser.setTime(Date.parse(NEXT));
test.browser.fireTimeout(Date.parse(NEXT) - NOW + 50);
await panel.flush();
assert.equal(checkbox(panel, sendLabel).props.checked, false);
assert.equal(button(panel, sendButton)._disabled, true);
assert.equal(checkbox(panel, previewLabel)._disabled, false);
assert.equal(test.browser.writes().length, 0);
panel.unmount();

// Genuine local business auth failures only. A Feishu/provider/device transport
// 401 or unavailable result must not de-authorize the application identity.
for (const [code, httpStatus, expectedAuthFailures] of [
  ['FEISHU_TOKEN_EXPIRED', 401, 0],
  ['AI_PROVIDER_UNAUTHORIZED', 401, 0],
  ['DEVICE_AUTH_UNAVAILABLE', 503, 0],
  ['AUTH_RETRY_REQUIRED', 409, 0],
  ['UNAUTHORIZED', 401, 1],
  ['DEVICE_UNAUTHORIZED', 401, 1],
]) {
  test = setup({ statusFailure: () => errorReply(code, httpStatus) });
  panel = test.mount();
  await panel.flush();
  assert.equal(test.server.authFailures, expectedAuthFailures);
  assert.equal(test.browser.writes().length, 0);
  assert.equal(test.browser.storageReads, 0);
  panel.unmount();
}
console.log(
  'Feishu synthetic client/panel checks passed: ten API methods, DTO/calendar/binding validation, write-only credentials, independent consent, fixed 12-hour windows, 202/unknown/no replay, GET-only recovery, withdrawn stale content, exact local auth mapping and lifecycle cleanup. No real credentials, model, platform, backend or enabled schedule.',
);
