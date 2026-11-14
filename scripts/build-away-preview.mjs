// Package the built React app as a self-contained, synthetic-data preview.
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = resolve(root, "apps/desktop/dist");
const output = resolve(process.argv[2] || resolve(dist, "away-preview.html"));
let html = await readFile(resolve(dist, "index.html"), "utf8");
const scripts = [
  ...html.matchAll(/<script\b[^>]*src="([^"]+)"[^>]*><\/script>/g),
];
const styles = [...html.matchAll(/<link\b[^>]*href="([^"]+\.css)"[^>]*>/g)];
if (scripts.length !== 1 || styles.length !== 1) {
  throw new Error(
    "Expected one built script and stylesheet; run the desktop build first.",
  );
}
for (const [tag, source] of [...scripts, ...styles]) {
  if (!/^\/assets\/[\w.-]+$/.test(source))
    throw new Error("Unexpected asset path.");
  const body = await readFile(resolve(dist, source.slice(1)), "utf8");
  if (source.endsWith(".css")) {
    if (/<\/style/i.test(body)) throw new Error("Unsafe inline stylesheet.");
    html = html.replace(tag, () => `<style>${body}</style>`);
  } else {
    if (/<\/script/i.test(body)) throw new Error("Unsafe inline script.");
    html = html.replace(tag, () => `<script type="module">${body}</script>`);
  }
}
// Hosted previews never connect to a visitor's local service.
html = html.replace(
  "<head>",
  `<head><script>if(location.protocol!=='file:'){const u=new URL(location.href);u.searchParams.set('demo','away');history.replaceState(null,'',u)}</script>`,
);
await mkdir(dirname(output), { recursive: true });
await writeFile(output, html);
console.log(
  `Synthetic Away Desk preview built (${Buffer.byteLength(html)} bytes).`,
);
