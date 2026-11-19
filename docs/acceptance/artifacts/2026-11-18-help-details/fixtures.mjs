// Data only: no network, account, credential, platform, model or persistence access.
import assert from 'node:assert/strict';

export const syntheticToken = 'synthetic-browser-not-a-real-credential';
export const modelID = 'a'.repeat(64);
export const modelName = 'synthetic-model';
export const modelHost = 'synthetic.invalid';
export const reportID = `fr_${'1'.repeat(32)}`;
export const deliveryID = `fd_${'2'.repeat(32)}`;
const binding = 'b'.repeat(64);
const contentHash = 'c'.repeat(64);
const timestamp = '2026-11-18T04:00:00Z';
const slotMilliseconds = 12 * 60 * 60 * 1000;
const windowEnd = Math.floor(Date.now() / slotMilliseconds) * slotMilliseconds;
const windowRange = {
  window_start: new Date(windowEnd - slotMilliseconds).toISOString(),
  window_end: new Date(windowEnd).toISOString(),
};
const generatedAt = new Date(windowEnd + 1000).toISOString();
const empty = { items: [], next_cursor: null };
export const state = {
  digest: 'succeeded',
  feishu: 'ready',
  sources: 'ready',
  milkyVerify: 'ok',
  holdMilkyVerify: false,
};

const conversation = (kind, id, title) => ({
  id,
  conversation_kind: kind,
  connection_id: 'synthetic-connection',
  platform: 'weixin',
  account_id: 'synthetic-account',
  peer_id: kind === 'group' ? `@@${id}` : `wxid_${id}`,
  title,
  last_message: '合成消息：请核对项目交付时间。',
  last_message_at: timestamp,
  unread_count: 0,
  pinned: false,
  archived: false,
  category: 'colleague',
});
const conversations = [
  conversation('private', 'synthetic-private', '合成联系人'),
  conversation('group', 'synthetic-group', '合成项目群'),
];
const messages = (id) => [{
  id: `${id}-message`,
  conversation_id: id,
  external_id: `${id}-external`,
  direction: 'incoming',
  body: '合成消息：请核对项目交付时间。',
  occurred_at: timestamp,
  read: true,
  source: 'wechatdb',
}];
const modelStatus = {
  configured: true,
  provider: 'synthetic',
  model: modelName,
  endpoint_host: modelHost,
  external: true,
  model_config_id: modelID,
  model_config_revision: 3,
  daily_limit: 50,
  used_today: 2,
  sending_enabled: false,
};
const modelPresets = {
  protocol: 'openai_chat_completions',
  model_id_required: true,
  live_verification_performed: false,
  items: [{
    id: 'custom',
    display_name: 'Custom · public HTTPS',
    base_url: '',
    api_key_required: true,
    implementation_status: 'template_only',
    source_url: 'https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md',
    source_ref: 'd5292055f5d00d99d949017745a81d31cd186222',
    source_license: 'Apache-2.0',
  }],
};
const assistantStatus = {
  ...modelStatus,
  session_ttl_seconds: 900,
  operations: [{
    id: 'list_conversations',
    method: 'GET',
    path: '/api/v1/conversations',
    effect: 'read',
    availability: 'available',
    reason: '合成目录：只读检索会话',
  }, {
    id: 'update_conversation',
    method: 'PATCH',
    path: '/api/v1/conversations/{conversation_id}',
    effect: 'write',
    availability: 'confirmation_required',
    reason: '合成目录：须独立逐项确认；本浏览器验证禁止执行写请求',
  }],
};
function digestReport() {
  return {
    id: 'synthetic-digest',
    day: '2026-11-18',
    state: state.digest,
    summary: `合成每日摘要内容（${state.digest}），不是实际模型输出。`,
    reminders: [{
      conversation_id: 'synthetic-private',
      title: '合成联系人',
      platform: 'weixin',
      conversation_kind: 'private',
      priority: 'normal',
      reason: '合成提醒：请人工核对交付时间。',
      evidence_ids: ['synthetic-private-message'],
    }],
    evidence_ids: ['synthetic-private-message'],
    conversation_count: 1,
    generated_at: state.digest === 'running' ? null : timestamp,
    ...(state.digest === 'failed' ? { error_code: 'AI_TIMEOUT' } : {}),
  };
}
function feishuDelivery() {
  if (['ready', 'running', 'failed', 'report-unknown'].includes(state.feishu)) return null;
  const deliveryState = state.feishu === 'sent' ? 'succeeded' : 'unknown';
  return {
    id: deliveryID,
    report_id: reportID,
    ...windowRange,
    state: deliveryState,
    target_binding_id: binding,
    config_revision: 2,
    model_config_id: modelID,
    model_config_revision: 3,
    content_hash: contentHash,
    message_id: deliveryState === 'succeeded' ? 'om_synthetic_message' : '',
    error_code: deliveryState === 'unknown' ? 'FEISHU_TIMEOUT' : '',
    created_at: generatedAt,
    completed_at: generatedAt,
  };
}
function feishuReport() {
  const reportState = state.feishu === 'report-unknown' ? 'unknown' : state.feishu === 'running' || state.feishu === 'failed' ? state.feishu : 'succeeded';
  return {
    id: reportID,
    ...windowRange,
    state: reportState,
    summary: '合成飞书预览摘要，请人工核对。',
    todos: [{ text: '合成待办：核对交付', evidence_ids: ['synthetic-private-message'], needs_review: true }],
    excerpts: [{
      message_id: 'synthetic-private-message',
      conversation_id: 'synthetic-private',
      text: '合成引用：请核对项目交付时间。',
      occurred_at: new Date(windowEnd - 60_000).toISOString(),
    }],
    conversation_ids: ['synthetic-private'],
    source_refs: [{ conversation_id: 'synthetic-private', settings_revision: 1 }],
    conversation_count: 1,
    model_config_id: modelID,
    model_config_revision: 3,
    config_revision: 2,
    target_binding_id: binding,
    text: '合成冻结正文：仅用于检查待发送正文可见。本验证不会发送飞书消息。',
    content_hash: contentHash,
    truncated: false,
    error_code: reportState === 'failed' ? 'AI_TIMEOUT' : '',
    created_at: generatedAt,
    generated_at: reportState === 'running' ? null : generatedAt,
    schedule_revision: 0,
    trigger: 'manual',
    delivery: feishuDelivery(),
  };
}
function sourceResult() {
  return {
    inserted: 3,
    duplicates: 1,
    conversations: 1,
    skipped: 0,
    limited: state.sources === 'limited',
    read_at: timestamp,
    read_only: true,
    sending_enabled: false,
  };
}
function profile(id) {
  return {
    conversation_id: id,
    contact_id: `contact-${id}`,
    display_name: '合成联系人',
    platform: 'weixin',
    public_name: '合成联系人',
    company: '合成项目',
    role: '协作者',
    manual_notes: '',
    category_override: '',
    auto_organize: false,
    public_search_enabled: false,
    model_config_id: modelID,
    revision: 1,
    processed_seq: 1,
    max_seq: 1,
    analysis_total: 1,
    analysis_processed: 1,
    category: 'colleague',
    ai_category: 'colleague',
    tags: ['合成数据'],
    summary: '合成档案摘要，未经真实资料核验。',
    evidence_ids: [`${id}-message`],
    facts: [],
    sources: [],
    jobs: [{
      id: `job-${id}`,
      conversation_id: id,
      kind: 'organize',
      state: 'succeeded',
      attempts: 1,
      next_attempt_at: null,
      lease_until: null,
      model_config_id: modelID,
      settings_revision: 1,
      max_seq: 1,
      last_error_code: null,
      updated_at: timestamp,
    }],
    updated_at: timestamp,
    document_markdown: '# 合成档案\n\n仅用于界面验证，不代表真实人物。',
    truncated: false,
    ai_paused: false,
  };
}
function error(code) {
  return { __status: 503, error: { code, message: '合成错误，不接触真实服务。' } };
}

