// Synthetic-only CDP regression. No app server, real service, account, model or platform is contacted.
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
  path.join(root, ".runtime", "chat-discovery-browser-"),
);
const profile = path.join(artifacts, "chromium-profile");
const j = JSON.stringify;
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const report = {
  synthetic_only: true,
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
  endpoint_host: "synthetic.invalid",
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
    `[...(document.querySelector(${j(scope)})?.querySelectorAll('button')||[])].find(e=>e.textContent.trim()===${j(text)})`,
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
  await click('button.nav-item[aria-label="消息"]');
  await until(
    `!!document.querySelector('.inbox-conversation')`,
    "private conversations",
  );
  await check(
    `document.querySelector('#inbox-kind-private')?.getAttribute('aria-selected')==='true'&&document.querySelector('#inbox-kind-group')?.textContent.includes('群聊')`,
    "Private/group tabs are explicit at first inbox paint",
  );
  await check(
    `document.querySelector('#inbox-keyword')?.placeholder.includes('消息正文')&&!!document.querySelector('#wechat-session-keyword')&&!!document.querySelector('#wechat-keyword')&&!!document.querySelector('#wechat-kind-group')`,
    "Stored-history and source-window keyword controls remain discoverable before source reads",
  );
  await check(
    `document.querySelector('#inbox-kind-private').getBoundingClientRect().height>=44&&document.querySelector('#inbox-keyword').getBoundingClientRect().top<800`,
    "Primary discovery controls have touch-size tabs and initial-viewport visibility",
  );
  await click("#inbox-kind-group");
  await until(
    `document.querySelector('.inbox-conversations')?.textContent.includes('合成项目群')`,
    "group list",
  );
  await input("#inbox-keyword", "交付");
  await until(
    `document.querySelector('.inbox-conversations')?.textContent.includes('正文命中：')`,
    "actual historical body snippets",
  );
  await check(
    `document.querySelector('.inbox-conversations')?.textContent.includes('较早原文：项目交付')&&document.querySelector('.inbox-conversations')?.textContent.includes('最近一条：准备下班')&&document.querySelector('.inbox-conversations')?.textContent.includes('名称或资料匹配')&&[...document.querySelectorAll('.inbox-conversations mark')].some(e=>e.textContent==='交付')`,
    "Body evidence is original, highlighted and distinct from latest preview and metadata-only matches",
  );
  await screenshot("desktop-groups-keyword.png");
  await click(".inbox-conversation");
  await until(
    `document.querySelector('.inbox-history')?.textContent.includes('较早原文')`,
    "filtered conversation originals",
  );
  await check(
    `document.querySelector('#inbox-message-keyword')?.value==='交付'&&!document.querySelector('.inbox-history')?.textContent.includes('最近一条')`,
    "Opening a result searches actual matching originals in that conversation",
  );
  await input("#inbox-message-keyword", "不存在的合成关键词");
  await until(
    `document.querySelector('.inbox-detail')?.textContent.includes('没有')&&!document.querySelector('.inbox-history')?.textContent.includes('较早原文')`,
    "honest empty body search",
  );
  await input("#inbox-message-keyword", "交付");
  await until(
    `document.querySelector('.inbox-history')?.textContent.includes('较早原文')`,
    "restored matching body",
  );
  await clickText("找不到？让 Agent 帮我", ".inbox-discovery");
  await until(
    `document.querySelector('#assistant-workspace')?.open&&!!document.querySelector('.assistant-help')`,
    "contextual Agent workspace",
  );
  await check(
    `!document.querySelector('#assistant-workspace textarea')?.value&&document.querySelector('#assistant-workspace')?.textContent.includes('确认带入草稿')`,
    "Help opens a reviewable task and does not fill or submit implicitly",
  );
  await check(
    `document.querySelector('#assistant-workspace').contains(document.activeElement)`,
    "Native modal focus starts inside the visible Agent workspace",
  );
  await clickText("确认带入草稿", "#assistant-workspace");
  await until(
    `document.querySelector('#assistant-workspace textarea')?.value.includes('交付')`,
    "explicit prepared task transfer",
  );
  await input("#assistant-workspace textarea", "合成 Agent 原草稿，不得覆盖。");
  const beforeCloseCalls = report.api.filter(
    (request) => !["GET", "OPTIONS", "HEAD"].includes(request.method),
  ).length;
  await key("Escape");
  await until(
    `!document.querySelector('#assistant-workspace').open`,
    "Escape closes Agent",
  );
  await check(
    `document.querySelector('#inbox-keyword')?.value==='交付'&&document.querySelector('#inbox-message-keyword')?.value==='交付'&&!!document.querySelector('.inbox-conversation.selected')`,
    "Closing Agent retains selected business chat and both searches",
  );
  await clickText("找不到？让 Agent 帮我", ".inbox-discovery");
  await check(
    `document.querySelector('#assistant-workspace textarea')?.value==='合成 Agent 原草稿，不得覆盖。'&&document.querySelector('#assistant-workspace')?.textContent.includes('追加到现有草稿')`,
    "Reopening help preserves the Agent draft and requires explicit append",
  );
  await clickText("追加到现有草稿", "#assistant-workspace");
  await check(
    `document.querySelector('#assistant-workspace textarea')?.value.startsWith('合成 Agent 原草稿，不得覆盖。')&&document.querySelector('#assistant-workspace textarea')?.value.includes('交付')`,
    "Explicit append preserves prior text without overwriting",
  );
  await viewport(390, 844);
  await check(
    `document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('#assistant-workspace').getBoundingClientRect().width<=innerWidth&&getComputedStyle(document.querySelector('#assistant-workspace')).backgroundColor==='rgb(255, 255, 255)'`,
    "390px Agent is white, within viewport and has no document horizontal overflow",
  );
  await check(
    `(()=>{const dialog=document.querySelector('#assistant-workspace');const target=document.querySelector('#inbox-kind-group');const previous=document.activeElement;if(!dialog||!target)return false;target.focus();return dialog.matches(':modal')&&document.activeElement===previous&&document.activeElement!==target})()`,
    "Native modal rejects programmatic focus on the underlying Inbox",
  );
  // Native dialog may yield focus to browser chrome/document container. Do not
  // add a production focus trap just to satisfy an overly strict test. Instead
  // record the real trace and reject focus on every underlying business control.
  const focusPositions = new Set();
  for (let i = 0; i < 18; i++) {
    await key("Tab");
    const focus = await checkNativeModalFocus(
      `Native Tab cannot focus background business content (${i + 1})`,
    );
    if (focus.inside) focusPositions.add(focus.index);
  }
  assert.ok(
    focusPositions.size >= 3,
    "Native Tab must reach at least three distinct Agent controls, not leave focus unchanged",
  );
  report.checks.push(
    "Native Tab reaches multiple Agent controls without focusing background content",
  );
  await evaluate(
    `document.querySelector('#assistant-workspace .assistant-workspace-bar button').focus()`,
  );
  await key("Tab", "Tab", 8);
  await checkNativeModalFocus(
    "Shift+Tab from the first Agent control cannot focus background business content",
  );
  await evaluate(
    `document.querySelector('#assistant-workspace .assistant-recovery > summary').focus()`,
  );
  await key("Tab");
  await checkNativeModalFocus(
    "Tab from the last Agent control cannot focus background business content",
  );
  await click("#assistant-message");
  await check(
    `document.activeElement===document.querySelector('#assistant-message')&&document.activeElement.value.startsWith('合成 Agent 原草稿，不得覆盖。')&&document.activeElement.value.includes('交付')&&![...document.querySelectorAll('#assistant-workspace input[type=checkbox]')].some(element=>element.checked)`,
    "Native touch returns to the preserved Agent draft without granting model consent",
  );
  await screenshot("narrow-agent-context.png");
  await key("Escape");
  await check(
    `!document.querySelector('#assistant-workspace').open&&document.activeElement?.textContent.includes('找不到')`,
    "Escape restores focus to the original help trigger",
  );
  await click('[aria-label="返回会话列表"]');
  await evaluate("window.scrollTo(0,0)");
  await settle();
  await check(
    `document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('#inbox-keyword').getBoundingClientRect().right<=innerWidth`,
    "390px inbox discovery wraps without horizontal overflow",
  );
  await screenshot("narrow-groups-keyword.png");
  await click("#inbox-kind-group");
  await key("ArrowLeft");
  await until(
    `document.querySelector('#inbox-kind-private')?.getAttribute('aria-selected')==='true'`,
    "keyboard private tab",
  );
  await check(
    `document.activeElement?.id==='inbox-kind-private'&&document.querySelector('#inbox-kind-group').tabIndex===-1`,
    "Tab roving focus and ArrowLeft select the matching private panel",
  );
  await clickText("读取会话列表", ".wechat-panel");
  await until(
    `document.querySelector('#wechat-peer')?.options.length===2`,
    "explicit synthetic source metadata read",
  );
  await click("#wechat-kind-group");
  await input("#wechat-session-keyword", "项目");
  await check(
    `document.querySelector('#wechat-peer').options.length===2&&document.querySelector('#wechat-peer').textContent.includes('合成项目群')&&!document.querySelector('#wechat-peer').textContent.includes('合成联系人')`,
    "WeChat group/name tabs filter only explicitly read source metadata",
  );
  await evaluate(
    `(()=>{const e=document.querySelector('#wechat-peer');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(e,'@@synthetic-group');e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`,
  );
  await settle();
  await click("#wechat-kind-private");
  await check(
    `document.querySelector('#wechat-peer').value===''&&document.querySelectorAll('.wechat-read-actions button').length===2&&[...document.querySelectorAll('.wechat-read-actions button')].every(e=>e.disabled)`,
    "Changing source type clears hidden selection and disables read/monitor actions",
  );
  await evaluate(
    `document.querySelector('.wechat-panel').scrollIntoView({block:'start'})`,
  );
  await settle();
  await check(
    `document.documentElement.scrollWidth<=innerWidth+1`,
    "390px source forms also remain within viewport",
  );
  await screenshot("narrow-wechat-source.png");
  assert.equal(beforeCloseCalls, 0);
  assert.equal(
    report.api.filter(
      (request) => !["GET", "HEAD", "OPTIONS"].includes(request.method),
    ).length,
    0,
    "Help and source discovery must never POST, write, enable a monitor, or invoke a model",
  );
  assert.equal(
    report.api.filter(
      (request) =>
        request.method === "GET" &&
        request.path === "/api/v1/connections/wechat-db/sessions",
    ).length,
    1,
    "Only the explicit metadata read fetches source sessions",
  );
  assert.ok(
    report.api.some(
      (request) =>
        request.path.endsWith("/messages") &&
        new URLSearchParams(request.query).get("q") === "交付",
    ),
  );
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
