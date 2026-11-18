// Model API / Lucide synthetic-only CDP regression. No app server, real service, account, model or platform is contacted.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";

assert.ok(
  process.argv.includes("--synthetic-only"),
  "Pass --synthetic-only explicitly",
);
const root = process.cwd();
const frontend = "http://127.0.0.1:1420";
const service = "http://127.0.0.1:17890";
const browser =
  process.env.SYNTHETIC_CHROMIUM ||
  path.join(
    process.env.LOCALAPPDATA || "",
    "ms-playwright",
    "chromium-1228",
    "chrome-win64",
    "chrome.exe",
  );
assert.ok(
  path.isAbsolute(browser),
  "Set SYNTHETIC_CHROMIUM to an installed Chromium executable",
);
const artifacts = await fs.mkdtemp(
  path.join(root, ".runtime", "model-api-icons-browser-"),
);
const profile = path.join(artifacts, "chromium-profile");
const j = JSON.stringify;
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const report = {
  synthetic_only: true,
  batch: "model-api-icons-desktop-and-narrow",
  server_started: false,
  all_application_http_requests_intercepted: true,
  actual_platform_or_model_calls: 0,
  checks: [],
  screenshots: [],
  api: [],
  consoleErrors: [],
  unexpectedTargets: [],
  interceptionErrors: [],
  unexpectedRequests: [],
};
const dist = new Map();
const distManifest = [];
const sourceManifest = [];
const backendFiles = [
  "internal/api/inbox.go",
  "internal/api/assistant_catalog.go",
  "internal/store/model.go",
  "internal/store/query.go",
  "api/openapi.yaml",
  "internal/api/model_presets.go",
  "internal/api/model_config.go",
  "internal/agent/config.go",
  "internal/mcp/server.go",
  "third_party/lucide/manifest.json",
  "third_party/lucide/LICENSE",
];
const harnessBytes = await fs.readFile(new URL(import.meta.url));
const harnessFingerprint = {
  sha256: sha(harnessBytes),
  bytes: harnessBytes.length,
};
async function fileFingerprint(absolute, relative) {
  const bytes = await fs.readFile(absolute);
  const stat = await fs.stat(absolute);
  return {
    path: relative,
    sha256: sha(bytes),
    bytes: bytes.length,
    mtime_ms: stat.mtimeMs,
  };
}
async function snapshot(directory, prefix, target = sourceManifest) {
  const entries = await fs.readdir(directory, { withFileTypes: true });
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) await snapshot(absolute, relative, target);
    else if (entry.isFile())
      target.push(await fileFingerprint(absolute, relative));
  }
}
await snapshot(path.join(root, "apps/desktop/src"), "apps/desktop/src");
for (const file of backendFiles)
  sourceManifest.push(await fileFingerprint(path.join(root, file), file));
