import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { transformSync } from "../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs";
import { deviceAuthSource } from "./frontend-auth-test-loader.mjs";
import { loadIcon } from "./frontend-icon-test-loader.mjs";

// Standalone, synthetic-only regression: no server, account, browser profile,
// database, platform connection or model. All fetches terminate in the fake below.
// The shipped components, helpers, API validators and device-auth passthrough run
// unchanged apart from removing module syntax and providing Vite's empty env.
const TOKEN = "synthetic-chat-discovery-token-NOT-A-CREDENTIAL";
const RAW_ERROR = "RAW_ERROR_SECRET_SENTINEL";
const BODY_SECRET = "BODY_PRIVATE_SENTINEL";
const DRAFT_SECRET = "DRAFT_PRIVATE_SENTINEL";
const DATE = "2026-11-17T01:02:03Z";
const plain = (value) => structuredClone(value);
const page = (items = [], next_cursor = null) => ({ items, next_cursor });
const conversation = (overrides = {}) => ({
  id: "synthetic-private",
  connection_id: "synthetic-connection",
  platform: "weixin",
  account_id: "ACCOUNT_PRIVATE_SENTINEL",
  peer_id: "wxid_synthetic_private",
  title: "合成联系人",
  conversation_kind: "private",
  last_message: "最近已入库记录",
  last_message_at: DATE,
  unread_count: 0,
  pinned: false,
  archived: false,
  ...overrides,
});
const message = (overrides = {}) => ({
  id: "synthetic-message",
  conversation_id: "synthetic-private",
  external_id: "synthetic-external",
  direction: "incoming",
  body: `${BODY_SECRET} 合成已入库正文`,
  occurred_at: DATE,
  read: false,
  source: "wechatdb",
  ...overrides,
});
const inboxInfo = {
  storage_enabled: true,
  source_mode: "mixed",
  import_limit: 1000,
  platforms: [
    {
      id: "weixin",
      name: "微信",
      live_status: "configured",
      message_count: 12,
    },
  ],
};
const monitorStatus = (overrides = {}) => ({
  enabled: false,
  peer_ids: [],
  interval_seconds: 30,
  state: "disabled",
  last_cycle_at: null,
  last_error_code: null,
  last_result: null,
  read_only: true,
  sending_enabled: false,
  ...overrides,
});
const wechatStatus = (overrides = {}) => ({
  configured: true,
  state: "ready",
  account_id: "wxid_synthetic_account",
  read_only: true,
  last_read_at: null,
  last_result: null,
  sending_enabled: false,
  monitor: monitorStatus(),
  ...overrides,
});
const readResult = {
  inserted: 0,
  duplicates: 1,
  conversations: 1,
  skipped: 0,
  limited: false,
  read_at: DATE,
  read_only: true,
  sending_enabled: false,
};

const compiled = new Map();
const testEnvironments = [];
function load(file, names, bindings) {
  if (!compiled.has(file)) {
    const source = readFileSync(
      new URL(`../apps/desktop/src/${file}`, import.meta.url),
      "utf8",
    )
      .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, "")
      .replace(/^import ['"][^'"]+['"];?\r?$/gm, "")
      .replace(/^export (?:default )?/gm, "")
      .replace(/import\.meta\.env/g, "({})");
    const output = transformSync(file, source, {
      jsx: { runtime: "classic", pragma: "jsx", pragmaFrag: "Fragment" },
    });
    assert.equal(output.errors.length, 0, `${file} must compile`);
    compiled.set(file, output.code);
  }
  return runInNewContext(
    `(function(){\n${compiled.get(file)}\nreturn {${names.join(",")}};\n})()`,
    bindings,
    { filename: file },
  );
}
const jsx = (type, supplied, ...children) => ({
  type,
  props: { ...supplied, children },
});
const Icon = loadIcon(jsx);
function nodes(value) {
  if (Array.isArray(value)) return value.flatMap(nodes);
  return value && typeof value === "object"
    ? [value, ...nodes(value.props?.children)]
    : [];
}
function text(value) {
  if (Array.isArray(value)) return value.map(text).join("");
  return value && typeof value === "object"
    ? text(value.props?.children)
    : String(value ?? "");
}
const hasClass = (node, value) =>
  String(node.props.className ?? "")
    .split(" ")
    .includes(value);
function find(root, predicate, description = "rendered control") {
  const found = nodes(root.tree ?? root).find(predicate);
  assert.ok(found, `missing ${description}`);
  return found;
}
const field = (h, id) => find(h, (node) => node.props.id === id, id);
const button = (h, label) =>
  find(
    h,
    (node) =>
      node.type === "button" &&
      (node.props["aria-label"] ?? text(node)).trim() === label,
    `button ${label}`,
  );
function assertIconOnly(h, label, name) {
  const control = button(h, label);
  assert.equal(control.props["aria-label"], label);
  assert.equal(control.props.title, label);
  assert.equal(
    text(control).trim(),
    "",
    "icon-only controls retain no visible explanation text",
  );
  const svg = nodes(control).filter((node) => node.type === "svg");
  assert.equal(
    svg.length,
    1,
    "icon controls must render one real SVG, not an Icon placeholder",
  );
  assert.equal(svg[0].props["data-icon"], name);
  assert.equal(svg[0].props["aria-hidden"], "true");
  assert.equal(svg[0].props.focusable, "false");
  assert.ok(
    nodes(svg[0]).some(
      (node) =>
        node.type === "path" &&
        typeof node.props.d === "string" &&
        node.props.d.length > 0,
    ),
    "the shipped SVG geometry must render",
  );
  return control;
}
const form = (h, className) =>
  find(
    h,
    (node) => node.type === "form" && hasClass(node, className),
    className,
  );

const listRows = (h) =>
  nodes(h.tree).filter(
    (node) => node.type === "button" && hasClass(node, "inbox-conversation"),
  );
const listRequests = (env) =>
  env.requests.filter((r) => r.url.pathname === "/api/v1/conversations");
const messageRequests = (env) =>
  env.requests.filter((r) =>
    /^\/api\/v1\/conversations\/.+\/messages$/.test(r.url.pathname),
  );
const sourceRequests = (env) =>
  env.requests.filter((r) =>
    /\/wechat-db\/(sessions|read|monitor)$/.test(r.url.pathname),
  );
