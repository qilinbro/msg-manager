// One isolated desktop/mobile UI batch. NO application network request is continued.
// Explicit final-build authorization is required; this file never starts an app server.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { apiFixture, state, syntheticToken, modelName, modelHost } from './fixtures.mjs';

const ownDirectory = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(ownDirectory, '../..');
assert.equal(path.resolve(process.cwd()), root, 'Run only from the intended checkout root');
assert.ok(process.argv.includes('--synthetic-only'), 'Explicit --synthetic-only required');
assert.ok(process.argv.includes('--approved-final-build'), 'Wait for lead approval of the final build');
const distArgument = process.argv.find((argument) => argument.startsWith('--dist='))?.slice(7);
assert.ok(distArgument && path.isAbsolute(distArgument), 'Pass the approved absolute --dist= path');
const approvedDist = await fs.realpath(distArgument);
const relativeDist = path.relative(await fs.realpath(root), approvedDist);
assert.ok(relativeDist && !relativeDist.startsWith('..') && !path.isAbsolute(relativeDist), 'The build must be inside this checkout');
const frontend = 'http://127.0.0.1:1420';
const service = 'http://127.0.0.1:17890';
const browser = process.env.SYNTHETIC_CHROMIUM || path.join(process.env.LOCALAPPDATA || '', 'ms-playwright/chromium-1228/chrome-win64/chrome.exe');
assert.ok(path.isAbsolute(browser), 'Use an installed absolute Chromium executable');
const artifacts = await fs.mkdtemp(path.join(ownDirectory, 'run-'));
const profile = path.join(artifacts, 'chromium-profile');
const j = JSON.stringify;
const startedAt = Date.now();
const trace = [];
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const traceSafe = (value) => JSON.parse(j(value, (key, item) => /^(authorization|postData|postDataEntries|api_key|token)$/i.test(key) ? '[omitted]' : item).replaceAll(syntheticToken, '[synthetic-token-omitted]'));
const visible = `(e)=>!!e&&e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true})`;
const report = {
  synthetic_only: true, server_started: false, logical_frontend: frontend,
  frontend_requests_fulfilled_from_frozen_dist: null,
  all_application_http_requests_intercepted: null, actual_platform_or_model_calls: 0,
  business_write_requests: 0, remembered_credential: false,
  fingerprints_unchanged: null, integrity_checked: false,
  checks: [], failures: [], screenshots: [], screenshot_metadata: [], api: [], staticRequests: [],
  scenarios: [], skipped: [], connection_safety: [], fingerprint_checks: {},
  networkRequests: [], unexpectedRequests: [], unexpectedTargets: [],
  consoleErrors: [], interceptionErrors: [], accessibility: [], disclosures: [], controlled_gets: [],
};
const frozenDist = new Map();
async function snapshot(directory, prefix = '', bytesMap) {
  const result = [];
  for (const entry of (await fs.readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    assert.ok(!entry.isSymbolicLink(), `No links in the frozen artifact: ${relative}`);
    if (entry.isDirectory()) result.push(...await snapshot(absolute, relative, bytesMap));
    else if (entry.isFile()) {
      const bytes = await fs.readFile(absolute);
      const stat = await fs.stat(absolute);
      result.push({ path: relative, bytes: bytes.length, sha256: sha(bytes), mtime_ms: stat.mtimeMs });
      bytesMap?.set(`/${relative}`, bytes);
    }
  }
  return result;
}
const sourceManifest = await snapshot(path.join(root, 'apps/desktop/src'), 'apps/desktop/src');
const distManifest = await snapshot(approvedDist, '', frozenDist);
assert.ok(frozenDist.has('/index.html'), 'Approved final build must have an index');
const harnessFiles = ['synthetic-browser.mjs', 'fixtures.mjs'];
const harnessManifest = await Promise.all(harnessFiles.map(async (file) => ({ path: file, sha256: sha(await fs.readFile(path.join(ownDirectory, file))) })));

let child, ws, session, ownedTargetId, expectedViewport, sequence = 0, closing = false, currentCase = 'initialization';
const pending = new Map();
const deadline = Date.now() + 300_000;
const heldGETs = new Map();
const interceptedNetworkIDs = new Set();
function command(method, params = {}, sessionID = session) {
  const id = ++sequence;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { pending.delete(id); reject(Error(`CDP timeout: ${method}`)); }, closing ? 2000 : 10000);
    pending.set(id, { resolve, reject, timeout, method });
    trace.push({ t: Date.now() - startedAt, direction: 'send', id, method, params: traceSafe(method === 'Fetch.fulfillRequest' ? { ...params, body: `[omitted base64; sha256=${sha(params.body || '')}]` } : params) });
    ws.send(j({ id, method, params, ...(sessionID ? { sessionId: sessionID } : {}) }));
  });
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw Error(j(result.exceptionDetails));
  return result.result?.value;
}
// Predicates must cross CDP by value as booleans, never React-attached DOM objects.
async function evaluatePredicate(expression) { return evaluate(`Boolean((${expression}))`); }
async function until(predicate, label, milliseconds = 10000) {
  const end = Math.min(deadline, Date.now() + milliseconds);
  while (Date.now() < end) {
    assert.deepEqual(report.interceptionErrors, [], 'Fail-closed interception error');
    assert.deepEqual(report.unexpectedTargets, [], 'Unexpected browser target');
    if (typeof predicate === 'function' ? Boolean(await predicate()) : await evaluatePredicate(predicate)) return;
    await delay(35);
  }
  throw Error(`Timed out: ${label}`);
}
async function settle() {
  await evaluate('document.fonts.ready.then(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))))');
}
function passed(label) { report.checks.push(`${currentCase}: ${label}`); }
function record(condition, label, actual) {
  if (condition) passed(label);
  else report.failures.push({ case: currentCase, label, ...(actual === undefined ? {} : { actual }) });
  return condition;
}
async function check(expression, label) { return record(await evaluatePredicate(expression), label); }
async function screenshot(name) {
  await settle();
  const layout = await command('Page.getLayoutMetrics');
  const dom = await evaluate(`({innerWidth,innerHeight,outerWidth,outerHeight,clientWidth:document.documentElement.clientWidth,clientHeight:document.documentElement.clientHeight,dpr:devicePixelRatio,root_count:document.querySelectorAll('#root').length,sidebar_count:document.querySelectorAll('.sidebar').length,visual:visualViewport?{width:visualViewport.width,height:visualViewport.height,scale:visualViewport.scale,pageX:visualViewport.pageLeft,pageY:visualViewport.pageTop}:null})`);
  const geometry = { case: currentCase, name, expected: expectedViewport, dom, cssLayoutViewport: layout.cssLayoutViewport, cssVisualViewport: layout.cssVisualViewport, cssContentSize: layout.cssContentSize };
  report.screenshot_metadata.push(geometry);
  // Client/visual dimensions exclude native scrollbars; inner dimensions include them.
  record(dom.root_count === 1 && dom.sidebar_count === 1 && dom.innerWidth === expectedViewport.width && dom.innerHeight === expectedViewport.height && dom.dpr === 1 && Math.abs(layout.cssLayoutViewport.clientWidth - dom.clientWidth) <= 1 && Math.abs(layout.cssVisualViewport.clientWidth - dom.visual.width) <= 1 && Math.abs(layout.cssVisualViewport.clientHeight - dom.visual.height) <= 1 && dom.visual.scale === 1, `${name}: single app root and expected layout viewport`, geometry);
  const result = await command('Page.captureScreenshot', { format: 'png', fromSurface: true, captureBeyondViewport: false });
  await fs.writeFile(path.join(artifacts, name), Buffer.from(result.data, 'base64'));
  report.screenshots.push(name);
}
async function scenario(label, work) {
  currentCase = label;
  const before = report.failures.length;
  const outcome = { case: label, status: 'running', assertions_before: report.checks.length };
  try {
    await work();
    outcome.status = report.failures.length === before ? 'passed' : 'failed';
    return outcome.status === 'passed';
  } catch (error) {
    outcome.status = 'interrupted';
    const failure = { case: label, label: 'Scenario interrupted', error: error.stack || String(error) };
    report.failures.push(failure);
    try {
      failure.body = await evaluate('document.body.innerText');
      await screenshot(`failure-${label.replace(/[^a-z0-9-]/gi, '-')}.png`);
    } catch {}
    if (report.interceptionErrors.length || report.unexpectedTargets.length || Date.now() >= deadline) throw error;
    return false;
  } finally {
    outcome.assertions_recorded = report.checks.length - outcome.assertions_before;
    outcome.findings_recorded = report.failures.length - before;
    report.scenarios.push(outcome);
    for (const [pathname, gate] of heldGETs) {
      gate.release();
      report.controlled_gets.push({ case: label, pathname, paused: gate.paused, settled: gate.settled, expected_abort: !!gate.expectedAbort, browser_canceled: gate.abortedByBrowser, late_fulfillment_rejected: !!gate.fulfill_after_abort_rejected });
    }
    heldGETs.clear();
  }
}
function dependentCases(width) {
  return ['overview', 'settings', 'milky-get-busy-success-error',
    ...['running', 'unknown', 'failed'].map(value => `digest-${value}`),
    'sources-ready-and-dirty', 'wechat-stop-get-refresh-late',
    ...['limited', 'skipped', 'monitor-error', 'error', 'unconfigured'].map(value => `sources-${value}`),
    'inbox-ai-profile-import',
    ...['ready', 'sent', 'running', 'failed', 'report-unknown', 'unknown'].map(value => `feishu-${value}`),
    'assistant-disclosures'].map(label => `${label}-${width}`);
}
function skipDependents(width, blockedBy) {
  for (const label of dependentCases(width)) report.skipped.push({ case: label, status: 'skipped', blocked_by: blockedBy, reason: 'Connection prerequisite did not pass; no product coverage is claimed for this case.' });
}
function finalizeCoverage() {
  const planned = [1440, 390].flatMap(width => [`connect-synthetic-only-${width}`, ...dependentCases(width)]);
  const reached = new Set([...report.scenarios, ...report.skipped].map(item => item.case));
  for (const label of planned) if (!reached.has(label)) report.skipped.push({ case: label, status: 'not-run', blocked_by: report.error ? 'batch-error' : 'unreached-control-flow', reason: 'Not executed; no product coverage is claimed.' });
  report.coverage = { requested_cases: planned.length, executed_cases: report.scenarios.length, passed_cases: report.scenarios.filter(item => item.status === 'passed').length, failed_cases: report.scenarios.filter(item => item.status === 'failed').length, interrupted_cases: report.scenarios.filter(item => item.status === 'interrupted').length, skipped_cases: report.skipped.length };
}
async function verifyFinalIntegrity() {
  // Always run from finally, including a failed connection or a global deadline.
  currentCase = 'final-integrity';
  record(report.consoleErrors.length === 0, 'No runtime exceptions or console errors', report.consoleErrors);
  record(report.unexpectedRequests.length === 0 && report.business_write_requests === 0, 'No unknown or non-GET business requests', report.unexpectedRequests);
  record(report.unexpectedTargets.length === 0 && report.interceptionErrors.length === 0, 'No escaped browser targets or interception errors');
  const http = report.networkRequests.filter(request => /^https?:/.test(request.url));
  const unintercepted = http.filter(request => !interceptedNetworkIDs.has(request.id));
  report.observed_http_requests = http.length;
  report.all_application_http_requests_intercepted = unintercepted.length === 0;
  record(unintercepted.length === 0, 'Every observed application HTTP request was intercepted', unintercepted);
  report.forwarded_requests = trace.filter(event => event.direction === 'send' && /^Fetch\.continue/.test(event.method)).length;
  record(report.forwarded_requests === 0, 'No application request was forwarded');
  report.frontend_requests_fulfilled_from_frozen_dist = report.staticRequests.length ? report.staticRequests.every(request => {
    const file = request.path === '/' ? '/index.html' : decodeURIComponent(request.path);
    const bytes = frozenDist.get(file) || (file === '/favicon.ico' ? Buffer.alloc(0) : undefined);
    return !!bytes && request.sha256 === sha(bytes);
  }) : null;
  if (report.staticRequests.length) record(report.frontend_requests_fulfilled_from_frozen_dist, 'Observed frontend bytes match frozen dist');
  for (const [label, expected, readCurrent] of [
    ['sources', sourceManifest, () => snapshot(path.join(root, 'apps/desktop/src'), 'apps/desktop/src')],
    ['dist', distManifest, () => snapshot(approvedDist)],
    ['harness', harnessManifest, () => Promise.all(harnessFiles.map(async file => ({ path: file, sha256: sha(await fs.readFile(path.join(ownDirectory, file))) })))],
  ]) {
    try {
      const unchanged = j(await readCurrent()) === j(expected);
      report.fingerprint_checks[label] = { checked: true, unchanged };
      record(unchanged, `${label}: frozen fingerprints unchanged`);
    } catch (error) {
      report.fingerprint_checks[label] = { checked: false, unchanged: null, error: String(error) };
      record(false, `${label}: could not verify frozen fingerprints`, String(error));
    }
  }
  const fingerprints = Object.values(report.fingerprint_checks);
  report.fingerprints_unchanged = fingerprints.every(item => item.checked) ? fingerprints.every(item => item.unchanged) : null;
  report.integrity_checked = true;
}
async function key(name, modifiers = 0) {
  const keys = { Tab: [9, 'Tab'], Enter: [13, 'Enter'], Space: [32, 'Space'], Escape: [27, 'Escape'], ArrowDown: [40, 'ArrowDown'], ArrowUp: [38, 'ArrowUp'], Home: [36, 'Home'], End: [35, 'End'] };
  const [windowsVirtualKeyCode, code] = keys[name];
  const actualKey = name === 'Space' ? ' ' : name;
  await command('Input.dispatchKeyEvent', { type: 'keyDown', key: actualKey, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers, ...(name === 'Space' ? { text: ' ', unmodifiedText: ' ' } : {}) });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key: actualKey, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
}
let touchInput = false;
async function pointClick(expression, label) {
  const point = await evaluate(`(()=>{const e=${expression};if(!e||e.disabled)throw Error('Missing/disabled '+${j(label)});e.scrollIntoView({block:'center',inline:'nearest'});e.focus();const r=e.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2,hit=document.elementFromPoint(x,y);if(!(${visible})(e)||r.width<=0||r.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight||!hit||!e.contains(hit))throw Error('Target is not visible/hittable '+${j(label)});return{x,y}})()`);
  if (touchInput) {
    await command('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...point, radiusX: 2, radiusY: 2, force: 1, id: 1 }] });
    await command('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  } else {
    await command('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
    await command('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', buttons: 1, clickCount: 1 });
    await command('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', buttons: 0, clickCount: 1 });
  }
  await settle();
}
async function click(selector) { await pointClick(`document.querySelector(${j(selector)})`, selector); }
async function clickText(text, scope = 'body') {
  await pointClick(`[...(document.querySelector(${j(scope)})?.querySelectorAll('button')||[])].find(e=>e.textContent.trim()===${j(text)})`, text);
}
async function input(selector, value) {
  await evaluate(`(()=>{const e=document.querySelector(${j(selector)});if(!e||e.disabled)throw Error('Missing/disabled input '+${j(selector)});const prototype=e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(e,${j(value)});e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await settle();
}
async function focus(expression) {
  await evaluate(`(()=>{const e=${expression};if(!e)throw Error('Missing focus target');e.scrollIntoView({block:'center',inline:'nearest'});e.focus();if(document.activeElement!==e)throw Error('Focus rejected');})()`);
}
async function toggleDetails(selector, open) {
  const expression = `document.querySelector(${j(selector)})`;
  const actual = await evaluate(`(${expression})?.open`);
  assert.equal(typeof actual, 'boolean', `Details exists: ${selector}`);
  if (actual !== open) {
    await focus(`(${expression}).querySelector(':scope > summary')`);
    await key('Enter');
    await settle();
  }
  assert.equal(await evaluate(`(${expression}).open`), open, `Native details state: ${selector}`);
}
async function navigate(label) { await click(`.sidebar button[aria-label=${j(label)}]`); }
async function visibleText(text, scope = 'body') {
  return evaluate(`(()=>{const r=document.querySelector(${j(scope)});return [...(r?.querySelectorAll('p,span,div,strong,code,dd,li,button,pre,label')||[])].some(e=>e.textContent.includes(${j(text)})&&(${visible})(e)&&![...e.children].some(c=>c.textContent.includes(${j(text)})))})()`);
}
async function overflow(label) {
  const result = await evaluate(`(()=>{const bad=[...document.querySelectorAll('body *')].filter(e=>(${visible})(e)&&e.getBoundingClientRect().width>0).filter(e=>{const r=e.getBoundingClientRect();return r.left<-.75||r.right>innerWidth+.75}).map(e=>({tag:e.tagName,id:e.id,class:String(e.className),left:e.getBoundingClientRect().left,right:e.getBoundingClientRect().right,width:e.getBoundingClientRect().width}));return{viewport:innerWidth,body:document.body.scrollWidth,document:document.documentElement.scrollWidth,bad:bad.slice(0,15)}})()`);
  record(result.body <= result.viewport + 1 && result.document <= result.viewport + 1 && result.bad.length === 0, `${label}: no horizontal viewport overflow`, result);
}
async function auditDisclosures(scope, label, detailsSelector = '.help-details', minimum = 1) {
  const selector = `${scope} ${detailsSelector}`;
  const count = await evaluate(`document.querySelectorAll(${j(selector)}).length`);
  record(count >= minimum, `${label}: expected disclosure count`, { count, minimum });
  for (let index = 0; index < count; index++) {
    const failuresBefore = report.failures.length;
    const expression = `document.querySelectorAll(${j(selector)})[${index}]`;
    const initial = await evaluate(`(()=>{const d=${expression};return{label:d.querySelector(':scope > summary')?.textContent.trim(),open:d.open,body:d.querySelector(':scope > .help-details-body')?.textContent.trim()||[...d.children].filter(c=>c.tagName!=='SUMMARY').map(c=>c.textContent).join(' ').trim()}})()`);
    record(!initial.open, `${label}/${initial.label}: default closed`);
    record(Boolean(initial.label && initial.body), `${label}/${initial.label}: summary and recoverable content exist`);
    // Temporarily open enclosing native low-frequency sections, never set .open or React state.
    const ancestors = await evaluate(`(()=>{const a=[];for(let p=(${expression}).parentElement;p;p=p.parentElement)if(p.tagName==='DETAILS'&&!p.open)a.push([...document.querySelectorAll('details')].indexOf(p));return a.reverse()})()`);
    for (const ancestor of ancestors) {
      await focus(`document.querySelectorAll('details')[${ancestor}].querySelector(':scope > summary')`);
      await key('Enter'); await settle();
    }
    const summary = `(${expression}).querySelector(':scope > summary')`;
    await focus(summary);
    // CDP modifier bits: Alt=1, Ctrl=2, Meta=4, Shift=8.
    await key('Tab', 8); await key('Tab');
    const focusEvidence = await evaluate(`({expected:(${summary}).textContent.trim(),actual:document.activeElement?.outerHTML?.slice(0,600)})`);
    assert.equal(record(await evaluatePredicate(`document.activeElement===${summary}`), `${label}/${initial.label}: reachable through native Tab order`, focusEvidence), true, 'Stop before sending activation keys to the wrong control');
    if (await evaluate(`(${expression}).open`)) { await key('Enter'); await settle(); }
    await check(`![...(${expression}).children].filter(c=>c.tagName!=='SUMMARY').some(c=>(${visible})(c))`, `${label}/${initial.label}: closed content hidden`);
    await key('Enter'); await settle();
    await check(`(${expression}).open&&[...(${expression}).children].filter(c=>c.tagName!=='SUMMARY').some(c=>(${visible})(c))`, `${label}/${initial.label}: Enter expands content`);
    await overflow(`${label}/${initial.label} expanded`);
    await key('Space'); await settle();
    await check(`!(${expression}).open&&document.activeElement===${summary}`, `${label}/${initial.label}: Space closes and retains focus`);
    await key('Space'); await settle();
    await check(`(${expression}).open&&(${expression}).textContent.includes(${j(initial.body.slice(0, 60))})`, `${label}/${initial.label}: Space reopens original content`);
    await key('Enter'); await settle();
    await key('Tab');
    await check(`!(${expression}).open&&!(${expression}).querySelector('.help-details-body')?.contains(document.activeElement)`, `${label}/${initial.label}: Enter closes; hidden descendants excluded from Tab`);
    for (const ancestor of ancestors.reverse()) {
      await focus(`document.querySelectorAll('details')[${ancestor}].querySelector(':scope > summary')`); await key('Enter'); await settle();
    }
    report.disclosures.push({ case: currentCase, scope: label, label: initial.label, default_closed: !initial.open, native_keyboard_tested: true, all_assertions_passed: report.failures.length === failuresBefore });
  }
}
async function accessibility(scope, label) {
  await check(`document.querySelectorAll('label details').length===0`, `${label}: no details nested in a label`);
  const selector = `${scope} :is(button,input:not([type=hidden]),textarea,select,a[href],summary)`;
  const controls = await evaluate(`[...document.querySelectorAll(${j(selector)})].map(e=>({visible:(${visible})(e),tag:e.tagName,id:e.id,describedBy:e.getAttribute('aria-describedby'),descriptionText:(e.getAttribute('aria-describedby')||'').split(/\\s+/).filter(Boolean).map(id=>document.getElementById(id)?.textContent?.trim()||'').join(' ').trim(),missingDescriptionID:(e.getAttribute('aria-describedby')||'').split(/\\s+/).filter(Boolean).some(id=>!document.getElementById(id))}))`);
  const documentTree = await command('DOM.getDocument', { depth: -1 });
  const nodeMap = new Map();
  const visit = (node) => { nodeMap.set(node.nodeId, node); for (const child of node.children || []) visit(child); };
  visit(documentTree.root);
  const { nodeIds } = await command('DOM.querySelectorAll', { nodeId: documentTree.root.nodeId, selector });
  const { nodes } = await command('Accessibility.getFullAXTree');
  const axMap = new Map(nodes.filter((node) => node.backendDOMNodeId).map((node) => [node.backendDOMNodeId, node]));
  assert.equal(nodeIds.length, controls.length, 'DOM and runtime control order must match');
  const failures = [];
  for (let index = 0; index < controls.length; index++) {
    const control = controls[index];
    if (!control.visible) continue;
    let dom = nodeMap.get(nodeIds[index]);
    if (!dom) dom = (await command('DOM.describeNode', { nodeId: nodeIds[index] })).node;
    const ax = axMap.get(dom.backendNodeId);
    if (!ax || ax.ignored || !ax.name?.value?.trim()) failures.push({ ...control, issue: 'Missing computed accessible name', ax });
    if (control.describedBy && (control.missingDescriptionID || !control.descriptionText || !ax?.description?.value?.trim())) failures.push({ ...control, issue: 'Missing referenced/computed description', computed: ax?.description?.value });
  }
  report.accessibility.push({ case: currentCase, label, visible_controls: controls.filter((item) => item.visible).length, failures });
  record(failures.length === 0, `${label}: computed accessible names and referenced descriptions preserved`, failures);
}
async function consents(scope, selectors, label) {
  for (const selector of selectors) {
    const result = await evaluate(`(()=>{const e=document.querySelector(${j(`${scope} ${selector}`)});return{exists:!!e,visible:(${visible})(e),unchecked:e?.checked===false,closedAncestor:!!e?.closest('details:not([open])'),label:e?.closest('label')?.textContent.trim(),buttonsNearby:!!e?.closest('form,fieldset,section')?.querySelector('button')}})()`);
    record(result.exists && result.visible && result.unchecked && !result.closedAncestor && !!result.label && result.buttonsNearby, `${label}/${selector}: separate consent remains visible, labeled, unchecked and beside actions`, result);
  }
}
function holdNextGET(pathname) {
  assert.ok(!heldGETs.has(pathname), 'Only one controlled gate per GET path');
  let release;
  const promise = new Promise((resolve) => { release = resolve; });
  const gate = { promise, release, paused: false, settled: false, abortedByBrowser: false };
  heldGETs.set(pathname, gate);
  return gate;
}
async function intercept(event) {
  const { request } = event;
  const method = request.method.toUpperCase();
  const url = new URL(request.url);
  if (event.networkId) interceptedNetworkIDs.add(event.networkId);
  let gate;
  try {
    let status = 200, mime = 'application/json; charset=utf-8', body;
    const headers = Object.fromEntries(Object.entries(request.headers).map(([name, value]) => [name.toLowerCase(), value]));
    const cors = [{ name: 'Access-Control-Allow-Origin', value: frontend }, { name: 'Access-Control-Allow-Methods', value: 'GET, OPTIONS' }, { name: 'Access-Control-Allow-Headers', value: 'Authorization, Content-Type, Accept' }];
    if (url.origin === frontend && ['GET', 'HEAD'].includes(method)) {
      const file = url.pathname === '/' ? '/index.html' : decodeURIComponent(url.pathname);
      body = frozenDist.get(file);
      if (!body && file === '/favicon.ico') { status = 204; body = Buffer.alloc(0); }
      assert.ok(body, `Unexpected static path: ${url.pathname}`);
      const extension = path.extname(file);
      mime = ({ '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2', '.ico': 'image/x-icon' })[extension] || 'application/octet-stream';
      report.staticRequests.push({ method, path: url.pathname, sha256: sha(body) });
    } else if (url.origin === service && method === 'OPTIONS') {
      assert.equal(headers['access-control-request-method'], 'GET', 'Only read-only GET transport preflights allowed');
      status = 204; body = Buffer.alloc(0);
      report.api.push({ method, path: url.pathname, status });
    } else if (url.origin === service && method === 'GET') {
      assert.equal(headers.authorization, `Bearer ${syntheticToken}`, 'Only the synthetic credential is permitted');
      assert.equal(request.postData, undefined, 'GET requests must not carry business bodies');
      const fixture = apiFixture(url, method);
      status = fixture.__status || 200;
      body = Buffer.from(j(fixture.__status ? { error: fixture.error } : fixture));
      report.api.push({ method, path: url.pathname, status, case: currentCase });
      gate = heldGETs.get(url.pathname);
      if (gate && !gate.paused) { gate.paused = true; gate.networkId = event.networkId; await gate.promise; }
    } else {
      report.unexpectedRequests.push({ method, url: request.url });
      if (url.origin === service && !['GET', 'OPTIONS'].includes(method)) report.business_write_requests++;
      throw Error(`Request forbidden; never forwarded: ${method} ${url.origin}${url.pathname}`);
    }
    try {
      await command('Fetch.fulfillRequest', {
        requestId: event.requestId, responseCode: status,
        responseHeaders: [...cors, { name: 'Content-Type', value: mime }, { name: 'Cache-Control', value: 'no-store' }, { name: 'Content-Security-Policy', value: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src ${service}; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'; worker-src 'none'` }],
        body: (method === 'HEAD' ? Buffer.alloc(0) : body).toString('base64'),
      });
      if (gate) gate.settled = true;
    } catch (error) {
      if (gate?.expectedAbort && gate.abortedByBrowser && /Invalid InterceptionId|Invalid interceptionId|Invalid requestId|No resource with given identifier/.test(String(error))) { gate.settled = true; gate.fulfill_after_abort_rejected = true; }
      else throw error;
    }
  } catch (error) {
    report.interceptionErrors.push(String(error));
    await command('Fetch.failRequest', { requestId: event.requestId, errorReason: 'BlockedByClient' }).catch(() => {});
  }
}

