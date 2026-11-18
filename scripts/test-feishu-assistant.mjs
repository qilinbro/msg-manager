import assert from 'node:assert/strict';
import {
  BINDING,
  END,
  HASH,
  NEXT,
  NOW,
  REPORT_ID,
  TOKEN,
  button,
  check,
  checkbox,
  click,
  componentHarness,
  errorReply,
  model,
  nodes,
  reply,
  report,
  text,
  tick,
  transport,
  waitForAbort,
} from './feishu-test-harness.mjs';

// Synthetic immutable server previews. No real model, Feishu account, message,
// credentials, enabled schedule, server startup, native shell or browser evidence.
const assistantExports = [
  'AssistantRequestError',
  'getAssistantStatus',
  'createAssistantRun',
  'getAssistantRun',
  'confirmAssistantAction',
  'rejectAssistantAction',
  'isAssistantBusinessAuthFailure',
];
const { confirmed: _confirmed, ...binding } = BINDING;
const FUTURE = '2099-06-01T12:00:00Z';
// Reconstruct the readonly field presentation, proving no frozen field or full
// outgoing text was dropped when replacing raw JSON with readable labels.
function previewValue(node) {
  const children = [node.props.children]
    .flat(Infinity)
    .filter((item) => item && typeof item === 'object');
  switch (node.props['data-value-kind']) {
    case 'object':
      return Object.fromEntries(
        children.map((field) => {
          const definition = nodes(field).find((item) => item.type === 'dd');
          const value = [definition.props.children]
            .flat(Infinity)
            .find((item) => item && typeof item === 'object');
          return [field.props['data-parameter-key'], previewValue(value)];
        }),
      );
    case 'array':
      return children.map((item) =>
        previewValue(
          [item.props.children]
            .flat(Infinity)
            .find((child) => child && typeof child === 'object'),
        ),
      );
    case 'null':
      return null;
    case 'boolean':
      return text(node) === '是';
    case 'number':
      return Number(text(node));
    case 'string':
      return text(node);
    default:
      throw new Error('not a readable fixed parameter');
  }
}
const catalogue = () => ({
  ...model(),
  external: true,
  session_ttl_seconds: 1800,
  operations: [
    {
      id: 'update_feishu_schedule',
      method: 'PATCH',
      path: '/api/v1/feishu/schedule',
      effect: 'write',
      availability: 'confirmation_required',
      reason: 'synthetic Feishu schedule operation',
    },
    {
      id: 'send_feishu_report',
      method: 'POST',
      path: '/api/v1/feishu/reports/{report_id}/send',
      effect: 'write',
      availability: 'confirmation_required',
      reason: 'synthetic frozen Feishu send',
    },
    {
      id: 'generate_feishu_preview',
      method: 'POST',
      path: '/api/v1/feishu/preview',
      effect: 'model_call',
      availability: 'confirmation_required',
      reason: 'synthetic bounded model preview',
    },
    {
      id: 'configure_feishu',
      method: 'PATCH',
      path: '/api/v1/feishu/config',
      effect: 'credential',
      availability: 'human_only',
      reason: 'manual only; secrets never supplied to Assistant',
    },
  ],
});
const action = (kind, extra = {}) => {
  const common = {
    ...binding,
    target_label_masked: '本人私聊（ou_***1234；归属尚未验证）',
    recipient_ownership_verified: false,
    consent_binding: { ...binding },
  };
  const spec =
    kind === 'enable' || kind === 'disable'
      ? {
          operation_id: 'update_feishu_schedule',
          method: 'PATCH',
          path: '/api/v1/feishu/schedule',
          arguments: {
            ...common,
            enabled: kind === 'enable',
            expected_revision: 1,
          },
        }
      : kind === 'send'
        ? {
            operation_id: 'send_feishu_report',
            method: 'POST',
            path: `/api/v1/feishu/reports/${REPORT_ID}/send`,
            arguments: {
              ...common,
              report_id: REPORT_ID,
              content_hash: HASH,
              report_preview: report(),
            },
          }
        : {
            operation_id: 'generate_feishu_preview',
            method: 'POST',
            path: '/api/v1/feishu/preview',
            arguments: {
              ...common,
              window_start: report().window_start,
              window_end: END,
            },
          };
  return {
    id: `synthetic-${kind}-action`,
    ...spec,
    effects: ['只用于合成回归；没有平台或模型调用。'],
    expires_at: FUTURE,
    ...extra,
  };
};
const run = (
  state = 'awaiting_confirmation',
  pending = action('enable'),
  extra = {},
) => ({
  session_id: 'synthetic-session',
  run_id: 'synthetic-run',
  state,
  assistant_message: '模型文字不是业务结果凭证。',
  tool_results: [],
  pending_action: state === 'awaiting_confirmation' ? pending : null,
  model_config_id: BINDING.model_config_id,
  model_config_revision: 0,
  expires_at: FUTURE,
  error: null,
  ...extra,
});
function setup(kind, pending = action(kind)) {
  const browser = transport(),
    api = browser.load('assistant-api.ts', assistantExports),
    cache = new Map();
  const server = {
    status: catalogue(),
    run: run('awaiting_confirmation', pending),
    authFailures: 0,
    confirm: null,
  };
  cache.set(TOKEN, {
    sessionID: 'synthetic-session',
    draft: '',
    recoveryID: 'synthetic-run',
    turns: [
      {
        key: 'synthetic-turn',
        prompt: '合成请求：仅验证确认界面。',
        runID: 'synthetic-run',
        run: structuredClone(server.run),
        uncertain: false,
        notice: null,
      },
    ],
  });
  browser.respond((request) => {
    if (request.url.endsWith('/status'))
      return server.statusFailure?.(request) ?? reply(server.status);
    if (request.url.endsWith('/confirm'))
      return (
        server.confirm?.(request) ??
        reply(
          server.run,
          server.run.state === 'running' ||
            server.run.state === 'awaiting_confirmation'
            ? 202
            : 200,
        )
      );
    if (request.url.endsWith('/reject')) {
      server.run = run('rejected');
      return reply(server.run);
    }
    assert.equal(
      request.options.method,
      'GET',
      'recovery never creates a new model request',
    );
    return reply(
      server.run,
      server.run.state === 'running' ||
        server.run.state === 'awaiting_confirmation'
        ? 202
        : 200,
    );
  });
  const props = {
    token: TOKEN,
    cache,
    onAuthFailure() {
      server.authFailures++;
    },
    onSettings() {},
  };
  return {
    browser,
    api,
    cache,
    server,
    props,
    mount() {
      return componentHarness(
        'AssistantPanel.tsx',
        'AssistantPanel',
        api,
        props,
        browser,
      );
    },
  };
}
const originalConsent = '我已核对工具';
const sendConsent = '我另行同意将上面完整核对的冻结正文';
const enableConsent = '我另行同意按上述固定目标、窗口与版本';
const confirmation = (panel) =>
  nodes(panel.tree).find(
    (node) => node.props.className === 'assistant-confirmation',
  );