export function apiFixture(url, method) {
  const p = url.pathname;
  assert.equal(method, 'GET', `No business mutations, including synthetic mutations, are allowed: ${method} ${p}`);
  if (p === '/api/v1/system') return {
    version: 'synthetic-browser', stage: 'synthetic', workspace_id: 'synthetic-workspace',
    automation: { paused: true, auto_send_enabled: false },
    storage: { configured: true, driver: 'sqlcipher' }, connectors: [],
  };
  if (p === '/api/v1/ai/model-presets') return modelPresets;
  if (p === '/api/v1/ai/model-config') return {
    base_url: `https://${modelHost}/v1`, model: modelName, daily_limit: 50,
    key_configured: true, revision: 3, configured: true, model_config_id: modelID, sending_enabled: false,
  };
  if (p === '/api/v1/ai/status') return modelStatus;
  if (p === '/api/v1/ai/profile-status') return {
    model_configured: true, search_configured: true, model_config_id: modelID,
    model: modelName, provider: 'synthetic', endpoint_host: modelHost,
    daily_limit: 50, used_today: 2, search_daily_limit: 10, search_used_today: 0, sending_enabled: false,
  };
  if (p === '/api/v1/digest/schedule') return {
    enabled: false, local_time: '09:00', timezone: 'Asia/Shanghai', revision: 1,
    last_run_day: '', model_config_id: '', model_config_revision: 0,
  };
  if (p === '/api/v1/digest/latest') return { report: digestReport(), sending_enabled: false };
  if (p === '/api/v1/connections/qq-milky/status') return {
    configured: true, state: 'identity_verified', account_id: '100000001', nickname: '合成 QQ',
    milky_version: '1.3', checked_at: timestamp, receiving_enabled: false,
    sending_enabled: false, read_only: true, last_sync_at: '', last_sync_result: '', skipped: 0, gaps: [],
    monitor: { enabled: false, state: 'disabled', peer_ids: [], read_only: true, last_error: '' },
  };
  if (p === '/api/v1/connections/qq-milky/verify') return state.milkyVerify === 'error'
    ? error('MILKY_UNREACHABLE')
    : {
        connection_id: 'qq-milky', state: 'identity_verified', account_id: '100000001', nickname: '合成 QQ',
        impl_name: 'synthetic', impl_version: '1.0', milky_version: '1.3', checked_at: timestamp,
        receiving_enabled: false, sending_enabled: false,
      };
  if (p === '/api/v1/inbox') return {
    storage_enabled: true, source_mode: 'mixed', import_limit: 1000,
    platforms: [{ id: 'weixin', name: '微信', live_status: 'configured', message_count: 2 }],
  };
  if (p === '/api/v1/conversations') {
    const kind = url.searchParams.get('conversation_kind');
    assert.ok(['private', 'group'].includes(kind), 'Conversation kind must remain explicit');
    const q = url.searchParams.get('q')?.trim() || '';
    return {
      items: conversations.filter((c) => c.conversation_kind === kind && (!q || c.title.includes(q) || messages(c.id).some((m) => m.body.includes(q)))),
      next_cursor: null,
    };
  }
  const scoped = p.match(/^\/api\/v1\/conversations\/(synthetic-(?:private|group))\/(messages|profile|profile-links|ai-context|analysis)$/);
  if (scoped) {
    const [, id, endpoint] = scoped;
    if (endpoint === 'messages') return { items: messages(id), next_cursor: null };
    if (endpoint === 'profile') return profile(id);
    if (endpoint === 'profile-links') return {
      conversation_id: id, candidates: [], linked: [], document_markdown: '', truncated: false,
    };
    if (endpoint === 'analysis') return { analysis: null, memories: [] };
    return {
      context_hash: 'synthetic-context', model: modelName, endpoint_host: modelHost, external: true,
      message_count: 1, memory_count: 0, truncated: false, messages: messages(id), memories: [],
      mode: 'draft_only', tone: 'natural',
    };
  }
  if (p === '/api/v1/connections/wechat-db/status') return state.sources === 'error'
    ? error('WECHAT_UNAVAILABLE')
    : {
        configured: true, state: 'read', account_id: 'synthetic-account', read_only: true,
        last_read_at: timestamp, last_result: sourceResult(), sending_enabled: false,
        monitor: {
          enabled: false, peer_ids: [], interval_seconds: 30, state: state.sources === 'monitor-error' ? 'error' : 'disabled',
          last_cycle_at: timestamp, last_error_code: state.sources === 'monitor-error' ? 'WECHAT_UNAVAILABLE' : null, last_result: sourceResult(), read_only: true, sending_enabled: false,
        },
      };
  if (p === '/api/v1/connections/wechat-db/sessions') {
    assert.equal(url.search, '', 'Session filtering must remain local');
    return {
      items: [
        { peer_id: 'wxid_synthetic_private', name: '合成联系人', conversation_kind: 'private' },
        { peer_id: '@@synthetic-group', name: '合成项目群', conversation_kind: 'group' },
      ],
      read_only: true, sending_enabled: false,
    };
  }
  if (p === '/api/v1/connections/email-imap/status') return state.sources === 'unconfigured'
    ? { configured: false, state: 'not_configured', account: null, folder: null, last_synced_at: null, last_result: null, sending_enabled: false }
    : state.sources === 'error'
    ? error('EMAIL_UNREACHABLE')
    : {
        configured: true, state: 'synced', account: 'synthetic@example.invalid', folder: 'INBOX',
        last_synced_at: timestamp,
        last_result: { inserted: 2, duplicates: 1, skipped: state.sources === 'skipped' ? 1 : 0, limited: state.sources === 'limited' },
        sending_enabled: false,
      };
  if (p === '/api/v1/assistant/status') return assistantStatus;
  if (p === '/api/v1/feishu/status') return {
    configured: true, key_configured: true, app_id: 'cli_synthetic_browser', config_revision: 2,
    target_binding_id: binding, target_label_masked: 'synthetic_self', recipient_open_id_masked: 'ou_***test',
    recipient_ownership_verified: false,
    schedule: {
      enabled: false, revision: 1, timezone: 'Asia/Shanghai', slots: ['08:00', '20:00'],
      model_config_id: '', model_config_revision: 0, config_revision: 0, target_binding_id: '',
      next_window_end: null, last_window_end: null,
    },
    latest_report_id: reportID, latest_delivery: feishuDelivery(),
    capabilities: { private_self_only: true, automatic_retry: false }, timezone: 'Asia/Shanghai', slots: ['08:00', '20:00'],
  };
  if (p === '/api/v1/feishu/reports/latest' || p === `/api/v1/feishu/reports/${reportID}`) return { report: feishuReport() };
  if (p === `/api/v1/feishu/deliveries/${deliveryID}`) return { delivery: feishuDelivery() };
  if (['connections', 'rules', 'approvals', 'outbox', 'audit', 'messages', 'contacts', 'reports'].some((name) => p === `/api/v1/${name}`)) return empty;
  throw Error(`Unexpected synthetic API: ${method} ${url.pathname}${url.search}`);
}
