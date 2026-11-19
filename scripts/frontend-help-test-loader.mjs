import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Compile the shipped disclosure, never a mock that drops its default-closed
// native details/summary semantics. Harnesses retain their own JSX factories.
const source = readFileSync(new URL('../apps/desktop/src/HelpDetails.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export /gm, '');
const transformed = transformSync('HelpDetails.tsx', source, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(transformed.errors.length, 0, 'HelpDetails.tsx must compile for synthetic execution');

export function loadHelpDetails(jsx) {
  assert.equal(typeof jsx, 'function', 'the disclosure loader requires the harness JSX factory');
  return runInNewContext(
    `(function(){\n${transformed.code}\nreturn HelpDetails;\n})()`,
    { jsx },
    { filename: 'HelpDetails.tsx' },
  );
}