const confirmationCheckboxes = (panel) =>
  nodes(confirmation(panel)).filter(
    (node) => node.type === 'input' && node.props.type === 'checkbox',
  );
const writes = (test) =>
  test.browser.requests.filter((request) => request.options.method !== 'GET');

// Ongoing sharing+automatic sending and real one-shot sends both add a separate,
// initially-unchecked Feishu consent; neither model consent nor its inverse suffices.
for (const kind of ['enable', 'send']) {
  const test = setup(kind);
  let panel = test.mount();
  await panel.flush();
  assert.equal(test.browser.requests.length, 1);
  assert.equal(writes(test).length, 0);
  assert.equal(confirmationCheckboxes(panel).length, 2);
  assert.ok(
    confirmationCheckboxes(panel).every((node) => node.props.checked === false),
  );
  assert.equal(button(panel, '确认')._disabled, true);
  const specificConsent = kind === 'enable' ? enableConsent : sendConsent;
  await check(panel, originalConsent);
  assert.equal(
    button(panel, '确认')._disabled,
    true,
    'model processing consent alone cannot authorize Feishu sending',
  );
  await check(panel, originalConsent, false);
  await check(panel, specificConsent);
  assert.equal(
    button(panel, '确认')._disabled,
    true,
    'Feishu sending consent cannot replace model processing consent',
  );
  await check(panel, originalConsent);
  assert.equal(button(panel, '确认')._disabled, false);
  const preview = confirmation(panel),
    body = nodes(preview).find(
      (node) => node.props['aria-label'] === '服务器固定操作参数',
    );
  const renderedArguments = previewValue(
    nodes(body).find((node) => node.props['data-value-kind'] === 'object'),
  );
  assert.deepEqual(renderedArguments, action(kind).arguments);
  assert.equal(
    nodes(preview).some((node) => node.type === 'pre'),
    false,
  );
  assert.equal(
    nodes(preview).filter(
      (node) =>
        node.type === 'textarea' ||
        node.type === 'select' ||
        (node.type === 'input' && node.props.type !== 'checkbox'),
    ).length,
    0,
    'immutable server preview cannot be edited',
  );
  assert.equal(body.props.dangerouslySetInnerHTML, undefined);
  if (kind === 'enable') {
    assert.ok(text(preview).includes('持续数据外发与自动发送授权'));
    assert.ok(text(preview).includes('08:00 / 20:00'));
    assert.ok(text(preview).includes('不会逐次询问'));
    assert.ok(text(preview).includes('不是群 Webhook'));
    assert.ok(text(preview).includes('离线不补发'));
  } else {
    assert.equal(
      renderedArguments.report_preview.text,
      report().text,
      'the full frozen outgoing text, not just its model summary, is shown',
    );
    assert.ok(text(body).includes(HASH));
    assert.ok(text(body).includes('ou_***1234'));
    assert.ok(text(body).includes(REPORT_ID));
    assert.ok(text(preview).includes('不是草稿'));
    assert.ok(text(preview).includes('HTTP 202 或 unknown 都不是成功'));
    assert.ok(text(preview).includes('未知结果请人工核实且不要重发'));
  }
  await click(panel, '查看结果');
  assert.ok(
    confirmationCheckboxes(panel).every((node) => node.props.checked === false),
  );
  assert.equal(writes(test).length, 0);
  await check(panel, originalConsent);
  await check(panel, specificConsent);
  await click(panel, '刷新状态');
  assert.ok(
    confirmationCheckboxes(panel).every((node) => node.props.checked === false),
  );
  assert.equal(
    writes(test).length,
    0,
    'unchanged model refresh cannot submit, replay, or retain either consent',
  );
  await check(panel, originalConsent);
  await check(panel, specificConsent);
  panel.unmount();
  await tick();
  panel = test.mount();
  await panel.flush();
  assert.ok(
    confirmationCheckboxes(panel).every((node) => node.props.checked === false),
  );
  assert.equal(
    writes(test).length,
    0,
    'consent is never stored in the cross-page workspace',
  );
  await check(panel, originalConsent);
  await check(panel, specificConsent);
  test.server.confirm = () => {
    test.server.run = run('running');
    return reply(test.server.run, 202);
  };
  await click(panel, '确认');
  assert.equal(writes(test).length, 1);
  assert.ok(
    writes(test)[0].url.endsWith('/assistant/runs/synthetic-run/confirm'),
  );
  assert.deepEqual(
    JSON.parse(writes(test)[0].options.body),
    {
      action_id: `synthetic-${kind}-action`,
      model_config_id: BINDING.model_config_id,
      model_config_revision: 0,
      confirmed: true,
    },
    'Feishu UI acknowledgment must not alter the existing immutable confirmation protocol',
  );
  assert.ok(text(panel.tree).includes('运行中'));
  assert.equal(confirmation(panel), undefined);
  test.server.run = run('completed', null, {
    tool_results: [
      {
        call_id: 'synthetic-call',
        operation_id: action(kind).operation_id,
        state: 'succeeded',
        http_status: 202,
        data: { synthetic_only: true, state: 'running' },
        error_code: '',
      },
    ],
  });
  await click(panel, '查看结果');
  assert.equal(
    writes(test).length,
    1,
    'GET-only asynchronous recovery never repeats a model call or send',
  );
  assert.equal(test.browser.storageReads, 0);
  assert.equal(test.server.authFailures, 0);
  panel.unmount();
  await tick();
  assert.equal(panel.postUnmountWrites, 0);
}

