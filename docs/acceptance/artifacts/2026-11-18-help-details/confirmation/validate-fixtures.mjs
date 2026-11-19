// In-memory contract preparation only. Never opens Chromium or any network connection.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import vm from 'node:vm';
import { stripTypeScriptTypes } from 'node:module';
import { fileURLToPath } from 'node:url';
import { apiFixture, state, syntheticToken, reportID, deliveryID } from './fixtures.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const context = vm.createContext({
  console, URL, AbortController, DOMException, Response, TextEncoder,
  setTimeout, clearTimeout, window: { setTimeout, clearTimeout },
});
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
let calls = 0;
const api = new vm.SyntheticModule(['SERVICE', 'ServiceError'], function () {
  this.setExport('SERVICE', { url: 'http://127.0.0.1:17890', error: null });
  this.setExport('ServiceError', ServiceError);
}, { context });
const auth = new vm.SyntheticModule(['authorizedFetch', 'authErrorMessage'], function () {
  this.setExport('authErrorMessage', (_code, message) => message);
  this.setExport('authorizedFetch', async (url, token, options) => {
    assert.equal(token, syntheticToken);
    const fixture = apiFixture(new URL(url), options.method || 'GET');
    calls++;
    return new Response(JSON.stringify(fixture.__status ? { error: fixture.error } : fixture), { status: fixture.__status || 200 });
  });
}, { context });
const modules = new Map();
async function load(name) {
  if (modules.has(name)) return modules.get(name);
  const filename = path.join(root, 'apps/desktop/src', name);
  const code = stripTypeScriptTypes(await fs.readFile(filename, 'utf8'), { mode: 'transform' });
  const module = new vm.SourceTextModule(code, { context, identifier: filename });
  modules.set(name, module);
  await module.link(async (specifier) => {
    if (specifier === './api') return api;
    if (specifier === './device-auth') return auth;
    if (specifier === './assistant-help') return load('assistant-help.ts');
    throw Error(`Unexpected fixture-validator import: ${specifier}`);
  });
  await module.evaluate();
  return module;
}
async function invoke(file, name, ...args) {
  const module = await load(file);
  assert.equal(typeof module.namespace[name], 'function', `${file}:${name}`);
  return module.namespace[name](syntheticToken, new AbortController().signal, ...args);
}
for (const [file, names] of [
  ['model-config-api.ts', ['getModelConfig', 'getModelPresets']],
  ['milky-api.ts', ['getMilkyStatus', 'verifyMilky']],
  ['ai-api.ts', ['getAIStatus']],
  ['profile-api.ts', ['getProfileStatus']],
  ['digest-api.ts', ['getDigestSchedule']],
  ['assistant-api.ts', ['getAssistantStatus']],
]) for (const name of names) await invoke(file, name);
for (const scenario of ['succeeded', 'failed', 'unknown', 'running']) {
  state.digest = scenario;
  await invoke('digest-api.ts', 'getLatestDigest');
}
for (const scenario of ['ready', 'limited', 'skipped', 'unconfigured', 'monitor-error']) {
  state.sources = scenario;
  await invoke('wechat-api.ts', 'getWechatStatus');
  await invoke('wechat-api.ts', 'getWechatSessions');
  await invoke('email-api.ts', 'getEmailStatus');
}
for (const scenario of ['ready', 'sent', 'unknown', 'running', 'failed', 'report-unknown']) {
  state.feishu = scenario;
  await invoke('feishu-api.ts', 'getFeishuStatus');
  await invoke('feishu-api.ts', 'getLatestFeishuReport');
  await invoke('feishu-api.ts', 'getFeishuReport', reportID);
  await invoke('feishu-api.ts', 'getFeishuModelStatus');
  if (scenario === 'sent' || scenario === 'unknown') await invoke('feishu-api.ts', 'getFeishuDelivery', deliveryID);
}
for (const [file, names] of [
  ['profile-api.ts', ['getProfile']],
  ['profile-links-api.ts', ['getProfileLinks']],
  ['ai-api.ts', ['getAIContext', 'getAnalysis']],
]) for (const name of names) await invoke(file, name, 'synthetic-private');
assert.throws(() => apiFixture(new URL('http://127.0.0.1:17890/api/v1/digest/run'), 'POST'), /No business mutations/);
assert.throws(() => apiFixture(new URL('http://127.0.0.1:17890/api/v1/does-not-exist'), 'GET'), /Unexpected synthetic API/);
console.log(`Fixture contracts passed: ${calls} in-memory GET calls; no browser, server or network used.`);
