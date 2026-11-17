import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export type AssistantJSON =
  | null
  | boolean
  | number
  | string
  | AssistantJSON[]
  | { [key: string]: AssistantJSON };

export interface AssistantOperation {
  id: string;
  method: string;
  path: string;
  // Unknown catalogue values remain visible, but never become client permissions.
  effect: string;
  availability: string;
  reason: string;
}
export interface AssistantStatus {
  configured: boolean;
  model: string | null;
  endpoint_host: string | null;
  external: boolean;
  model_config_id: string;
  model_config_revision: number;
  daily_limit: number;
  used_today: number;
  sending_enabled: false;
  session_ttl_seconds: number;
  operations: AssistantOperation[];
}
export interface AssistantToolResult {
  call_id: string;
  operation_id: string;
  state: 'succeeded' | 'failed' | 'pending' | 'rejected' | 'unknown';
  http_status: number | null;
  data: AssistantJSON;
  error_code: string;
}
export interface AssistantAction {
  id: string;
  operation_id: string;
  method: string;
  path: string;
  arguments: { [key: string]: AssistantJSON };
  effects: string[];
  expires_at: string;
}
export interface AssistantRun {
  session_id: string;
  run_id: string;
  state:
    | 'running'
    | 'awaiting_confirmation'
    | 'completed'
    | 'failed'
    | 'rejected'
    | 'unknown';
  assistant_message: string;
  tool_results: AssistantToolResult[];
  pending_action: AssistantAction | null;
  model_config_id: string;
  model_config_revision: number;
  expires_at: string;
  error: { code: string; message: string } | null;
}
export interface AssistantModelConsent {
  model_config_id: string;
  model_config_revision: number;
  confirmed: true;
}

export class AssistantRequestError extends ServiceError {
  readonly uncertain: boolean;
  readonly runID: string | null;

  constructor(
    message: string,
    code: string,
    status?: number,
    uncertain = false,
    runID: string | null = null,
  ) {
    super(message, code, status);
    this.name = 'AssistantRequestError';
    this.uncertain = uncertain;
    this.runID = runID;
  }
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown): value is string => typeof value === 'string';
const id = (value: unknown): value is string =>
  text(value) && value.length > 0 && value.length <= 256;
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const nullableText = (value: unknown): value is string | null =>
  value === null || text(value);
const date = (value: unknown): value is string =>
  text(value) && value.length <= 64 && Number.isFinite(Date.parse(value));
const json = (value: unknown, depth = 0): value is AssistantJSON => {
  if (depth > 32) return false;
  if (value === null || text(value) || typeof value === 'boolean') return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every((item) => json(item, depth + 1));
  return (
    record(value) && Object.values(value).every((item) => json(item, depth + 1))
  );
};
const validStatus = (value: unknown): value is AssistantStatus =>
  record(value) &&
  typeof value.configured === 'boolean' &&
  nullableText(value.model) &&
  nullableText(value.endpoint_host) &&
  typeof value.external === 'boolean' &&
  text(value.model_config_id) &&
  count(value.model_config_revision) &&
  (!value.configured ||
    (id(value.model_config_id) &&
      id(value.model) &&
      id(value.endpoint_host))) &&
  count(value.daily_limit) &&
  count(value.used_today) &&
  value.sending_enabled === false &&
  count(value.session_ttl_seconds) &&
  value.session_ttl_seconds > 0 &&
  Array.isArray(value.operations) &&
  value.operations.length <= 512 &&
  new Set(
    value.operations.map((operation) =>
      record(operation) ? operation.id : null,
    ),
  ).size === value.operations.length &&
  value.operations.every(
    (operation) =>
      record(operation) &&
      id(operation.id) &&
      text(operation.method) &&
      text(operation.path) &&
      text(operation.effect) &&
      text(operation.availability) &&
      text(operation.reason),
  );
const validToolResult = (value: unknown): value is AssistantToolResult =>
  record(value) &&
  id(value.call_id) &&
  id(value.operation_id) &&
  ['succeeded', 'failed', 'pending', 'rejected', 'unknown'].includes(
    String(value.state),
  ) &&
  (value.http_status === null ||
    (count(value.http_status) &&
      value.http_status >= 100 &&
      value.http_status <= 599)) &&
  json(value.data) &&
  text(value.error_code);
const validAction = (value: unknown): value is AssistantAction =>
  record(value) &&
  id(value.id) &&
  id(value.operation_id) &&
  text(value.method) &&
  text(value.path) &&
  record(value.arguments) &&
  json(value.arguments) &&
  Array.isArray(value.effects) &&
  value.effects.every(text) &&
  date(value.expires_at);
