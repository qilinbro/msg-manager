import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {runInNewContext} from 'node:vm';

// Exercise the shipped import converter without starting a browser or making
// network requests. Only the service transport dependency is replaced.
const source=readFileSync(new URL('../apps/desktop/src/inbox-api.ts',import.meta.url),'utf8');
const compiled=stripTypeScriptTypes(source).replace(/^import .* from '\.\/api';\r?\n/m,'const {SERVICE,ServiceError}=transport;\n').replace(/^export /gm,'')+'\nmodule.exports={parseImport};';
const compiledModule={exports:{}};
runInNewContext(compiled,{module:compiledModule,TextEncoder,URLSearchParams,transport:{SERVICE:{url:'http://127.0.0.1:17890',error:null},ServiceError:class extends Error{}}},{filename:'inbox-api.ts'});
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
console.log('Inbox import regression checks passed: mail metadata and read flags are preserved.');