async function settingsBatch(width) {
  const milky = 'section[aria-labelledby="milky-title"]';
  const digest = 'section[aria-labelledby="digest-title"]';
  await scenario(`settings-${width}`, async () => {
    await navigate('设置');
    await until(`document.querySelector('#ai-model-name')?.value===${j(modelName)}`, 'synthetic model config');
    await until(`document.querySelector(${j(digest)})?.textContent.includes('合成每日摘要内容（succeeded）')`, 'latest digest');
    record(await visibleText('合成每日摘要内容（succeeded）', digest), 'Digest business summary remains visible');
    record(await visibleText('合成提醒：请人工核对交付时间。', digest), 'Digest reminder remains visible');
    await consents('#main-content', ['.ai-model-consent input', `${digest} > label.assistant-check input`, `${digest} .digest-manual > label.assistant-check input`], 'Model/Digest consent');
    await auditDisclosures('#main-content', 'settings');
    await accessibility('#main-content', 'settings');
    await overflow('settings');
    await evaluate('document.querySelector("#ai-model-title").scrollIntoView({block:"start"})');
    await screenshot(`settings-${width}.png`);
  });
  await scenario(`milky-get-busy-success-error-${width}`, async () => {
    const gate = holdNextGET('/api/v1/connections/qq-milky/verify');
    await clickText('核验账号与版本', milky);
    await until(() => gate.paused, 'held Milky GET verification');
    record(await visibleText('正在核验…', milky), 'Milky in-progress state stays visible');
    gate.release();
    await until(`document.querySelector(${j(milky)})?.querySelector('.success-message')`, 'Milky successful operation record');
    await check(`(()=>{const p=document.querySelector(${j(milky)})?.querySelector('.success-message');return !!p&&!!p.closest('details:not([open])')&&!(${visible})(p)})()`, 'Milky successful detail is initially folded');
    await auditDisclosures(milky, 'Milky-success', '.help-details:has(.success-message)');
    state.milkyVerify = 'error';
    await clickText('核验账号与版本', milky);
    await until(`(${visible})(document.querySelector(${j(milky)})?.querySelector('[role=alert]'))`, 'Milky visible GET error');
    await check(`!document.querySelector(${j(milky)})?.querySelector('[role=alert]')?.closest('details:not([open])')`, 'Milky error is outside folded guidance');
    state.milkyVerify = 'ok';
  });
  for (const digestState of ['running', 'unknown', 'failed']) await scenario(`digest-${digestState}-${width}`, async () => {
    state.digest = digestState;
    await click(`${digest} [aria-label="读取实际计划与最近摘要"]`);
    await until(`document.querySelector(${j(digest)})?.textContent.includes(${j(`合成每日摘要内容（${digestState}）`)})`, 'updated digest GET state');
    record(await visibleText(`合成每日摘要内容（${digestState}）`, digest), 'Non-success digest body stays visible');
    if (digestState !== 'failed') record(await visibleText('存在运行中、未知或中断结果', digest), 'Unresolved status and no-replay guidance stay visible');
    else record(await visibleText('AI_TIMEOUT', digest), 'Failure code stays visible');
    await overflow(`digest-${digestState}`);
  });
  state.digest = 'succeeded';
}
async function sourcePage() {
  await navigate('设置'); await navigate('消息');
  await until('document.querySelector(".inbox-conversation")&&!document.querySelector(".inbox-loading")', 'synthetic inbox');
  await until(`document.querySelector('.wechat-panel .email-account')||document.querySelector('.wechat-panel [role=alert]')`, 'source GET state');
  await until(`document.querySelector('.email-panel:not(.wechat-panel) .email-state-label')?.textContent&&!document.querySelector('.email-panel:not(.wechat-panel) .email-state-label').textContent.includes('正在读取')`, 'email GET settled');
  await settle();
}
async function sourcesBatch(width) {
  await scenario(`sources-ready-and-dirty-${width}`, async () => {
    state.sources = 'ready'; await sourcePage();
    await check('document.querySelector(".inbox-sources")?.open===false', 'Source section defaults closed');
    await toggleDetails('.inbox-sources', true);
    await check('!document.querySelector("#wechat-action-impact")', 'No-selection egress explanation is not repeated');
    await check(`!(${visible})(document.querySelector('.wechat-panel .help-details-body'))`, 'Static WeChat guidance defaults hidden');
    await check(`[...document.querySelectorAll('.email-panel:not(.wechat-panel) .help-details')].some(d=>d.querySelector('summary')?.textContent==='邮箱同步记录'&&!d.open)`, 'Email successful history defaults folded');
    await auditDisclosures('.inbox-sources', 'sources');
    await accessibility('.inbox-sources', 'sources-before-selection');
    await clickText('读取会话列表', '.wechat-panel');
    await until('document.querySelector("#wechat-peer")?.options.length>1', 'synthetic session GET options');
    await focus('document.querySelector("#wechat-peer")');
    await key('Home'); await key('ArrowDown'); await key('Tab'); await settle();
    await until(`(${visible})(document.querySelector('#wechat-action-impact'))`, 'selected-session egress impact');
    record(await visibleText('可能将入库内容发给所配置模型并产生费用', '.wechat-panel'), 'Selected-session model/cost impact is visible');
    await clickText('加入后台读取', '.wechat-panel');
    record(await visibleText('选择或频率已修改，保存后才会生效。', '.wechat-panel'), 'Unsaved monitor draft remains visible');
    await check(`(${visible})(document.querySelector('#wechat-monitor-impact'))`, 'Monitor draft authorization/cost impact is visible');
    await check('document.querySelector(".inbox-sources > summary")?.textContent.includes("微信需检查")===true', 'Dirty draft advertises attention on source summary');
    await accessibility('.inbox-sources', 'sources-selected-and-dirty');
    await overflow('sources-selected-and-dirty');
    await screenshot(`sources-selected-${width}.png`);
    await toggleDetails('.inbox-sources', false);
    await check(`(${visible})(document.querySelector('.inbox-sources > summary'))&&document.querySelector('.inbox-sources > summary').textContent.includes('微信需检查')`, 'Dirty attention remains outside closed source section');
  });
  await scenario(`wechat-stop-get-and-late-response-${width}`, async () => {
    state.sources = 'ready'; await sourcePage(); await toggleDetails('.inbox-sources', true);
    const gate = holdNextGET('/api/v1/connections/wechat-db/sessions');
    await clickText('读取会话列表', '.wechat-panel');
    await until(() => gate.paused, 'held session GET');
    record(await visibleText('正在读取会话', '.wechat-panel'), 'Session GET busy state remains visible');
    gate.expectedAbort = true;
    await clickText('停止等待', '.wechat-panel');
    record(await visibleText('本次处理结果尚未确认，请先更新状态。工作台不会自动重试。', '.wechat-panel'), 'Local uncertain is visible after stopping only a GET');
    record(await visibleText('已停止等待，后台可能仍在读取。', '.wechat-panel'), 'Typed stop-wait warning is visible');
    await click('.wechat-panel [aria-label="更新状态"]');
    await until(`!([...document.querySelectorAll('.wechat-panel p')].some(e=>e.textContent.includes('本次处理结果尚未确认')&&(${visible})(e)))`, 'GET refresh clears uncertain');
    record(await visibleText('已停止等待，后台可能仍在读取。', '.wechat-panel'), 'GET refresh preserves typed warning');
    gate.release();
    await until(() => gate.settled, 'late held GET fulfillment/abort after status refresh');
    await settle();
    await check('document.querySelector("#wechat-peer")?.options.length===1', 'Late canceled GET does not repopulate cleared sessions');
    record(await visibleText('已停止等待，后台可能仍在读取。', '.wechat-panel'), 'Late GET does not overwrite refreshed warning state');
    await toggleDetails('.inbox-sources', false);
    await check('document.querySelector(".inbox-sources > summary")?.textContent.includes("微信需检查")===true', 'Preserved warning advertises attention when source closed');
  });
  for (const sourceState of ['limited', 'skipped', 'monitor-error', 'error', 'unconfigured']) await scenario(`sources-${sourceState}-${width}`, async () => {
    state.sources = sourceState; await sourcePage();
    if (sourceState === 'limited') await check('document.querySelector(".inbox-sources > summary")?.textContent.includes("微信需检查")&&document.querySelector(".inbox-sources > summary")?.textContent.includes("邮箱需检查")', 'Limited source attention visible outside closed section');
    if (sourceState === 'monitor-error' || sourceState === 'error') await check('document.querySelector(".inbox-sources > summary")?.textContent.includes("微信需检查")===true', 'Source errors advertise attention before expansion');
    if (sourceState === 'skipped') await check('document.querySelector(".inbox-sources")?.open===false&&document.querySelector(".inbox-sources > summary")?.textContent.includes("邮箱需检查")===true', 'Skipped email advertises attention outside the closed source section');
    await toggleDetails('.inbox-sources', true);
    if (sourceState === 'limited') {
      record(await visibleText('本次已达到读取窗口上限', '.wechat-panel'), 'WeChat limited warning stays visible');
      record(await visibleText('本次仅处理最近一批邮件，未完整收取历史。', '.email-panel:not(.wechat-panel)'), 'Email limited warning stays visible');
    } else if (sourceState === 'skipped') {
      record(await visibleText('上次同步跳过 1 封邮件，结果不完整。', '.email-panel:not(.wechat-panel)'), 'Skipped email incomplete-result warning stays visible');
      await check(`(()=>{const e=document.querySelector('.email-panel:not(.wechat-panel) .email-last-result');return!!e&&e.textContent.includes('上次保存 2 封')&&!(${visible})(e)&&!!e.closest('details:not([open])')})()`, 'Skipped email keeps successful receipt counts folded');
      await auditDisclosures('.email-panel:not(.wechat-panel)', 'email-skipped-history');
      record(await visibleText('上次同步跳过 1 封邮件，结果不完整。', '.email-panel:not(.wechat-panel)'), 'Incomplete-result warning remains outside the reclosed success helper');
      await evaluate('document.querySelector(".email-panel:not(.wechat-panel)").scrollIntoView({block:"start"})');
      await screenshot(`sources-skipped-${width}.png`);
    } else if (sourceState === 'error') {
      await check(`(${visible})(document.querySelector('.wechat-panel [role=alert]'))&&(${visible})(document.querySelector('.email-panel:not(.wechat-panel) [role=alert]'))`, 'Both source GET errors are direct visible alerts');
    } else if (sourceState === 'monitor-error') {
      await check(`[...document.querySelectorAll('.wechat-monitor .is-error')].some(e=>(${visible})(e)&&!e.closest('details:not([open])'))`, 'WeChat monitor error stays outside helper folds');
    } else {
      const helper = `.email-panel:not(.wechat-panel) .help-details:has(> summary)`;
      await check(`[...document.querySelectorAll(${j(helper)})].some(d=>d.querySelector('summary').textContent==='邮箱同步说明'&&!d.open&&d.textContent.includes('在本机配置账号与授权码后重启服务'))`, 'Email setup guidance is available in a default-closed helper');
      await auditDisclosures('.email-panel:not(.wechat-panel)', 'email-configuration');
    }
    await overflow(`sources-${sourceState}`);
    if (sourceState === 'limited') await screenshot(`sources-limited-${width}.png`);
  });
  state.sources = 'ready';
}
async function inboxPanelsBatch(width) {
  await scenario(`inbox-ai-profile-import-${width}`, async () => {
    await sourcePage();
    await clickText('导入消息', '.inbox-root');
    await until('document.querySelector(".inbox-import")', 'local import controls');
    await auditDisclosures('.inbox-import', 'import-guidance');
    await accessibility('.inbox-import', 'import');
    await overflow('import');
    await click('[aria-label="关闭导入"]');
    await click('.inbox-conversation');
    await until('document.querySelector(".inbox-history")', 'conversation history');
    await auditDisclosures('.inbox-detail', 'conversation-guidance');
    await clickText('AI 管理', '.inbox-conversation-actions');
    await until(`document.querySelector('.ai-panel')?.textContent.includes(${j(modelName)})`, 'AI status');
    await auditDisclosures('.ai-panel', 'AI-guidance');
    await clickText('预览本次分析范围', '.ai-panel');
    await until('document.querySelector(".ai-preview")', 'read-only AI context preview');
    await consents('.ai-panel', ['.ai-consent input'], 'AI per-call consent');
    await accessibility('.ai-panel', 'AI-preview');
    await overflow('AI-preview');
    await screenshot(`ai-preview-${width}.png`);
    await click('.inbox-conversation-actions [aria-label="个人档案"]');
    await until('document.querySelector(".profile-panel .profile-consent input")', 'synthetic profile');
    await auditDisclosures('.profile-panel', 'profile-guidance');
    await consents('.profile-panel', ['.profile-consent input'], 'Profile per-call consent');
    await accessibility('.profile-panel', 'profile');
    await overflow('profile');
    await screenshot(`profile-${width}.png`);
  });
}
async function feishuBatch(width) {
  // Delivery-unknown is last so its retained read-only recovery cannot bleed into another synthetic case.
  for (const feishuState of ['ready', 'sent', 'running', 'failed', 'report-unknown', 'unknown']) await scenario(`feishu-${feishuState}-${width}`, async () => {
    state.feishu = feishuState;
    await navigate('设置'); await navigate('飞书早晚报');
    await until('document.querySelector(".feishu-report")', 'synthetic frozen Feishu report');
    if (feishuState === 'ready') {
      await check(`(${visible})(document.querySelector('.feishu-frozen-text'))&&document.querySelector('.feishu-frozen-text').textContent.includes('合成冻结正文')&&!document.querySelector('.feishu-frozen-text').closest('details:not([open])')`, 'Pending-send complete frozen business text remains visible');
      await consents('.feishu-page', ['#feishu-schedule-model-consent', '#feishu-schedule-send-consent', '#feishu-preview-consent', '#feishu-send-consent'], 'Feishu independent model/send consents');
      await auditDisclosures('.feishu-page', 'Feishu-guidance');
      await accessibility('.feishu-page', 'Feishu-ready');
      await screenshot(`feishu-ready-${width}.png`);
    } else if (feishuState === 'sent') {
      record(await visibleText('飞书平台已接受', '.feishu-receipt'), 'Successful receipt keeps platform acceptance visible');
      await check(`[...document.querySelectorAll('.feishu-receipt .help-details')].some(d=>!d.open&&d.querySelector('summary')?.textContent==='投递回执明细')`, 'Successful receipt metadata defaults folded');
      await auditDisclosures('.feishu-receipt', 'Feishu-success-receipt');
    } else if (feishuState === 'unknown') {
      record(await visibleText('人工核实，不要再次发送', '.feishu-receipt'), 'Unknown delivery explicitly forbids blind resend');
      await check(`[...document.querySelectorAll('.feishu-receipt .feishu-facts')].some(e=>(${visible})(e)&&!e.closest('details:not([open])'))`, 'Unknown delivery facts are directly visible');
      await screenshot(`feishu-unknown-${width}.png`);
    } else {
      const critical = feishuState === 'running' ? '后台仍在生成，请重新读取此报告' : feishuState === 'failed' ? '本窗口生成失败，不重新生成或发送' : '生成结果未知，可能已经产生模型费用';
      record(await visibleText(critical, '.feishu-report'), 'Non-success generation status and safe-next-action remain visible');
    }
    await overflow(`feishu-${feishuState}`);
  });
  state.feishu = 'ready';
}
async function assistantBatch(width) {
  await scenario(`assistant-${width}`, async () => {
    await click('.sidebar [aria-label="Agent 助手"]');
    await until('document.querySelector("#assistant-workspace")?.open&&document.querySelector("#assistant-message")&&!document.querySelector("#assistant-message").disabled', 'native assistant modal');
    await check('document.querySelector(".assistant-more")?.open===false', 'Assistant low-frequency details default closed');
    await auditDisclosures('#assistant-workspace', 'assistant-native-more', '.assistant-more');
    await input('#assistant-message', '仅为界面检查准备的合成草稿；不提交，不调用模型。');
    await consents('#assistant-workspace', ['.assistant-round-consent input'], 'Assistant independent per-round consent');
    record(await visibleText('可能产生费用', '.assistant-round-consent'), 'Assistant data/cost disclosure remains beside submit');
    await accessibility('#assistant-workspace', 'assistant');
    await focus('document.querySelector("#assistant-message")');
    for (let index = 0; index < 18; index++) {
      await key('Tab');
      await check(`(()=>{const d=document.querySelector('#assistant-workspace'),e=document.activeElement;return d.contains(e)||e===document.body||e===document.documentElement})()`, 'Native modal prevents focus escaping to underlying controls');
    }
    await overflow('assistant');
    await screenshot(`assistant-${width}.png`);
    await key('Escape'); await settle();
    await check('document.querySelector("#assistant-workspace")?.open===false', 'Escape closes assistant without submission');
    await click('.sidebar [aria-label="Agent 助手"]');
    await until('document.querySelector("#assistant-workspace")?.open', 'assistant reopen');
    await check('document.querySelector("#assistant-message")?.value.includes("合成草稿")===true', 'Reopening assistant restores unsubmitted draft');
    await consents('#assistant-workspace', ['.assistant-round-consent input'], 'Assistant consent stays unchecked after reopen');
    await key('Escape'); await settle();
  });
}