const validRun = (value: unknown): value is AssistantRun =>
  record(value) &&
  id(value.session_id) &&
  id(value.run_id) &&
  [
    'running',
    'awaiting_confirmation',
    'completed',
    'failed',
    'rejected',
    'unknown',
  ].includes(String(value.state)) &&
  text(value.assistant_message) &&
  Array.isArray(value.tool_results) &&
  value.tool_results.length <= 64 &&
  value.tool_results.every(validToolResult) &&
  new Set(value.tool_results.map((item) => item.call_id)).size ===
    value.tool_results.length &&
  (value.pending_action === null || validAction(value.pending_action)) &&
  (value.state === 'awaiting_confirmation') ===
    (value.pending_action !== null) &&
  id(value.model_config_id) &&
  count(value.model_config_revision) &&
  date(value.expires_at) &&
  (value.error === null ||
    (record(value.error) &&
      text(value.error.code) &&
      text(value.error.message)));

const ERRORS: Record<string, string> = {
  ASSISTANT_NOT_FOUND:
    '未找到该运行。它可能不属于当前授权身份，或后台已重启；不会自动重建或重试。',
  ASSISTANT_EXPIRED:
    '助手会话或确认已过期。请核实已有操作，再开始新会话；本地业务授权未被移除。',
  ASSISTANT_ACTION_CONSUMED: '这项确认已被处理。请读取运行结果，不要重复确认。',
  ASSISTANT_CONFIG_CHANGED:
    '模型目标或配置版本已变化。请刷新模型状态并重新核对，不会沿用旧同意。',
  ASSISTANT_LIMIT:
    '助手容量或调用次数已达限制。请先读取已有运行结果，稍后再继续。',
  ASSISTANT_NOT_CONFIGURED: '尚未配置模型，请先在设置中完成配置。',
  AI_NOT_CONFIGURED: '尚未配置模型，请先在设置中完成配置。',
  AI_QUOTA_EXCEEDED: '共享模型额度已用尽，请核对今日用量与模型服务状态。',
  INVALID_REQUEST: '提交内容不符合当前服务合同。请刷新状态，不会自动重试。',
};

export const isAssistantBusinessAuthFailure = (error: unknown): boolean =>
  error instanceof ServiceError &&
  error.status === 401 &&
  (error.code === 'UNAUTHORIZED' || error.code === 'DEVICE_UNAUTHORIZED');

