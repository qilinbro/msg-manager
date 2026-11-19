import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import {
  AssistantRequestError,
  confirmAssistantAction,
  createAssistantRun,
  getAssistantRun,
  getAssistantStatus,
  isAssistantBusinessAuthFailure,
  rejectAssistantAction,
} from './assistant-api';
import type {
  AssistantAction,
  AssistantJSON,
  AssistantRun,
  AssistantStatus,
} from './assistant-api';
import {
  ASSISTANT_MESSAGE_BYTE_LIMIT,
  inspectAssistantMessage,
  normalizeAssistantHelpRequest,
  prepareAssistantHelpDraft,
} from './assistant-help';
import type { AssistantHelpRequest } from './assistant-help';
import { Icon } from './Icon';
import './assistant.css';

interface AssistantTurn {
  key: string;
  prompt: string | null;
  runID: string | null;
  run: AssistantRun | null;
  notice: string | null;
  uncertain: boolean;
}
export interface AssistantWorkspace {
  sessionID: string;
  turns: AssistantTurn[];
  draft: string;
  recoveryID: string;
}
const emptyWorkspace = (): AssistantWorkspace => ({
  sessionID: '',
  turns: [],
  draft: '',
  recoveryID: '',
});
const RUN_LABELS: Record<AssistantRun['state'], string> = {
  running: '运行中',
  awaiting_confirmation: '等待逐项确认',
  completed: '本轮已完成',
  failed: '本轮失败',
  rejected: '已拒绝此项',
  unknown: '结果未知，需核实',
};
const TOOL_LABELS = {
  succeeded: '工具已完成',
  failed: '工具失败',
  pending: '等待处理',
  rejected: '已拒绝',
  unknown: '结果未知',
};
const EFFECT_LABELS: Record<string, string> = {
  read: '读取本地资料',
  write: '修改本地资料',
  external_read: '连接外部服务',
  model_call: '调用模型 / 可能计费',
  credential: '凭据与授权',
};
const AVAILABILITY_LABELS: Record<string, string> = {
  available: '可读取',
  confirmation_required: '逐项确认',
  human_only: '仅限人工操作',
  not_implemented: '尚未实现',
};
const formatTime = (value: string) =>
  new Date(value).toLocaleString('zh-CN', { hour12: false });
const unresolved = (turn: AssistantTurn | undefined): boolean =>
  Boolean(
    turn &&
    (turn.uncertain ||
      (!turn.run && turn.runID) ||
      turn.run?.state === 'running' ||
      turn.run?.state === 'awaiting_confirmation' ||
      turn.run?.state === 'unknown' ||
      turn.run?.tool_results.some((item) => item.state === 'unknown')),
  );

const PARAMETER_LABELS: Record<string, string> = {
  enabled: '启用',
  mode: '模式',
  text: '完整正文',
  report_preview: '发送内容',
  report_id: '报告编号',
  report_hash: '正文校验值',
  body_hash: '正文校验值',
  content_hash: '正文校验值',
  target: '目标',
  target_preview: '目标信息',
  recipient: '收件人',
  recipient_preview: '收件人信息',
  recipient_id: '收件人编号',
  open_id: '账号标识',
  redacted_target: '脱敏目标',
  window_start: '窗口开始',
  window_end: '窗口结束',
  timezone: '时区',
  schedule_version: '计划版本',
  config_revision: '配置版本',
  model_config_id: '模型配置编号',
  model_config_revision: '模型配置版本',
  conversation_id: '会话编号',
  contact_id: '联系人编号',
  limit: '数量上限',
};

// Render every immutable field without exposing a business JSON dump. React
// escapes values, including multiline text; no field is editable or shortened.
function ParameterValue({ value }: { value: AssistantJSON }) {
  if (Array.isArray(value))
    return value.length ? (
      <ol className="assistant-parameter-list" data-value-kind="array">
        {value.map((item, index) => (
          <li key={index}>
            <ParameterValue value={item} />
          </li>
        ))}
      </ol>
    ) : (
      <ol
        className="assistant-parameter-list"
        data-value-kind="array"
        aria-label="空列表"
      />
    );
  if (value !== null && typeof value === 'object')
    return (
      <dl className="assistant-parameters" data-value-kind="object">
        {Object.entries(value).map(([key, item]) => (
          <div key={key} data-parameter-key={key}>
            <dt>
              {Object.hasOwn(PARAMETER_LABELS, key)
                ? PARAMETER_LABELS[key]
                : key}
            </dt>
            <dd>
              <ParameterValue value={item} />
            </dd>
          </div>
        ))}
      </dl>
    );
  return (
    <span
      className="assistant-parameter-value"
      data-value-kind={value === null ? 'null' : typeof value}
    >
      {value === null
        ? '未设置'
        : typeof value === 'boolean'
          ? value
            ? '是'
            : '否'
          : String(value)}
    </span>
  );
}

const ACTION_LABELS: Record<string, string> = {
  update_feishu_schedule: '调整飞书早晚报计划',
  send_feishu_report: '发送飞书报告',
  generate_feishu_preview: '生成飞书报告预览',
};