try {
  await fs.mkdir(profile, { recursive: true });
  child = spawn(browser, [
    '--headless=new', '--window-size=1440,1000', '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
    '--disable-sync', '--disable-component-update', '--disable-default-apps', '--disable-extensions',
    '--disable-features=MediaRouter,OptimizationHints,AutofillServerCommunication', '--metrics-recording-only',
    '--no-pings', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank',
  ], { stdio: 'ignore' });
  let launchError;
  child.on('error', (error) => { launchError = error; });
  let portInfo;
  await until(async () => {
    if (launchError) throw launchError;
    try { portInfo = (await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).trim().split(/\r?\n/); return portInfo.length >= 2; } catch { return false; }
  }, 'isolated Chromium CDP port');
  ws = new WebSocket(`ws://127.0.0.1:${portInfo[0]}${portInfo[1]}`);
  await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }); });
  ws.addEventListener('message', ({ data }) => {
    const message = JSON.parse(String(data));
    if (message.id) {
      const item = pending.get(message.id); if (!item) return;
      trace.push({ t: Date.now() - startedAt, direction: 'receive', id: message.id, method: item.method, error: message.error, result_keys: Object.keys(message.result || {}) });
      clearTimeout(item.timeout); pending.delete(message.id);
      message.error ? item.reject(Error(j(message.error))) : item.resolve(message.result);
      return;
    }
    trace.push({ t: Date.now() - startedAt, direction: 'event', method: message.method, params: traceSafe(message.params || {}) });
    if (message.method === 'Fetch.requestPaused') void intercept(message.params);
    if (message.method === 'Network.requestWillBeSent') report.networkRequests.push({ id: message.params.requestId, method: message.params.request.method, url: message.params.request.url });
    if (message.method === 'Network.loadingFailed') for (const gate of heldGETs.values()) if (gate.networkId === message.params.requestId && message.params.canceled) gate.abortedByBrowser = true;
    if (message.method === 'Runtime.exceptionThrown') report.consoleErrors.push(message.params.exceptionDetails);
    if (message.method === 'Runtime.consoleAPICalled' && message.params.type === 'error') report.consoleErrors.push(message.params.args.map((arg) => arg.value || arg.description));
    if (message.method === 'Target.attachedToTarget' && message.params.targetInfo.targetId !== ownedTargetId) report.unexpectedTargets.push(message.params.targetInfo);
  });
  const { targetInfos } = await command('Target.getTargets', {}, null);
  const page = targetInfos.find((target) => target.type === 'page'); assert.ok(page);
  ownedTargetId = page.targetId;
  session = (await command('Target.attachToTarget', { targetId: page.targetId, flatten: true }, null)).sessionId;
  await command('Page.enable'); await command('Runtime.enable'); await command('DOM.enable'); await command('Accessibility.enable'); await command('Network.enable');
  await command('Network.setCacheDisabled', { cacheDisabled: true });
  await command('Network.setBypassServiceWorker', { bypass: true });
  await command('Network.setBlockedURLs', { urls: ['ws://*', 'wss://*'] });
  await command('Fetch.enable', { patterns: [{ urlPattern: '*' }] });
  await command('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await command('Browser.setDownloadBehavior', { behavior: 'deny' }, null);
  await command('Page.addScriptToEvaluateOnNewDocument', { source: `(()=>{for(const name of ['WebSocket','EventSource','Worker','SharedWorker','RTCPeerConnection','webkitRTCPeerConnection'])Object.defineProperty(window,name,{configurable:false,value:class DisabledInSyntheticPass{constructor(){throw Error(name+' forbidden in isolated synthetic pass')}}});Object.defineProperty(window,'open',{configurable:false,value:()=>{throw Error('Popups forbidden')}});Object.defineProperty(navigator,'clipboard',{configurable:false,value:Object.freeze({read:()=>Promise.reject(Error('Clipboard forbidden')),readText:()=>Promise.reject(Error('Clipboard forbidden')),write:()=>Promise.reject(Error('Clipboard forbidden')),writeText:()=>Promise.reject(Error('Clipboard forbidden'))})});if(navigator.serviceWorker)Object.defineProperty(navigator.serviceWorker,'register',{configurable:false,value:()=>Promise.reject(Error('Service workers forbidden'))});})()` });
  for (const [width, height] of [[1440, 1000], [390, 844]]) {
    Object.assign(state, { digest: 'succeeded', feishu: 'ready', sources: 'ready', milkyVerify: 'ok', holdMilkyVerify: false });
    touchInput = width < 500;
    expectedViewport = { width, height, touch: touchInput };
    await command('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    await command('Emulation.setTouchEmulationEnabled', { enabled: touchInput });
    const connection = { width, height, initial_remember_checked: null, native_unchecked: false, hard_asserted_unchecked: false, connected: false };
    report.connection_safety.push(connection);
    const connectionCase = `connect-synthetic-only-${width}`;
    const prerequisitePassed = await scenario(connectionCase, async () => {
      const viewportQuery = `?synthetic_viewport=${width}`;
      await command('Page.navigate', { url: `${frontend}/${viewportQuery}` });
      await until(`location.search===${j(viewportQuery)}&&document.querySelector('.sidebar [aria-label=设置]')`, 'fresh frozen frontend document');
      await navigate('设置');
      await until('document.querySelector("#service-token")', 'connection form');
      const rememberInitiallyChecked = await evaluate('document.querySelector(".remember-device-choice input")?.checked');
      connection.initial_remember_checked = rememberInitiallyChecked;
      assert.equal(typeof rememberInitiallyChecked, 'boolean', 'Remember-device control must exist');
      if (rememberInitiallyChecked) {
        await check(`(${visible})(document.querySelector('#remember-impact'))&&document.querySelector('#remember-impact').textContent.includes('请勿用于共享电脑')`, 'Checked remember-device keeps shared-computer impact visible');
        await accessibility('#main-content', 'connection-remember-checked');
        await click('.remember-device-choice input');
        connection.native_unchecked = true;
      }
      // Hard safety prerequisite: not merely a recorded assertion. Never attempt enrollment.
      assert.equal(await evaluate('document.querySelector(".remember-device-choice input")?.checked'), false, 'Abort before connecting unless remembering is disabled');
      connection.hard_asserted_unchecked = true;
      passed('Remember-device hard-asserted unchecked before connection');
      await check('!document.querySelector("#remember-impact")&&document.querySelector(".remember-device-choice input").getAttribute("aria-describedby")==="remember-description"', 'Unchecking removes immediate remember impact and its ARIA reference');
      await auditDisclosures('#main-content', 'pre-connection-guidance');
      await input('#service-token', syntheticToken);
      await clickText('连接服务');
      await until('document.querySelector(".service-status")?.textContent==="服务可访问"', 'critical connected state, not folded success text');
      connection.connected = true;
      await check('document.querySelector(".remember-device-choice input")?.checked===false', 'Synthetic credential was not remembered');
      await screenshot(`connection-${width}.png`);
    });
    if (!prerequisitePassed) {
      skipDependents(width, connectionCase);
      continue;
    }
    await scenario(`overview-${width}`, async () => { await navigate('概览'); await auditDisclosures('#main-content', 'overview', '.help-details', 0); await accessibility('#main-content', 'overview'); await overflow('overview'); });
    await settingsBatch(width);
    await sourcesBatch(width);
    await inboxPanelsBatch(width);
    await feishuBatch(width);
    await assistantBatch(width);
  }
} catch (error) {
  report.result = 'failed'; report.error = error.stack || String(error); process.exitCode = 1;
} finally {
  for (const gate of heldGETs.values()) gate.release();
  closing = true;
  if (ws?.readyState === WebSocket.OPEN) { try { await command('Browser.close', {}, null); } catch {} ws.close(); }
  if (child && child.exitCode === null) {
    await Promise.race([new Promise((resolve) => child.once('exit', resolve)), delay(2000)]);
    if (child.exitCode === null) child.kill();
  }
  for (const item of pending.values()) { clearTimeout(item.timeout); item.reject(Error('Isolated browser closed')); }
  pending.clear();
  await verifyFinalIntegrity();
  finalizeCoverage();
  report.result = report.failures.length || report.error || report.skipped.length ? 'failed' : 'passed';
  if (report.result === 'failed') process.exitCode = 1;
  report.elapsed_ms = Date.now() - startedAt;
  report.evidence = { manifest: 'manifest.json', protocol_trace: 'cdp-trace.jsonl', report: 'report.json', summary: 'summary.md', screenshots: report.screenshots };
  await fs.writeFile(path.join(artifacts, 'manifest.json'), `${j({ approved_dist: approvedDist, harness: harnessManifest, sources: sourceManifest, dist: distManifest }, null, 2)}\n`);
  await fs.writeFile(path.join(artifacts, 'cdp-trace.jsonl'), `${trace.map((event) => j(event)).join('\n')}\n`);
  await fs.writeFile(path.join(artifacts, 'report.json'), `${j(report, null, 2)}\n`);
  const summary = [
    '# Synthetic helper-disclosure browser batch', '',
    `- Result: **${report.result}**; ${report.checks.length} passed assertions; ${report.failures.length} findings.`,
    `- Planned viewports: desktop 1440×1000 and touch-enabled 390×844; connected: ${report.connection_safety.filter(item => item.connected).map(item => `${item.width}×${item.height}`).join(', ') || 'none'}.`,
    `- Cases: ${report.coverage.requested_cases} planned; ${report.coverage.executed_cases} executed (${report.coverage.passed_cases} passed, ${report.coverage.failed_cases} failed, ${report.coverage.interrupted_cases} interrupted); ${report.coverage.skipped_cases} skipped/not run. Interrupted cases do not imply completion of their remaining checks.`,
    `- Intercepted synthetic business GETs: ${report.api.filter(item => item.method === 'GET').length}; OPTIONS preflights: ${report.api.filter(item => item.method === 'OPTIONS').length}; forbidden business-write attempts: ${report.business_write_requests}.`,
    `- Frontend responses: ${report.staticRequests.length}; observed bytes match frozen dist: ${report.frontend_requests_fulfilled_from_frozen_dist ?? 'not observed'}; every observed HTTP request intercepted: ${report.all_application_http_requests_intercepted ?? 'not checked'}; forwarding commands: ${report.forwarded_requests ?? 'not checked'}.`,
    '- Safety policy allowed only synthetic GET business responses and their OPTIONS preflights. No real platform/model acceptance is claimed. No personal profile, clipboard or production credential was used.',
    ...report.connection_safety.map(item => `- Connection ${item.width}: remember initially checked=${item.initial_remember_checked ?? 'not inspected'}; native uncheck=${item.native_unchecked}; hard-asserted unchecked before connection=${item.hard_asserted_unchecked}; connected=${item.connected}.`),
    `- Final integrity check executed: ${report.integrity_checked}; source/dist/harness fingerprints unchanged: ${report.fingerprints_unchanged ?? 'not fully checked'}. See fingerprint_checks in the report for per-category evidence.`,
    `- Completed native disclosure sequences: ${report.disclosures.length}; all local assertions passed in ${report.disclosures.filter(item => item.all_assertions_passed).length}. Computed AX scopes checked: ${report.accessibility.length} (${report.accessibility.filter(item => !item.failures.length).length} without findings). Only executed checks have evidence; no global keyboard/AX/consent/overflow pass is implied.`,
    '- Actual assertions, consent checks, disclosure/AX records and screenshot geometry are in [report](<report.json>); [trace](<cdp-trace.jsonl>) omits authorization, request bodies and fulfilled asset bodies.',
    '', '## Findings', '',
    ...report.failures.map((finding) => `- ${finding.case}: ${finding.label}${finding.actual ? ` — ${j(finding.actual)}` : ''}${finding.error ? ` — ${finding.error}` : ''}`),
    ...(report.error ? [`- Batch error: ${report.error}`] : []),
    ...(!report.failures.length && !report.error ? ['- None recorded.'] : []),
    '', '## Skipped / not run', '',
    ...report.skipped.map(item => `- ${item.case}: ${item.status}; blocked by ${item.blocked_by}. ${item.reason}`),
    ...(!report.skipped.length ? ['- None.'] : []),
    '', '## Screenshots', '',
    ...report.screenshots.map((filename) => `- [${filename}](<${filename}>)`), '',
  ].join('\n');
  await fs.writeFile(path.join(artifacts, 'summary.md'), summary);
  console.log(`${report.result}: ${artifacts}\n${report.checks.length} checks; ${report.failures.length} findings; ${report.api.length} intercepted GET/OPTIONS API requests; ${report.business_write_requests} business writes; 0 real platform/model calls.`);
  if (report.error) console.error(report.error);
}
