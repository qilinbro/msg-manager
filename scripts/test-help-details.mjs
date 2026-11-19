import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from '../apps/desktop/node_modules/react/index.js';
import { renderToStaticMarkup } from '../apps/desktop/node_modules/react-dom/server.node.js';
import { loadHelpDetails } from './frontend-help-test-loader.mjs';

// Offline render tests of the shipped component. Native browser keyboard,
// computed AX, visibility and responsive acceptance require separate verification.
const jsx = (type, supplied, ...children) => ({
  type,
  props: { ...(supplied ?? {}), children },
});
const HelpDetails = loadHelpDetails(jsx);
const explanation = jsx('p', { id: 'original-help-description' }, '保留的原始说明');
const first = HelpDetails({ label: '读取说明', children: explanation });
assert.equal(first.type, 'details', 'use native disclosure semantics');
assert.equal(first.props.className, 'help-details');
assert.equal(Object.hasOwn(first.props, 'open'), false, 'help must start closed');
assert.equal(Object.hasOwn(first.props, 'hidden'), false, 'do not hide the whole disclosure');
assert.equal(Object.hasOwn(first.props, 'onToggle'), false, 'do not persist opening as a new default');
assert.deepEqual(Object.keys(first.props).sort(), ['children', 'className']);
const [summary, body] = first.props.children;
assert.equal(summary.type, 'summary');
assert.deepEqual(summary.props.children, ['读取说明']);
assert.equal(body.type, 'div');
assert.equal(body.props.className, 'help-details-body');
assert.equal(body.props.children[0], explanation, 'keep original node identity, text and description IDs');
assert.equal(Object.hasOwn(body.props, 'hidden'), false, 'the browser, not CSS deletion, controls visibility');
const second = HelpDetails({ label: '格式说明', className: 'inbox-format-help', children: '原文' });
assert.equal(second.props.className, 'help-details inbox-format-help');
assert.equal(Object.hasOwn(second.props, 'open'), false, 'another instance must also start closed');

// Use React itself to verify real HTML serialization and escaping, not an
// approximation that trusts helper/receipt text as HTML.
const ReactHelpDetails = loadHelpDetails(createElement);
const html = renderToStaticMarkup(createElement(ReactHelpDetails, {
  label: '说明<script>not executable</script>',
}, createElement('p', { id: 'preserved-help-id' }, '原文 <img src=x onerror=bad()>')));
assert.ok(html.startsWith('<details class="help-details"><summary>'));
assert.ok(html.endsWith('</div></details>'));
assert.ok(html.includes('id="preserved-help-id"'), 'retain aria-describedby targets');
assert.ok(html.includes('&lt;script&gt;not executable&lt;/script&gt;'));
assert.ok(html.includes('&lt;img src=x onerror=bad()&gt;'));
assert.ok(!/<(?:script|img)\b/.test(html), 'text must not become active markup');
assert.ok(!/<details[^>]*\bopen(?:\s|=|>)/.test(html));
assert.ok(!/aria-hidden|display:\s*none|visibility:\s*hidden/.test(html));

// Referenced text must not live only in a closed native-details subtree.
// This verifies DOM placement and escaping, not browser-computed AX output.
const descriptionText = '说明 <script>not executable</script>';
const describedHTML = renderToStaticMarkup(createElement(ReactHelpDetails, {
  label: '字段说明',
  descriptions: [{ id: 'safe-help-description', text: descriptionText }],
}, createElement('p', null, descriptionText)));
const disclosureStart = describedHTML.indexOf('<details ');
assert.ok(disclosureStart > 0, 'only described disclosures need a layout wrapper');
const outsideDisclosure = describedHTML.slice(0, disclosureStart);
assert.ok(outsideDisclosure.includes('id="safe-help-description"'));
assert.ok(outsideDisclosure.includes('class="help-accessible-description"'));
assert.ok(outsideDisclosure.includes('说明 &lt;script&gt;not executable&lt;/script&gt;'));
assert.ok(!/\bhidden\b|aria-hidden/.test(outsideDisclosure), 'mirror must remain accessible');
assert.equal(describedHTML.match(/id="safe-help-description"/g)?.length, 1, 'never duplicate description IDs');
assert.ok(!describedHTML.slice(disclosureStart).includes('id="safe-help-description"'));
assert.ok(!/<details[^>]*\bopen(?:\s|=|>)/.test(describedHTML));
const stylesheet = readFileSync(new URL('../apps/desktop/src/styles.css', import.meta.url), 'utf8');
const mirrorRule = stylesheet.match(/\.help-accessible-description\s*\{([^}]+)\}/)?.[1];
assert.ok(mirrorRule);
assert.match(mirrorRule, /position:\s*absolute/);
assert.match(mirrorRule, /clip-path:\s*inset\(50%\)/);
assert.ok(!/display:\s*none|visibility:\s*hidden/.test(mirrorRule));

