import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync } from 'node:fs';
import { loadIcon } from './frontend-icon-test-loader.mjs';

// Offline provenance and actual shared renderer, not a duplicated icon registry.
const at = (path) => new URL(`../${path}`, import.meta.url);
const read = (path) => readFileSync(at(path));
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const manifestBytes = read('third_party/lucide/manifest.json');
const manifest = JSON.parse(manifestBytes);
const ref = '149eba0aea48178a5ab1efe2c701de1da94f4be8';
assert.equal(manifest.project, 'Lucide');
assert.equal(manifest.version, '1.75.0');
assert.equal(manifest.source_ref, ref);
assert.equal(manifest.source_url, `https://github.com/lucide-icons/lucide/tree/${ref}`);
assert.equal(manifest.component, 'apps/desktop/src/Icon.tsx');
assert.equal(manifest.icons.length, 49);
assert.equal(Object.keys(manifest.aliases).length, 50);
assert.equal(new Set(manifest.icons.map((item) => item.name)).size, 49);
assert.deepEqual(readdirSync(at('third_party/lucide/icons')).sort(), manifest.icons.map((item) => `${item.name}.svg`).sort());
const license = read('third_party/lucide/LICENSE');
assert.equal(sha(license), 'b495047bd93a9b06913511076f504daba17d5bbeb3e0650f3bb53a4220329c57');
assert.equal(sha(license), manifest.license_sha256);
assert.ok(license.toString('utf8').includes('ISC License'));
assert.ok(license.toString('utf8').includes('MIT License'));
assert.ok(license.toString('utf8').includes('Cole Bemis'));
assert.deepEqual(read('apps/desktop/public/licenses/lucide-LICENSE.txt'), license);
assert.deepEqual(read('apps/desktop/public/licenses/lucide-provenance.json'), manifestBytes);
const attributes = read('.gitattributes').toString('utf8');
assert.ok(attributes.includes('/third_party/lucide/** -text'));
assert.ok(attributes.includes('/apps/desktop/public/licenses/lucide-* -text'));

