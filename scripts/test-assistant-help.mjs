import assert from "node:assert/strict";
import {
  TOKEN,
  NOW,
  transport,
  componentHarness,
  nodes,
  text,
  field,
  button,
  checkbox,
  check,
  click,
  fill,
  event,
} from "./feishu-test-harness.mjs";
import {
  browserHarness,
  nodes as appNodes,
  find as appFind,
  settings,
} from "./test-service-session.mjs";

// Synthetic execution of the shipped TS/TSX and authorized clients. No service,
// model, account, browser, desktop runtime or business data is contacted here.
// Focus assertions below check intent only, not native dialog/keyboard behavior.
const HELP_EXPORTS = [
  "ASSISTANT_MESSAGE_BYTE_LIMIT",
  "inspectAssistantMessage",
  "normalizeAssistantHelpRequest",
  "prepareAssistantHelpDraft",
];
const API_EXPORTS = [
  "AssistantRequestError",
  "isAssistantBusinessAuthFailure",
  "getAssistantStatus",
  "createAssistantRun",
  "getAssistantRun",
  "confirmAssistantAction",
  "rejectAssistantAction",
];
const FUTURE = "2099-12-31T23:59:59Z";
const status = () => ({
  configured: true,
  model: "synthetic-help-model",
  endpoint_host: "synthetic-help.invalid",
  external: true,
  model_config_id: "synthetic-help-config",
  model_config_revision: 0,
  daily_limit: 50,
  used_today: 2,
  session_ttl_seconds: 1800,
  sending_enabled: false,
  operations: [],
});
const action = () => ({
  id: "synthetic-help-action",
  operation_id: "pause_synthetic",
  method: "PATCH",
  path: "/api/v1/synthetic",
  arguments: { id: "synthetic-record", paused: true },
  effects: ["暂停这条合成记录"],
  expires_at: FUTURE,
});
const run = (state = "completed", extra = {}) => ({
  session_id: "synthetic-help-session",
  run_id: "synthetic-help-run",
  state,
  assistant_message: "合成模型文字不是操作成功凭证。",
  tool_results: [],
  pending_action: state === "awaiting_confirmation" ? action() : null,
  model_config_id: status().model_config_id,
  model_config_revision: 0,
  expires_at: FUTURE,
  error: null,
  ...extra,
});
const reply = (payload, code = 200) => ({
  ok: code >= 200 && code < 300,
  status: code,
  json: async () => structuredClone(payload),
});
const task = (prompt = "只检查当前已选的合成会话，不要发送消息。") => ({
  source: "消息 / 合成会话 synthetic-conversation",
  prompt,
});
const turn = (snapshot, extra = {}) => ({
  key: "synthetic-help-turn",
  prompt: "先前明确提交的合成任务",
  runID: snapshot?.run_id ?? null,
  run: snapshot,
  uncertain: false,
  notice: null,
  ...extra,
});
const roundConsent = "我同意本轮调用";
const actionConsent = "我已核对工具、目标、完整参数和上述影响";

async function panelFor({
  draft = "",
  turns = [],
  helpRequest = null,
  token = TOKEN,
  strict = false,
} = {}) {
  const browser = transport();
  browser.respond(() => reply(status()));
  const api = browser.load("assistant-api.ts", API_EXPORTS);
  const helpers = browser.load("assistant-help.ts", HELP_EXPORTS);
  const initial = {
    sessionID: "synthetic-help-session",
    turns,
    draft,
    recoveryID: "",
  };
  const cache = new Map([[token, initial]]);
  let authFailures = 0,
    settingsOpened = 0,
    dismissed = 0,
    focusTarget = null;
  const panel = componentHarness(
    "AssistantPanel.tsx",
    "AssistantPanel",
    { ...api, ...helpers },
    {
      token,
      cache,
      helpRequest,
      visible: true,
      onAuthFailure() {
        authFailures++;
      },
      onSettings() {
        settingsOpened++;
      },
      onDismissHelp() {
        dismissed++;
      },
    },
    browser,
    strict,
  );
  const flush = panel.flush.bind(panel);
  panel.flush = async () => {
    await flush();
    // Add only an observed focus-intent sink. This does not emulate a browser's
    // focus algorithm, layout, dialog top layer or native keyboard navigation.
    for (const node of nodes(panel.tree))
      node.element.focus = () => {
        focusTarget = node.props.id ?? null;
      };
  };
  await panel.flush();
  return {
    panel,
    browser,
    cache,
    initial,
    get workspace() {
      return cache.get(token);
    },
    get focusTarget() {
      return focusTarget;
    },
    get dismissed() {
      return dismissed;
    },
    get settingsOpened() {
      return settingsOpened;
    },
    finish() {
      panel.unmount();
      assert.equal(panel.postUnmountWrites, 0);
      assert.equal(browser.storageReads, 0);
      assert.equal(authFailures, 0);
    },
  };
}
async function submitRound(panel) {
  const form = nodes(panel.tree).find(
    (node) =>
      node.type === "form" && node.props.className === "assistant-composer",
  );
  assert.ok(form);
  form.props.onSubmit(event());
  await panel.flush();
}

