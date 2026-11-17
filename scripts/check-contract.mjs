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
console.log('API contract references, inbox routes and disabled sender boundaries verified.');