function event(node, extra = {}) {
  return {
    currentTarget: node?.element,
    target: node?.element,
    prevented: false,
    preventDefault() {
      this.prevented = true;
    },
    stopPropagation() {},
    ...extra,
  };
}
async function click(h, node) {
  assert.notEqual(
    Boolean(node.props.disabled),
    true,
    `cannot click disabled ${text(node)}`,
  );
  node.props.onClick(event(node));
  await h.flush();
}
async function fill(h, id, value) {
  const node = field(h, id);
  assert.notEqual(
    Boolean(node.props.disabled),
    true,
    `cannot fill disabled ${id}`,
  );
  node.props.onChange(
    event(node, { target: { value, checked: Boolean(value) } }),
  );
  await h.flush();
}
async function submit(h, className) {
  const node = form(h, className);
  // Direct invocation deliberately also covers guard code when native validation
  // or disabled submit controls are bypassed by a synthetic caller.
  node.props.onSubmit(event(node));
  await h.flush();
}
function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function environment(responder) {
  let now = 0;
  let timerID = 0;
  const timers = new Map();
  const listeners = new Map();
  const requests = [];
  const transportFailures = [];
  const addEventListener = (name, callback) => {
    if (!listeners.has(name)) listeners.set(name, new Set());
    listeners.get(name).add(callback);
  };
  const removeEventListener = (name, callback) =>
    listeners.get(name)?.delete(callback);
  const window = {
    setTimeout(callback, ms = 0) {
      const id = ++timerID;
      timers.set(id, { callback, due: now + ms, ms });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    setInterval(callback, ms) {
      const id = ++timerID;
      timers.set(id, { callback, due: now + ms, ms, interval: true });
      return id;
    },
    clearInterval(id) {
      timers.delete(id);
    },
    addEventListener,
    removeEventListener,
  };
  const document = {
    visibilityState: "visible",
    addEventListener,
    removeEventListener,
  };
  const forbiddenStorage = {
    get() {
      throw Error("discovery must not access browser credential storage");
    },
  };
  for (const name of ["localStorage", "sessionStorage", "indexedDB"])
    Object.defineProperty(window, name, forbiddenStorage);
  const globals = {
    window,
    document,
    URL,
    URLSearchParams,
    TextEncoder,
    TextDecoder,
    Headers,
    AbortController,
    DOMException,
    jsx,
    Icon,
    Fragment: "fragment",
    console,
    async fetch(input, init = {}) {
      try {
        const url = new URL(input);
        assert.equal(
          url.origin,
          "http://127.0.0.1:17890",
          "only the fixed local API origin is allowed",
        );
        assert.equal(url.hash, "");
        assert.equal(url.username, "");
        assert.equal(url.password, "");
        assert.equal(init.credentials, "omit");
        assert.equal(init.cache, "no-store");
        assert.equal(init.redirect, "error");
        const headers = new Headers(init.headers);
        assert.equal(headers.get("Authorization"), `Bearer ${TOKEN}`);
        assert.equal(headers.get("Accept"), "application/json");
        assert.ok(
          init.signal instanceof AbortSignal,
          "each request must be abortable",
        );
        const request = {
          url,
          method: init.method ?? "GET",
          body: init.body === undefined ? undefined : JSON.parse(init.body),
          signal: init.signal,
        };
        if (request.method === "GET")
          assert.equal(init.body, undefined, "read-only GETs have no body");
        else assert.equal(headers.get("Content-Type"), "application/json");
        requests.push(request);
        // Intentionally do NOT auto-reject on abort: late results exercise both the
        // actual transport's post-JSON abort check and component stale-state guards.
        const reply = await responder(request);
        assert.ok(
          reply !== undefined,
          `unhandled synthetic request ${request.method} ${url}`,
        );
        const status = reply?.$status ?? 200;
        const payload = reply?.$status ? reply.payload : reply;
        return {
          ok: status >= 200 && status < 300,
          status,
          json: async () => plain(payload),
        };
      } catch (failure) {
        // The real clients intentionally catch network errors. Do not let them
        // turn a failed harness assertion/unhandled URL into a passing test.
        transportFailures.push(failure);
        throw failure;
      }
    },
  };
  for (const name of ["localStorage", "sessionStorage", "indexedDB"])
    Object.defineProperty(globals, name, forbiddenStorage);
  const auth = runInNewContext(
    `(function(){${deviceAuthSource}; return {authorizedFetch,authErrorMessage};})()`,
    globals,
  );
  const core = load("api.ts", ["SERVICE", "ServiceError"], {
    ...globals,
    ...auth,
  });
  const profiles = load(
    "profile-api.ts",
    ["PROFILE_CATEGORIES", "CATEGORY_NAMES", "isProfileCategory"],
    { ...globals, ...auth, ...core },
  );
  const inbox = load(
    "inbox-api.ts",
    [
      "PLATFORM_NAMES",
      "getInboxInfo",
      "getConversations",
      "getConversationMessages",
      "updateConversation",
    ],
    { ...globals, ...auth, ...core, ...profiles },
  );
  const wechat = load(
    "wechat-api.ts",
    [
      "getWechatStatus",
      "getWechatSessions",
      "readWechatMessages",
      "saveWechatMonitor",
      "wechatMonitorError",
    ],
    { ...globals, ...auth, ...core },
  );
  const helpers = {
    ...load("ConversationKindSelect.tsx", ["ConversationKindSelect"], globals),
    ...load(
      "keyword-search.tsx",
      ["KeywordMatch", "asciiFold", "keywordError"],
      globals,
    ),
  };
  const env = {
    globals,
    core,
    profiles,
    inbox,
    wechat,
    helpers,
    requests,
    timers,
    transportFailures,
    focused: null,
  };
  testEnvironments.push(env);
  env.elapse = async (ms, h) => {
    const until = now + ms;
    let iterations = 0;
    while (true) {
      const next = [...timers]
        .filter(([, timer]) => timer.due <= until)
        .sort((a, b) => a[1].due - b[1].due || a[0] - b[0])[0];
      if (!next) break;
      assert.ok(++iterations < 500, "synthetic clock must settle");
      const [id, timer] = next;
      now = timer.due;
      if (timer.interval) timer.due += timer.ms;
      else timers.delete(id);
      timer.callback();
      await h?.flush();
    }
    now = until;
    await h?.flush();
  };
  return env;
}

// A small keyed hook renderer, not a browser/React DOM substitute. It runs actual
// state setters, effect cleanups, refs and nested function components. Synthetic
// element focus supports tab-handler assertions; CSS layout, real focus/AT and
// native form behavior still require the independent browser acceptance check.
function mount(env, file, name, suppliedProps = {}) {
  let dirty = true;
  let tree;
  let active;
  let effects = [];
  let mounted = true;
  let postUnmountWrites = 0;
  let props = suppliedProps;
  const instances = new Map();
  const elements = new Map();
  const changed = (a, b) =>
    !a ||
    !b ||
    a.length !== b.length ||
    b.some((value, index) => !Object.is(value, a[index]));
  const slot = (create) => {
    assert.ok(active, "hooks require an active function component");
    const index = active.cursor++;
    if (!(index in active.slots)) active.slots[index] = create();
    return active.slots[index];
  };
  const hooks = {
    useState(initial) {
      const instance = active;
      const state = slot(() => ({
        kind: "state",
        value: typeof initial === "function" ? initial() : initial,
      }));
      return [
        state.value,
        (value) => {
          if (!mounted || !instance.mounted) {
            postUnmountWrites++;
            return;
          }
          const next = typeof value === "function" ? value(state.value) : value;
          if (!Object.is(next, state.value)) {
            state.value = next;
            dirty = true;
          }
        },
      ];
    },
    useRef(initial) {
      return slot(() => ({ current: initial }));
    },
    useEffect(callback, deps) {
      const effect = slot(() => ({
        kind: "effect",
        deps: undefined,
        cleanup: undefined,
      }));
      if (changed(effect.deps, deps)) {
        effect.deps = deps;
        effects.push(() => {
          effect.cleanup?.();
          effect.cleanup = callback();
        });
      }
    },
    useMemo(callback, deps) {
      const memo = slot(() => ({ deps: undefined, value: undefined }));
      if (changed(memo.deps, deps)) {
        memo.value = callback();
        memo.deps = deps;
      }
      return memo.value;
    },
    useCallback(callback, deps) {
      return hooks.useMemo(() => callback, deps);
    },
  };
  hooks.useLayoutEffect = hooks.useEffect;
  const bindings = {
    ...env.globals,
    ...env.core,
    ...env.profiles,
    ...env.inbox,
    ...env.wechat,
    ...env.helpers,
    ...hooks,
    EmailPanel: "EmailPanelStub",
    AIPanel: "AIPanelStub",
    ProfilePanel: "ProfilePanelStub",
    importMessages() {
      assert.fail("discovery must not import messages");
    },
    importTelegram() {
      assert.fail("discovery must not import Telegram");
    },
    parseImport() {
      assert.fail("discovery must not parse/import drafts");
    },
  };
  bindings.WechatPanel = load(
    "WechatPanel.tsx",
    ["WechatPanel"],
    bindings,
  ).WechatPanel;
  const component =
    file === "WechatPanel.tsx"
      ? bindings.WechatPanel
      : load(file, [name], bindings)[name];
  const cleanup = (instance) => {
    instance.mounted = false;
    for (const state of instance.slots) state?.cleanup?.();
  };
  function expand(value, id, parent, seen, hostSeen, disabled = false) {
    if (Array.isArray(value))
      return value.map((item, i) =>
        expand(
          item,
          `${id}/${item?.props?.key ?? i}`,
          parent,
          seen,
          hostSeen,
          disabled,
        ),
      );
    if (!value || typeof value !== "object") return value;
    if (typeof value.type === "function")
      return render(
        value.type,
        value.props,
        `${id}/${value.type.name}`,
        parent,
        seen,
        hostSeen,
        disabled,
      );
    let element = elements.get(id);
    if (!element || element.type !== value.type) {
      element = {
        type: value.type,
        scrollTop: 0,
        scrollHeight: 1000,
        clientHeight: 300,
        focus() {
          env.focused = this;
        },
        isConnected: true,
      };
      elements.set(id, element);
    }
    hostSeen.add(id);
    const node = { type: value.type, props: { ...value.props }, element };
    element.node = node;
    element.parentElement = parent;
    element.isConnected = true;
    element.value = node.props.value ?? "";
    element.checked = Boolean(node.props.checked);
    node.props.disabled = Boolean(disabled || node.props.disabled);
    element.disabled = node.props.disabled;
    if (typeof node.props.ref === "function") node.props.ref(element);
    else if (node.props.ref) node.props.ref.current = element;
    node.props.children = expand(
      node.props.children,
      `${id}/children`,
      element,
      seen,
      hostSeen,
      disabled || (node.type === "fieldset" && node.props.disabled),
    );
    return node;
  }
  function render(type, childProps, id, parent, seen, hostSeen, disabled) {
    let instance = instances.get(id);
    if (!instance) {
      instance = { slots: [], cursor: 0, mounted: true };
      instances.set(id, instance);
    }
    seen.add(id);
    const previous = active;
    active = instance;
    instance.cursor = 0;
    const output = type(childProps);
    const result = expand(
      output,
      `${id}/output`,
      parent,
      seen,
      hostSeen,
      disabled,
    );
    active = previous;
    return result;
  }
  return {
    get tree() {
      return tree;
    },
    get postUnmountWrites() {
      return postUnmountWrites;
    },
    updateProps(next) {
      props = { ...props, ...next };
      dirty = true;
    },
    async flush() {
      for (let pass = 0; pass < 90; pass++) {
        if (dirty && mounted) {
          dirty = false;
          effects = [];
          const seen = new Set();
          const hostSeen = new Set();
          tree = render(component, props, "root", null, seen, hostSeen, false);
          for (const [id, instance] of instances)
            if (!seen.has(id)) {
              cleanup(instance);
              instances.delete(id);
            }
          for (const [id, element] of elements)
            if (!hostSeen.has(id)) {
              element.isConnected = false;
              elements.delete(id);
            }
          const queue = effects;
          effects = [];
          for (const effect of queue) effect();
        }
        await Promise.resolve();
      }
      assert.equal(dirty, false, "component state/effects must settle");
    },
    unmount() {
      mounted = false;
      for (const instance of [...instances.values()].reverse())
        cleanup(instance);
    },
  };
}
const inboxProps = (help = []) => ({
  token: TOKEN,
  aiDrafts: new Map([
    [
      "synthetic-private",
      {
        body: DRAFT_SECRET,
        analysis: {
          id: "synthetic-analysis",
          conversation_id: "synthetic-private",
          context_hash: "synthetic-context",
          model: "synthetic-no-model-call",
          created_at: DATE,
          stale: false,
          intent: "synthetic-intent",
          summary: DRAFT_SECRET,
          draft: DRAFT_SECRET,
          risk_flags: [],
          evidence_ids: [],
          usage: { prompt_tokens: 0, completion_tokens: 0 },
          sending_enabled: false,
        },
      },
    ],
  ]),
  onSettings() {
    assert.fail("unexpected settings navigation");
  },
  onAuthFailure() {
    assert.fail("unexpected authentication failure");
  },
  onAssistantHelp(request) {
    help.push(plain(request));
  },
});
const wechatProps = (help = []) => ({
  token: TOKEN,
  storageEnabled: true,
  revision: 0,
  onSynced() {},
  onBackgroundSynced() {},
  onAuthFailure() {
    assert.fail("unexpected authentication failure");
  },
  onAssistantHelp(request) {
    help.push(plain(request));
  },
});
function assertKind(h, prefix, selected) {
  const select = field(h, prefix + "-kind");
  assert.equal(select.type, "select");
  assert.equal(select.props.value, selected);
  assert.ok(
    select.props["aria-label"],
    "kind dropdown needs an accessible name",
  );
  const choices =
    prefix === "wechat" ? ["私聊", "群聊"] : ["全部类型", "私聊", "群聊"];
  assert.deepEqual(
    nodes(select)
      .filter((node) => node.type === "option")
      .map(text),
    choices,
  );
}
function assertOutside(h, child, excludedClass) {
  const containers = nodes(h.tree).filter((node) =>
    hasClass(node, excludedClass),
  );
  assert.ok(containers.length > 0, `missing ${excludedClass} container`);
  for (const container of containers)
    assert.ok(
      !nodes(container).includes(child),
      `${child.props.id ?? child.type} must be outside ${excludedClass}`,
    );
}
async function search(h, env, id, value) {
  await fill(h, id, value);
  await env.elapse(250, h);
}
function helpConditions(request, expectedKeys) {
  assert.deepEqual(Object.keys(request).sort(), ["prompt", "source"]);
  assert.equal(typeof request.source, "string");
  assert.equal(typeof request.prompt, "string");
  const labels = {
    会话类型: "conversation_kind",
    平台: "platform",
    关系分类: "category",
    会话视图: "view",
    列表关键词: "keyword",
    "所选会话 ID":
      request.source === "微信只读接入" ? "peer_id" : "conversation_id",
    会话关键词: "session_keyword",
    正文关键词:
      request.source === "微信只读接入" ? "body_keyword" : "message_keyword",
  };
  const names = {
    私聊: "private",
    群聊: "group",
    全部: "all",
    全部会话: "all",
  };
  const conditions = Object.fromEntries(
    request.prompt
      .split("\n")
      .slice(2)
      .map((line) => {
        const delimiter = line.indexOf("：");
        const key = labels[line.slice(0, delimiter)];
        const value = line.slice(delimiter + 1);
        assert.ok(key, "handoff used an unexpected label");
        return [
          key,
          value === "无"
            ? key === "peer_id" ||
              key === "conversation_id" ||
              (key === "message_keyword" &&
                request.prompt.includes("所选会话 ID：无"))
              ? null
              : ""
            : value.startsWith('"')
              ? JSON.parse(value)
              : (names[value] ?? value),
        ];
      }),
  );
  assert.deepEqual(
    Object.keys(conditions).sort(),
    [...expectedKeys].sort(),
    "handoff condition allowlist",
  );
  for (const forbidden of [
    TOKEN,
    RAW_ERROR,
    BODY_SECRET,
    DRAFT_SECRET,
    "ACCOUNT_PRIVATE_SENTINEL",
    "TITLE_PRIVATE_SENTINEL",
  ]) {
    assert.ok(
      !JSON.stringify(request).includes(forbidden),
      `handoff leaked ${forbidden}`,
    );
  }
  return conditions;
}

// Literal highlighting and the same byte/NUL validator used by both searches.
{
  const env = environment(() => assert.fail("pure helpers must never fetch"));
  const { keywordError, asciiFold } = env.helpers;
  assert.equal(keywordError("  " + "a".repeat(256) + "  "), null);
  assert.match(keywordError("a".repeat(257)), /256/);
  assert.equal(keywordError("中".repeat(85)), null);
  assert.match(keywordError("中".repeat(86)), /256/);
  assert.equal(keywordError("😀".repeat(64)), null);
  assert.match(keywordError("😀".repeat(65)), /256/);
  assert.match(keywordError("a\0b"), /空字符/);
  assert.equal(asciiFold("AbC_ÄÉİ"), "abc_ÄÉİ");
  for (const [body, keyword, expected] of [
    ["a.b A.B aXb", "a.b", ["a.b", "A.B"]],
    ["literal 100% versus 1000", "%", ["%"]],
    ["x_y xay", "_", ["_"]],
    ["[x]*+? [X]*+? [xx]", "[x]*+?", ["[x]*+?", "[X]*+?"]],
    ["Ää Éé", "ä", ["ä"]],
    ["<img src=x onerror=SECRET>", "img", ["img"]],
    ["abcdef", "", []],
    ["abcdef", "\0", []],
  ]) {
    const h = mount(env, "keyword-search.tsx", "KeywordMatch", {
      text: body,
      keyword,
    });
    await h.flush();
    assert.equal(
      text(h.tree),
      body,
      "highlighting must preserve original text",
    );
    assert.deepEqual(
      nodes(h.tree)
        .filter((node) => node.type === "mark")
        .map(text),
      expected,
    );
    assert.ok(
      nodes(h.tree).every(
        (node) => !node.props.dangerouslySetInnerHTML && node.type !== "img",
      ),
    );
    h.unmount();
  }
  const h = mount(env, "keyword-search.tsx", "KeywordMatch", {
    text: "a".repeat(180),
    keyword: "a",
  });
  await h.flush();
  assert.equal(
    nodes(h.tree).filter((node) => node.type === "mark").length,
    100,
    "highlight work must be bounded",
  );
  assert.equal(text(h.tree), "a".repeat(180));
  h.unmount();
}

// Actual API URL construction, required kind discriminants and optional bounded
// backend snippets. These fixtures model responses; backend all-history search
// correctness belongs to the repository's backend/OpenAPI tests, not this fake.
{
  let response = page([conversation()]);
  const env = environment(() => response);
  const signal = new AbortController().signal;
  const filters = {
    platform: "weixin",
    conversation_kind: "private",
    category: "client",
    q: "预算%_AbC",
    unread: true,
    pinned: true,
    archived: false,
    cursor: "next+/= &?",
  };
  await env.inbox.getConversations(TOKEN, signal, filters);
  assert.deepEqual(Object.fromEntries(env.requests.at(-1).url.searchParams), {
    limit: "50",
    archived: "false",
    platform: "weixin",
    conversation_kind: "private",
    category: "client",
    q: "预算%_AbC",
    unread: "true",
    pinned: "true",
    cursor: "next+/= &?",
  });
  await env.inbox.getConversations(TOKEN, signal, {
    archived: false,
    conversation_kind: "group",
  });
  assert.equal(
    env.requests.at(-1).url.searchParams.get("conversation_kind"),
    "group",
  );
  const snippet = "😀".repeat(376);
  response = page([conversation({ matched_message: snippet })]);
  const bounded = await env.inbox.getConversations(TOKEN, signal, {
    archived: false,
    conversation_kind: "private",
    q: "😀",
  });
  assert.equal(
    bounded.items[0].matched_message,
    snippet,
    "server snippet must be preserved, never fabricated",
  );
  for (const invalid of [
    conversation({ conversation_kind: undefined }),
    conversation({ conversation_kind: "channel" }),
    conversation({ matched_message: "😀".repeat(377) }),
    conversation({ matched_message: 7 }),
  ]) {
    response = page([invalid]);
    await assert.rejects(
      env.inbox.getConversations(TOKEN, signal, { archived: false }),
      (failure) => failure.code === "INVALID_RESPONSE",
    );
  }
  const beforeInvalidKind = env.requests.length;
  await assert.rejects(
    env.inbox.getConversations(TOKEN, signal, {
      archived: false,
      conversation_kind: "channel",
    }),
    (failure) => failure.code === "INVALID_RESPONSE",
  );
  assert.equal(env.requests.length, beforeInvalidKind);
  response = page([message()]);
  await env.inbox.getConversationMessages(
    TOKEN,
    signal,
    "synthetic/id ?#",
    "older+/= &",
    "  预算%_AbC  ",
  );
  assert.equal(
    env.requests.at(-1).url.pathname,
    "/api/v1/conversations/synthetic%2Fid%20%3F%23/messages",
  );
  assert.deepEqual(Object.fromEntries(env.requests.at(-1).url.searchParams), {
    limit: "50",
    cursor: "older+/= &",
    q: "预算%_AbC",
  });
  await env.inbox.getConversationMessages(
    TOKEN,
    signal,
    "synthetic-private",
    undefined,
    "   ",
  );
  assert.deepEqual(Object.fromEntries(env.requests.at(-1).url.searchParams), {
    limit: "50",
  });
  response = {
    $status: 400,
    payload: { error: { code: "INVALID_REQUEST", message: RAW_ERROR } },
  };
  await assert.rejects(
    env.inbox.getConversationMessages(
      TOKEN,
      signal,
      "synthetic-private",
      undefined,
      "a\0b",
    ),
    (failure) =>
      failure.code === "INVALID_REQUEST" &&
      !failure.message.includes(RAW_ERROR),
  );
  response = {
    read_only: true,
    sending_enabled: false,
    items: [
      {
        peer_id: "@@synthetic_group",
        name: "合成群",
        conversation_kind: "group",
      },
    ],
  };
  const sessions = await env.wechat.getWechatSessions(TOKEN, signal);
  assert.equal(sessions[0].conversation_kind, "group");
  assert.equal(
    env.requests.at(-1).url.pathname,
    "/api/v1/connections/wechat-db/sessions",
  );
  assert.equal(env.requests.at(-1).method, "GET");
  assert.equal(
    env.requests.at(-1).url.search,
    "",
    "sessions endpoint must stay query-free",
  );
  response = {
    read_only: true,
    sending_enabled: false,
    items: [{ peer_id: "wxid_synthetic", name: "合成联系人" }],
  };
  await assert.rejects(
    env.wechat.getWechatSessions(TOKEN, signal),
    (failure) => failure.code === "INVALID_RESPONSE",
  );
  assert.ok(env.requests.every((request) => request.method === "GET"));
  assert.equal(
    env.timers.size,
    0,
    "finished API calls must remove timeout timers",
  );
}

// Persistent controls exist even before connection/status/list data is available.
{
  const env = environment(() =>
    assert.fail("disconnected Inbox must not fetch"),
  );
  const h = mount(env, "Inbox.tsx", "Inbox", { ...inboxProps(), token: "" });
  await h.flush();
  assertKind(h, "inbox", "private");
  assert.equal(field(h, "inbox-keyword").props["aria-label"], "搜索会话与消息");
  assert.equal(env.requests.length, 0);
  h.unmount();
}

// Inbox discovery, body-evidence rendering, query propagation, independent
// history search/clear/pagination, honest empty states and UI-only Agent help.
{
  const query = "AbC%_";
  const oldSnippet = `已入库较早正文 ${query} 正文证据`;
  const bodyRow = conversation({
    title: "TITLE_PRIVATE_SENTINEL",
    last_message: "最新预览不含搜索词",
    matched_message: oldSnippet,
  });
  const titleRow = conversation({
    id: "synthetic-title",
    title: `${query} 名称独有`,
    peer_id: "wxid_synthetic_title",
  });
  const groupRow = conversation({
    id: "synthetic-group",
    conversation_kind: "group",
    title: "合成群聊",
    peer_id: "@@synthetic_group",
  });
  const help = [];
  let sourceNeedsAttention = false;
  const env = environment((request) => {
    const { pathname, searchParams } = request.url;
    if (pathname === "/api/v1/inbox") return inboxInfo;
    if (pathname === "/api/v1/connections/wechat-db/status")
      return sourceNeedsAttention
        ? wechatStatus({
            monitor: monitorStatus({
              state: "error",
              last_error_code: "WECHAT_DB_UNAVAILABLE",
            }),
          })
        : wechatStatus();
    if (pathname === "/api/v1/conversations") {
      const q = searchParams.get("q") ?? "";
      if (q === "后端拒绝")
        return {
          $status: 400,
          payload: { error: { code: "INVALID_REQUEST", message: RAW_ERROR } },
        };
      if (q === "没有任何匹配" || new TextEncoder().encode(q).byteLength >= 256)
        return page();
      if (searchParams.get("conversation_kind") === "group")
        return page([groupRow]);
      if (q === "名称独有") return page([titleRow]);
      return page(
        [titleRow, bodyRow],
        searchParams.has("cursor") ? null : "list+/=&cursor",
      );
    }
    if (pathname.endsWith("/messages")) {
      const q = searchParams.get("q") ?? "";
      if (q === "后端拒绝")
        return {
          $status: 400,
          payload: { error: { code: "INVALID_REQUEST", message: RAW_ERROR } },
        };
      if (pathname.includes("synthetic-title") || q === "没有正文")
        return page();
      if (searchParams.has("cursor"))
        return page([
          message({
            id: "synthetic-older",
            body: `更早 ${q} ${BODY_SECRET}`,
            external_id: "synthetic-older-external",
          }),
        ]);
      return page(
        [
          message({
            body: `${BODY_SECRET} ${q ? q.toLowerCase() : "全部最新正文"}`,
          }),
        ],
        q ? "history+/=&cursor" : null,
      );
    }
    assert.fail(`unexpected discovery action ${request.method} ${request.url}`);
  });
  const h = mount(env, "Inbox.tsx", "Inbox", inboxProps(help));
  await h.flush();
  assertKind(h, "inbox", "private");
  assertKind(h, "wechat", "private");
  assert.equal(
    listRequests(env)[0].url.searchParams.get("conversation_kind"),
    "private",
  );
  assert.ok(
    env.requests.every((request) => request.method === "GET"),
    "initial render is GET-only",
  );
  assert.equal(
    sourceRequests(env).length,
    0,
    "mount must not read source sessions/messages or enable automation",
  );
  const discovery = find(
    h,
    (node) => node.type === "section" && hasClass(node, "inbox-discovery"),
  );
  for (const control of [
    discovery,
    field(h, "inbox-kind"),
    field(h, "inbox-keyword"),
  ])
    assertOutside(h, control, "inbox-conversation-scroll");
  const beforeType = listRequests(env).length;
  await fill(h, "inbox-keyword", `  ${query}  `);
  await env.elapse(249, h);
  assert.equal(
    listRequests(env).length,
    beforeType,
    "typing should debounce without reading a source",
  );
  await env.elapse(1, h);
  assert.equal(listRequests(env).at(-1).url.searchParams.get("q"), query);
  assert.equal(
    listRequests(env).at(-1).url.searchParams.get("conversation_kind"),
    "private",
  );
  assert.equal(listRows(h).length, 2);
  const titleNode = listRows(h).find((node) => text(node).includes("名称独有"));
  const bodyNode = listRows(h).find((node) =>
    text(node).includes("TITLE_PRIVATE_SENTINEL"),
  );
  assert.ok(text(titleNode).includes("名称或资料匹配"));
  assert.ok(
    !text(titleNode).includes("正文命中"),
    "name-only matches must not invent body evidence",
  );
  assert.ok(text(bodyNode).includes(`正文命中：${oldSnippet}`));
  assert.ok(
    nodes(bodyNode).some(
      (node) => node.type === "mark" && text(node) === query,
    ),
    "real supplied history snippet is highlighted literally",
  );
  await click(h, button(h, "加载更多会话"));
  assert.equal(
    listRequests(env).at(-1).url.searchParams.get("cursor"),
    "list+/=&cursor",
  );
  assert.equal(listRequests(env).at(-1).url.searchParams.get("q"), query);
  assert.equal(
    listRequests(env).at(-1).url.searchParams.get("conversation_kind"),
    "private",
  );
  await click(
    h,
    listRows(h).find((node) => text(node).includes("TITLE_PRIVATE_SENTINEL")),
  );
  assert.equal(
    nodes(h.tree).filter(
      (node) =>
        node.type === "input" &&
        node.props.type === "search" &&
        node.props.id.startsWith("inbox-"),
    ).length,
    1,
  );
  assert.equal(
    nodes(h.tree).filter(
      (node) =>
        hasClass(node, "inbox-sources") &&
        node.type === "details" &&
        !node.props.open,
    ).length,
    1,
  );
  const beforeScope = env.requests.length;
  await fill(h, "inbox-search-scope", "current");
  assert.equal(
    env.requests.length,
    beforeScope,
    "switching search scope alone does not fetch",
  );
  assert.equal(field(h, "inbox-keyword").props.value, query);
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.get("q"),
    query,
    "selection propagates the list query to body history",
  );
  assert.ok(
    nodes(h.tree).some(
      (node) => node.type === "mark" && text(node) === query.toLowerCase(),
    ),
  );
  const beforeHelp = env.requests.length;
  await click(h, button(h, "Agent 帮我"));
  assert.equal(
    env.requests.length,
    beforeHelp,
    "help must only prepare a request, not fetch/read/write/invoke a model",
  );
  assert.equal(help.at(-1).source, "消息检索");
  const conditions = helpConditions(help.at(-1), [
    "conversation_kind",
    "platform",
    "category",
    "view",
    "keyword",
    "conversation_id",
    "message_keyword",
  ]);
  assert.deepEqual(conditions, {
    conversation_kind: "private",
    platform: "all",
    category: "all",
    view: "all",
    keyword: query,
    conversation_id: "synthetic-private",
    message_keyword: query,
  });
  assert.ok(
    !help.at(-1).prompt.includes(oldSnippet),
    "matched-message content must never become Agent context",
  );
  const listCount = listRequests(env).length;
  await search(h, env, "inbox-keyword", "独立 body");
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.get("q"),
    "独立 body",
  );
  assert.equal(
    listRequests(env).length,
    listCount,
    "body search is independent from list search",
  );
  await fill(h, "inbox-search-scope", "list");
  assert.equal(field(h, "inbox-keyword").props.value, `  ${query}  `);
  await fill(h, "inbox-search-scope", "current");
  assert.equal(field(h, "inbox-keyword").props.value, "独立 body");
  await click(h, button(h, "加载更早消息"));
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.get("cursor"),
    "history+/=&cursor",
  );
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.get("q"),
    "独立 body",
    "earlier-page requests must retain q",
  );
  assert.ok(text(h.tree).includes("更早 独立 body"));
  await click(h, assertIconOnly(h, "清除搜索关键词", "close"));
  assert.equal(field(h, "inbox-keyword").props.value, "");
  assert.equal(messageRequests(env).at(-1).url.searchParams.has("q"), false);
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.has("cursor"),
    false,
  );
  assert.equal(listRequests(env).length, listCount);
  assert.ok(text(h.tree).includes("全部最新正文"));

  for (const invalid of ["a".repeat(257), "中".repeat(86), "a\0b"]) {
    const before = messageRequests(env).length;
    await fill(h, "inbox-keyword", invalid);
    assert.equal(field(h, "inbox-keyword").props["aria-invalid"], true);
    await submit(h, "inbox-keyword-form");
    await env.elapse(250, h);
    assert.equal(
      messageRequests(env).length,
      before,
      "invalid body q must be stopped before fetch",
    );
    assert.ok(nodes(h.tree).some((node) => node.props.role === "alert"));
    await fill(h, "inbox-keyword", "");
    await env.elapse(250, h);
  }
  await search(h, env, "inbox-keyword", "😀".repeat(64));
  assert.equal(
    messageRequests(env).at(-1).url.searchParams.get("q"),
    "😀".repeat(64),
    "exact 256-byte body q must be accepted",
  );
  await search(h, env, "inbox-keyword", "后端拒绝");
  assert.ok(nodes(h.tree).some((node) => node.props.role === "alert"));
  assert.ok(
    !text(h.tree).includes(RAW_ERROR),
    "backend fallback must not expose raw errors",
  );
  const beforeErrorHelp = env.requests.length;
  await click(h, button(h, "Agent 帮我"));
  assert.equal(env.requests.length, beforeErrorHelp);
  helpConditions(help.at(-1), [
    "conversation_kind",
    "platform",
    "category",
    "view",
    "keyword",
    "conversation_id",
    "message_keyword",
  ]);

  await fill(h, "inbox-search-scope", "list");
  await click(h, button(h, "清除搜索关键词"));
  assert.equal(field(h, "inbox-keyword").props.value, "");
  assert.equal(listRequests(env).at(-1).url.searchParams.has("q"), false);
  for (const invalid of ["a".repeat(257), "中".repeat(86), "a\0b"]) {
    const before = listRequests(env).length;
    await fill(h, "inbox-keyword", invalid);
    assert.equal(field(h, "inbox-keyword").props["aria-invalid"], true);
    await submit(h, "inbox-keyword-form");
    await env.elapse(250, h);
    assert.equal(
      listRequests(env).length,
      before,
      "invalid list q must be stopped before fetch",
    );
    assert.ok(nodes(h.tree).some((node) => node.props.role === "alert"));
    await click(h, button(h, "清除搜索关键词"));
  }
  await search(h, env, "inbox-keyword", "a".repeat(256));
  assert.equal(
    listRequests(env).at(-1).url.searchParams.get("q"),
    "a".repeat(256),
  );
  await search(h, env, "inbox-keyword", "后端拒绝");
  assert.ok(nodes(h.tree).some((node) => node.props.role === "alert"));
  assert.ok(!text(h.tree).includes(RAW_ERROR));
  await search(h, env, "inbox-keyword", "没有任何匹配");
  assert.ok(text(h.tree).includes("没有找到相关会话"));
  assert.equal(listRows(h).length, 0);
  const emptyHelpCount = env.requests.length;
  await click(h, button(h, "Agent 帮我"));
  assert.equal(env.requests.length, emptyHelpCount);
  assert.equal(
    helpConditions(help.at(-1), [
      "conversation_kind",
      "platform",
      "category",
      "view",
      "keyword",
      "conversation_id",
      "message_keyword",
    ]).conversation_id,
    null,
  );
  await search(h, env, "inbox-keyword", "名称独有");
  await click(h, listRows(h)[0]);
  assert.ok(text(h.tree).includes("当前会话没有匹配的正文。"));
  await click(h, button(h, "清除搜索关键词"));
  await fill(h, "inbox-kind", "group");
  assertKind(h, "inbox", "group");
  assert.equal(
    listRequests(env).at(-1).url.searchParams.get("conversation_kind"),
    "group",
  );
  assert.ok(text(h.tree).includes("合成群聊"));
  assert.equal(
    nodes(h.tree).some((node) => node.props.id === "inbox-search-scope"),
    false,
    "kind change clears the selected conversation",
  );
  await fill(h, "inbox-kind", "all");
  assertKind(h, "inbox", "all");
  assert.equal(
    listRequests(env).at(-1).url.searchParams.has("conversation_kind"),
    false,
  );
  assert.equal(
    sourceRequests(env).length,
    0,
    "all stored-history discovery stays independent of source reads",
  );
  const sourceSummary = () =>
    find(
      h,
      (node) => node.type === "summary" && text(node).startsWith("消息来源"),
    );
  sourceNeedsAttention = true;
  await click(h, button(h, "刷新消息中心"));
  assert.ok(
    text(sourceSummary()).includes("微信需检查"),
    "source errors stay visible in the collapsed summary",
  );
  sourceNeedsAttention = false;
  await click(h, button(h, "刷新消息中心"));
  assert.equal(text(sourceSummary()), "消息来源");
  assert.ok(env.requests.every((request) => request.method === "GET"));
  h.unmount();
  assert.equal(
    env.timers.size,
    0,
    "unmount must clean source polling and search debounce timers",
  );
}