// 1. The handoff's allowlist is source + explicit prompt, not the source object.
{
  const browser = transport();
  const {
    ASSISTANT_MESSAGE_BYTE_LIMIT: limit,
    inspectAssistantMessage: inspect,
    normalizeAssistantHelpRequest: normalize,
    prepareAssistantHelpDraft: merge,
  } = browser.load("assistant-help.ts", HELP_EXPORTS);
  assert.equal(limit, 8000);
  const request = {
    source: "  消息  ",
    prompt: "  检查已选会话 synthetic-id，类型 private，平台 qq。  ",
    get messages() {
      throw Error("Must not collect chat bodies");
    },
    get draft() {
      throw Error("Must not collect business drafts");
    },
    credential: "synthetic-never-forwarded",
    error: "synthetic-never-forwarded",
  };
  assert.deepEqual(structuredClone(normalize(request)), {
    source: "消息",
    prompt: "检查已选会话 synthetic-id，类型 private，平台 qq。",
  });
  for (const invalid of [
    null,
    {},
    { source: "消息", prompt: "" },
    { source: 42, prompt: "x" },
    { source: "x".repeat(121), prompt: "x" },
    { source: "消息", prompt: "x".repeat(6001) },
  ])
    assert.equal(
      normalize(invalid),
      null,
      "invalid handoffs are not truncated",
    );
  const help = { source: "消息", prompt: "合成任务" };
  assert.equal(merge("", help, "fill", false).draft, "来源：消息\n合成任务");
  for (const existing of ["原助手草稿", "  \n"]) {
    assert.equal(merge(existing, help, "fill", false).reason, "nonempty");
    assert.equal(
      merge(existing, help, "append", false).draft,
      `${existing}\n\n来源：消息\n合成任务`,
    );
  }
  assert.equal(merge("", help, "fill", true).reason, "unresolved");
  const suffix = "\n\n来源：消息\n合成任务";
  const boundary = "x".repeat(8000 - Buffer.byteLength(suffix, "utf8"));
  const merged = merge(boundary, help, "append", false);
  assert.equal(merged.draft, `${boundary}${suffix}`);
  assert.equal(Buffer.byteLength(merged.draft, "utf8"), 8000);
  assert.equal(merge(`${boundary}x`, help, "append", false).reason, "too_long");
  for (const unit of ["中", "😀"]) {
    const unitBytes = Buffer.byteLength(unit, "utf8");
    const sized = (bytes) =>
      unit.repeat(Math.floor(bytes / unitBytes)) +
      "a".repeat(bytes % unitBytes);
    const exact = sized(8000);
    assert.equal(inspect(exact).bytes, Buffer.byteLength(exact, "utf8"));
    assert.equal(inspect(exact).issue, null);
    assert.equal(inspect(`${exact}x`).bytes, 8001);
    assert.equal(inspect(`${exact}x`).issue, "too_long");
    const prefix = "来源：消息\n";
    const exactTask = {
      source: "消息",
      prompt: sized(8000 - Buffer.byteLength(prefix, "utf8")),
    };
    assert.ok(normalize(exactTask), "source/prompt rules stay unchanged");
    const filled = merge("", exactTask, "fill", false);
    assert.equal(filled.draft, `${prefix}${exactTask.prompt}`);
    assert.equal(Buffer.byteLength(filled.draft, "utf8"), 8000);
    assert.equal(
      merge("", { ...exactTask, prompt: `${exactTask.prompt}x` }, "fill", false)
        .reason,
      "too_long",
    );
    const original = sized(8000 - Buffer.byteLength(suffix, "utf8"));
    const appended = merge(original, help, "append", false);
    assert.equal(appended.draft, `${original}${suffix}`);
    assert.equal(Buffer.byteLength(appended.draft, "utf8"), 8000);
    assert.equal(
      merge(`${original}x`, help, "append", false).reason,
      "too_long",
    );
  }
  for (const [draft, request] of [
    ["原\0草稿", help],
    ["", { source: "消\0息", prompt: "合成任务" }],
    ["", { source: "消息", prompt: "合成\0任务" }],
  ])
    assert.equal(
      merge(draft, request, draft ? "append" : "fill", false).reason,
      "nul",
    );
  assert.equal(inspect("合成\0输入").issue, "nul");
  assert.equal(
    inspect("合成\\0输入").issue,
    null,
    "literal backslash is not NUL",
  );
  assert.equal(browser.requests.length, 0);
  assert.equal(browser.storageReads, 0);
}