// Preview is model-only; disable is cancellation of later processing, not a
// guarantee to recall in-flight/accepted messages. Neither adds send consent.
for (const kind of ['preview', 'disable']) {
  const test = setup(kind),
    panel = test.mount();
  await panel.flush();
  assert.equal(confirmationCheckboxes(panel).length, 1);
  assert.equal(checkbox(panel, originalConsent).props.checked, false);
  const preview = confirmation(panel);
  if (kind === 'preview') {
    assert.ok(text(preview).includes('最近一个已完整结束的固定 12 小时窗口'));
    assert.ok(text(preview).includes('不是过去 24 小时'));
    assert.ok(text(preview).includes('不会发至飞书'));
    assert.ok(text(preview).includes('每窗口不重做'));
  } else {
    assert.ok(text(preview).includes('停止/取消后续处理'));
    assert.ok(text(preview).includes('不能保证撤回在途请求或平台已接受的消息'));
    assert.ok(text(preview).includes('本助手确认后仍可能继续模型处理'));
  }
  await check(panel, originalConsent);
  assert.equal(button(panel, '确认')._disabled, false);
  test.server.confirm = () => {
    test.server.run = run('completed');
    return reply(test.server.run);
  };
  await click(panel, '确认');
  assert.equal(writes(test).length, 1);
  assert.deepEqual(
    Object.keys(JSON.parse(writes(test)[0].options.body)).sort(),
    ['action_id', 'confirmed', 'model_config_id', 'model_config_revision'],
  );
  panel.unmount();
}