// Out-of-order list and history searches, including cursor pages, after q/kind
// changes. Mock fetch ignores abort on purpose so a stale completion really runs.
{
  const pending = new Map();
  const held = (key) => {
    const gate = deferred();
    pending.set(key, gate);
    return gate.promise;
  };
  const env = environment((request) => {
    const { pathname, searchParams: p } = request.url;
    if (pathname === "/api/v1/inbox") return inboxInfo;
    if (pathname === "/api/v1/connections/wechat-db/status")
      return wechatStatus();
    if (pathname === "/api/v1/conversations") {
      const q = p.get("q") ?? "";
      const kind = p.get("conversation_kind");
      if (q === "旧查询") return held(`list-${kind}`);
      if (p.has("cursor")) return held("list-page");
      return page(
        [
          conversation({
            conversation_kind: kind,
            title: `最新列表 ${q || "默认"}`,
          }),
        ],
        "list-next",
      );
    }
    if (pathname.endsWith("/messages")) {
      const q = p.get("q") ?? "";
      if (q === "旧正文") return held("message-query");
      if (p.has("cursor")) return held("message-page");
      return page(
        [message({ body: `最新正文 ${q || "默认"}` })],
        "message-next",
      );
    }
    assert.fail(`unexpected race-test request ${request.url}`);
  });
  const h = mount(env, "Inbox.tsx", "Inbox", inboxProps());
  await h.flush();
  await search(h, env, "inbox-keyword", "旧查询");
  const privateOld = listRequests(env).at(-1);
  await fill(h, "inbox-kind", "group");
  const groupOld = listRequests(env).at(-1);
  assert.equal(privateOld.signal.aborted, true);
  await search(h, env, "inbox-keyword", "新查询");
  assert.equal(groupOld.signal.aborted, true);
  assert.ok(text(h.tree).includes("最新列表 新查询"));
  pending.get("list-group").resolve(
    page([
      conversation({
        conversation_kind: "group",
        title: "STALE_GROUP_QUERY",
      }),
    ]),
  );
  pending
    .get("list-private")
    .resolve(page([conversation({ title: "STALE_PRIVATE_KIND" })]));
  await h.flush();
  assert.ok(text(h.tree).includes("最新列表 新查询"));
  assert.ok(!text(h.tree).includes("STALE_"));
  await click(h, button(h, "加载更多会话"));
  const oldListPage = listRequests(env).at(-1);
  await search(h, env, "inbox-keyword", "更新查询");
  assert.equal(oldListPage.signal.aborted, true);
  pending.get("list-page").resolve(
    page([
      conversation({
        id: "stale-list-page",
        conversation_kind: "group",
        title: "STALE_LIST_PAGE",
      }),
    ]),
  );
  await h.flush();
  assert.ok(!text(h.tree).includes("STALE_"));
  assert.ok(text(h.tree).includes("最新列表 更新查询"));
  await click(h, listRows(h)[0]);
  await fill(h, "inbox-search-scope", "current");
  await search(h, env, "inbox-keyword", "旧正文");
  const oldMessageQuery = messageRequests(env).at(-1);
  await search(h, env, "inbox-keyword", "新正文");
  assert.equal(oldMessageQuery.signal.aborted, true);
  pending
    .get("message-query")
    .resolve(page([message({ body: "STALE_MESSAGE_QUERY" })]));
  await h.flush();
  assert.ok(text(h.tree).includes("最新正文 新正文"));
  assert.ok(!text(h.tree).includes("STALE_"));
  await click(h, button(h, "加载更早消息"));
  const oldMessagePage = messageRequests(env).at(-1);
  await fill(h, "inbox-kind", "private");
  assert.equal(oldMessagePage.signal.aborted, true);
  pending
    .get("message-page")
    .resolve(
      page([message({ id: "stale-history", body: "STALE_HISTORY_KIND" })]),
    );
  await h.flush();
  assert.ok(!text(h.tree).includes("STALE_"));
  assert.equal(
    nodes(h.tree).some((node) => node.props.id === "inbox-search-scope"),
    false,
  );
  assertKind(h, "inbox", "private");
  assert.ok(env.requests.every((request) => request.method === "GET"));
  h.unmount();
}