// 2. Opening/closing help changes no business route, key, draft map or workspace.
// The App harness does not mount Inbox or a real dialog; browser preservation,
// native Escape/focus return, mobile overflow and scroll are separate checks.
{
  const app = browserHarness({ mode: "app" });
  app.evaluate("assistant-help.ts", HELP_EXPORTS);
  await app.flush();
  appFind(
    app,
    (node) => node.type === "button" && node.props["aria-label"] === "消息",
  ).props.onClick();
  await app.flush();
  const inbox = appFind(app, (node) => node.type === "InboxStub");
  inbox.props.aiDrafts.set("synthetic-conversation", {
    draft: "业务草稿不带走",
  });
  const request = task();
  inbox.props.onAssistantHelp({
    ...request,
    chat_body: "不得自动采集",
    token: "不得转交",
  });
  await app.flush();
  const assistant = () =>
    appFind(app, (node) => node.type === "AssistantPanelStub");
  assert.equal(assistant().props.visible, true);
  assert.deepEqual(structuredClone(assistant().props.helpRequest), request);
  const sameInbox = appFind(app, (node) => node.type === "InboxStub");
  assert.equal(sameInbox.props.key, inbox.props.key);
  assert.equal(sameInbox.props.aiDrafts, inbox.props.aiDrafts);
  assert.equal(
    sameInbox.props.aiDrafts.get("synthetic-conversation").draft,
    "业务草稿不带走",
  );
  const sameCache = assistant().props.cache;
  const cached = { draft: "已有助手草稿", turns: [turn(run("unknown"))] };
  sameCache.set(assistant().props.token, cached);
  const dialog = () => appFind(app, (node) => node.type === "dialog");
  assert.equal(dialog().props["aria-labelledby"], "assistant-title");
  const close = appFind(
    app,
    (node) =>
      node.type === "button" &&
      (node.props["aria-label"] ?? text(node)).trim() === "关闭助手",
  );
  assert.equal(close.props.title, "关闭助手");
  assert.equal(text(close).trim(), "");
  assert.ok(
    appNodes(close).some(
      (node) =>
        node.type === "svg" &&
        node.props["data-icon"] === "close" &&
        node.props["aria-hidden"] === "true" &&
        node.props.focusable === "false",
    ),
    "close control must render the shipped decorative SVG",
  );
  dialog().props.onClose();
  await app.flush();
  assert.equal(assistant().props.visible, false);
  assert.equal(assistant().props.cache, sameCache);
  assert.equal(sameCache.get(assistant().props.token), cached);
  appFind(app, (node) =>
    node.props.className?.includes("nav-assistant"),
  ).props.onClick();
  await app.flush();
  assert.equal(assistant().props.visible, true);
  assert.deepEqual(structuredClone(assistant().props.helpRequest), request);
  assert.ok(
    !appNodes(app.value).some((node) =>
      node.props.className?.includes("topbar-assistant"),
    ),
    "messages already has contextual help and does not repeat it in the header",
  );
  appFind(
    app,
    (node) => node.type === "button" && node.props["aria-label"] === "概览",
  ).props.onClick();
  await app.flush();
  const pageHelp = appFind(app, (node) =>
    node.props.className?.includes("topbar-assistant"),
  );
  assert.equal(text(pageHelp).trim(), "页面求助");
  pageHelp.props.onClick();
  await app.flush();
  assert.equal(assistant().props.helpRequest.source, "概览");
  assert.equal(Object.keys(assistant().props.helpRequest).length, 2);
  assert.ok(
    !JSON.stringify(assistant().props.helpRequest).includes("业务草稿不带走"),
  );
  assert.equal(sameCache.get(assistant().props.token), cached);
  assert.equal(app.requests.length, 0, "opening UI does not submit anything");
  // Contacts forwards the same contextual-help callback contract.
  dialog().props.onClose();
  await app.flush();
  appFind(
    app,
    (node) => node.type === "button" && node.props["aria-label"] === "联系人",
  ).props.onClick();
  await app.flush();
  assert.equal(
    typeof appFind(app, (node) => node.type === "InboxStub").props
      .onAssistantHelp,
    "function",
  );
  assert.ok(
    !appNodes(app.value).some((node) =>
      node.props.className?.includes("topbar-assistant"),
    ),
    "contacts already has contextual help and does not repeat it in the header",
  );
  // A different authorization identity must not inherit the old pending task.
  appFind(
    app,
    (node) => node.type === "button" && node.props["aria-label"] === "设置",
  ).props.onClick();
  await app.flush();
  settings(app).onDraftChange("synthetic-new-help-identity");
  await app.flush();
  await settings(app).onConnect(event());
  await app.flush();
  assert.ok(
    !appNodes(app.value).some((node) => node.type === "AssistantPanelStub"),
  );
  appFind(app, (node) =>
    node.props.className?.includes("nav-assistant"),
  ).props.onClick();
  await app.flush();
  assert.equal(assistant().props.helpRequest, null);
  assert.notEqual(assistant().props.token, "");
  assert.equal(sameCache.size, 0);
  app.unmount();
  assert.equal(app.postUnmountStateWrites, 0);
}