// Classification is conservative across catalog aliases and concrete paths.
// Missing/non-boolean enabled cannot accidentally be interpreted as safe disable.
for (const pending of [
  action('enable', { operation_id: 'synthetic-alias' }),
  action('send', { operation_id: 'synthetic-alias' }),
  action('enable', {
    arguments: { ...action('enable').arguments, enabled: undefined },
  }),
  action('enable', {
    arguments: { ...action('enable').arguments, enabled: 'false' },
  }),
  action('send', { path: '/api/v1/synthetic-route-alias' }),
]) {
  const test = setup('enable', pending),
    panel = test.mount();
  await panel.flush();
  assert.equal(confirmationCheckboxes(panel).length, 2);
  await check(panel, originalConsent);
  assert.equal(button(panel, '确认')._disabled, true);
  assert.equal(writes(test).length, 0);
  panel.unmount();
}

// Version/target reads, even when returning the same action ID, invalidate both
// checkboxes. A new model binding must first match the frozen server run.
let test = setup('send'),
  panel = test.mount();
await panel.flush();
await check(panel, originalConsent);
await check(panel, sendConsent);
test.server.status.model_config_revision = 1;
await click(panel, '刷新状态');
assert.ok(
  confirmationCheckboxes(panel).every(
    (node) => !node.props.checked && node._disabled,
  ),
);
assert.equal(button(panel, '确认')._disabled, true);
test.server.run = run(
  'awaiting_confirmation',
  action('send', {
    arguments: {
      ...action('send').arguments,
      config_revision: 2,
      target_binding_id: 'c'.repeat(64),
    },
  }),
  { model_config_revision: 1 },
);
await click(panel, '查看结果');
assert.ok(confirmationCheckboxes(panel).every((node) => !node.props.checked));
await check(panel, originalConsent);
assert.equal(button(panel, '确认')._disabled, true);
await check(panel, sendConsent);
assert.equal(button(panel, '确认')._disabled, false);
assert.equal(writes(test).length, 0);
panel.unmount();

// Unknown confirmation/stop/leave cannot re-submit either a Feishu send or an
// automatic schedule. Only explicit GET recovery is available; raw input/body
// and consent never enter persistent storage.
for (const kind of ['enable', 'send']) {
  test = setup(kind);
  panel = test.mount();
  await panel.flush();
  await check(panel, originalConsent);
  await check(panel, kind === 'enable' ? enableConsent : sendConsent);
  test.server.confirm = waitForAbort;
  await click(panel, '确认');
  await click(panel, '停止等待');
  assert.equal(writes(test).length, 1);
  assert.equal(button(panel, '确认')._disabled, true);
  assert.equal(button(panel, '拒绝')._disabled, true);
  assert.ok(confirmationCheckboxes(panel).every((node) => !node.props.checked));
  panel.unmount();
  await tick();
  panel = test.mount();
  await panel.flush();
  assert.equal(writes(test).length, 1);
  assert.equal(button(panel, '确认')._disabled, true);
  test.server.run = run('unknown');
  await click(panel, '查看结果');
  assert.ok(text(panel.tree).includes('结果未知'));
  assert.equal(writes(test).length, 1);
  test.server.run = run('completed');
  await click(panel, '查看结果');
  assert.equal(writes(test).length, 1);
  assert.equal(test.browser.storageReads, 0);
  panel.unmount();
}

// Rejection remains available without model readiness and without either
// checkbox; it sends only the original action ID, never a send authorization.
test = setup('send');
test.server.statusFailure = () => errorReply('AI_NOT_CONFIGURED', 503);
panel = test.mount();
await panel.flush();
assert.equal(button(panel, '确认')._disabled, true);
assert.equal(button(panel, '拒绝')._disabled, false);
await click(panel, '拒绝');
assert.equal(writes(test).length, 1);
assert.ok(writes(test)[0].url.endsWith('/reject'));
assert.deepEqual(JSON.parse(writes(test)[0].options.body), {
  action_id: 'synthetic-send-action',
});
panel.unmount();
// Native expiry cannot leave an actionable old send confirmation.
test = setup('send');
panel = test.mount();
await panel.flush();
await check(panel, originalConsent);
await check(panel, sendConsent);
test.browser.setTime(Date.parse(NEXT));
test.browser.fireTimeout(Date.parse(FUTURE) - NOW + 50);
await panel.flush();
assert.equal(button(panel, '确认')._disabled, true);
assert.equal(writes(test).length, 0);
panel.unmount();
console.log(
  'Feishu synthetic Assistant checks passed: independent unchecked sending consent for enable/send, model-only preview, explicit disable risks, immutable full report preview and existing confirmation body, reread/version/remount consent reset, conservative aliases, no unknown replay, and model-independent rejection. No real model, account, platform, backend or enabled schedule.',
);