function ActionPreview({
  action,
  run,
  modelReady,
  modelDescription,
  busy,
  uncertain,
  now,
  onConfirm,
  onReject,
}: {
  action: AssistantAction;
  run: AssistantRun;
  modelReady: boolean;
  modelDescription: string;
  busy: boolean;
  uncertain: boolean;
  now: number;
  onConfirm: () => void;
  onReject: () => void;
}) {
  const [acknowledged, setAcknowledged] = useState(false);
  const [feishuAcknowledged, setFeishuAcknowledged] = useState(false);
  const feishuSchedule =
    action.operation_id === 'update_feishu_schedule' ||
    (action.method === 'PATCH' && action.path === '/api/v1/feishu/schedule');
  const feishuEnable = feishuSchedule && action.arguments.enabled !== false;
  const feishuSend =
    action.operation_id === 'send_feishu_report' ||
    (action.method === 'POST' &&
      /^\/api\/v1\/feishu\/reports\/[^/]+\/send$/.test(action.path));
  const feishuPreview =
    action.operation_id === 'generate_feishu_preview' ||
    (action.method === 'POST' && action.path === '/api/v1/feishu/preview');
  const needsFeishuConsent = feishuEnable || feishuSend;
  const expired =
    Date.parse(action.expires_at) <= now || Date.parse(run.expires_at) <= now;
  return (
    <section
      className="assistant-confirmation"
      aria-labelledby={`confirm-${encodeURIComponent(action.id)}`}
    >
      <h3 id={`confirm-${encodeURIComponent(action.id)}`}>确认操作</h3>
      <p>
        {Object.hasOwn(ACTION_LABELS, action.operation_id)
          ? ACTION_LABELS[action.operation_id]
          : action.operation_id}
      </p>
      <p className="assistant-note">
        {modelDescription} · 配置版本 {run.model_config_revision}
      </p>
      <h4>固定参数</h4>
      <div aria-label="服务器固定操作参数">
        <ParameterValue value={action.arguments} />
      </div>
      <details className="assistant-action-details">
        <summary>操作详情</summary>
        <p className="assistant-note">
          服务器固定预览，仅确认此编号与模型版本；参数不可修改。
        </p>
        <p className="assistant-note">
          拒绝只提交此确认编号，不调用模型。通用会话发送尚未开放；仅本人飞书早晚报支持独立授权后的发送。
        </p>
        <dl className="assistant-action-facts">
          <div>
            <dt>操作</dt>
            <dd>
              <code>{action.operation_id}</code>
            </dd>
          </div>
          <div>
            <dt>业务接口</dt>
            <dd>
              <code>
                {action.method} {action.path}
              </code>
            </dd>
          </div>
          <div>
            <dt>确认编号</dt>
            <dd>
              <code>{action.id}</code>
            </dd>
          </div>
          <div>
            <dt>有效至</dt>
            <dd>
              <time dateTime={action.expires_at}>
                {formatTime(action.expires_at)}
              </time>
            </dd>
          </div>
          <div>
            <dt>模型配置</dt>
            <dd>
              <code>{run.model_config_id}</code> · 版本{' '}
              {run.model_config_revision}
            </dd>
          </div>
        </dl>
      </details>
      <h4>影响</h4>
      {action.effects.length ? (
        <ul>
          {action.effects.map((effect, index) => (
            <li key={`${index}:${effect}`}>{effect}</li>
          ))}
        </ul>
      ) : (
        <p>未提供影响说明。请先核对业务范围，勿凭模型文字判断安全。</p>
      )}
      {feishuEnable ? (
        <p className="assistant-warning">
          这是持续数据外发与自动发送授权：后台会在北京时间 08:00 /
          20:00，按相邻时点的 12 小时半开窗口，
          把相关已入库、未暂停的私聊与群聊内容交给绑定模型，再自动发至已保存的本人飞书私聊，不会逐次询问。
          请核对固定目标与模型、配置版本；不是群
          Webhook，不接受任意收件人。主机与后台须持续运行和联网，离线不补发。
        </p>
      ) : feishuSend ? (
        <p className="assistant-warning">
          这是一次真实的外部消息发送，不是草稿：仅向已保存的本人飞书私聊发送服务器冻结正文。
          请核对上面的完整正文、12
          小时窗口、脱敏目标、哈希与版本；不允许改正文或改收件人。
          平台接受不代表手机系统已通知或已读；运行中、HTTP 202 或 unknown
          都不是成功，未知结果请人工核实且不要重发。
        </p>
      ) : feishuPreview ? (
        <p className="assistant-warning">
          仅生成最近一个已完整结束的固定 12 小时窗口预览，不是过去 24
          小时。相关已入库、未暂停的私聊与群聊内容会交给绑定模型，
          可能产生费用；不会发至飞书，每窗口不重做。HTTP 202
          仅表示后台处理中，请只读恢复。
        </p>
      ) : feishuSchedule ? (
        <p className="assistant-note">
          关闭计划会停止/取消后续处理，但不能保证撤回在途请求或平台已接受的消息；飞书业务本身不调用模型，但本助手确认后仍可能继续模型处理。
        </p>
      ) : null}
      {expired ? (
        <p className="assistant-warning">
          按本机时间此预览已过期。请读取结果，以后台时钟和状态为准。
        </p>
      ) : null}
      {!modelReady ? (
        <p className="assistant-warning">
          当前模型状态未就绪，或目标 /
          版本与本次运行不同。请刷新状态；旧同意不会用于新目标。
        </p>
      ) : null}
      {uncertain ? (
        <p className="assistant-warning">
          上一次提交结果尚未核实，请先读取运行结果，不可再次确认或拒绝。
        </p>
      ) : null}
      <label className="assistant-check">
        <input
          type="checkbox"
          checked={acknowledged}
          disabled={busy || expired || uncertain || !modelReady}
          onChange={(event) => setAcknowledged(event.target.checked)}
        />
        <span>
          我已核对工具、目标、完整参数和上述影响，仅确认这一项操作及其后续模型处理，可能产生费用。
        </span>
      </label>
      {needsFeishuConsent ? (
        <label className="assistant-check">
          <input
            type="checkbox"
            checked={feishuAcknowledged}
            disabled={busy || expired || uncertain || !modelReady}
            onChange={(event) => setFeishuAcknowledged(event.target.checked)}
          />
          <span>
            {feishuEnable
              ? '我另行同意按上述固定目标、窗口与版本，持续向本人飞书私聊自动发送摘要；理解这会持续外发消息资料，不会逐次确认，平台接受不等于手机通知。'
              : '我另行同意将上面完整核对的冻结正文真实发送至固定的本人飞书私聊；理解平台接受不等于手机系统通知或已读，结果未知时不重发。'}
            此飞书发送同意不替代模型数据处理同意。
          </span>
        </label>
      ) : null}
      <div className="assistant-actions">
        <button
          type="button"
          className="button button-primary"
          disabled={
            !acknowledged ||
            (needsFeishuConsent && !feishuAcknowledged) ||
            busy ||
            expired ||
            uncertain ||
            !modelReady
          }
          onClick={() => {
            setAcknowledged(false);
            setFeishuAcknowledged(false);
            onConfirm();
          }}
        >
          确认
        </button>
        <button
          type="button"
          className="button button-secondary"
          disabled={busy || expired || uncertain}
          onClick={() => {
            setAcknowledged(false);
            setFeishuAcknowledged(false);
            onReject();
          }}
        >
          拒绝
        </button>
      </div>
    </section>
  );
}