// 3. Empty drafts require a click; bringing a task neither submits nor consents.
{
  const env = await panelFor({ helpRequest: task(), strict: true });
  const { panel, browser } = env;
  assert.equal(field(panel, "assistant-message").props.value, "");
  assert.ok(text(panel.tree).includes(`带入${task().source}？`));
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(checkbox(panel, roundConsent)._disabled, true);
  await submitRound(panel);
  assert.equal(browser.writes().length, 0);
  await click(panel, "带入");
  assert.equal(env.workspace.draft, `来源：${task().source}\n${task().prompt}`);
  assert.equal(env.dismissed, 1);
  assert.equal(env.focusTarget, "assistant-message");
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(button(panel, "提交")._disabled, true);
  assert.equal(browser.writes().length, 0);
  browser.respond(({ options }) =>
    options.method === "GET"
      ? reply(status())
      : reply(run("awaiting_confirmation"), 202),
  );
  await check(panel, roundConsent);
  await submitRound(panel);
  const request = browser.writes()[0];
  assert.equal(new URL(request.url).pathname, "/api/v1/assistant/runs");
  const body = JSON.parse(request.options.body);
  assert.deepEqual(Object.keys(body).sort(), [
    "confirmed",
    "message",
    "model_config_id",
    "model_config_revision",
    "session_id",
  ]);
  assert.equal(body.message, `来源：${task().source}\n${task().prompt}`);
  assert.equal(body.model_config_revision, 0);
  assert.equal(checkbox(panel, actionConsent).props.checked, false);
  assert.equal(button(panel, "确认")._disabled, true);
  assert.equal(
    browser.writes().length,
    1,
    "no mutation confirmation is implied",
  );
  env.finish();
}

// 4. Existing text (including whitespace) is preserved; append is explicit.
{
  const original = "  \n原有助手草稿，不得替换。\n";
  const env = await panelFor({ draft: original });
  const { panel, browser } = env;
  await check(panel, roundConsent);
  panel.updateProps({ helpRequest: task() });
  await panel.flush();
  assert.equal(env.workspace.draft, original);
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(button(panel, "提交")._disabled, true);
  await click(panel, "追加");
  assert.equal(
    env.workspace.draft,
    `${original}\n\n来源：${task().source}\n${task().prompt}`,
  );
  assert.equal(browser.writes().length, 0);
  panel.updateProps({ helpRequest: task("第二项合成任务") });
  await panel.flush();
  const beforeDismiss = env.workspace;
  await click(panel, "忽略");
  assert.equal(env.workspace, beforeDismiss);
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(env.focusTarget, "assistant-message");
  env.finish();
}
{
  const original = "x".repeat(7995);
  const env = await panelFor({ draft: original, helpRequest: task() });
  assert.equal(button(env.panel, "追加")._disabled, true);
  button(env.panel, "追加").props.onClick();
  await env.panel.flush();
  assert.equal(env.workspace.draft, original);
  assert.ok(text(env.panel.tree).includes("不会截断或覆盖内容"));
  assert.equal(env.browser.writes().length, 0);
  await fill(env.panel, "assistant-message", "手动精简后的草稿");
  await click(env.panel, "追加");
  assert.ok(env.workspace.draft.startsWith("手动精简后的草稿\n\n来源："));
  env.finish();
}

