import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';

const spec = JSON.parse(readFileSync(new URL('../api/openapi.yaml', import.meta.url), 'utf8'));
assert.equal(spec.openapi, '3.1.0');
assert.ok(spec.paths['/api/v1/system']);
assert.ok(!spec.paths['/api/v1/connections/qq-onebot/verify'], 'The retired OneBot endpoint must not be advertised');
assert.ok(!spec.components.schemas.QQVerification, 'The retired OneBot response schema must be removed');
assert.deepEqual(spec.components.schemas.Connector.properties.id.enum, [
  'qq-milky', 'weixin-clawbot', 'telegram-bot', 'email-imap', 'wechat-db',
], 'The connector catalog must expose Milky as its only QQ integration');
const events = spec.paths['/api/v1/events']?.get;
assert.ok(events?.responses['501'], 'Event streaming is reserved until persistence exists');
assert.ok(!events.responses['200'], 'Do not advertise an implemented event stream');
assert.equal(events['x-implementation-status'], 'planned');
const anonymousDeviceProofs = new Set([
  'post /api/v1/auth/challenges',
  'post /api/v1/auth/sessions',
]);
assert.deepEqual(spec.security, [{ localBearer: [] }]);
for (const [path, operations] of Object.entries(spec.paths)) {
  if (!path.startsWith('/api/v1/')) continue;
  for (const [method, operation] of Object.entries(operations)) {
    assert.ok(operation.responses['401'], path + ' must document authentication/proof failure');
    if (anonymousDeviceProofs.has(`${method} ${path}`)) {
      assert.deepEqual(operation.security, [], 'Only explicit origin-bound proof endpoints omit bearer authentication');
    } else if (operation.security !== undefined) {
      assert.deepEqual(operation.security, spec.security, path + ' must not weaken bearer authentication');
    }
  }
}
const pairDevice = spec.paths['/api/v1/auth/devices']?.post;
assert.equal(pairDevice?.['x-master-only'], true);
assert.ok(pairDevice.responses['201']);
assert.ok(spec.paths['/api/v1/auth/challenges']?.post.responses['429']);
assert.ok(spec.paths['/api/v1/auth/sessions']?.post.responses['200']);
assert.ok(spec.paths['/api/v1/auth/devices/{device_id}']?.delete.responses['204']);
assert.deepEqual(spec.components.schemas.DevicePairRequest.required, ['public_key']);
assert.equal(spec.components.schemas.DevicePairRequest.additionalProperties, false);
assert.equal(spec.components.schemas.DeviceSessionRequest.properties.signature.minLength, 86);
assert.equal(spec.components.schemas.DeviceID.maxLength, 22);
assert.equal(spec.components.parameters.DeviceOrigin.required, true);
assert.ok(!spec.components.parameters.DeviceOrigin.schema.enum.includes('null'));
function checkRefs(value) {
  if (!value || typeof value !== 'object') return;
  if (value.$ref) {
    assert.ok(value.$ref.startsWith('#/'), 'External references are not bundled');
    let target = spec;
    for (const key of value.$ref.slice(2).split('/')) target = target?.[key];
    assert.ok(target, 'Unresolved schema: ' + value.$ref);
  }
  for (const entry of Object.values(value)) checkRefs(entry);
}
checkRefs(spec);
for (const path of Object.values(spec.paths)) {
  for (const method of ['post', 'patch', 'put', 'delete']) {
    if (path[method] && path[method]['x-implementation-status'] !== 'implemented') assert.ok(path[method].responses['501'], 'Unimplemented mutations must remain unavailable');
  }
}
const implementedMutations = Object.entries(spec.paths).flatMap(([path, operations]) =>
  Object.entries(operations).filter(([method, op]) => ['post', 'patch', 'put', 'delete'].includes(method) && op['x-implementation-status'] === 'implemented').map(([method]) => `${method} ${path}`));