export function AssistantPanel({
  token,
  cache,
  onAuthFailure,
  onSettings,
  visible = true,
  helpRequest = null,
  onDismissHelp,
}: {
  token: string;
  cache: Map<string, AssistantWorkspace>;
  onAuthFailure: () => void;
  onSettings: () => void;
  visible?: boolean;
  helpRequest?: AssistantHelpRequest | null;
  onDismissHelp?: () => void;
}) {
  const [workspace, setWorkspace] = useState<AssistantWorkspace>(
    () => cache.get(token) ?? emptyWorkspace(),
  );
  const workspaceRef = useRef(workspace);
  const draftLimits = inspectAssistantMessage(workspace.draft);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const [handledHelp, setHandledHelp] = useState<AssistantHelpRequest | null>(
    null,
  );
  const preparedHelp =
    helpRequest && helpRequest !== handledHelp
      ? normalizeAssistantHelpRequest(helpRequest)
      : null;
  const [status, setStatus] = useState<AssistantStatus | null>(null);
  const [statusBusy, setStatusBusy] = useState(Boolean(token));
  const [statusError, setStatusError] = useState<string | null>(null);
  const [consented, setConsented] = useState(false);
  const [confirmationEpoch, setConfirmationEpoch] = useState(0);
  const [resetAcknowledged, setResetAcknowledged] = useState(false);
  useEffect(() => {
    // Opening, hiding or preparing another task never carries forward consent.
    setConsented(false);
    setConfirmationEpoch((current) => current + 1);
    setResetAcknowledged(false);
  }, [helpRequest, visible]);
  const [busy, setBusy] = useState<
    'start' | 'read' | 'confirm' | 'reject' | 'stopping' | null
  >(null);
  const [now, setNow] = useState(Date.now());
  const mounted = useRef(false);
  const authFailure = useRef(onAuthFailure);
  const statusRequest = useRef<AbortController | null>(null);
  const operation = useRef<{
    controller: AbortController;
    mutation: boolean;
    key: string;
  } | null>(null);
  useEffect(() => {
    authFailure.current = onAuthFailure;
  }, [onAuthFailure]);

  const update = (
    transform: (current: AssistantWorkspace) => AssistantWorkspace,
  ) => {
    const next = transform(workspaceRef.current);
    workspaceRef.current = next;
    cache.set(token, next);
    setWorkspace(next);
  };
  const updateTurn = (
    key: string,
    transform: (turn: AssistantTurn) => AssistantTurn,
  ) =>
    update((current) => ({
      ...current,
      turns: current.turns.map((turn) =>
        turn.key === key ? transform(turn) : turn,
      ),
    }));
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      statusRequest.current?.abort();
      const pending = operation.current;
      if (!pending) return;
      pending.controller.abort();
      if (pending.mutation) {
        const current = workspaceRef.current;
        cache.set(token, {
          ...current,
          turns: current.turns.map((turn) =>
            turn.key === pending.key
              ? {
                  ...turn,
                  uncertain: true,
                  notice:
                    '离开页面已停止等待，但未取消后台操作。请先读取或人工核实结果，不要重复提交。',
                }
              : turn,
          ),
        });
      }
    };
  }, [cache, token]);

  const loadStatus = useCallback(async () => {
    if (!token) return;
    statusRequest.current?.abort();
    const controller = new AbortController();
    statusRequest.current = controller;
    setStatusBusy(true);
    setStatusError(null);
    setConsented(false);
    setConfirmationEpoch((current) => current + 1);
    setStatus(null);
    try {
      const next = await getAssistantStatus(token, controller.signal);
      if (!mounted.current || controller.signal.aborted) return;
      setStatus(next);
    } catch (failure: unknown) {
      if (!mounted.current || controller.signal.aborted) return;
      if (isAssistantBusinessAuthFailure(failure)) authFailure.current();
      else
        setStatusError(
          failure instanceof Error ? failure.message : '无法读取助手状态。',
        );
    } finally {
      if (mounted.current && statusRequest.current === controller)
        setStatusBusy(false);
    }
  }, [token]);
  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  const latest = workspace.turns.at(-1);
  const run = latest?.run;
  const hasUnresolved = unresolved(latest);
  const sessionExpired = Boolean(run && Date.parse(run.expires_at) <= now);
  useEffect(() => {
    if (!run) return;
    const expires = [
      Date.parse(run.expires_at),
      ...(run.pending_action
        ? [Date.parse(run.pending_action.expires_at)]
        : []),
    ].filter((time) => time > Date.now());
    if (!expires.length) return;
    const timer = globalThis.setTimeout(
      () => setNow(Date.now()),
      Math.min(...expires) - Date.now() + 50,
    );
    return () => globalThis.clearTimeout(timer);
  }, [run, now]);
  const modelReady = Boolean(status?.configured && !statusBusy && !statusError);
  const quotaExhausted = Boolean(
    status?.configured && status.used_today >= status.daily_limit,
  );
  const modelMatchesRun = Boolean(
    modelReady &&
    run &&
    status?.model_config_id === run.model_config_id &&
    status.model_config_revision === run.model_config_revision,
  );
  const currentRunID = run?.run_id ?? latest?.runID ?? null;
  const needsResetAcknowledgment = hasUnresolved || sessionExpired;

  const perform = async (
    kind: 'start' | 'read' | 'confirm' | 'reject',
    key: string,
    action: (signal: AbortSignal) => Promise<AssistantRun>,
  ) => {
    if (operation.current) return;
    const controller = new AbortController();
    operation.current = { controller, mutation: kind !== 'read', key };
    setBusy(kind);
    setConsented(false);
    // A refreshed snapshot is never covered by a prior checkbox, even when
    // the server returns the same immutable action ID and model binding.
    setConfirmationEpoch((current) => current + 1);
    setResetAcknowledged(false);
    updateTurn(key, (turn) => ({ ...turn, notice: null }));
    try {
      const snapshot = await action(controller.signal);
      if (!mounted.current || operation.current?.controller !== controller)
        return;
      update((current) => ({
        ...current,
        sessionID: snapshot.session_id,
        recoveryID: snapshot.run_id,
        turns: current.turns.map((turn) =>
          turn.key === key
            ? {
                ...turn,
                runID: snapshot.run_id,
                run: snapshot,
                notice: null,
                uncertain:
                  snapshot.state === 'unknown' ||
                  snapshot.tool_results.some(
                    (item) => item.state === 'unknown',
                  ),
              }
            : turn,
        ),
      }));
      setNow(Date.now());
      if (kind !== 'read') void loadStatus();
    } catch (failure: unknown) {
      if (!mounted.current || operation.current?.controller !== controller)
        return;
      if (isAssistantBusinessAuthFailure(failure)) {
        authFailure.current();
        return;
      }
      updateTurn(key, (turn) => ({
        ...turn,
        runID:
          failure instanceof AssistantRequestError && failure.runID
            ? failure.runID
            : turn.runID,
        uncertain:
          turn.uncertain ||
          (kind !== 'read' &&
            (!(failure instanceof AssistantRequestError) ||
              failure.uncertain)) ||
          (Boolean(turn.runID) &&
            failure instanceof AssistantRequestError &&
            [
              'ASSISTANT_ACTION_CONSUMED',
              'ASSISTANT_CONFIG_CHANGED',
              'ASSISTANT_EXPIRED',
              'ASSISTANT_NOT_FOUND',
            ].includes(failure.code)),
        notice:
          failure instanceof Error
            ? failure.message
            : '未取得可核对结果。请先读取结果，不要重复提交。',
      }));
      if (
        failure instanceof AssistantRequestError &&
        failure.code === 'ASSISTANT_CONFIG_CHANGED'
      )
        void loadStatus();
    } finally {
      if (mounted.current && operation.current?.controller === controller) {
        operation.current = null;
        setBusy(null);
      }
    }
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const current = workspaceRef.current;
    if (
      !visible ||
      preparedHelp ||
      !status?.configured ||
      statusBusy ||
      !consented ||
      operation.current ||
      hasUnresolved ||
      sessionExpired ||
      quotaExhausted ||
      !current.draft.trim() ||
      inspectAssistantMessage(current.draft).issue !== null
    )
      return;
    const prompt = current.draft;
    const key = crypto.randomUUID();
    const sessionID = current.sessionID;
    const consent = {
      model_config_id: status.model_config_id,
      model_config_revision: status.model_config_revision,
      confirmed: true as const,
    };
    update((current) => ({
      ...current,
      draft: '',
      turns: [
        ...current.turns,
        { key, prompt, runID: null, run: null, notice: null, uncertain: false },
      ],
    }));
    void perform('start', key, (signal) =>
      createAssistantRun(token, signal, {
        session_id: sessionID,
        message: prompt,
        ...consent,
      }),
    );
  };
  const refresh = () => {
    if (!latest || !currentRunID || operation.current) return;
    void perform('read', latest.key, (signal) =>
      getAssistantRun(token, currentRunID, signal),
    );
  };
  const decide = (accept: boolean) => {
    if (
      !visible ||
      !latest ||
      !run?.pending_action ||
      operation.current ||
      latest.uncertain ||
      run.state !== 'awaiting_confirmation'
    )
      return;
    if (
      Date.parse(run.pending_action.expires_at) <= Date.now() ||
      Date.parse(run.expires_at) <= Date.now()
    ) {
      setNow(Date.now());
      return;
    }
    const actionID = run.pending_action.id;
    if (accept && (!status || !modelMatchesRun)) return;
    void perform(accept ? 'confirm' : 'reject', latest.key, (signal) =>
      accept
        ? confirmAssistantAction(token, run.run_id, signal, {
            action_id: actionID,
            model_config_id: run.model_config_id,
            model_config_revision: run.model_config_revision,
            confirmed: true,
          })
        : rejectAssistantAction(token, run.run_id, signal, actionID),
    );
  };
  const recover = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (operation.current || hasUnresolved || !workspace.recoveryID.trim())
      return;
    const runID = workspace.recoveryID.trim();
    const key = crypto.randomUUID();
    update((current) => ({
      ...current,
      turns: [
        ...current.turns,
        { key, prompt: null, runID, run: null, notice: null, uncertain: false },
      ],
    }));
    void perform('read', key, (signal) =>
      getAssistantRun(token, runID, signal),
    );
  };
  const stopWaiting = () => {
    const pending = operation.current;
    if (!pending) return;
    if (pending.mutation)
      updateTurn(pending.key, (turn) => ({
        ...turn,
        uncertain: true,
        notice:
          '已停止等待，未取消后台操作。服务端可能已处理并产生费用；请先读取或人工核实，不要重复提交。',
      }));
    setBusy('stopping');
    pending.controller.abort();
  };
  const reset = () => {
    if (operation.current || (needsResetAcknowledgment && !resetAcknowledged))
      return;
    update(() => emptyWorkspace());
    setConsented(false);
    setResetAcknowledged(false);
    setNow(Date.now());
  };

  const finishHelp = () => {
    setHandledHelp(helpRequest);
    setConsented(false);
    setConfirmationEpoch((current) => current + 1);
    setResetAcknowledged(false);
    if (composerRef.current && !composerRef.current.disabled)
      composerRef.current.focus();
    else headingRef.current?.focus();
    onDismissHelp?.();
  };
  const applyHelp = () => {
    if (!visible || !token || !preparedHelp || operation.current) return;
    const current = workspaceRef.current;
    const currentRun = current.turns.at(-1)?.run;
    const pending =
      unresolved(current.turns.at(-1)) ||
      Boolean(currentRun && Date.parse(currentRun.expires_at) <= Date.now());
    const result = prepareAssistantHelpDraft(
      current.draft,
      preparedHelp,
      current.draft.length ? 'append' : 'fill',
      pending,
    );
    if (!result.ok) return;
    update((value) => ({ ...value, draft: result.draft }));
    finishHelp();
  };
  const helpDraft = preparedHelp
    ? prepareAssistantHelpDraft(
        workspace.draft,
        preparedHelp,
        workspace.draft.length ? 'append' : 'fill',
        hasUnresolved || sessionExpired,
      )
    : null;
  const helpDraftDescription =
    '只更新助手草稿，不会自动提交。提交前仍需核对内容并授权。';
  const preparedTask = preparedHelp ? (
    <section className="assistant-help" aria-labelledby="assistant-help-title">
      <h2 id="assistant-help-title">带入{preparedHelp.source}？</h2>
      <span id="assistant-help-note" className="help-accessible-description">
        {helpDraftDescription}
      </span>
      <details className="assistant-help-details">
        <summary>查看带入内容</summary>
        <p className="assistant-help-prompt">{`来源：${preparedHelp.source}\n${preparedHelp.prompt}`}</p>
        <p className="assistant-note">{helpDraftDescription}</p>
      </details>
      {hasUnresolved || sessionExpired ? (
        <p className="assistant-warning">
          请先核实当前运行；带入不会清空它或重放请求。
        </p>
      ) : helpDraft && !helpDraft.ok && helpDraft.reason === 'too_long' ? (
        <p className="assistant-warning">
          带入后会超过 {ASSISTANT_MESSAGE_BYTE_LIMIT}{' '}
          字节（UTF-8）。请精简现有草稿；不会截断或覆盖内容。
        </p>
      ) : helpDraft && !helpDraft.ok && helpDraft.reason === 'nul' ? (
        <p className="assistant-warning">
          内容包含空字符（NUL），请先移除；不会截断或覆盖内容。
        </p>
      ) : workspace.draft.length ? (
        <p className="assistant-note">已有草稿，将追加在末尾。</p>
      ) : null}
      <div className="assistant-actions">
        <button
          type="button"
          className="button button-primary"
          aria-describedby="assistant-help-note"
          disabled={!visible || !token || Boolean(busy) || !helpDraft?.ok}
          onClick={applyHelp}
        >
          {workspace.draft.length ? '追加' : '带入'}
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={finishHelp}
        >
          忽略
        </button>
      </div>
    </section>
  ) : null;

  if (!token)
    return (
      <section className="assistant-page">
        <h1
          className="assistant-sr-only"
          id="assistant-title"
          ref={headingRef}
          tabIndex={-1}
        >
          Agent 助手
        </h1>
        {preparedTask}
        <div className="assistant-empty">
          <p>请先连接本地服务。</p>
          <button
            type="button"
            className="button button-primary"
            onClick={onSettings}
          >
            去设置
          </button>
        </div>
      </section>
    );

  const runControls =
    currentRunID || busy ? (
      <div className="assistant-run-controls">
        {currentRunID ? (
          <button
            type="button"
            className="button button-secondary"
            disabled={Boolean(busy)}
            onClick={refresh}
          >
            <Icon name="refresh" /> 查看结果
          </button>
        ) : null}
        {busy ? (
          <button
            type="button"
            className="button button-quiet"
            disabled={busy === 'stopping'}
            onClick={stopWaiting}
          >
            <Icon name="pause" /> 停止等待
          </button>
        ) : null}
        {busy || latest?.uncertain ? (
          <p className="assistant-note">
            停止等待不等于后台取消；后台可能已处理并产生费用，请先核实。
          </p>
        ) : null}
      </div>
    ) : null;

  return (
    <section className="assistant-page" aria-labelledby="assistant-title">
      <h1
        className="assistant-sr-only"
        id="assistant-title"
        ref={headingRef}
        tabIndex={-1}
      >
        Agent 助手
      </h1>
      {preparedTask}
      {statusBusy ? (
        <p className="assistant-note" role="status">
          正在读取配置…
        </p>
      ) : statusError ? (
        <div className="assistant-setup-notice" role="status">
          <p className="assistant-error">{statusError}</p>
          <button
            type="button"
            className="button button-secondary"
            onClick={() => void loadStatus()}
          >
            重试
          </button>
        </div>
      ) : !status?.configured ? (
        <div className="assistant-setup-notice" role="status">
          <p>请先配置模型。</p>
          <button
            type="button"
            className="button button-secondary"
            onClick={onSettings}
          >
            去设置
          </button>
        </div>
      ) : null}

      <section className="assistant-conversation" aria-label="助手对话">
        <div className="assistant-transcript">
          {!workspace.turns.length && !preparedHelp ? (
            <div className="assistant-empty">
              <h2>你想做什么？</h2>
            </div>
          ) : null}
          {workspace.turns.map((turn) => (
            <article className="assistant-turn" key={turn.key}>
              {turn.prompt !== null ? (
                <div className="assistant-user">
                  <h3>你</h3>
                  <p>{turn.prompt}</p>
                </div>
              ) : null}
              <div className="assistant-reply">
                <div className="assistant-reply-heading">
                  <h3>助手</h3>
                  <span>
                    {turn.uncertain
                      ? '结果待核实'
                      : turn.run
                        ? RUN_LABELS[turn.run.state]
                        : turn.notice
                          ? '未取得结果'
                          : '等待结果'}
                  </span>
                </div>
                {turn.run?.assistant_message ? (
                  <p>{turn.run.assistant_message}</p>
                ) : turn.run?.state === 'awaiting_confirmation' ? (
                  <p className="assistant-note">请核对下方操作。</p>
                ) : turn.run ? (
                  <p className="assistant-note">
                    本轮无回复文本，详情见“更多”。
                  </p>
                ) : null}
              </div>
              {turn.run?.error ? (
                <p className="assistant-error" role="alert">
                  <code>{turn.run.error.code}</code> · {turn.run.error.message}
                </p>
              ) : null}
              {turn.notice ? (
                <p
                  className={
                    turn.uncertain ? 'assistant-warning' : 'assistant-error'
                  }
                  role="status"
                >
                  {turn.notice}
                </p>
              ) : null}
              {turn.uncertain && !turn.notice ? (
                <p className="assistant-warning">
                  结果未知，请先核实业务状态，不要重新提交。
                </p>
              ) : null}
              {turn === latest && turn.uncertain && !currentRunID ? (
                <p className="assistant-note">
                  未取得运行编号，请返回原业务页核对。
                </p>
              ) : null}
              {turn === latest &&
              turn.run?.state === 'awaiting_confirmation' &&
              turn.run.pending_action ? (
                <ActionPreview
                  key={`${turn.run.run_id}:${turn.run.pending_action.id}:${status?.model_config_id ?? 'unavailable'}:${status?.model_config_revision ?? 'unavailable'}:${confirmationEpoch}`}
                  action={turn.run.pending_action}
                  run={turn.run}
                  modelReady={modelMatchesRun}
                  modelDescription={
                    modelMatchesRun && status?.configured
                      ? `${status.model} · ${status.endpoint_host}`
                      : '模型状态未就绪'
                  }
                  busy={Boolean(busy) || !visible}
                  uncertain={turn.uncertain}
                  now={now}
                  onConfirm={() => decide(true)}
                  onReject={() => decide(false)}
                />
              ) : null}
            </article>
          ))}
        </div>
        <div className="assistant-live" role="status" aria-atomic="true">
          {busy ? (
            <span>
              {busy === 'start'
                ? '正在调用模型…'
                : busy === 'read'
                  ? '正在读取结果…'
                  : busy === 'confirm'
                    ? '正在确认操作…'
                    : busy === 'reject'
                      ? '正在拒绝操作…'
                      : '正在停止等待…'}
            </span>
          ) : hasUnresolved && run?.state !== 'awaiting_confirmation' ? (
            <span>请先查看或核实当前结果。</span>
          ) : null}
        </div>
        {busy || hasUnresolved || sessionExpired ? runControls : null}
        <form className="assistant-composer" onSubmit={submit}>
          <label className="assistant-sr-only" htmlFor="assistant-message">
            请求
          </label>
          <textarea
            id="assistant-message"
            ref={composerRef}
            value={workspace.draft}
            aria-invalid={draftLimits.issue !== null}
            aria-describedby={`assistant-message-limit${draftLimits.issue ? ' assistant-message-error' : ''}`}
            rows={3}
            disabled={Boolean(busy) || hasUnresolved || sessionExpired}
            placeholder="输入请求"
            onChange={(event) => {
              const draft = event.target.value;
              update((current) => ({ ...current, draft }));
              setConsented(false);
            }}
          />
          {draftLimits.issue ? (
            <p className="assistant-warning" id="assistant-message-error">
              {draftLimits.issue === 'too_long'
                ? `超过 ${ASSISTANT_MESSAGE_BYTE_LIMIT} 字节（UTF-8），请精简后提交；输入已保留，不会自动截断。`
                : '输入含空字符（NUL），请移除后提交；输入已保留，不会自动修改。'}
            </p>
          ) : null}
          <div
            className="assistant-round-consent"
            hidden={
              !workspace.draft.trim() ||
              !modelReady ||
              hasUnresolved ||
              sessionExpired
            }
          >
            <p className="assistant-note" id="assistant-round-disclosure">
              本轮输入、助手上下文及获准工具返回的业务资料将交给模型处理，可能产生费用。发送或持续外发另行确认。
            </p>
            <label className="assistant-check">
              <input
                type="checkbox"
                checked={consented}
                disabled={
                  !visible ||
                  Boolean(preparedHelp) ||
                  !modelReady ||
                  Boolean(busy) ||
                  hasUnresolved ||
                  sessionExpired ||
                  quotaExhausted ||
                  draftLimits.issue !== null
                }
                aria-describedby="assistant-round-disclosure"
                onChange={(event) => setConsented(event.target.checked)}
              />
              <span>
                我同意本轮调用{' '}
                <strong>{status?.model || '尚未配置的模型'}</strong> ·{' '}
                <strong>{status?.endpoint_host || '尚未读取'}</strong> ·
                配置版本 {status?.model_config_revision ?? '—'}
              </span>
            </label>
          </div>
          {quotaExhausted ? (
            <p className="assistant-warning">
              今日模型额度已用尽，可查看已有结果。
            </p>
          ) : null}
          {sessionExpired ? (
            <p className="assistant-warning">
              会话已过期。请先核实结果，再从“更多”新建会话；已执行操作不会撤回。
            </p>
          ) : null}
          <div className="assistant-actions assistant-submit-row">
            <button
              type="submit"
              className={`button ${preparedHelp || hasUnresolved || !modelReady ? 'button-secondary' : 'button-primary'}`}
              disabled={
                !visible ||
                Boolean(preparedHelp) ||
                !modelReady ||
                !consented ||
                !workspace.draft.trim() ||
                draftLimits.issue !== null ||
                Boolean(busy) ||
                hasUnresolved ||
                sessionExpired ||
                quotaExhausted
              }
            >
              <Icon name="send" /> 提交
            </button>
            <span
              className={
                draftLimits.bytes >= ASSISTANT_MESSAGE_BYTE_LIMIT * 0.8
                  ? 'assistant-note'
                  : 'assistant-sr-only'
              }
              id="assistant-message-limit"
            >
              {draftLimits.bytes} / {ASSISTANT_MESSAGE_BYTE_LIMIT} 字节（UTF-8）
            </span>
          </div>
        </form>
      </section>

      <details className="assistant-more">
        <summary>更多</summary>
        <div className="assistant-more-body">
          <div className="assistant-actions">
            <button
              type="button"
              className="button button-secondary button-icon"
              aria-label="刷新状态"
              title="刷新状态"
              disabled={statusBusy || Boolean(busy)}
              onClick={() => void loadStatus()}
            >
              <Icon name="refresh" />
            </button>
            <button
              type="button"
              className="button button-quiet"
              onClick={onSettings}
            >
              <Icon name="settings" /> 模型设置
            </button>
          </div>
          {status?.configured ? (
            <p className="assistant-note">
              {status.model} · {status.endpoint_host} ·{' '}
              {status.external ? '外部模型' : '本地模型'} · 今日额度{' '}
              {status.used_today} / {status.daily_limit}
            </p>
          ) : null}
          {!busy && !hasUnresolved && !sessionExpired ? runControls : null}
          <section
            className="assistant-recovery"
            aria-labelledby="assistant-recovery-title"
          >
            <h2 id="assistant-recovery-title">运行与会话</h2>
            <p>
              运行仅在后台内存保留约 30
              分钟，重启后可能丢失。只用已知编号和同一授权身份读取，不会重跑。
            </p>
            <form className="assistant-recovery-form" onSubmit={recover}>
              <label htmlFor="assistant-recovery-id">已有运行编号</label>
              <input
                id="assistant-recovery-id"
                value={workspace.recoveryID}
                maxLength={256}
                autoComplete="off"
                spellCheck={false}
                disabled={Boolean(busy) || hasUnresolved}
                onChange={(event) => {
                  const recoveryID = event.target.value;
                  update((current) => ({ ...current, recoveryID }));
                }}
              />
              <button
                type="submit"
                className="button button-secondary"
                disabled={
                  Boolean(busy) || hasUnresolved || !workspace.recoveryID.trim()
                }
              >
                <Icon name="eye" /> 只读恢复
              </button>
            </form>
            <p>新会话只清助手上下文，不取消后台操作，也不清业务草稿。</p>
            {needsResetAcknowledgment ? (
              <label className="assistant-check">
                <input
                  type="checkbox"
                  checked={resetAcknowledged}
                  disabled={Boolean(busy)}
                  onChange={(event) =>
                    setResetAcknowledged(event.target.checked)
                  }
                />
                <span>
                  我已记录必要的运行编号，并已在业务页面核实未决或未知结果；新会话不会取消或撤销后台操作。
                </span>
              </label>
            ) : null}
            <button
              type="button"
              className="button button-secondary"
              disabled={
                Boolean(busy) ||
                (needsResetAcknowledgment && !resetAcknowledged)
              }
              onClick={reset}
            >
              <Icon name="plus" /> 新会话
            </button>
          </section>
          {workspace.turns.map((turn, index) => (
            <details className="assistant-audit" key={turn.key}>
              <summary>
                第 {index + 1} 轮运行与审计
                {turn.run ? ` · ${turn.run.tool_results.length} 项` : ''}
              </summary>
              {turn.prompt === null ? (
                <p className="assistant-note">已读取原运行，不会重新执行。</p>
              ) : null}
              {turn.runID ? (
                <p className="assistant-run-id">
                  运行编号 <code>{turn.runID}</code>
                  {turn.run ? (
                    <>
                      {' '}
                      · 有效至{' '}
                      <time dateTime={turn.run.expires_at}>
                        {formatTime(turn.run.expires_at)}
                      </time>
                    </>
                  ) : null}
                </p>
              ) : (
                <p className="assistant-note">
                  未取得运行编号，请在原业务页核实。
                </p>
              )}
              {turn.run?.tool_results.length ? (
                <ol>
                  {turn.run.tool_results.map((item) => (
                    <li key={item.call_id}>
                      <div className="assistant-audit-heading">
                        <code>{item.operation_id}</code>
                        <strong>{TOOL_LABELS[item.state]}</strong>
                      </div>
                      <p className="assistant-note">
                        调用编号 <code>{item.call_id}</code>
                        {item.http_status === null
                          ? ' · 无 HTTP 状态'
                          : ` · HTTP ${item.http_status}`}
                        {item.error_code ? (
                          <>
                            {' '}
                            · <code>{item.error_code}</code>
                          </>
                        ) : null}
                      </p>
                      {item.data !== null ? (
                        <details>
                          <summary>查看返回数据</summary>
                          <ParameterValue value={item.data} />
                        </details>
                      ) : (
                        <p className="assistant-note">没有返回数据。</p>
                      )}
                    </li>
                  ))}
                </ol>
              ) : (
                <p className="assistant-note">本轮没有工具执行记录。</p>
              )}
            </details>
          ))}
          {status ? (
            <details className="assistant-scope">
              <summary>权限目录（{status.operations.length} 项）</summary>
              <p>
                仅覆盖本项目业务接口。凭据与授权在设置中管理；外部读取、费用和写入以固定预览为准。通用会话发送未开放；本人飞书早晚报须独立授权。
              </p>
              <ul className="assistant-catalogue">
                {status.operations.map((item) => (
                  <li key={item.id}>
                    <div>
                      <strong>{item.id}</strong>
                      <span>
                        {Object.hasOwn(AVAILABILITY_LABELS, item.availability)
                          ? AVAILABILITY_LABELS[item.availability]
                          : `未知权限（${item.availability}）`}
                      </span>
                    </div>
                    <code>
                      {item.method} {item.path}
                    </code>
                    <p>
                      {Object.hasOwn(EFFECT_LABELS, item.effect)
                        ? EFFECT_LABELS[item.effect]
                        : `未知影响（${item.effect}）`}
                      {item.reason ? ` · ${item.reason}` : ''}
                    </p>
                  </li>
                ))}
              </ul>
            </details>
          ) : null}
        </div>
      </details>
    </section>
  );
}