// Guard the requested project-wide adoption. This inventory complements, not
// replaces, existing synthetic component/consent/error regression suites.
const mirroredIDs = {
  'App.tsx': ['token-description', 'remember-description'],
  'AIModelPanel.tsx': ['ai-model-preset-help'],
  'WechatPanel.tsx': ['wechat-read-help', 'wechat-monitor-help'],
  'FeishuPanel.tsx': ['feishu-credentials-help', 'feishu-app-id-hint', 'feishu-app-secret-hint', 'feishu-open-id-hint'],
};
assert.equal(new Set(Object.values(mirroredIDs).flat()).size, 9);
for (const file of [
  'App.tsx', 'Inbox.tsx', 'WechatPanel.tsx', 'EmailPanel.tsx',
  'MilkyPanel.tsx', 'DigestPanel.tsx', 'AIModelPanel.tsx', 'AIPanel.tsx',
  'FeishuPanel.tsx', 'ProfilePanel.tsx', 'ProfileLinksPanel.tsx',
]) {
  const source = readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8');
  assert.ok(/import \{ HelpDetails \} from '\.\/HelpDetails';/.test(source), `${file}: use the shared disclosure`);
  assert.ok(/<HelpDetails\b/.test(source), `${file}: optional guidance/receipts must be folded`);
  assert.ok(!/<HelpDetails\b[^>]*\bopen\s*(?:=|>)/.test(source), `${file}: do not opt into open-by-default help`);
  for (const id of mirroredIDs[file] ?? []) {
    assert.ok(source.includes(`id: '${id}'`), `${file}: bind ${id} through the outside-description API`);
    assert.ok(!source.includes(`id="${id}"`), `${file}: remove the duplicate ID from the closed body`);
  }
}
// Assistant already has purpose-specific native disclosures: reuse these rather
// than forcing the helper into another nested disclosure just to satisfy inventory.
const assistant = readFileSync(new URL('../apps/desktop/src/AssistantPanel.tsx', import.meta.url), 'utf8');
for (const [className, label] of [
  ['assistant-action-details', '操作详情'],
  ['assistant-help-details', '查看带入内容'],
  ['assistant-more', '更多'],
]) {
  assert.ok(assistant.includes(`<details className="${className}">`), `${className}: keep native closed defaults`);
  assert.ok(assistant.includes(`<summary>${label}</summary>`), `${className}: retain the discoverable label`);
}
assert.ok(assistant.indexOf('id="assistant-help-note"') < assistant.indexOf('<details className="assistant-help-details">'), 'Assistant description mirror precedes its closed disclosure');
assert.ok(assistant.includes('<span id="assistant-help-note" className="help-accessible-description">'));
assert.equal((assistant.match(/id="assistant-help-note"/g) ?? []).length, 1);
console.log('Help disclosure regressions passed: shipped native details/summary, closed independent defaults, intact content, React escaping, 10 outside-description source guards with escaped SSR mirrors, no persistence/global hiding, 11 shared-helper modules and existing closed Assistant disclosures. Offline rendering/source checks only; not browser AX acceptance.');