async function freezeDist(directory, prefix = "") {
  const entries = await fs.readdir(directory, { withFileTypes: true });
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) await freezeDist(absolute, relative);
    else if (entry.isFile()) {
      dist.set(`/${relative}`, await fs.readFile(absolute));
      distManifest.push(await fileFingerprint(absolute, relative));
    }
  }
}
await freezeDist(path.join(root, "apps/desktop/dist"));
assert.ok(dist.has("/index.html"));
const date = "2026-11-18T04:00:00Z";
const conversation = (kind, id, title) => ({
  id,
  conversation_kind: kind,
  connection_id: "synthetic-connection",
  platform: "weixin",
  account_id: "synthetic-account",
  peer_id: kind === "group" ? `${id}@chatroom` : `wxid_${id}`,
  title,
  last_message: "最近一条：准备下班",
  last_message_at: date,
  unread_count: 0,
  pinned: false,
  archived: false,
});
const conversations = [
  conversation("private", "synthetic-private", "合成联系人"),
  conversation("group", "synthetic-group", "合成项目群"),
  conversation("group", "synthetic-name-only", "交付名称匹配群"),
];
const messages = (id) => [
  {
    id: `${id}-older`,
    conversation_id: id,
    external_id: `${id}-older-external`,
    direction: "incoming",
    body:
      id === "synthetic-name-only"
        ? "仅名称匹配，正文没有这个词"
        : "较早原文：项目交付请安排在周五，保留历史记录。",
    occurred_at: "2026-11-17T04:00:00Z",
    read: true,
    source: "wechatdb",
  },
  {
    id: `${id}-latest`,
    conversation_id: id,
    external_id: `${id}-latest-external`,
    direction: "incoming",
    body: "最近一条：准备下班",
    occurred_at: date,
    read: true,
    source: "wechatdb",
  },
];
const fold = (s) => s.replace(/[A-Z]/g, (c) => c.toLowerCase());
const contains = (s, q) => fold(s).includes(fold(q));
const empty = { items: [], next_cursor: null };
const assistantStatus = {
  configured: true,
  provider: "synthetic",
  model: "synthetic-model",
  endpoint_host: "api.openai.com",
  external: true,
  model_config_id: "synthetic-model-config",
  model_config_revision: 3,
  daily_limit: 50,
  used_today: 0,
  sending_enabled: false,
  session_ttl_seconds: 900,
  operations: [
    {
      id: "list_conversations",
      method: "GET",
      path: "/api/v1/conversations",
      effect: "read",
      availability: "available",
      reason: "合成目录：按类型和关键词检索会话",
    },
    {
      id: "get_conversation_messages",
      method: "GET",
      path: "/api/v1/conversations/{conversation_id}/messages",
      effect: "read",
      availability: "available",
      reason: "合成目录：会话内正文检索",
    },
    {
      id: "update_conversation",
      method: "PATCH",
      path: "/api/v1/conversations/{conversation_id}",
      effect: "write",
      availability: "confirmation_required",
      reason: "合成目录：固定预览后逐项确认本地变更",
    },
  ],
};
const presetRefs = {
  openai: 'd5292055f5d00d99d949017745a81d31cd186222',
  deepseek: '172dc8b74a0d549f4bce9850f7fe6a25b37a5ac7',
  gemini: '853a69ac49e21a917e7cdd178cadac193652c627',
  dashscope: '31a4d36d123688581a9e9744427272b33ce940e0',
};
const modelPresets = {
  protocol: 'openai_chat_completions', model_id_required: true, live_verification_performed: false,
  items: [
    { id: 'openai', display_name: 'OpenAI', base_url: 'https://api.openai.com/v1', api_key_required: true, implementation_status: 'protocol_supported', source_url: `https://github.com/openai/openai-go/blob/${presetRefs.openai}/README.md`, source_ref: presetRefs.openai, source_license: 'Apache-2.0' },
    { id: 'deepseek', display_name: 'DeepSeek', base_url: 'https://api.deepseek.com', api_key_required: true, implementation_status: 'protocol_supported', source_url: `https://github.com/deepseek-ai/awesome-deepseek-integration/tree/${presetRefs.deepseek}`, source_ref: presetRefs.deepseek, source_license: 'CC0-1.0' },
    { id: 'gemini', display_name: 'Gemini · OpenAI compatible', base_url: 'https://generativelanguage.googleapis.com/v1beta/openai', api_key_required: true, implementation_status: 'protocol_supported', source_url: `https://github.com/google-gemini/cookbook/blob/${presetRefs.gemini}/quickstarts/Get_started_OpenAI_Compatibility.ipynb`, source_ref: presetRefs.gemini, source_license: 'Apache-2.0' },
    { id: 'dashscope', display_name: 'DashScope · OpenAI compatible', base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', api_key_required: true, implementation_status: 'protocol_supported', source_url: `https://github.com/QwenLM/Qwen-Agent/blob/${presetRefs.dashscope}/examples/assistant_qwen3_coder.py`, source_ref: presetRefs.dashscope, source_license: 'Apache-2.0' },
    { id: 'custom', display_name: 'Custom · public HTTPS', base_url: '', api_key_required: true, implementation_status: 'template_only', source_url: `https://github.com/openai/openai-go/blob/${presetRefs.openai}/README.md`, source_ref: presetRefs.openai, source_license: 'Apache-2.0' },
    { id: 'loopback', display_name: 'Local · loopback HTTP', base_url: 'http://127.0.0.1:11434/v1', api_key_required: false, implementation_status: 'template_only', source_url: `https://github.com/openai/openai-go/blob/${presetRefs.openai}/README.md`, source_ref: presetRefs.openai, source_license: 'Apache-2.0' },
  ],
};
const readonlySettings = new Set([
  "/api/v1/ai/model-config",
  "/api/v1/ai/status",
  "/api/v1/digest/schedule",
  "/api/v1/digest/latest",
  "/api/v1/connections/qq-milky/status",
]);
function apiFixture(url, method) {
  const p = url.pathname;
  assert.equal(
    method,
    "GET",
    `No business mutations are allowed in this browser test: ${method} ${p}`,
  );
  if (p === "/api/v1/system")
    return {
      version: "synthetic-browser",
      stage: "synthetic",
      workspace_id: "synthetic-workspace",
      automation: { paused: true, auto_send_enabled: false },
      storage: { configured: true, driver: "sqlcipher" },
      connectors: [],
    };
  if (p === "/api/v1/inbox")
    return {
      storage_enabled: true,
      source_mode: "mixed",
      import_limit: 1000,
      platforms: [
        {
          id: "weixin",
          name: "微信",
          live_status: "configured",
          message_count: 6,
        },
      ],
    };
  if (p === "/api/v1/conversations") {
    const kind = url.searchParams.get("conversation_kind");
    assert.ok(
      ["private", "group"].includes(kind),
      "Actual UI must send an explicit conversation kind",
    );
    const q = url.searchParams.get("q")?.trim() || "";
    const items = conversations
      .filter(
        (c) =>
          c.conversation_kind === kind &&
          (!q ||
            contains(c.title, q) ||
            messages(c.id).some((m) => contains(m.body, q))),
      )
      .map((c) => {
        const match =
          q && [...messages(c.id)].reverse().find((m) => contains(m.body, q));
        return { ...c, ...(match ? { matched_message: match.body } : {}) };
      });
    return { items, next_cursor: null };
  }
  const history = p.match(
    /^\/api\/v1\/conversations\/(synthetic-(?:private|group|name-only))\/messages$/,
  );
  if (history)
    return {
      items: messages(history[1]).filter(
        (m) =>
          !url.searchParams.get("q") ||
          contains(m.body, url.searchParams.get("q").trim()),
      ),
      next_cursor: null,
    };
  if (p === "/api/v1/connections/wechat-db/status")
    return {
      configured: true,
      state: "ready",
      account_id: "synthetic-account",
      read_only: true,
      last_read_at: null,
      last_result: null,
      sending_enabled: false,
      monitor: {
        enabled: false,
        peer_ids: [],
        interval_seconds: 30,
        state: "disabled",
        last_cycle_at: null,
        last_error_code: null,
        last_result: null,
        read_only: true,
        sending_enabled: false,
      },
    };
  if (p === "/api/v1/connections/wechat-db/sessions") {
    assert.equal(
      url.search,
      "",
      "Source sessions must be filtered locally, not by unsupported endpoint parameters",
    );
    return {
      items: [
        {
          peer_id: "wxid_synthetic_private",
          name: "合成联系人",
          conversation_kind: "private",
        },
        {
          peer_id: "@@synthetic-group",
          name: "合成项目群",
          conversation_kind: "group",
        },
      ],
      read_only: true,
      sending_enabled: false,
    };
  }
  if (p === "/api/v1/connections/email-imap/status")
    return {
      configured: false,
      state: "not_configured",
      account: null,
      folder: null,
      last_synced_at: null,
      last_result: null,
      sending_enabled: false,
    };
  if (p === "/api/v1/assistant/status") return assistantStatus;
  if (p === "/api/v1/ai/model-presets") { assert.equal(url.search, ''); return modelPresets; }
  if (p === "/api/v1/ai/model-config") return { base_url: 'https://api.openai.com/v1', model: 'synthetic-model', daily_limit: 50, key_configured: true, revision: 3, configured: true, model_config_id: 'synthetic-model-config', sending_enabled: false };
  if (p === "/api/v1/ai/status") return { configured: true, provider: 'synthetic', model: 'synthetic-model', endpoint_host: 'api.openai.com', model_config_id: 'synthetic-model-config', model_config_revision: 3, daily_limit: 50, used_today: 0, sending_enabled: false };
  if (p === "/api/v1/digest/schedule") return { enabled: false, local_time: '20:30', timezone: 'Asia/Shanghai', revision: 1, last_run_day: '', model_config_id: 'synthetic-model-config', model_config_revision: 3 };
  if (p === "/api/v1/digest/latest") return { sending_enabled: false, report: null };
  if (p === "/api/v1/connections/qq-milky/status") return { configured: false, state: 'not_configured', account_id: '', nickname: '', milky_version: '1.3', checked_at: '', receiving_enabled: false, sending_enabled: false, read_only: true, last_sync_at: '', last_sync_result: '', skipped: 0, gaps: [], monitor: { enabled: false, state: 'disabled', peer_ids: [], read_only: true, last_error: '' } };
  if (readonlySettings.has(p))
    return {
      __status: 503,
      error: {
        code: "SYNTHETIC_SETTINGS_OUT_OF_SCOPE",
        message: "设置面板不在本合成场景内；无真实连接。",
      },
    };
  if (
    [
      "/api/v1/connections",
      "/api/v1/rules",
      "/api/v1/approvals",
      "/api/v1/outbox",
      "/api/v1/audit",
    ].includes(p)
  )
    return empty;
  throw Error(
    `Unexpected synthetic API: ${method} ${url.pathname}${url.search}`,
  );
}

let child,
  ws,
  session,
  ownedTargetId,
  sequence = 0;
const pending = new Map();
const workDeadline = Date.now() + 150_000;
let closing = false;
function command(method, params = {}, sessionID = session) {
  const id = ++sequence;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(
      () => {
        pending.delete(id);
        reject(Error(`CDP timeout: ${method}`));
      },
      closing ? 2000 : 10000,
    );
    pending.set(id, { resolve, reject, timeout });
    ws.send(
      j({ id, method, params, ...(sessionID ? { sessionId: sessionID } : {}) }),
    );
  });
}
async function evaluate(expression) {
  const result = await command("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) throw Error(j(result.exceptionDetails));
  return result.result?.value;
}
async function until(expression, label, ms = 10_000) {
  const end = Math.min(workDeadline, Date.now() + ms);
  while (Date.now() < end) {
    assert.deepEqual(report.interceptionErrors, [], label);
    if (
      typeof expression === "function"
        ? await expression()
        : await evaluate(expression)
    )
      return;
    await delay(40);
  }
  throw Error(`Timed out: ${label}`);
}
async function check(expression, label) {
  assert.equal(await evaluate(expression), true, label);
  report.checks.push(label);
}
async function settle() {
  await evaluate(
    "document.fonts.ready.then(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))))",
  );
}
let touchInput = false;
async function pointClick(expression, label) {
  const point = await evaluate(
    `(()=>{const e=${expression};if(!e||e.disabled)throw Error('Missing/disabled '+${j(label)});e.scrollIntoView({block:'center',inline:'center'});e.focus();const r=e.getBoundingClientRect();const x=r.x+r.width/2,y=r.y+r.height/2;const hit=document.elementFromPoint(x,y);if(r.width<=0||r.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight||!hit||!e.contains(hit))throw Error('Target is not visible/hittable '+${j(label)});return{x,y}})()`,
  );
  if (touchInput) {
    await command("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ ...point, radiusX: 2, radiusY: 2, force: 1, id: 1 }],
    });
    await command("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    report.native_touch_taps = (report.native_touch_taps || 0) + 1;
  } else {
    await command("Input.dispatchMouseEvent", { type: "mouseMoved", ...point });
    await command("Input.dispatchMouseEvent", {
      type: "mousePressed",
      ...point,
      button: "left",
      buttons: 1,
      clickCount: 1,
    });
    await command("Input.dispatchMouseEvent", {
      type: "mouseReleased",
      ...point,
      button: "left",
      buttons: 0,
      clickCount: 1,
    });
    report.native_mouse_clicks = (report.native_mouse_clicks || 0) + 1;
  }
  await settle();
}
async function click(selector) {
  await pointClick(`document.querySelector(${j(selector)})`, selector);
}
async function clickText(text, scope = "body") {
  await pointClick(
    `[...(document.querySelector(${j(scope)})?.querySelectorAll('button')||[])].find(e=>(e.getAttribute('aria-label')||e.textContent).trim()===${j(text)})`,
    text,
  );
}
async function input(selector, value) {
  await evaluate(
    `(()=>{const e=document.querySelector(${j(selector)});if(!e||e.disabled)throw Error('Missing/disabled input '+${j(selector)});Object.getOwnPropertyDescriptor(e instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(e,${j(value)});e.dispatchEvent(new Event('input',{bubbles:true}));return true})()`,
  );
  await settle();
}
async function key(key, code = key, modifiers = 0) {
  const virtualKey = {
    Tab: 9,
    Escape: 27,
    ArrowLeft: 37,
    ArrowRight: 39,
    Home: 36,
    End: 35,
    Enter: 13,
  }[key];
  assert.ok(virtualKey, `Unsupported synthetic key: ${key}`);
  const event = {
    key,
    code,
    modifiers,
    windowsVirtualKeyCode: virtualKey,
    nativeVirtualKeyCode: virtualKey,
  };
  await command("Input.dispatchKeyEvent", { type: "keyDown", ...event });
  await command("Input.dispatchKeyEvent", { type: "keyUp", ...event });
  report.native_keyboard_keys = (report.native_keyboard_keys || 0) + 1;
  await settle();
}
async function checkNativeModalFocus(label) {
  const focus = await evaluate(
    `(()=>{const dialog=document.querySelector('#assistant-workspace');const element=document.activeElement;return {modal:!!(dialog?.open&&dialog.matches(':modal')),inside:!!dialog?.contains(element),document_container:element===document.body||element===document.documentElement,page_focused:document.hasFocus(),tag:element?.tagName,id:element?.id,index:dialog?[...dialog.querySelectorAll('button,input,textarea,a,summary,[tabindex]')].indexOf(element):-1,hidden_recovery:!!element?.closest('.assistant-recovery:not([open]) form')}})()`,
  );
  assert.equal(focus.modal, true, `${label}: native modal must remain open`);
  assert.equal(
    focus.inside || focus.document_container,
    true,
    `${label}: no underlying business control may receive focus`,
  );
  assert.equal(
    focus.hidden_recovery,
    false,
    `${label}: closed recovery fields remain unfocusable`,
  );
  report.native_modal_focus ??= [];
  report.native_modal_focus.push({ step: label, ...focus });
  report.checks.push(label);
  return focus;
}
async function screenshot(file) {
  await settle();
  const shot = await command("Page.captureScreenshot", {
    format: "png",
    captureBeyondViewport: false,
  });
  await fs.writeFile(
    path.join(artifacts, file),
    Buffer.from(shot.data, "base64"),
  );
  report.screenshots.push(file);
}
async function viewport(width, height) {
  await command("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
  });
  touchInput = width < 500;
  await command("Emulation.setTouchEmulationEnabled", { enabled: touchInput });
  await settle();
}
const cors = [
  { name: "Access-Control-Allow-Origin", value: frontend },
  { name: "Access-Control-Allow-Methods", value: "GET, HEAD, OPTIONS" },
  {
    name: "Access-Control-Allow-Headers",
    value: "Authorization, Content-Type, Accept",
  },
  { name: "Cache-Control", value: "no-store" },
];
async function intercept(event) {
  const url = new URL(event.request.url);
  const method = event.request.method;
  let status = 200,
    body = Buffer.alloc(0),
    mime = "application/json";
  try {
    if (url.origin === frontend && ["GET", "HEAD"].includes(method)) {
      const resource = url.pathname === "/" ? "/index.html" : url.pathname;
      if (resource === "/favicon.ico") status = 204;
      else {
        assert.ok(
          dist.has(resource),
          `Unexpected frontend resource: ${resource}`,
        );
        body = dist.get(resource);
        mime = resource.endsWith(".html")
          ? "text/html; charset=utf-8"
          : resource.endsWith(".css")
            ? "text/css; charset=utf-8"
            : resource.endsWith(".js")
              ? "text/javascript; charset=utf-8"
              : resource.endsWith(".svg")
                ? "image/svg+xml"
                : "application/octet-stream";
      }
    } else if (url.origin === service) {
      report.api.push({ method, path: url.pathname, query: url.search });
      if (method === "OPTIONS") status = 204;
      else {
        assert.equal(Object.entries(event.request.headers).find(([key]) => key.toLowerCase()==='authorization')?.[1], 'Bearer synthetic-browser-not-a-real-credential', 'Every fixture business API is authenticated');
        const fixture = apiFixture(url, method);
        status = fixture.__status || 200;
        body = Buffer.from(
          j(fixture.__status ? { error: fixture.error } : fixture),
        );
      }
    } else {
      report.unexpectedRequests.push({ method, url: url.href });
      throw Error("No outbound browser traffic is allowed");
    }
    await command("Fetch.fulfillRequest", {
      requestId: event.requestId,
      responseCode: status,
      responseHeaders: [
        ...cors,
        { name: "Content-Type", value: mime },
        {
          name: "Content-Security-Policy",
          value: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src ${service}; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'; worker-src 'none'`,
        },
      ],
      body: (method === "HEAD" ? Buffer.alloc(0) : body).toString("base64"),
    });
  } catch (error) {
    report.interceptionErrors.push(String(error));
    await command("Fetch.failRequest", {
      requestId: event.requestId,
      errorReason: "BlockedByClient",
    }).catch(() => {});
  }
}