// 5. Active, awaiting-confirmation, unknown, lost-response and expired runs must
// be handled first. A prepared task cannot reset/replace them or replay writes.
for (const snapshot of [
  run("running"),
  run("awaiting_confirmation"),
  run("unknown"),
  null,
  run("completed", { expires_at: new Date(NOW - 1000).toISOString() }),
]) {
  const originalTurn = turn(snapshot, snapshot ? {} : { uncertain: true });
  const env = await panelFor({ turns: [originalTurn], helpRequest: task() });
  const { panel, browser } = env;
  assert.equal(button(panel, "带入")._disabled, true);
  button(panel, "带入").props.onClick();
  await panel.flush();
  assert.equal(env.workspace.turns[0], originalTurn);
  assert.equal(env.workspace.draft, "");
  panel.updateProps({ visible: false });
  await panel.flush();
  panel.updateProps({ visible: true });
  await panel.flush();
  assert.equal(env.workspace.turns[0], originalTurn);
  assert.equal(browser.writes().length, 0);
  env.finish();
}
{
  const env = await panelFor({
    turns: [turn(run("unknown"), { uncertain: true })],
    helpRequest: task(),
  });
  const { panel, browser } = env;
  browser.respond(({ url }) =>
    reply(new URL(url).pathname.endsWith("/status") ? status() : run()),
  );
  await click(panel, "查看结果");
  assert.equal(browser.writes().length, 0);
  assert.ok(browser.requests.at(-1).url.endsWith("/runs/synthetic-help-run"));
  assert.equal(browser.requests.at(-1).options.method, "GET");
  assert.equal(button(panel, "带入")._disabled, false);
  await click(panel, "带入");
  assert.equal(env.workspace.turns[0].run.run_id, "synthetic-help-run");
  assert.equal(env.workspace.turns[0].run.state, "completed");
  assert.equal(browser.writes().length, 0);
  env.finish();
}

// 6. New tasks and close/reopen invalidate per-action acknowledgments without
// changing the immutable preview. Explicit confirmation keeps the real body.
{
  const pending = run("awaiting_confirmation");
  const env = await panelFor({ turns: [turn(pending)] });
  const { panel, browser } = env;
  await check(panel, actionConsent);
  panel.updateProps({ helpRequest: task() });
  await panel.flush();
  assert.equal(checkbox(panel, actionConsent).props.checked, false);
  assert.equal(env.workspace.turns[0].run, pending);
  await check(panel, actionConsent);
  panel.updateProps({ visible: false });
  await panel.flush();
  assert.equal(checkbox(panel, actionConsent).props.checked, false);
  assert.equal(button(panel, "确认")._disabled, true);
  button(panel, "确认").props.onClick();
  await panel.flush();
  assert.equal(browser.writes().length, 0);
  panel.updateProps({ visible: true });
  await panel.flush();
  assert.equal(checkbox(panel, actionConsent).props.checked, false);
  assert.equal(button(panel, "带入")._disabled, true);
  browser.respond(({ options }) =>
    reply(options.method === "GET" ? status() : run()),
  );
  await check(panel, actionConsent);
  await click(panel, "确认");
  assert.equal(browser.writes().length, 1);
  const request = browser.writes()[0];
  assert.ok(request.url.endsWith("/runs/synthetic-help-run/confirm"));
  assert.deepEqual(JSON.parse(request.options.body), {
    action_id: pending.pending_action.id,
    model_config_id: pending.model_config_id,
    model_config_revision: 0,
    confirmed: true,
  });
  assert.equal(env.workspace.turns[0].run.state, "completed");
  env.finish();
}