const allowedElements = new Set(['path', 'circle', 'rect', 'line', 'polyline', 'polygon', 'ellipse']);
const allowedAttributes = new Set(['d', 'cx', 'cy', 'r', 'x', 'y', 'width', 'height', 'rx', 'ry', 'x1', 'x2', 'y1', 'y2', 'points', 'fill']);
const normalize = (attributes) => Object.fromEntries(Object.entries(attributes).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => [key, String(value)]));
const upstreamGeometry = new Map();
for (const item of manifest.icons) {
  assert.match(item.name, /^[a-z0-9-]+$/);
  assert.equal(item.source_url, `https://raw.githubusercontent.com/lucide-icons/lucide/${ref}/icons/${item.name}.svg`);
  const bytes = read(`third_party/lucide/icons/${item.name}.svg`), xml = bytes.toString('utf8');
  assert.equal(sha(bytes), item.sha256, `${item.name}: preserve exact pinned upstream bytes`);
  assert.ok(!/<!DOCTYPE|<!ENTITY|<script|<image|<use\b|<foreignObject|href\s*=|\bon[a-z]+\s*=/i.test(xml), item.name);
  const children = xml.replace(/^[\s\S]*?<svg\b[^>]*>/, '').replace(/<\/svg>\s*$/, '');
  const geometry = [];
  for (const match of children.matchAll(/<([a-z]+)\b([^<>]*?)\s*\/>/g)) {
    const [, tag, raw] = match;
    assert.ok(allowedElements.has(tag), `${item.name}: ${tag}`);
    const attrs = Object.fromEntries([...raw.matchAll(/([a-z][a-z0-9-]*)="([^"]*)"/g)].map((m) => [m[1], m[2]]));
    for (const [key, value] of Object.entries(attrs)) {
      assert.ok(allowedAttributes.has(key), `${item.name}: ${key}`);
      if (key === 'fill') assert.equal(value, 'currentColor');
    }
    geometry.push({ tag, attrs: normalize(attrs) });
  }
  assert.ok(geometry.length > 0);
  assert.equal(children.replace(/<[a-z]+\b[^<>]*?\s*\/>/g, '').trim(), '', `${item.name}: unsupported nested or executable geometry`);
  upstreamGeometry.set(item.name, geometry);
}
function jsx(type, supplied, ...children) {
  const props = { ...(supplied ?? {}), children };
  return typeof type === 'function' ? type(props) : { type, props };
}
const Icon = loadIcon(jsx, 'fragment');
function geometry(tree) {
  if (Array.isArray(tree)) return tree.flatMap(geometry);
  if (!tree || typeof tree !== 'object') return [];
  if (tree.type === 'fragment') return geometry(tree.props.children);
  assert.ok(allowedElements.has(tree.type), `unsafe rendered element ${tree.type}`);
  const { children, ...attrs } = tree.props;
  for (const [key, value] of Object.entries(attrs)) {
    assert.ok(allowedAttributes.has(key), `unsafe rendered attribute ${key}`);
    if (key === 'fill') assert.equal(value, 'currentColor');
  }
  assert.equal(children.flat(Infinity).filter(Boolean).length, 0);
  return [{ tag: tree.type, attrs: normalize(attrs) }];
}
for (const [alias, upstream] of Object.entries(manifest.aliases)) {
  const rendered = Icon({ name: alias });
  assert.equal(rendered.type, 'svg');
  assert.equal(rendered.props['data-icon'], alias);
  assert.equal(rendered.props.className, 'ui-icon');
  assert.equal(rendered.props['aria-hidden'], 'true');
  assert.equal(rendered.props.focusable, 'false');
  assert.equal(rendered.props.width, 18); assert.equal(rendered.props.height, 18);
  assert.equal(rendered.props.viewBox, '0 0 24 24');
  assert.equal(rendered.props.stroke, 'currentColor'); assert.equal(rendered.props.fill, 'none');
  assert.equal(rendered.props.strokeWidth, 2);
  assert.equal(rendered.props.strokeLinecap, 'round'); assert.equal(rendered.props.strokeLinejoin, 'round');
  assert.deepEqual(geometry(rendered.props.children), upstreamGeometry.get(upstream), alias);
  assert.equal(Icon({ name: alias, size: 24 }).props.width, 24);
}
const iconSource = read('apps/desktop/src/Icon.tsx').toString('utf8');
assert.ok(!/dangerouslySetInnerHTML|<img\b|<image\b|<use\b|fetch\s*\(|https?:\/\//.test(iconSource.replace(/^\/\*[^]*?\*\//, '')), 'renderer cannot fetch or inject SVG');
const files = ['App', 'AIModelPanel', 'AssistantPanel', 'Inbox', 'WechatPanel', 'FeishuPanel', 'DigestPanel'];
const used = new Set();
for (const file of files) {
  const source = read(`apps/desktop/src/${file}.tsx`).toString('utf8');
  assert.match(source, /import \{ Icon(?:, type IconName)? \} from '\.\/Icon';/);
  assert.ok(!/function Icon\(/.test(source), `${file} must not keep a divergent local icon registry`);
  for (const match of source.matchAll(/<Icon\s+name="([a-z-]+)"/g)) {
    assert.ok(match[1] in manifest.aliases, `${file}: unknown ${match[1]}`); used.add(match[1]);
  }
}
assert.ok(used.size >= 20, 'icons must serve actual shipped UI controls, not a disconnected demo');
const styles = read('apps/desktop/src/styles.css').toString('utf8');
assert.match(styles, /\.button-icon\s*\{[^}]*width:\s*44px;[^}]*min-width:\s*44px;[^}]*min-height:\s*44px;/);
assert.ok(!/\.nav-item svg\s*\{[^}]*display:\s*none/.test(styles), 'narrow navigation keeps its functional icons');
// Guard the higher-specificity local overrides found by the real browser audit.
// Check every pixel minimum for these selectors, including fine-pointer narrow layouts.
for (const [file, selectors] of [
  ['profile.css', ['.profile-inline-actions .button', '.profile-settings > .button', '.profile-panel .ai-text-button']],
  ['inbox.css', ['.inbox-view-filters button', '.inbox-platform-filter', '.wechat-monitor-controls .button']],
]) {
  const css = read(`apps/desktop/src/${file}`).toString('utf8');
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selector, body]) => ({
    selectors: selector.split(',').map((part) => part.trim()),
    minimum: body.match(/min-height:\s*(\d+(?:\.\d+)?)px\s*;/)?.[1],
  }));
  for (const selector of selectors) {
    const minima = rules.filter((rule) => rule.selectors.includes(selector) && rule.minimum !== undefined);
    assert.ok(minima.length > 0, `${file}: ${selector} must retain a target minimum`);
    for (const { minimum } of minima) assert.ok(Number(minimum) >= 44, `${file}: ${selector} cannot override the shared 44px target with ${minimum}px`);
  }
}
console.log(`UI icon checks passed: ${manifest.icons.length} pinned upstream SVGs, ${Object.keys(manifest.aliases).length} real rendered aliases, ${used.size} control icons, exact geometry/hash, full ISC+Feather MIT notices, offline copies and safe decorative SVG semantics. No network IO.`);
