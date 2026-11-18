import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {runInNewContext} from 'node:vm';

// Exercise the shipped import converter without starting a browser or making
// network requests. Only the service transport dependency is replaced.
const source=readFileSync(new URL('../apps/desktop/src/inbox-api.ts',import.meta.url),'utf8');
const profileModule={exports:{}};
const profileSource=stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/profile-api.ts',import.meta.url),'utf8')).replace(/^import .* from '\.\/api';\r?\n/m,'const {SERVICE,ServiceError}=transport;\n').replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm,'')+'\nmodule.exports={isProfileCategory};';
runInNewContext(profileSource,{module:profileModule,transport:{SERVICE:{url:'http://127.0.0.1:17890',error:null},ServiceError:class extends Error{}}},{filename:'profile-api.ts'});
const compiled=stripTypeScriptTypes(source).replace(/^import .* from '\.\/api';\r?\n/m,'const {SERVICE,ServiceError}=transport;\n').replace(/^import .* from '\.\/profile-api';\r?\n/m,'const {isProfileCategory}=profileDependency;\n').replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm,'')+'\nmodule.exports={parseImport};';
const compiledModule={exports:{}};
runInNewContext(compiled,{module:compiledModule,profileDependency:profileModule.exports,TextEncoder,URLSearchParams,transport:{SERVICE:{url:'http://127.0.0.1:17890',error:null},ServiceError:class extends Error{}}},{filename:'inbox-api.ts'});
const {parseImport}=compiledModule.exports;
const headers={subject:'合成主题',from_name:'合成发件人',from_address:'sender@example.test',to:['receiver@example.test'],folder:'INBOX'};
const input={platform:'email',account_id:'synthetic-mail',account_name:'合成邮箱',messages:[{external_id:'one',peer_id:'thread-one',peer_name:'合成主题',direction:'incoming',body:'合成正文',occurred_at:'2026-01-01T00:00:00Z',read:true,email:headers}]};
const converted=parseImport(input,{platform:'qq',account_id:'unused',account_name:'unused'},1000);
assert.deepEqual(JSON.parse(JSON.stringify(converted.messages[0].email)),headers);
assert.equal(converted.messages[0].read,true,'an imported seen flag must not be discarded');
input.messages[0].read=false;
assert.equal(parseImport(input,{},1000).messages[0].read,false,'false is a meaningful unread flag');
const malformed=structuredClone(input);malformed.messages[0].email.to='not-an-array';
assert.throws(()=>parseImport(malformed,{},1000),/元数据/);
const wrongRead=structuredClone(input);wrongRead.messages[0].read='true';
assert.throws(()=>parseImport(wrongRead,{},1000),/已读状态/);
const wrongPlatform=structuredClone(input);wrongPlatform.platform='qq';
assert.throws(()=>parseImport(wrongPlatform,{},1000),/元数据/);
for (const platform of ['xianyu','taobao','jd','pinduoduo']) {
  const message={external_id:`${platform}:message/42`,peer_id:'peer/unchanged:01',peer_name:'合成店铺',direction:'incoming',body:'合成购物私聊',occurred_at:'2026-11-17T08:00:00+08:00'};
  const data={platform,account_id:'own:unchanged/01',account_name:'合成购物账号',messages:[message]};
  const parsed=parseImport(data,{},1000);
  assert.equal(parsed.platform,platform);
  assert.equal(parsed.account_id,data.account_id);
  assert.equal(parsed.messages[0].external_id,message.external_id);
  assert.equal(parsed.messages[0].peer_id,message.peer_id);
  assert.equal(parsed.messages[0].conversation_kind,'private');
  assert.equal(parseImport([message],{platform,account_id:data.account_id,account_name:data.account_name},1000).platform,platform);
  assert.throws(()=>parseImport({...data,messages:[{...message,conversation_kind:'group'}]}, {},1000),/仅支持私聊文本/);
  assert.throws(()=>parseImport({...data,conversation_kind:'group'}, {},1000),/统一 JSON 私聊文本格式/);
  assert.throws(()=>parseImport({...data,messages:[{...message,type:'image'}]}, {},1000),/私聊文本字段/);
  assert.throws(()=>parseImport({...data,messages:[{...message,email:headers}]}, {},1000),/私聊文本字段/);
}
const group={platform:'weixin',account_id:'self',account_name:'合成微信',messages:[{external_id:'group-message',peer_id:'@@synthetic-group',peer_name:'合成群',conversation_kind:'group',direction:'incoming',body:'合成群正文',occurred_at:'2026-11-17T08:00:00+08:00'}]};
assert.equal(parseImport(group,{},1000).messages[0].conversation_kind,'group','non-shopping explicit groups remain groups');
console.log('Inbox import regression checks passed: shopping private-text scope and original IDs, non-shopping group preservation, mail metadata/read flags.');