// WeChat metadata discovery is local after an explicit query-free sessions GET.
// Hidden selections cannot read even when submit/select handlers are invoked
// directly. Manual body reads and monitor writes keep separate explicit actions.
{
  const help = [];
  let monitor = monitorStatus();
  const sessions = [
    {
      peer_id: "wxid_synthetic_alpha",
      name: "Synthetic Alpha",
      conversation_kind: "private",
    },
    {
      peer_id: "wxid_CASE_ID_42",
      name: "合成乙",
      conversation_kind: "private",
    },
    {
      peer_id: "@@synthetic_group",
      name: "Synthetic GROUP%_ Team",
      conversation_kind: "group",
    },
    {
      peer_id: "@@synthetic_decoy",
      name: "Synthetic GROUPxxZ Team",
      conversation_kind: "group",
    },
  ];
  const env = environment((request) => {
    const path = request.url.pathname;
    if (path === "/api/v1/connections/wechat-db/status")
      return wechatStatus({ monitor });
    if (path === "/api/v1/connections/wechat-db/sessions")
      return { read_only: true, sending_enabled: false, items: sessions };
    if (path === "/api/v1/connections/wechat-db/read") return readResult;
    if (path === "/api/v1/connections/wechat-db/monitor") {
      monitor = monitorStatus({
        ...request.body,
        state: request.body.enabled ? "waiting" : "disabled",
      });
      return monitor;
    }
    assert.fail(`unexpected WeChat discovery request ${request.url}`);
  });
  const h = mount(env, "WechatPanel.tsx", "WechatPanel", wechatProps(help));
  await h.flush();
  assertKind(h, "wechat", "private");
  assertIconOnly(h, "更新状态", "refresh");
  field(h, "wechat-session-keyword");
  field(h, "wechat-keyword");
  assert.ok(text(h.tree).includes("还未读取会话"));
  assert.equal(button(h, "读取并保存消息").props.disabled, true);
  assert.equal(button(h, "开启后台读取").props.disabled, true);
  assert.ok(
    env.requests.every(
      (request) =>
        request.method === "GET" && request.url.pathname.endsWith("/status"),
    ),
  );
  const beforeHelp = env.requests.length;
  await click(h, button(h, "排查接入"));
  assert.equal(env.requests.length, beforeHelp);
  assert.equal(help.at(-1).source, "微信只读接入");
  let conditions = helpConditions(help.at(-1), [
    "conversation_kind",
    "session_keyword",
    "peer_id",
    "body_keyword",
  ]);
  assert.deepEqual(conditions, {
    conversation_kind: "private",
    session_keyword: "",
    peer_id: null,
    body_keyword: "",
  });
  await fill(h, "wechat-session-keyword", "gRoUp%_");
  await fill(h, "wechat-keyword", "正文选词");
  await fill(h, "wechat-kind", "group");
  assertKind(h, "wechat", "group");
  assert.equal(
    sourceRequests(env).length,
    0,
    "typing/kind selection never loads source sessions",
  );
  await click(h, button(h, "读取会话列表"));
  const sessionsRequest = env.requests.find((request) =>
    request.url.pathname.endsWith("/sessions"),
  );
  assert.equal(sessionsRequest.method, "GET");
  assert.equal(
    sessionsRequest.url.search,
    "",
    "UI filters must not change the sessions API contract",
  );
  const options = () =>
    nodes(field(h, "wechat-peer"))
      .filter((node) => node.type === "option" && node.props.value)
      .map((node) => node.props.value);
  assert.deepEqual(
    options(),
    ["@@synthetic_group"],
    "kind/name filtering is ASCII-insensitive and %/_ are literal",
  );
  assert.equal(field(h, "wechat-keyword").props.value, "正文选词");
  assert.ok(text(h.tree).includes("仅筛选本次读取的消息"));
  await fill(h, "wechat-peer", "@@synthetic_group");
  assert.equal(button(h, "读取并保存消息").props.disabled, false);
  const beforeSelectedHelp = env.requests.length;
  await click(h, button(h, "排查接入"));
  assert.equal(env.requests.length, beforeSelectedHelp);
  conditions = helpConditions(help.at(-1), [
    "conversation_kind",
    "session_keyword",
    "peer_id",
    "body_keyword",
  ]);
  assert.deepEqual(conditions, {
    conversation_kind: "group",
    session_keyword: "gRoUp%_",
    peer_id: "@@synthetic_group",
    body_keyword: "正文选词",
  });
  assert.ok(
    !help.at(-1).prompt.includes("Synthetic GROUP%_ Team"),
    "handoff must not include session display names",
  );
  await fill(h, "wechat-session-keyword", "missing name");
  assert.equal(field(h, "wechat-peer").props.value, "");
  assert.equal(button(h, "读取并保存消息").props.disabled, true);
  assert.ok(text(h.tree).includes("当前群聊没有匹配"));
  const beforeHidden = env.requests.length;
  await submit(h, "wechat-read-form");
  // Inject the now-hidden id through the select handler: submit must still use
  // selectedVisible, not merely trust a nonempty peerID state value.
  field(h, "wechat-peer").props.onChange({
    target: { value: "@@synthetic_group" },
  });
  await h.flush();
  await submit(h, "wechat-read-form");
  button(h, "加入后台读取").props.onClick();
  await h.flush();
  assert.equal(
    env.requests.length,
    beforeHidden,
    "hidden selection must not submit a read or save monitor state",
  );
  assert.equal(button(h, "开启后台读取").props.disabled, true);
  await click(h, button(h, "排查接入"));
  assert.equal(
    helpConditions(help.at(-1), [
      "conversation_kind",
      "session_keyword",
      "peer_id",
      "body_keyword",
    ]).peer_id,
    null,
  );
  await fill(h, "wechat-session-keyword", "");
  assert.equal(
    field(h, "wechat-peer").props.value,
    "",
    "clearing the filter must not resurrect the old selection",
  );
  await fill(h, "wechat-peer", "@@synthetic_group");
  await fill(h, "wechat-kind", "private");
  assert.equal(
    field(h, "wechat-peer").props.value,
    "",
    "kind change clears the selected peer",
  );
  assert.deepEqual(options(), ["wxid_synthetic_alpha", "wxid_CASE_ID_42"]);
  await fill(h, "wechat-session-keyword", "case_id_42");
  assert.deepEqual(
    options(),
    ["wxid_CASE_ID_42"],
    "local lookup also matches peer IDs",
  );
  await fill(h, "wechat-peer", "wxid_CASE_ID_42");
  await fill(h, "wechat-session-keyword", "alpha");
  assert.equal(
    field(h, "wechat-peer").props.value,
    "",
    "name change clears even a previously valid peer",
  );
  assert.deepEqual(options(), ["wxid_synthetic_alpha"]);
  assert.equal(
    env.requests.filter((request) => request.url.pathname.endsWith("/sessions"))
      .length,
    1,
    "all subsequent metadata filtering stays local",
  );
  await fill(h, "wechat-session-keyword", "");
  await fill(h, "wechat-kind", "group");
  await fill(h, "wechat-peer", "@@synthetic_group");
  const beforeRead = env.requests.length;
  await submit(h, "wechat-read-form");
  assert.equal(env.requests.length, beforeRead + 1);
  const read = env.requests.at(-1);
  assert.equal(read.method, "POST");
  assert.equal(read.url.pathname, "/api/v1/connections/wechat-db/read");
  assert.equal(read.url.search, "");
  assert.deepEqual(read.body, {
    peer_id: "@@synthetic_group",
    keyword: "正文选词",
  });
  assert.equal(
    env.requests.filter((request) => request.url.pathname.endsWith("/monitor"))
      .length,
    0,
    "manual reads must not implicitly enable monitoring",
  );
  const beforeAdd = env.requests.length;
  await click(h, button(h, "加入后台读取"));
  assert.equal(
    env.requests.length,
    beforeAdd,
    "adding an explicit peer only edits the local monitor draft",
  );
  find(
    h,
    (node) =>
      node.type === "ul" && node.props["aria-label"] === "已选择的后台读取会话",
  );
  const remove = assertIconOnly(h, "移除会话 @@synthetic_group", "close");
  await click(h, remove);
  assert.equal(button(h, "开启后台读取").props.disabled, true);
  assert.equal(
    env.requests.length,
    beforeAdd,
    "removing a peer only edits the local monitor draft",
  );
  await click(h, button(h, "加入后台读取"));
  assert.equal(
    env.requests.length,
    beforeAdd,
    "re-adding a peer does not save or start background reads",
  );
  assert.equal(button(h, "开启后台读取").props.disabled, false);
  await fill(h, "wechat-kind", "private");
  assert.equal(button(h, "读取并保存消息").props.disabled, true);
  assert.equal(
    button(h, "开启后台读取").props.disabled,
    false,
    "monitor peer draft remains separate from the manual selector",
  );
  await click(h, button(h, "开启后台读取"));
  const monitorWrites = env.requests.filter((request) =>
    request.url.pathname.endsWith("/monitor"),
  );
  assert.equal(monitorWrites.length, 1);
  assert.equal(monitorWrites[0].method, "POST");
  assert.equal(
    monitorWrites[0].url.pathname,
    "/api/v1/connections/wechat-db/monitor",
  );
  assert.equal(monitorWrites[0].url.search, "");
  assert.deepEqual(monitorWrites[0].body, {
    enabled: true,
    peer_ids: ["@@synthetic_group"],
    interval_seconds: 30,
  });
  assert.equal(
    env.requests.filter((request) => request.url.pathname.endsWith("/read"))
      .length,
    1,
  );
  const beforeFinalHelp = env.requests.length;
  await click(h, button(h, "排查接入"));
  assert.equal(
    env.requests.length,
    beforeFinalHelp,
    "help never invokes model, read or monitor endpoints",
  );
  h.unmount();
  assert.equal(env.timers.size, 0);
  assert.equal(h.postUnmountWrites, 0);
}

for (const env of testEnvironments) {
  assert.equal(
    env.transportFailures.length,
    0,
    env.transportFailures
      .map((failure) => failure.stack ?? String(failure))
      .join("\n"),
  );
  assert.equal(
    env.timers.size,
    0,
    "all synthetic environments must finish without outstanding timers",
  );
}

console.log(
  "Chat discovery synthetic regressions passed: conversation type selects, one search field with independent scopes, query URLs, bounded snippets, literal highlights, 256-byte/NUL guards, pagination, empty states, safe UI-only help, explicit source actions, and stale response cancellation. Synthetic data only.",
);