// 7. Closing while a submitted request is in flight hides only the panel. It
// neither aborts backend work nor starts another request when opened again.
{
  const env = await panelFor({ draft: "明确提交一次的合成任务" });
  const { panel, browser } = env;
  let complete;
  browser.respond(({ options }) =>
    options.method === "GET"
      ? reply(status())
      : new Promise((resolve) => {
          complete = resolve;
        }),
  );
  await check(panel, roundConsent);
  await submitRound(panel);
  const submitted = browser.writes()[0];
  assert.equal(submitted.options.signal.aborted, false);
  panel.updateProps({ visible: false, helpRequest: task() });
  await panel.flush();
  assert.equal(submitted.options.signal.aborted, false);
  assert.equal(env.workspace.turns.length, 1);
  complete(reply(run()));
  await panel.flush();
  assert.equal(env.workspace.turns[0].run.state, "completed");
  panel.updateProps({ visible: true });
  await panel.flush();
  assert.equal(browser.writes().length, 1);
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(env.workspace.draft, "");
  await click(panel, "带入");
  assert.equal(browser.writes().length, 1);
  env.finish();
}

// 8. Without authorization the task is visible, never sent, and configuration
// remains a manual action; dismissing it grants no service or model permission.
{
  const env = await panelFor({ token: "", helpRequest: task() });
  assert.ok(text(env.panel.tree).includes(task().prompt));
  assert.equal(button(env.panel, "带入")._disabled, true);
  assert.equal(env.browser.requests.length, 0);
  await click(env.panel, "去设置");
  assert.equal(env.settingsOpened, 1);
  assert.equal(env.browser.requests.length, 0);
  await click(env.panel, "忽略");
  assert.equal(env.focusTarget, "assistant-title");
  assert.equal(env.workspace.draft, "");
  env.finish();
}

