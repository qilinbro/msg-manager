import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Compile the shipped icon component, not a stand-in. Each VM harness supplies
// its existing JSX factory so the real SVG geometry participates in its tree.
const source = readFileSync(new URL('../apps/desktop/src/Icon.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export /gm, '');
const transformed = transformSync('Icon.tsx', source, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(transformed.errors.length, 0, 'Icon.tsx must compile for synthetic execution');

export function loadIcon(jsx, Fragment = 'fragment') {
  assert.equal(typeof jsx, 'function', 'the icon loader requires the harness JSX factory');
  return runInNewContext(
    `(function(){\n${transformed.code}\nreturn Icon;\n})()`,
    { jsx, Fragment },
    { filename: 'Icon.tsx' },
  );
}
