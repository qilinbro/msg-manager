import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';

const spec = JSON.parse(readFileSync(new URL('../api/openapi.yaml', import.meta.url), 'utf8'));
assert.equal(spec.openapi, '3.1.0');
assert.ok(spec.paths['/api/v1/system']);
const qqVerify = spec.paths['/api/v1/connections/qq-onebot/verify']?.get;
assert.ok(qqVerify?.responses['200'], 'QQ identity verification must document its real response');
assert.ok(qqVerify.responses['503'], 'QQ configuration and provider failures must be explicit');
assert.equal(spec.components.schemas.QQVerification.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.QQVerification.properties.receiving_enabled.const, false);
const events = spec.paths['/api/v1/events']?.get;
assert.ok(events?.responses['501'], 'Event streaming is reserved until persistence exists');
assert.ok(!events.responses['200'], 'Do not advertise an implemented event stream');
assert.equal(events['x-implementation-status'], 'planned');
for (const path of Object.keys(spec.paths)) {
  if (path.startsWith('/api/v1/')) {
    assert.deepEqual(spec.security, [{ localBearer: [] }]);
    for (const operation of Object.values(spec.paths[path])) {
      assert.ok(operation.responses['401'], path + ' must document authentication failure');
    }
  }
}
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
  'patch /api/v1/conversations/{conversation_id}',
  'post /api/v1/imports/messages',
  'post /api/v1/imports/telegram',
  'post /api/v1/connections/email-imap/sync',
  'post /api/v1/connections/wechat-db/read',
  'post /api/v1/conversations/{conversation_id}/analyze',
  'patch /api/v1/conversations/{conversation_id}/draft',
  'patch /api/v1/conversations/{conversation_id}/ai-settings',
  'patch /api/v1/conversations/{conversation_id}/memories/{memory_id}',
  'delete /api/v1/conversations/{conversation_id}/memories/{memory_id}',
].sort());
assert.ok(spec.paths['/api/v1/inbox'].get.responses['200']);
assert.deepEqual(spec.components.schemas.InboxStatus.properties.source_mode.enum,['import','mixed']);
assert.equal(spec.components.schemas.EmailStatus.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.EmailSyncResult.properties.sending_enabled.const,false);
assert.equal(spec.components.schemas.WechatStatus.properties.read_only.const,true);
assert.equal(spec.components.schemas.WechatReadResult.properties.sending_enabled.const,false);
assert.deepEqual(Object.keys(spec.paths['/api/v1/connections/wechat-db/read'].post.requestBody.content['application/json'].schema.properties),['peer_id']);
assert.equal(spec.components.schemas.AIStatus.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.AIAnalysis.properties.sending_enabled.const, false);
assert.equal(spec.components.schemas.AIAnalyzeRequest.properties.confirmed.const, true);
assert.deepEqual(spec.components.schemas.AISettings.properties.mode.enum, ['draft_only','paused']);
console.log('API contract references, inbox routes and disabled sender boundaries verified.');
