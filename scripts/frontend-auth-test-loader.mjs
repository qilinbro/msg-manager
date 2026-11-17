import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Inline the *shipped* transport in legacy VM client checks. Its fetch binding
// resolves in each test's own browser context, so master-token requests still
// exercise the real passthrough branch and all existing HTTP assertions.
const source = readFileSync(new URL('../apps/desktop/src/device-auth.ts', import.meta.url), 'utf8')
  .replace(/^export /gm, '');
const transformed = transformSync('device-auth.ts', source);
assert.equal(transformed.errors.length, 0, 'device auth transport must compile');
export const deviceAuthSource = `const {authorizedFetch,authErrorMessage} = (function(){
${transformed.code}
return {authorizedFetch,authErrorMessage};
})();\n`;