// 9. UTF-8 limits apply to the complete handoff and current composer, not JS
// length. Disabled controls are bypassed below to exercise each real handler.
for (const unit of ["中", "😀"]) {
  const help = { source: "消息", prompt: "合成任务" };
  const suffix = "\n\n来源：消息\n合成任务";
  const unitBytes = Buffer.byteLength(unit, "utf8");
  const sized = (bytes) =>
    unit.repeat(Math.floor(bytes / unitBytes)) + "a".repeat(bytes % unitBytes);
  const exact = sized(8000 - Buffer.byteLength(suffix, "utf8"));
  const original = `${exact}x`;
  const env = await panelFor({ draft: original, helpRequest: help });
  const { panel, browser } = env;
  assert.equal(Buffer.byteLength(`${original}${suffix}`, "utf8"), 8001);
  assert.ok(`${original}${suffix}`.length < 8000);
  assert.equal(button(panel, "追加")._disabled, true);
  assert.ok(text(panel.tree).includes("字节（UTF-8）"));
  const before = env.workspace;
  button(panel, "追加").props.onClick();
  await panel.flush();
  assert.equal(
    env.workspace,
    before,
    "overflow never modifies the cached draft",
  );
  assert.equal(browser.writes().length, 0);
  await fill(panel, "assistant-message", exact);
  assert.equal(button(panel, "追加")._disabled, false);
  await click(panel, "追加");
  assert.equal(env.workspace.draft, `${exact}${suffix}`);
  assert.equal(Buffer.byteLength(env.workspace.draft, "utf8"), 8000);
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(browser.writes().length, 0);
  env.finish();

  const prefix = "来源：消息\n";
  const prompt = sized(8000 - Buffer.byteLength(prefix, "utf8"));
  const fillEnv = await panelFor({
    helpRequest: { source: "消息", prompt: `${prompt}x` },
  });
  assert.equal(button(fillEnv.panel, "带入")._disabled, true);
  assert.ok(text(fillEnv.panel.tree).includes(`${prompt}x`));
  button(fillEnv.panel, "带入").props.onClick();
  await fillEnv.panel.flush();
  assert.equal(fillEnv.workspace.draft, "");
  assert.equal(fillEnv.browser.writes().length, 0);
  fillEnv.panel.updateProps({ helpRequest: { source: "消息", prompt } });
  await fillEnv.panel.flush();
  await click(fillEnv.panel, "带入");
  assert.equal(fillEnv.workspace.draft, `${prefix}${prompt}`);
  assert.equal(Buffer.byteLength(fillEnv.workspace.draft, "utf8"), 8000);
  assert.equal(fillEnv.browser.writes().length, 0);
  fillEnv.finish();
}
for (const [exact, invalid, issue] of [
  ["中".repeat(2666) + "ab", "中".repeat(2666) + "abx", "too_long"],
  ["😀".repeat(2000), "😀".repeat(2000) + "x", "too_long"],
  ["合成输入", "合成\0输入", "nul"],
]) {
  const env = await panelFor({ draft: exact });
  const { panel, browser } = env;
  const textarea = field(panel, "assistant-message");
  assert.equal(textarea.props.maxLength, undefined, "never truncate a paste");
  assert.equal(textarea.props["aria-invalid"], false);
  assert.equal(textarea.props["aria-describedby"], "assistant-message-limit");
  await check(panel, roundConsent);
  const staleSubmit = nodes(panel.tree).find(
    (node) =>
      node.type === "form" && node.props.className === "assistant-composer",
  ).props.onSubmit;
  field(panel, "assistant-message").props.onChange({
    target: { value: invalid },
  });
  staleSubmit(event());
  await panel.flush();
  assert.equal(
    env.workspace.draft,
    invalid,
    "latest draft survives a stale handler",
  );
  assert.equal(env.workspace.turns.length, 0);
  assert.equal(browser.writes().length, 0);
  assert.equal(field(panel, "assistant-message").props["aria-invalid"], true);
  assert.equal(
    field(panel, "assistant-message").props["aria-describedby"],
    "assistant-message-limit assistant-message-error",
  );
  const error = nodes(panel.tree).find(
    (node) => node.props.id === "assistant-message-error",
  );
  assert.ok(
    text(error).includes(
      issue === "nul" ? "空字符（NUL）" : "超过 8000 字节（UTF-8）",
    ),
  );
  assert.ok(
    text(panel.tree).includes(
      `${Buffer.byteLength(invalid, "utf8")} / 8000 字节（UTF-8）`,
    ),
  );
  assert.equal(button(panel, "提交")._disabled, true);
  assert.equal(checkbox(panel, roundConsent)._disabled, true);
  checkbox(panel, roundConsent).props.onChange({ target: { checked: true } });
  await panel.flush();
  await submitRound(panel);
  assert.equal(
    env.workspace.draft,
    invalid,
    "direct submit cannot clear invalid input",
  );
  assert.equal(env.workspace.turns.length, 0);
  assert.equal(browser.writes().length, 0);
  await fill(panel, "assistant-message", exact);
  assert.equal(field(panel, "assistant-message").props["aria-invalid"], false);
  assert.equal(
    field(panel, "assistant-message").props["aria-describedby"],
    "assistant-message-limit",
  );
  assert.equal(
    nodes(panel.tree).some(
      (node) => node.props.id === "assistant-message-error",
    ),
    false,
  );
  assert.equal(checkbox(panel, roundConsent).props.checked, false);
  assert.equal(button(panel, "提交")._disabled, true);
  browser.respond(({ options }) =>
    options.method === "GET" ? reply(status()) : reply(run()),
  );
  await check(panel, roundConsent);
  await submitRound(panel);
  assert.equal(browser.writes().length, 1);
  assert.equal(JSON.parse(browser.writes()[0].options.body).message, exact);
  assert.equal(env.workspace.turns[0].prompt, exact);
  env.finish();
}
{
  const helpRequest = { source: "消息", prompt: "合成\0任务" };
  const env = await panelFor({ helpRequest });
  assert.equal(button(env.panel, "带入")._disabled, true);
  assert.ok(text(env.panel.tree).includes("空字符（NUL）"));
  button(env.panel, "带入").props.onClick();
  await env.panel.flush();
  assert.equal(env.workspace.draft, "");
  assert.equal(env.browser.writes().length, 0);
  env.finish();
}

console.log(
  "Assistant-help synthetic checks passed (9 groups): allowlisted visible context, stable business shell, explicit fill/append without overwrite or truncation, exact Chinese/emoji UTF-8 boundaries and NUL rejection, current-draft submit guards, no automatic submit/consent, unresolved/expired protection, GET-only recovery, fresh immutable-action acknowledgments, close-during-flight preservation and manual configuration. Native dialog, layout, scroll, real models/accounts and Tauri remain separate acceptance checks.",
);