try {
  await fs.mkdir(profile, { recursive: true });
  child = spawn(
    browser,
    [
      "--headless=new",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-sync",
      "--disable-component-update",
      "--disable-default-apps",
      "--disable-extensions",
      "--disable-features=MediaRouter,OptimizationHints,AutofillServerCommunication",
      "--metrics-recording-only",
      "--no-pings",
      "--remote-debugging-address=127.0.0.1",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      "about:blank",
    ],
    { stdio: "ignore" },
  );
  let launchError;
  child.on("error", (error) => {
    launchError = error;
  });
  let portInfo;
  await until(async () => {
    if (launchError) throw launchError;
    try {
      portInfo = (
        await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")
      )
        .trim()
        .split(/\r?\n/);
      return portInfo.length >= 2;
    } catch {
      return false;
    }
  }, "isolated Chromium CDP port");
  ws = new WebSocket(`ws://127.0.0.1:${portInfo[0]}${portInfo[1]}`);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", reject, { once: true });
  });
  ws.addEventListener("message", ({ data }) => {
    const message = JSON.parse(String(data));
    if (message.id) {
      const item = pending.get(message.id);
      if (!item) return;
      clearTimeout(item.timeout);
      pending.delete(message.id);
      message.error
        ? item.reject(Error(j(message.error)))
        : item.resolve(message.result);
      return;
    }
    if (message.method === "Fetch.requestPaused")
      void intercept(message.params);
    if (message.method === "Runtime.exceptionThrown")
      report.consoleErrors.push(message.params.exceptionDetails);
    if (
      message.method === "Runtime.consoleAPICalled" &&
      message.params.type === "error"
    )
      report.consoleErrors.push(
        message.params.args.map((arg) => arg.value || arg.description),
      );
    if (
      message.method === "Target.attachedToTarget" &&
      message.params.targetInfo.targetId !== ownedTargetId
    )
      report.unexpectedTargets.push(message.params.targetInfo);
  });
  const targets = await command("Target.getTargets", {}, null);
  const page = targets.targetInfos.find((target) => target.type === "page");
  assert.ok(page);
  ownedTargetId = page.targetId;
  session = (
    await command(
      "Target.attachToTarget",
      { targetId: page.targetId, flatten: true },
      null,
    )
  ).sessionId;
  await command("Page.enable");
  await command("Runtime.enable");
  await command("Network.enable");
  await command("Network.setCacheDisabled", { cacheDisabled: true });
  await command("Fetch.enable", { patterns: [{ urlPattern: "*" }] });
  await command("Target.setAutoAttach", {
    autoAttach: true,
    waitForDebuggerOnStart: true,
    flatten: true,
  });
  await command("Network.setBypassServiceWorker", { bypass: true });
  await command("Network.setBlockedURLs", { urls: ["ws://*", "wss://*"] });
  await command("Browser.setDownloadBehavior", { behavior: "deny" }, null);
  await command("Page.addScriptToEvaluateOnNewDocument", {
    source: `(()=>{for(const name of ['WebSocket','EventSource','Worker','SharedWorker','RTCPeerConnection','webkitRTCPeerConnection'])Object.defineProperty(window,name,{configurable:false,value:class DisabledInSyntheticPass{constructor(){throw Error(name+' forbidden in isolated synthetic UI pass')}}});Object.defineProperty(window,'open',{configurable:false,value:()=>{throw Error('Popups forbidden in isolated synthetic UI pass')}});if(navigator.serviceWorker)Object.defineProperty(navigator.serviceWorker,'register',{configurable:false,value:()=>Promise.reject(Error('Service workers forbidden in isolated synthetic UI pass'))});})()`,
  });
  await viewport(1440, 1000);
  await command("Page.navigate", { url: frontend });
  await until(
    `!!document.querySelector('button.nav-item[aria-label="设置"]')`,
    "app shell",
  );
  await click('button.nav-item[aria-label="设置"]');
  await until(
    `!!document.querySelector('#service-token')`,
    "memory-only service connect",
  );
  await input("#service-token", "synthetic-browser-not-a-real-credential");
  await evaluate(
    `(()=>{const e=document.querySelector('.remember-device-choice input');if(e?.checked)e.click();return true})()`,
  );
  await clickText("连接服务", ".connection-form");
  await until(
    `document.querySelector('.connection-form .success-message')?.textContent.includes('服务可访问')`,
    "synthetic service connected",
  );
  await until(`document.querySelector('#ai-model-preset')?.options.length===7&&!document.querySelector('#ai-model-preset').disabled&&document.querySelector('#ai-model-name').value==='synthetic-model'`, 'authenticated static model metadata');
  const visibleIconButtons = `(()=>[...document.querySelectorAll('button')].filter(e=>e.getBoundingClientRect().width>0&&e.getBoundingClientRect().height>0&&e.querySelector('svg')))()`;
  async function auditIcons(label) {
    const audit = await evaluate(`(${visibleIconButtons}).map(e=>({label:(e.getAttribute('aria-label')||e.textContent).trim(),title:e.getAttribute('title'),icon_only:!e.textContent.trim(),w:e.getBoundingClientRect().width,h:e.getBoundingClientRect().height,icons:[...e.querySelectorAll('svg')].map(s=>({name:s.getAttribute('data-icon'),hidden:s.getAttribute('aria-hidden'),focusable:s.getAttribute('focusable')}))}))`);
    // Collect the whole viewport batch before failing, so one CSS fix can
    // address every actual target without weakening the final assertions.
    const violations = [];
    if(audit.length<6)violations.push(`${label}: fewer than six visible icon controls`);
    for(const entry of audit){
      if(!entry.label)violations.push(`${label}: missing accessible name`);
      if(entry.w<43.9||entry.h<43.9)violations.push(`${label}: target ${entry.label} (${entry.w}x${entry.h})`);
      if(entry.icon_only&&entry.title!==entry.label)violations.push(`${label}: visible hover label ${entry.label}`);
      for(const icon of entry.icons){if(!icon.name||icon.hidden!=='true'||icon.focusable!=='false')violations.push(`${label}: unsafe decorative SVG ${entry.label}`);}
    }
    (report.icon_audits??=[]).push({label,buttons:audit,violations});
    (report.icon_violations??=[]).push(...violations);
    if(!violations.length)report.checks.push(`${label}: visible icon buttons have accessible names, 44px targets and decorative safe SVGs`);
  }
  async function choosePreset(value){
    await evaluate(`(()=>{const e=document.querySelector('#ai-model-preset');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(e,${j(value)});e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
    await settle();
  }
  async function scrollStart(selector){await evaluate(`document.querySelector(${j(selector)}).scrollIntoView({block:'start'})`);await settle();}
  await check(`document.querySelector('#ai-model-key').value===''&&document.querySelector('.ai-model-form button[type=submit]').disabled&&!document.querySelector('.ai-model-consent input').checked`, 'Saved model keys remain write-only and save consent starts unchecked');
  await auditIcons('Desktop settings');
  const metadataBefore=report.api.filter(r=>r.method==='GET'&&r.path==='/api/v1/ai/model-presets').length;
  const callsBeforePreset=report.api.length;
  await input('#ai-model-key','synthetic-never-save-key');
  await click('.ai-model-consent input');
  await choosePreset('gemini');
  await check(`document.querySelector('#ai-model-url').value==='https://generativelanguage.googleapis.com/v1beta/openai'&&document.querySelector('#ai-model-name').value===''&&document.querySelector('#ai-model-key').value===''&&!document.querySelector('.ai-model-consent input').checked&&document.querySelector('.ai-model-form button[type=submit]').disabled`, 'Explicit Gemini preset fills only URL and clears model, typed key and consent without saving');
  await check(`(()=>{const a=document.querySelector('.ai-preset-source a');const u=new URL(a.href);return u.hostname==='github.com'&&u.pathname.includes('${presetRefs.gemini}')&&a.target==='_blank'&&a.relList.contains('noopener')&&a.relList.contains('noreferrer')&&a.referrerPolicy==='no-referrer'})()`, 'Provider source is a pinned official GitHub link without opener or referrer');
  await check(`document.querySelector('#ai-model-preset-help').textContent.includes('不测试连接或调用模型')&&document.querySelector('#ai-model-preset-help').textContent.includes('协议支持不等于服务已接通')`, 'Protocol support is visibly distinguished from a verified connection');
  assert.equal(report.api.length,callsBeforePreset,'Preset selection must not fetch, probe, save or invoke any provider');
  await scrollStart('#ai-model-title');
  await screenshot('desktop-model-presets.png');
  await choosePreset('loopback');
  await check(`document.querySelector('#ai-model-url').value==='http://127.0.0.1:11434/v1'&&document.querySelector('#ai-model-preset option:checked').textContent.includes('仅模板')&&document.querySelector('.ai-preset-source').textContent.includes('仅配置模板')&&document.querySelector('#ai-model-key').placeholder.includes('免密')&&document.querySelector('#ai-model-key').value===''`, 'Local loopback is visibly a key-optional template without inheriting cloud credentials, not an integration guarantee');
  await choosePreset('dashscope');
  await input('#ai-model-name','synthetic-model-for-review');
  await input('#ai-model-key','synthetic-never-save-key');
  await click('.ai-model-consent input');
  await input('#ai-model-url','https://manual.synthetic.invalid/v1');
  await check(`document.querySelector('#ai-model-preset').value===''&&document.querySelector('#ai-model-name').value===''&&document.querySelector('#ai-model-key').value===''&&!document.querySelector('.ai-model-consent input').checked`, 'Manual target edits cannot carry another target model, key or consent');
  await choosePreset('gemini');
  await input('#ai-model-name','synthetic-model-for-review');
  await click('.ai-model-consent input');
  await input('#ai-model-limit','25');
  await check(`!document.querySelector('.ai-model-consent input').checked&&document.querySelector('.ai-model-form button[type=submit]').disabled`, 'Changing shared daily quota revokes save consent');
  await click('[aria-label="重新读取预设"]');
  await until(()=>Promise.resolve(report.api.filter(r=>r.method==='GET'&&r.path==='/api/v1/ai/model-presets').length===metadataBefore+1),'explicit metadata-only reload');
  await until(`!document.querySelector('#ai-model-preset').disabled`,'metadata reload settled');
  await check(`document.querySelector('#ai-model-preset').value==='gemini'&&document.querySelector('#ai-model-name').value==='synthetic-model-for-review'&&document.querySelector('#ai-model-limit').value==='25'`, 'Metadata reload does not overwrite manually edited form fields');
  await viewport(860,1000);
  await scrollStart('#ai-model-title');
  await check(`document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('#ai-model-preset').getBoundingClientRect().right<=innerWidth`, '860px intermediate layout keeps provider controls inside the viewport');
  await auditIcons('860px settings');
  await viewport(390,844);
  await scrollStart('#ai-model-title');
  await check(`document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('#ai-model-preset').getBoundingClientRect().right<=innerWidth`, '390px model preset picker and GitHub source wrap without horizontal overflow');
  await auditIcons('390px settings');
  await screenshot('narrow-model-presets.png');
  await scrollStart('.ai-model-consent');
  await check(`document.querySelector('.ai-model-form button[type=submit]').disabled&&!document.querySelector('.ai-model-consent input').checked`, '390px confirmation keeps explicit save disabled');
  await screenshot('narrow-model-confirmation.png');
  await viewport(1440,1000);
  await click('button.nav-item[aria-label="消息"]');
  await until(`!!document.querySelector('.inbox-conversation')`,'fixture inbox');
  await click('#inbox-kind-group');
  await input('#inbox-keyword','交付');
  await until(`document.querySelector('.inbox-conversations')?.textContent.includes('正文命中：')`,'literal historical match');
  await auditIcons('Desktop inbox');
  await check(`!!document.querySelector('[aria-label="清除搜索关键词"] svg[data-icon="close"]')&&[...document.querySelectorAll('.inbox-discovery button')].some(e=>e.getAttribute('aria-label')==='找不到？让 Agent 帮我'&&e.textContent.trim()==='Agent 帮我')`, 'Inbox familiar secondary actions use icons while contextual help retains its full accessible name');
  await evaluate('window.scrollTo(0,0)');await settle();
  await screenshot('desktop-inbox-icons.png');
  await click('.inbox-conversation');
  await until(`!!document.querySelector('#inbox-message-keyword')`,'selected conversation');
  await clickText('找不到？让 Agent 帮我','.inbox-discovery');
  await until(`document.querySelector('#assistant-workspace')?.open&&!!document.querySelector('.assistant-help')`,'contextual Agent modal');
  await check(`document.querySelector('#assistant-workspace').matches(':modal')&&document.querySelector('#assistant-workspace').contains(document.activeElement)&&!document.querySelector('#assistant-message').value&&!document.querySelector('.assistant-check input').checked`, 'Icon entry opens a real native modal with reviewable help, no implicit draft transfer or consent');
  await check(`!!document.querySelector('[aria-label="关闭助手"][title="关闭助手"] svg[data-icon="close"]')&&!!document.querySelector('[aria-label="刷新状态"][title="刷新状态"] svg[data-icon="refresh"]')&&document.querySelector('#assistant-round-disclosure').textContent.includes('可能产生费用')&&document.querySelector('#assistant-round-disclosure').textContent.includes('独立确认')`, 'Shorter Agent copy retains named icon controls, model fee disclosure and independent platform consent');
  await clickText('确认带入草稿','#assistant-workspace');
  await until(`document.querySelector('#assistant-message').value.includes('交付')`,'explicit draft transfer');
  await input('#assistant-message','合成模型配置与业务API检查，不要提交。');
  await auditIcons('Desktop Agent modal');
  await screenshot('desktop-agent-icons.png');
  await viewport(390,844);
  await check(`document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('#assistant-workspace').getBoundingClientRect().width<=innerWidth&&getComputedStyle(document.querySelector('#assistant-workspace')).backgroundColor==='rgb(255, 255, 255)'`, '390px Agent remains white and within the viewport');
  await check(`(()=>{const d=document.querySelector('#assistant-workspace');const target=document.querySelector('#inbox-kind-group');const previous=document.activeElement;target.focus();return d.matches(':modal')&&document.activeElement===previous&&document.activeElement!==target})()`, 'Native dialog rejects focus on the underlying business page');
  const positions=new Set();
  for(let i=0;i<16;i++){await key('Tab');const position=await checkNativeModalFocus(`Native Tab stays out of background content (${i+1})`);if(position.inside)positions.add(position.index);}
  assert.ok(positions.size>=3,'Native keyboard navigation reaches at least three distinct Agent controls');
  await click('#assistant-message');
  await check(`document.querySelector('#assistant-message').value==='合成模型配置与业务API检查，不要提交。'&&!document.querySelector('.assistant-check input').checked&&document.querySelector('.assistant-composer button[type=submit]').disabled`, 'Native touch returns to preserved Agent draft without granting model consent');
  await auditIcons('390px Agent modal');
  await screenshot('narrow-agent-icons.png');
  await key('Escape');
  await check(`!document.querySelector('#assistant-workspace').open&&document.activeElement?.getAttribute('aria-label')==='找不到？让 Agent 帮我'&&document.querySelector('#inbox-keyword').value==='交付'&&!!document.querySelector('.inbox-conversation.selected')`, 'Escape restores the contextual icon trigger and preserves selected chat and search');
  await click('[aria-label="返回会话列表"]');
  await evaluate('window.scrollTo(0,0)');await settle();
  await check(`document.documentElement.scrollWidth<=innerWidth+1&&[...document.querySelectorAll('.nav-item svg')].every(e=>getComputedStyle(e).display!=='none')`, '390px navigation keeps its icons and wraps without overflow');
  await auditIcons('390px inbox');
  await screenshot('narrow-inbox-icons.png');
  assert.equal(report.api.filter(r=>!['GET','HEAD','OPTIONS'].includes(r.method)).length,0,'No save, tool run, source write, provider invocation, schedule or send is allowed');
  assert.equal(report.api.filter(r=>r.method==='GET'&&r.path==='/api/v1/ai/model-presets').length,metadataBefore+1,'Only mount and explicit reload read provider metadata');
  report.checks.push('All application API requests authenticated and intercepted; zero mutations and no real provider/platform calls');
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.interceptionErrors, []);
  assert.deepEqual(report.unexpectedRequests, []);
  assert.deepEqual(report.unexpectedTargets, []);
  const afterSources = [];
  await snapshot(
    path.join(root, "apps/desktop/src"),
    "apps/desktop/src",
    afterSources,
  );
  for (const file of backendFiles)
    afterSources.push(await fileFingerprint(path.join(root, file), file));
  assert.deepEqual(
    afterSources,
    sourceManifest,
    "Frozen source path set, bytes, SHA-256 and mtime must remain unchanged",
  );
  const afterDist = [];
  await snapshot(path.join(root, "apps/desktop/dist"), "", afterDist);
  assert.deepEqual(
    afterDist,
    distManifest,
    "Frozen dist path set, bytes, SHA-256 and mtime must remain unchanged",
  );
  assert.equal(
    sha(await fs.readFile(new URL(import.meta.url))),
    harnessFingerprint.sha256,
    "Browser harness must remain unchanged while running",
  );
  report.frozen_sources = sourceManifest.length;
  report.frozen_dist = distManifest.length;
  report.fingerprints_unchanged = true;
  assert.deepEqual(report.icon_violations ?? [], [], 'Every audited icon target, name, hover label and decorative SVG must pass; accumulated failures cannot be ignored');
  report.result = "passed";
} catch (error) {
  report.result = "failed";
  report.error = error.stack || String(error);
  process.exitCode = 1;
  if (session && ws?.readyState === WebSocket.OPEN) {
    try {
      report.failureText = await evaluate("document.body.innerText");
      report.failureFocus = await evaluate(
        `(()=>{const element=document.activeElement;const dialog=document.querySelector('#assistant-workspace');return {page_focused:document.hasFocus(),tag:element?.tagName,id:element?.id,classes:element?.className,dialog_open:dialog?.open,inside_dialog:!!dialog?.contains(element)}})()`,
      );
      await screenshot("failure.png");
    } catch {}
  }
} finally {
  closing = true;
  if (ws?.readyState === WebSocket.OPEN) {
    try {
      await command("Browser.close", {}, null);
    } catch {}
    ws.close();
  }
  if (child && child.exitCode === null) {
    await Promise.race([
      new Promise((resolve) => child.once("exit", resolve)),
      delay(2000),
    ]);
    if (child.exitCode === null) child.kill();
  }
  for (const item of pending.values()) {
    clearTimeout(item.timeout);
    item.reject(Error("Synthetic browser closed"));
  }
  pending.clear();
  await fs.writeFile(
    path.join(artifacts, "source-manifest.json"),
    `${JSON.stringify({ harness: harnessFingerprint, files: sourceManifest, dist: distManifest }, null, 2)}\n`,
  );
  await fs.writeFile(
    path.join(artifacts, "report.json"),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  console.log(
    `${report.result}: ${artifacts}\n${report.checks.length} checks; ${report.api.length} intercepted API requests; ${report.api.filter((r) => !["GET", "HEAD", "OPTIONS"].includes(r.method)).length} mutations.`,
  );
  if (report.error) console.error(report.error);
}