assert.deepEqual(implementedMutations.sort(), [
  'post /api/v1/auth/devices',
  'post /api/v1/auth/challenges',
  'post /api/v1/auth/sessions',
  'delete /api/v1/auth/devices/{device_id}',
  'patch /api/v1/conversations/{conversation_id}',
  'post /api/v1/imports/messages',
  'post /api/v1/imports/telegram',
  'post /api/v1/connections/email-imap/sync',
  'post /api/v1/connections/wechat-db/read',
  'post /api/v1/connections/wechat-db/monitor',
  'post /api/v1/connections/qq-milky/sync',
  'post /api/v1/connections/qq-milky/monitor',
	'patch /api/v1/ai/model-config',
	'post /api/v1/conversations/{conversation_id}/profile-links',
	'delete /api/v1/conversations/{conversation_id}/profile-links/{target_conversation_id}',
  'post /api/v1/conversations/{conversation_id}/analyze',
  'patch /api/v1/conversations/{conversation_id}/draft',
  'patch /api/v1/conversations/{conversation_id}/ai-settings',
  'patch /api/v1/conversations/{conversation_id}/memories/{memory_id}',
  'delete /api/v1/conversations/{conversation_id}/memories/{memory_id}',
  'patch /api/v1/conversations/{conversation_id}/profile',
  'post /api/v1/conversations/{conversation_id}/profile/generate',
  'post /api/v1/conversations/{conversation_id}/profile/enrich',
  'patch /api/v1/conversations/{conversation_id}/profile/facts/{fact_id}',
  'patch /api/v1/digest/schedule',
  'post /api/v1/digest/run',
  'patch /api/v1/feishu/config',
  'delete /api/v1/feishu/config',
  'patch /api/v1/feishu/schedule',
  'post /api/v1/feishu/preview',
  'post /api/v1/feishu/reports/{report_id}/send',
  'post /api/v1/assistant/runs',
  'post /api/v1/assistant/runs/{run_id}/confirm',
  'post /api/v1/assistant/runs/{run_id}/reject',
].sort());
assert.ok(spec.paths['/api/v1/inbox'].get.responses['200']);
assert.deepEqual(spec.components.schemas.InboxStatus.properties.source_mode.enum,['import','mixed']);
assert.equal(spec.components.schemas.EmailStatus.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.EmailSyncResult.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.WechatStatus.properties.read_only.const,true);
assert.equal(spec.components.schemas.WechatReadResult.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.WechatMonitorStatus.properties.read_only.const,true);
assert.equal(spec.components.schemas.WechatMonitorStatus.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.WechatMonitorSettings.properties.interval_seconds.minimum,10);
assert.equal(spec.components.schemas.WechatMonitorSettings.properties.peer_ids.maxItems,20);
assert.ok(spec.components.schemas.WechatStatus.required.includes('monitor'));
assert.deepEqual(Object.keys(spec.paths['/api/v1/connections/wechat-db/read'].post.requestBody.content['application/json'].schema.properties),['peer_id','keyword']);
assert.equal(spec.paths['/api/v1/connections/wechat-db/read'].post.requestBody.content['application/json'].schema.properties.keyword.maxLength,128);
assert.equal(spec.components.schemas.AIStatus.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.AIAnalysis.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.AIAnalyzeRequest.properties.confirmed.const, true);
assert.deepEqual(spec.components.schemas.AISettings.properties.mode.enum, ['draft_only','paused']);
assert.equal(spec.components.schemas.ContactProfileStatus.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.ContactProfileRequest.properties.confirmed.const,true);
assert.deepEqual(spec.components.schemas.ContactProfileFact.properties.state.enum,['unverified','confirmed','rejected']);
assert.ok(spec.paths['/api/v1/conversations/{conversation_id}/profile/generate'].post.responses['202']);
assert.ok(!spec.components.schemas.ContactProfileJob.properties.lease_token);
assert.equal(spec.components.schemas.MilkyStatus.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.MilkyStatus.properties.read_only.const, true);
assert.equal(spec.components.schemas.MilkyVerification.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.MilkyVerification.properties.receiving_enabled.type, 'boolean');
assert.equal(spec.components.schemas.MilkyVerification.properties.milky_version.const, '1.3');
assert.equal(spec.components.schemas.MilkySyncResult.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.MilkySyncResult.properties.source_kind.const, 'protocol_sync');
assert.equal(spec.components.schemas.MilkyMonitor.properties.read_only.const, true);
assert.equal(spec.components.schemas.MilkySyncRequest.properties.limit.maximum, 30);
assert.equal(spec.components.schemas.MilkyMonitorRequest.properties.peer_ids.maxItems, 20);
assert.ok(spec.paths['/api/v1/connections/qq-milky/verify'].get.responses['200']);
const schemas = spec.components.schemas;
for (const name of ['AIStatus', 'AssistantStatus', 'AssistantRun', 'AssistantStartRequest', 'AssistantConfirmRequest', 'DigestSchedule', 'DigestRunRequest']) {
  assert.ok(schemas[name].required.includes('model_config_id'), name + ' must bind consent to the target');
  assert.ok(schemas[name].required.includes('model_config_revision'), name + ' must reject ABA config changes');
  assert.equal(schemas[name].additionalProperties, false, name + ' must have an explicit contract');
  assert.equal(schemas[name].properties.model_config_revision.minimum, 0, 'Environment-only model config revision zero is valid');
}
for (const name of ['AssistantStartRequest', 'AssistantConfirmRequest', 'DigestRunRequest', 'DigestScheduleSave']) {
  assert.equal(schemas[name].properties.confirmed.const, true, name + ' must require fresh explicit consent');
}
assert.deepEqual(schemas.AssistantRejectRequest.required, ['action_id']);
assert.deepEqual(schemas.AssistantConfirmRequest.required, ['action_id', 'model_config_id', 'model_config_revision', 'confirmed']);
assert.ok(!schemas.AssistantConfirmRequest.properties.arguments, 'Confirmation may not replace frozen arguments');
assert.ok(!schemas.AssistantStartRequest.properties.tools, 'Clients may not inject tool definitions');
assert.ok(!schemas.AssistantStartRequest.properties.history, 'Clients may not inject model roles or history');
assert.deepEqual(schemas.AssistantOperation.properties.availability.enum, ['available', 'confirmation_required', 'human_only', 'not_implemented']);
assert.equal(schemas.AssistantStatus.properties.sending_enabled.const, false);
assert.equal(schemas.AssistantStatus.properties.session_ttl_seconds.const, 1800);
assert.ok(schemas.AssistantRun.properties.state.enum.includes('unknown'));
assert.ok(schemas.AssistantToolResult.properties.state.enum.includes('unknown'));
const assistantMethods = Object.entries(spec.paths).filter(([path]) => path.startsWith('/api/v1/assistant/')).flatMap(([path, ops]) => Object.entries(ops).map(([method, op]) => ({ path, method, op })));
assert.equal(assistantMethods.length, 5, 'Only bounded status/start/recover/confirm/reject control methods may exist');
for (const { path, method, op } of assistantMethods) {
  assert.equal(op['x-implementation-status'], 'implemented', `${method} ${path}`);
  assert.ok(op.responses['200']);
  if (method === 'post') {
    assert.ok(op.responses['413'], 'Assistant JSON must document the 16 KiB body limit');
    assert.ok(op.responses['415'], 'Assistant JSON must document media-type rejection');
  }
  if (path !== '/api/v1/assistant/status') {
    assert.ok(op.responses['202'], 'Nonterminal snapshots must not masquerade as completion');
    assert.ok(op.responses['404'] && op.responses['410'], 'Run absence/expiry must not revoke devices via 401');
  }
}
assert.equal(schemas.DigestLatest.properties.sending_enabled.const, false);
assert.ok(schemas.DigestLatest.required.includes('sending_enabled'));
assert.equal(schemas.DigestScheduleSave.additionalProperties, false);
assert.ok(schemas.DigestScheduleSave.allOf[0].then.required.includes('model_config_id'));
assert.ok(!schemas.DigestScheduleSave.required.includes('model_config_id'), 'Disabling must work without configured model credentials');
assert.deepEqual(schemas.DigestReport.properties.state.enum, ['running', 'succeeded', 'failed', 'unknown']);
assert.equal(spec.paths['/api/v1/digest/run'].post.requestBody.content['application/json'].schema.$ref, '#/components/schemas/DigestRunRequest');
assert.equal(spec.paths['/api/v1/digest/run'].post.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/DigestLatest');
const feishuMethods = Object.entries(spec.paths)
  .filter(([path]) => path.startsWith('/api/v1/feishu/'))
  .flatMap(([path, operations]) => Object.entries(operations).map(([method, op]) => ({ path, method, op })));
assert.equal(feishuMethods.length, 10, 'Only the frozen self-private Feishu control surface may be exposed');
for (const { path, method, op } of feishuMethods) {
  assert.equal(op['x-implementation-status'], 'implemented', `${method} ${path}`);
  assert.ok(op.responses['200'] && op.responses['401'] && op.responses['409']);
  if (method !== 'get') {
    assert.equal(op['x-automatic-retry'], false, 'Feishu mutations must never be automatically replayed');
    const name = op.requestBody.content['application/json'].schema.$ref.split('/').at(-1);
    assert.equal(schemas[name].additionalProperties, false);
    assert.equal(schemas[name].properties.confirmed.const, true);
    assert.ok(schemas[name].required.includes('confirmed'));
  }
}
for (const method of ['patch', 'delete']) {
  assert.equal(spec.paths['/api/v1/feishu/config'][method]['x-assistant-availability'], 'human_only');
}
for (const [path, method, wrapper] of [
  ['/api/v1/feishu/preview', 'post', 'FeishuReportResult'],
  ['/api/v1/feishu/reports/{report_id}/send', 'post', 'FeishuDeliveryResult'],
]) {
  const operation = spec.paths[path][method];
  assert.equal(operation['x-assistant-availability'], 'confirmation_required');
  for (const status of ['200', '202']) {
    assert.equal(operation.responses[status].content['application/json'].schema.$ref, '#/components/schemas/' + wrapper);
  }
}
assert.deepEqual(schemas.FeishuConfigSave.required, ['expected_revision', 'app_id', 'app_secret', 'retain_secret', 'recipient_open_id', 'confirmed']);
assert.equal(schemas.FeishuConfigSave.properties.expected_revision.minimum, 1);
assert.equal(schemas.FeishuConfigSave.properties.app_secret.writeOnly, true);
assert.equal(schemas.FeishuConfigSave.properties.recipient_open_id.writeOnly, true);
assert.deepEqual(schemas.FeishuConfigDelete.required, ['expected_revision', 'confirmed']);
assert.equal(schemas.FeishuSchedule.properties.revision.minimum, 1);
assert.equal(schemas.FeishuSchedule.properties.config_revision.minimum, 0, 'An initially disabled schedule is unbound');
assert.equal(schemas.FeishuStatus.properties.config_revision.minimum, 1, 'Configuration CAS starts at one, including unconfigured');
assert.equal(schemas.FeishuStatus.properties.recipient_ownership_verified.const, false, 'Saving a self-declared recipient is not platform ownership verification');
assert.equal(schemas.FeishuStatus.properties.capabilities.properties.private_self_only.const, true);
assert.equal(schemas.FeishuStatus.properties.capabilities.properties.automatic_retry.const, false);
assert.deepEqual(schemas.FeishuSchedule.properties.slots.prefixItems, [{ const: '08:00' }, { const: '20:00' }]);
assert.equal(schemas.FeishuSchedule.properties.timezone.const, 'Asia/Shanghai');
assert.deepEqual(schemas.FeishuScheduleSave.required, ['enabled', 'expected_revision', 'confirmed']);
const feishuBinding = ['model_config_id', 'model_config_revision', 'config_revision', 'target_binding_id'];
assert.deepEqual(schemas.FeishuScheduleSave.allOf[0].then.required, feishuBinding, 'Only enabling requires usable bound model and recipient settings');
for (const name of ['FeishuPreviewRequest', 'FeishuSendRequest']) {
  for (const field of feishuBinding) assert.ok(schemas[name].required.includes(field));
  assert.equal(schemas[name].properties.model_config_revision.minimum, 0);
  assert.equal(schemas[name].properties.config_revision.minimum, 1);
  for (const field of ['text', 'recipient_open_id', 'window_start', 'window_end', 'force', 'url', 'scopes']) {
    assert.ok(!schemas[name].properties[field], 'Clients/Agent must not replace frozen content, window, recipient, endpoint or scope');
  }
}
assert.ok(schemas.FeishuSendRequest.required.includes('content_hash'));
assert.equal(schemas.FeishuReportID.pattern, '^fr_[a-f0-9]{32}$');
assert.equal(schemas.FeishuDeliveryID.pattern, '^fd_[a-f0-9]{32}$');
assert.deepEqual(schemas.FeishuState.enum, ['running', 'succeeded', 'failed', 'unknown']);
assert.equal(schemas.FeishuReport.properties.source_refs.maxItems, 4);
assert.ok(schemas.FeishuReport.required.includes('source_refs'));
assert.equal(schemas.FeishuReport.properties.delivery.anyOf[0].$ref, '#/components/schemas/FeishuDelivery');
assert.equal(schemas.FeishuTodo.properties.needs_review.const, true);
for (const field of ['app_secret', 'access_token', 'recipient_open_id']) {
  for (const name of ['FeishuStatus', 'FeishuReport', 'FeishuDelivery']) assert.ok(!schemas[name].properties[field]);
}
assert.ok(!schemas.FeishuDelivery.properties.text, 'Receipts remain content-free for manual verification after binding revocation');
console.log('API contracts, bounded Assistant, local digest compatibility and 10 bound no-retry Feishu methods verified.');