async function request(
  token: string,
  path: string,
  signal: AbortSignal,
  method: 'GET' | 'POST',
  body?: unknown,
): Promise<{ value: unknown; status: number }> {
  if (SERVICE.error)
    throw new AssistantRequestError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const timer = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    method === 'GET' ? 10_000 : 120_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/assistant/${path}`,
      token,
      {
        method,
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        signal: controller.signal,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('已停止等待', 'AbortError');
    if (!response.ok) {
      const code =
        record(payload) && record(payload.error) && text(payload.error.code)
          ? payload.error.code
          : 'ASSISTANT_UNAVAILABLE';
      throw new AssistantRequestError(
        authErrorMessage(
          code,
          Object.hasOwn(ERRORS, code)
            ? ERRORS[code]
            : response.status === 401
              ? '助手请求未获授权。请核对本地服务状态；本地业务授权不会因未知错误被移除。'
              : '助手服务暂不可用。已知运行编号时请只读取结果，不要重复提交。',
        ),
        code,
        response.status,
        method === 'POST' &&
          (response.status >= 500 || response.status === 408),
      );
    }
    return { value: payload, status: response.status };
  } catch (failure: unknown) {
    if (failure instanceof ServiceError) throw failure;
    const stopped = signal.aborted;
    throw new AssistantRequestError(
      method === 'POST'
        ? `${stopped ? '已停止等待' : timedOut ? '等待超时' : '连接中断'}；后台可能已处理并产生费用。请读取或人工核实结果，不要重复提交。`
        : stopped
          ? '已停止读取。此操作不会取消后台运行。'
          : timedOut
            ? '读取超时。可再次读取结果，不会重跑模型或工具。'
            : '无法连接助手服务。请检查本地后台后重新读取。',
      stopped ? 'LOCAL_ABORT' : timedOut ? 'TIMEOUT' : 'NETWORK_ERROR',
      undefined,
      method === 'POST',
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

const requireConsent = (input: AssistantModelConsent) => {
  if (
    !id(input.model_config_id) ||
    !count(input.model_config_revision) ||
    input.confirmed !== true
  )
    throw new AssistantRequestError(
      '请核对模型目标及版本，并明确同意本次调用。',
      'CONSENT_REQUIRED',
    );
};
const decodeRun = (
  response: { value: unknown; status: number },
  mutation: boolean,
  expected: {
    runID?: string;
    sessionID?: string;
    consent?: AssistantModelConsent;
  } = {},
): AssistantRun => {
  const { value, status } = response;
  const valid = validRun(value);
  const nonterminal =
    valid &&
    (value.state === 'running' || value.state === 'awaiting_confirmation');
  if (
    !valid ||
    (expected.runID !== undefined && value.run_id !== expected.runID) ||
    (expected.sessionID && value.session_id !== expected.sessionID) ||
    (expected.consent &&
      (value.model_config_id !== expected.consent.model_config_id ||
        value.model_config_revision !==
          expected.consent.model_config_revision)) ||
    (mutation &&
      ((status !== 200 && status !== 202) || (status === 202) !== nonterminal))
  )
    throw new AssistantRequestError(
      mutation
        ? '运行返回格式无效，结果未知。请通过运行编号读取结果，不要重复提交。'
        : '运行返回格式无效。未将模型文本或 HTTP 状态视为操作成功。',
      'INVALID_RESPONSE',
      undefined,
      mutation,
      expected.runID ??
        (record(value) && id(value.run_id) ? value.run_id : null),
    );
  return value;
};
const runPath = (runID: string) => {
  if (!id(runID) || runID !== runID.trim())
    throw new AssistantRequestError('请输入有效的运行编号。', 'INVALID_RUN_ID');
  return `runs/${encodeURIComponent(runID)}`;
};

export const getAssistantStatus = async (
  token: string,
  signal: AbortSignal,
): Promise<AssistantStatus> => {
  const { value, status } = await request(token, 'status', signal, 'GET');
  if (status !== 200 || !validStatus(value))
    throw new AssistantRequestError(
      '助手状态返回格式无效，暂不允许调用模型。',
      'INVALID_RESPONSE',
    );
  return value;
};
export const createAssistantRun = async (
  token: string,
  signal: AbortSignal,
  input: AssistantModelConsent & { session_id: string; message: string },
): Promise<AssistantRun> => {
  requireConsent(input);
  if (
    !text(input.session_id) ||
    input.session_id.length > 256 ||
    !text(input.message) ||
    !input.message.trim() ||
    input.message.length > 8000
  )
    throw new AssistantRequestError(
      '请输入 1–8000 字的本轮请求。',
      'INVALID_MESSAGE',
    );
  return decodeRun(
    await request(token, 'runs', signal, 'POST', {
      session_id: input.session_id,
      message: input.message,
      model_config_id: input.model_config_id,
      model_config_revision: input.model_config_revision,
      confirmed: true,
    }),
    true,
    { sessionID: input.session_id, consent: input },
  );
};
export const getAssistantRun = async (
  token: string,
  runID: string,
  signal: AbortSignal,
): Promise<AssistantRun> =>
  decodeRun(await request(token, runPath(runID), signal, 'GET'), false, {
    runID,
  });
export const confirmAssistantAction = async (
  token: string,
  runID: string,
  signal: AbortSignal,
  input: AssistantModelConsent & { action_id: string },
): Promise<AssistantRun> => {
  requireConsent(input);
  if (!id(input.action_id))
    throw new AssistantRequestError(
      '确认编号无效，请读取运行结果。',
      'INVALID_ACTION_ID',
    );
  return decodeRun(
    await request(token, `${runPath(runID)}/confirm`, signal, 'POST', {
      action_id: input.action_id,
      model_config_id: input.model_config_id,
      model_config_revision: input.model_config_revision,
      confirmed: true,
    }),
    true,
    { runID, consent: input },
  );
};
export const rejectAssistantAction = async (
  token: string,
  runID: string,
  signal: AbortSignal,
  actionID: string,
): Promise<AssistantRun> => {
  if (!id(actionID))
    throw new AssistantRequestError(
      '确认编号无效，请读取运行结果。',
      'INVALID_ACTION_ID',
    );
  return decodeRun(
    await request(token, `${runPath(runID)}/reject`, signal, 'POST', {
      action_id: actionID,
    }),
    true,
    { runID },
  );
};
