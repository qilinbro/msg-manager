// Keep the UI, downloadable SVGs and favicon on the same vector geometry.
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { chineseWordmark, latinWordmark, relayPath } from '../apps/desktop/src/brand/identity.ts';

const output = fileURLToPath(new URL('../apps/desktop/public/brand/', import.meta.url));
const symbol = (first, second = first) => `<path fill="${first}" d="${relayPath}"/><path fill="${second}" d="${relayPath}" transform="rotate(180 32 32)"/>`;
const svg = (viewBox, title, body) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${viewBox}" role="img" aria-labelledby="title"><title id="title">${title}</title>${body}</svg>\n`;
const lockup = (text, accent = text, latin = text) => `${symbol(text, accent)}<path fill="${text}" d="${chineseWordmark}"/><path fill="${latin}" d="${latinWordmark}"/>`;
const files = {
  'symbol.svg': svg('0 0 64 64', '接班 · 续行标志', symbol('#141b21')),
  'symbol-reverse.svg': svg('0 0 64 64', '接班 · 白色标志', symbol('#f3f7ee')),
  'symbol-accent.svg': svg('0 0 64 64', '接班 · 绿意标志', symbol('#c9f578')),
  'lockup.svg': svg('0 0 168 64', '接班 Away Desk', lockup('#141b21')),
  'lockup-reverse.svg': svg('0 0 168 64', '接班 Away Desk', lockup('#f3f7ee', '#c9f578', '#98a8b5')),
  'app-icon.svg': svg('0 0 128 128', '接班 Away Desk 应用图标', `<rect x="4" y="4" width="120" height="120" rx="28" fill="#141b21"/><g transform="translate(20 20) scale(1.375)">${symbol('#f3f7ee', '#c9f578')}</g>`),
  // The favicon uses a larger optical size than the desktop application icon.
  'favicon.svg': svg('0 0 64 64', '接班', `<rect width="64" height="64" rx="14" fill="#141b21"/>${symbol('#f3f7ee', '#c9f578')}`),
};
await mkdir(output, { recursive: true });
await Promise.all(Object.entries(files).map(([name, content]) => writeFile(`${output}${name}`, content)));
console.log(`Exported ${Object.keys(files).length} Away Desk brand assets.`);
