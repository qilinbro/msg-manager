import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export type FeishuState = 'running' | 'succeeded' | 'failed' | 'unknown';
export interface FeishuSchedule {
  enabled: boolean;
  revision: number;
  timezone: 'Asia/Shanghai';
  slots: ['08:00', '20:00'];
  model_config_id: string;
  model_config_revision: number;
  config_revision: number;
  target_binding_id: string;
  next_window_end: string | null;
  last_window_end: string | null;
}
export interface FeishuDelivery {
  id: string;
  report_id: string;
  window_start: string;
  window_end: string;
  state: FeishuState;
  target_binding_id: string;
  config_revision: number;
  model_config_id: string;
  model_config_revision: number;
  content_hash: string;
  message_id: string;
  error_code: string;
  created_at: string;
  completed_at: string | null;
}
export interface FeishuReport {
  id: string;
  window_start: string;
  window_end: string;
  state: FeishuState;
  summary: string;
  todos: { text: string; evidence_ids: string[]; needs_review: true }[];
  excerpts: {
    message_id: string;
    conversation_id: string;
    text: string;
    occurred_at: string;
  }[];
  conversation_ids: string[];
  source_refs: { conversation_id: string; settings_revision: number }[];
  conversation_count: number;
  model_config_id: string;
  model_config_revision: number;
  config_revision: number;
  target_binding_id: string;
  text: string;
  content_hash: string;
  truncated: boolean;
  error_code: string;
  created_at: string;
  generated_at: string | null;
  schedule_revision: number;
  trigger: 'manual' | 'schedule';
  delivery: FeishuDelivery | null;
}
export interface FeishuStatus {
  configured: boolean;
  key_configured: boolean;
  app_id: string;
  config_revision: number;
  target_binding_id: string;
  target_label_masked: string;
  recipient_open_id_masked: string;
  recipient_ownership_verified: false;
  schedule: FeishuSchedule;
  latest_report_id: string;
  latest_delivery: FeishuDelivery | null;
  capabilities: { private_self_only: true; automatic_retry: false };
  timezone: 'Asia/Shanghai';
  slots: ['08:00', '20:00'];
}
export interface FeishuModelStatus {
  configured: boolean;
  model: string | null;
  endpoint_host: string | null;
  model_config_id: string;
  model_config_revision: number;
  daily_limit: number;
  used_today: number;
}
export interface FeishuBinding {
  confirmed: true;
  model_config_id: string;
  model_config_revision: number;
  config_revision: number;
  target_binding_id: string;
}
export interface FeishuConfigInput {
  expected_revision: number;
  app_id: string;
  app_secret: string;
  retain_secret: boolean;
  recipient_open_id: string;
  confirmed: true;
}
export type FeishuScheduleInput =
  | ({ enabled: true; expected_revision: number } & FeishuBinding)
  | { enabled: false; expected_revision: number; confirmed: true };

export class FeishuRequestError extends ServiceError {
  constructor(
    message: string,
    code: string,
    status?: number,
    public readonly uncertain = false,
  ) {
    super(message, code, status);
    this.name = 'FeishuRequestError';
  }
}
export const isFeishuBusinessAuthFailure = (failure: unknown): boolean =>
  failure instanceof ServiceError &&
  failure.status === 401 &&
  (failure.code === 'UNAUTHORIZED' || failure.code === 'DEVICE_UNAUTHORIZED');

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const text = (value: unknown): value is string => typeof value === 'string';
const id = (value: unknown): value is string =>
  text(value) &&
  value.length > 0 &&
  value.length <= 256 &&
  value === value.trim();
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const revision = (value: unknown): value is number =>
  count(value) && value >= 1;
const hash = (value: unknown): value is string =>
  text(value) && /^[a-f0-9]{64}$/.test(value);
const reportID = (value: unknown): value is string =>
  text(value) && /^fr_[a-f0-9]{32}$/.test(value);
const deliveryID = (value: unknown): value is string =>
  text(value) && /^fd_[a-f0-9]{32}$/.test(value);
const timestamp = (value: unknown): value is string => {
  if (!text(value)) return false;
  const parts =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(
      value,
    );
  if (!parts || !Number.isFinite(Date.parse(value))) return false;
  const [
    ,
    year,
    month,
    day,
    hour,
    minute,
    second,
    offsetHour = '0',
    offsetMinute = '0',
  ] = parts;
  const days = new Date(Date.UTC(Number(year), Number(month), 0)).getUTCDate();
  return (
    Number(month) >= 1 &&
    Number(month) <= 12 &&
    Number(day) >= 1 &&
    Number(day) <= days &&
    Number(hour) < 24 &&
    Number(minute) < 60 &&
    Number(second) < 60 &&
    Number(offsetHour) < 24 &&
    Number(offsetMinute) < 60
  );
};
const nullableTime = (value: unknown): value is string | null =>
  value === null || timestamp(value);
const slotTime = (value: unknown): value is string =>
  timestamp(value) &&
  !/\.\d*[1-9]\d*/.test(value) &&
  Date.parse(value) % (12 * 60 * 60 * 1000) === 0;
const slots = (value: unknown): value is ['08:00', '20:00'] =>
  Array.isArray(value) &&
  value.length === 2 &&
  value[0] === '08:00' &&
  value[1] === '20:00';
const state = (value: unknown): value is FeishuState =>
  value === 'running' ||
  value === 'succeeded' ||
  value === 'failed' ||
  value === 'unknown';
const code = (value: unknown): value is string =>
  text(value) && /^[A-Z][A-Z0-9_]{0,127}$/.test(value);
const emptyOrCode = (value: unknown): value is string =>
  value === '' || code(value);
const strings = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every(id);
const noCredentials = (value: Record<string, unknown>): boolean =>
  ![
    'app_secret',
    'secret',
    'recipient_open_id',
    'open_id',
    'api_key',
    'token',
    'access_token',
    'tenant_access_token',
    'webhook',
    'url',
  ].some((key) => Object.hasOwn(value, key));
const windowRange = (value: Record<string, unknown>): boolean =>
  slotTime(value.window_start) &&
  slotTime(value.window_end) &&
  Date.parse(value.window_end) - Date.parse(value.window_start) ===
    12 * 60 * 60 * 1000;
const validBinding = (value: unknown): value is FeishuBinding =>
  record(value) &&
  value.confirmed === true &&
  hash(value.model_config_id) &&
  count(value.model_config_revision) &&
  revision(value.config_revision) &&
  hash(value.target_binding_id);

function invalid(mutation = false): never {
  throw new FeishuRequestError(
    mutation
      ? '返回数据未通过核对，操作结果未知。请只读取状态或回执，不要重复提交。'
      : '返回数据未通过核对，未将 HTTP 状态视为成功。请检查服务版本后重新读取。',
    'INVALID_RESPONSE',
    undefined,
    mutation,
  );
}
function invalidInput(): never {
  throw new FeishuRequestError(
    '请核对必填内容、版本与本次同意后再提交。',
    'INVALID_REQUEST',
  );
}

function schedule(value: unknown): value is FeishuSchedule {
  return (
    record(value) &&
    noCredentials(value) &&
    typeof value.enabled === 'boolean' &&
    revision(value.revision) &&
    value.timezone === 'Asia/Shanghai' &&
    slots(value.slots) &&
    text(value.model_config_id) &&
    count(value.model_config_revision) &&
    count(value.config_revision) &&
    (value.target_binding_id === '' || hash(value.target_binding_id)) &&
    (value.next_window_end === null || slotTime(value.next_window_end)) &&
    (value.last_window_end === null || slotTime(value.last_window_end)) &&
    (value.enabled
      ? hash(value.model_config_id) &&
        revision(value.config_revision) &&
        hash(value.target_binding_id) &&
        slotTime(value.next_window_end)
      : value.model_config_id === '' &&
        value.model_config_revision === 0 &&
        value.config_revision === 0 &&
        value.target_binding_id === '' &&
        value.next_window_end === null)
  );
}
function delivery(value: unknown): value is FeishuDelivery {
  return (
    record(value) &&
    noCredentials(value) &&
    deliveryID(value.id) &&
    reportID(value.report_id) &&
    windowRange(value) &&
    state(value.state) &&
    hash(value.target_binding_id) &&
    revision(value.config_revision) &&
    hash(value.model_config_id) &&
    count(value.model_config_revision) &&
    hash(value.content_hash) &&
    text(value.message_id) &&
    emptyOrCode(value.error_code) &&
    timestamp(value.created_at) &&
    nullableTime(value.completed_at) &&
    (value.state === 'running'
      ? value.completed_at === null &&
        value.message_id === '' &&
        value.error_code === ''
      : timestamp(value.completed_at)) &&
    (value.state !== 'succeeded' ||
      (id(value.message_id) && value.error_code === '')) &&
    (value.state !== 'failed' ||
      (value.message_id === '' && code(value.error_code)))
  );
}
function report(value: unknown): value is FeishuReport {
  return (
    record(value) &&
    noCredentials(value) &&
    reportID(value.id) &&
    windowRange(value) &&
    state(value.state) &&
    text(value.summary) &&
    Array.isArray(value.todos) &&
    value.todos.length <= 8 &&
    value.todos.every(
      (item) =>
        record(item) &&
        noCredentials(item) &&
        text(item.text) &&
        strings(item.evidence_ids) &&
        item.needs_review === true,
    ) &&
    Array.isArray(value.excerpts) &&
    value.excerpts.length <= 3 &&
    value.excerpts.every(
      (item) =>
        record(item) &&
        noCredentials(item) &&
        id(item.message_id) &&
        id(item.conversation_id) &&
        text(item.text) &&
        timestamp(item.occurred_at),
    ) &&
    strings(value.conversation_ids) &&
    value.conversation_ids.length <= 4 &&
    Array.isArray(value.source_refs) &&
    value.source_refs.length <= 4 &&
    value.source_refs.every(
      (item) =>
        record(item) &&
        noCredentials(item) &&
        id(item.conversation_id) &&
        count(item.settings_revision),
    ) &&
    count(value.conversation_count) &&
    value.conversation_count === value.conversation_ids.length &&
    hash(value.model_config_id) &&
    count(value.model_config_revision) &&
    revision(value.config_revision) &&
    hash(value.target_binding_id) &&
    text(value.text) &&
    (value.content_hash === '' || hash(value.content_hash)) &&
    typeof value.truncated === 'boolean' &&
    emptyOrCode(value.error_code) &&
    timestamp(value.created_at) &&
    nullableTime(value.generated_at) &&
    (value.state === 'running'
      ? value.generated_at === null
      : timestamp(value.generated_at)) &&
    (value.trigger === 'manual'
      ? value.schedule_revision === 0
      : value.trigger === 'schedule' && revision(value.schedule_revision)) &&
    (value.state !== 'succeeded' ||
      (value.text.length > 0 &&
        hash(value.content_hash) &&
        timestamp(value.generated_at) &&
        value.error_code === '')) &&
    (value.state !== 'failed' || code(value.error_code)) &&
    (value.delivery === null ||
      (delivery(value.delivery) &&
        value.delivery.report_id === value.id &&
        value.delivery.window_start === value.window_start &&
        value.delivery.window_end === value.window_end &&
        value.delivery.content_hash === value.content_hash &&
        value.delivery.target_binding_id === value.target_binding_id &&
        value.delivery.config_revision === value.config_revision &&
        value.delivery.model_config_id === value.model_config_id &&
        value.delivery.model_config_revision === value.model_config_revision))
  );
}
function status(value: unknown): value is FeishuStatus {
  return (
    record(value) &&
    noCredentials(value) &&
    typeof value.configured === 'boolean' &&
    typeof value.key_configured === 'boolean' &&
    text(value.app_id) &&
    revision(value.config_revision) &&
    (value.target_binding_id === '' || hash(value.target_binding_id)) &&
    text(value.target_label_masked) &&
    text(value.recipient_open_id_masked) &&
    value.recipient_ownership_verified === false &&
    schedule(value.schedule) &&
    (!value.schedule.enabled ||
      (value.configured &&
        value.schedule.config_revision === value.config_revision &&
        value.schedule.target_binding_id === value.target_binding_id)) &&
    (value.latest_report_id === '' || reportID(value.latest_report_id)) &&
    (value.latest_delivery === null || delivery(value.latest_delivery)) &&
    record(value.capabilities) &&
    noCredentials(value.capabilities) &&
    value.capabilities.private_self_only === true &&
    value.capabilities.automatic_retry === false &&
    value.timezone === 'Asia/Shanghai' &&
    slots(value.slots) &&
    (value.configured
      ? value.key_configured &&
        /^cli_[A-Za-z0-9_-]{1,124}$/.test(value.app_id) &&
        hash(value.target_binding_id) &&
        id(value.target_label_masked) &&
        /^ou_\*{3}[A-Za-z0-9_-]{1,4}$/.test(value.recipient_open_id_masked)
      : !value.key_configured &&
        value.app_id === '' &&
        value.target_binding_id === '' &&
        value.target_label_masked === '' &&
        value.recipient_open_id_masked === '')
  );
}

const ERRORS: Record<string, string> = {
  UNAUTHORIZED: '本地服务授权已失效，请重新连接。',
  DEVICE_UNAUTHORIZED: '本机授权已失效，请重新连接。',
  FEISHU_NOT_CONFIGURED: '请先保存自建应用与本人的应用内 Open ID。',
  FEISHU_CONFIG_CHANGED: '飞书配置已变化。请重新读取，旧同意已失效。',
  FEISHU_MODEL_CHANGED: '模型目标或版本已变化。请重新读取，旧同意已失效。',
  FEISHU_SOURCE_CHANGED: '摘要来源已变化或暂停，旧预览已撤下；请重新读取。',
  FEISHU_STALE: '版本或摘要已变化，请重新读取，不会自动重放。',
  FEISHU_EXPIRED: '预览已过发送时限，不会向下一个窗口补发。',
  FEISHU_NOT_FOUND: '未找到该记录，或当前服务版本不提供此接口。',
  FEISHU_CONFIRMATION_REQUIRED: '本次操作需要重新核对并明确同意。',
  FEISHU_KEY_UNAVAILABLE: '本地加密密钥不可用，请检查后台配置；未进行发送。',
  AI_PAUSED: '摘要来源已暂停，旧预览已撤下。',
  AI_NOT_CONFIGURED: '模型未配置；仍可关闭计划或清除飞书配置。',
  AI_QUOTA_EXCEEDED: '今日共享模型额度已用尽；仍可读取记录或关闭计划。',
  STORAGE_DISABLED: '加密消息存储尚未启用，请先检查后台存储配置。',
};

async function request(
  path: string,
  token: string,
  signal: AbortSignal,
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE' = 'GET',
  body?: unknown,
): Promise<{ value: unknown; httpStatus: number }> {
  const mutation = method !== 'GET';
  if (SERVICE.error)
    throw new FeishuRequestError(SERVICE.error, 'CONFIG_ERROR');
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
    mutation ? 40_000 : 10_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/${path}`,
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
    const value: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('已停止等待', 'AbortError');
    if (!response.ok) {
      const errorCode =
        record(value) && record(value.error) && code(value.error.code)
          ? value.error.code
          : 'INVALID_ERROR_RESPONSE';
      throw new FeishuRequestError(
        authErrorMessage(
          errorCode,
          Object.hasOwn(ERRORS, errorCode)
            ? ERRORS[errorCode]
            : mutation
              ? '操作未获有效确认。请读取状态或回执，勿盲目重复提交。'
              : '无法读取飞书数据，请检查本地服务后重新读取。',
        ),
        errorCode,
        response.status,
        mutation &&
          (response.status >= 500 ||
            response.status === 408 ||
            errorCode === 'AUTH_RETRY_REQUIRED' ||
            errorCode === 'INVALID_ERROR_RESPONSE'),
      );
    }
    if (
      (response.status !== 200 && response.status !== 202) ||
      !record(value) ||
      Object.hasOwn(value, 'error')
    )
      invalid(mutation);
    return { value, httpStatus: response.status };
  } catch (failure: unknown) {
    if (failure instanceof FeishuRequestError) throw failure;
    if (timedOut)
      throw new FeishuRequestError(
        mutation
          ? '等待超时，后台可能已经处理。请只读取状态或回执，不要重新提交。'
          : '读取超时，请检查本地服务后重新读取。',
        'TIMEOUT',
        undefined,
        mutation,
      );
    if (controller.signal.aborted)
      throw new FeishuRequestError(
        mutation
          ? '已停止本地等待，未取消后台操作；结果未知，请只读取核实。'
          : '已停止读取。',
        'ABORTED',
        undefined,
        mutation,
      );
    throw new FeishuRequestError(
      mutation
        ? '连接中断，后台可能已经处理；结果未知。请只读取核实，不要重复提交。'
        : '无法读取数据，请检查本地服务后重新读取。',
      'NETWORK_ERROR',
      undefined,
      mutation,
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}
const bindingBody = (input: FeishuBinding): FeishuBinding => ({
  confirmed: true,
  model_config_id: input.model_config_id,
  model_config_revision: input.model_config_revision,
  config_revision: input.config_revision,
  target_binding_id: input.target_binding_id,
});
function checkedStatus(
  value: unknown,
  httpStatus: number,
  mutation = false,
): FeishuStatus {
  if (httpStatus !== 200 || !status(value)) invalid(mutation);
  return value;
}
export async function getFeishuStatus(
  token: string,
  signal: AbortSignal,
): Promise<FeishuStatus> {
  const result = await request('feishu/status', token, signal);
  return checkedStatus(result.value, result.httpStatus);
}
export async function getFeishuSchedule(
  token: string,
  signal: AbortSignal,
): Promise<FeishuSchedule> {
  const result = await request('feishu/schedule', token, signal);
  if (result.httpStatus !== 200 || !schedule(result.value)) invalid();
  return result.value;
}
export async function saveFeishuConfig(
  token: string,
  signal: AbortSignal,
  input: FeishuConfigInput,
): Promise<FeishuStatus> {
  if (
    !revision(input.expected_revision) ||
    !/^cli_[A-Za-z0-9_-]{1,124}$/.test(input.app_id) ||
    !/^ou_[A-Za-z0-9_-]{1,125}$/.test(input.recipient_open_id) ||
    typeof input.retain_secret !== 'boolean' ||
    !text(input.app_secret) ||
    input.app_secret.length > 4096 ||
    input.confirmed !== true ||
    (input.retain_secret
      ? input.app_secret !== ''
      : input.app_secret.trim().length === 0)
  )
    invalidInput();
  const result = await request('feishu/config', token, signal, 'PATCH', {
    expected_revision: input.expected_revision,
    app_id: input.app_id,
    app_secret: input.app_secret,
    retain_secret: input.retain_secret,
    recipient_open_id: input.recipient_open_id,
    confirmed: true,
  });
  const next = checkedStatus(result.value, result.httpStatus, true);
  if (
    next.config_revision <= input.expected_revision ||
    next.app_id !== input.app_id ||
    next.schedule.enabled
  )
    invalid(true);
  return next;
}
export async function clearFeishuConfig(
  token: string,
  signal: AbortSignal,
  input: { expected_revision: number; confirmed: true },
): Promise<FeishuStatus> {
  if (!revision(input.expected_revision) || input.confirmed !== true)
    invalidInput();
  const result = await request('feishu/config', token, signal, 'DELETE', {
    expected_revision: input.expected_revision,
    confirmed: true,
  });
  const next = checkedStatus(result.value, result.httpStatus, true);
  if (
    next.config_revision <= input.expected_revision ||
    next.configured ||
    next.key_configured ||
    next.schedule.enabled ||
    next.target_binding_id !== ''
  )
    invalid(true);
  return next;
}
export async function saveFeishuSchedule(
  token: string,
  signal: AbortSignal,
  input: FeishuScheduleInput,
): Promise<FeishuSchedule> {
  if (
    !revision(input.expected_revision) ||
    input.confirmed !== true ||
    (input.enabled !== false && input.enabled !== true) ||
    (input.enabled && !validBinding(input))
  )
    invalidInput();
  const result = await request('feishu/schedule', token, signal, 'PATCH', {
    enabled: input.enabled,
    expected_revision: input.expected_revision,
    confirmed: true,
    ...(input.enabled ? bindingBody(input) : {}),
  });
  if (
    result.httpStatus !== 200 ||
    !schedule(result.value) ||
    result.value.enabled !== input.enabled ||
    result.value.revision <= input.expected_revision ||
    (input.enabled &&
      (result.value.model_config_id !== input.model_config_id ||
        result.value.model_config_revision !== input.model_config_revision ||
        result.value.config_revision !== input.config_revision ||
        result.value.target_binding_id !== input.target_binding_id))
  )
    invalid(true);
  return result.value;
}
function checkedReport(
  value: unknown,
  httpStatus: number,
  mutation = false,
  expectedID?: string,
): FeishuReport {
  if (
    !record(value) ||
    !report(value.report) ||
    (expectedID && value.report.id !== expectedID) ||
    (httpStatus === 202 && value.report.state !== 'running')
  )
    invalid(mutation);
  return value.report;
}
export async function generateFeishuPreview(
  token: string,
  signal: AbortSignal,
  input: FeishuBinding,
): Promise<FeishuReport> {
  if (!validBinding(input)) invalidInput();
  const result = await request(
    'feishu/preview',
    token,
    signal,
    'POST',
    bindingBody(input),
  );
  const next = checkedReport(result.value, result.httpStatus, true);
  if (
    next.model_config_id !== input.model_config_id ||
    next.model_config_revision !== input.model_config_revision ||
    next.config_revision !== input.config_revision ||
    next.target_binding_id !== input.target_binding_id
  )
    invalid(true);
  return next;
}
export async function getLatestFeishuReport(
  token: string,
  signal: AbortSignal,
): Promise<FeishuReport | null> {
  const result = await request('feishu/reports/latest', token, signal);
  if (
    record(result.value) &&
    result.value.report === null &&
    result.httpStatus === 200
  )
    return null;
  return checkedReport(result.value, result.httpStatus);
}
export async function getFeishuReport(
  token: string,
  signal: AbortSignal,
  reportId: string,
): Promise<FeishuReport> {
  if (!reportID(reportId)) invalidInput();
  const result = await request(
    `feishu/reports/${encodeURIComponent(reportId)}`,
    token,
    signal,
  );
  return checkedReport(result.value, result.httpStatus, false, reportId);
}
export async function sendFeishuReport(
  token: string,
  signal: AbortSignal,
  reportId: string,
  input: FeishuBinding & { content_hash: string },
): Promise<FeishuDelivery> {
  if (!reportID(reportId) || !validBinding(input) || !hash(input.content_hash))
    invalidInput();
  const result = await request(
    `feishu/reports/${encodeURIComponent(reportId)}/send`,
    token,
    signal,
    'POST',
    { ...bindingBody(input), content_hash: input.content_hash },
  );
  const value = result.value;
  if (
    !record(value) ||
    !delivery(value.delivery) ||
    value.delivery.report_id !== reportId ||
    value.delivery.content_hash !== input.content_hash ||
    value.delivery.model_config_id !== input.model_config_id ||
    value.delivery.model_config_revision !== input.model_config_revision ||
    value.delivery.config_revision !== input.config_revision ||
    value.delivery.target_binding_id !== input.target_binding_id ||
    (result.httpStatus === 202 && value.delivery.state !== 'running')
  )
    invalid(true);
  return value.delivery;
}
export async function getFeishuDelivery(
  token: string,
  signal: AbortSignal,
  deliveryId: string,
): Promise<FeishuDelivery> {
  if (!deliveryID(deliveryId)) invalidInput();
  const result = await request(
    `feishu/deliveries/${encodeURIComponent(deliveryId)}`,
    token,
    signal,
  );
  if (
    !record(result.value) ||
    !delivery(result.value.delivery) ||
    result.value.delivery.id !== deliveryId ||
    (result.httpStatus === 202 && result.value.delivery.state !== 'running')
  )
    invalid();
  return result.value.delivery;
}
// This dedicated read preserves exact device-auth errors: a provider/platform 401
// must never be re-labelled as a revoked local business credential.
export async function getFeishuModelStatus(
  token: string,
  signal: AbortSignal,
): Promise<FeishuModelStatus> {
  const result = await request('ai/status', token, signal);
  const value = result.value;
  if (
    result.httpStatus !== 200 ||
    !record(value) ||
    typeof value.configured !== 'boolean' ||
    !(value.model === null || text(value.model)) ||
    !(value.endpoint_host === null || text(value.endpoint_host)) ||
    !text(value.model_config_id) ||
    !count(value.model_config_revision) ||
    !count(value.daily_limit) ||
    !count(value.used_today) ||
    value.sending_enabled !== false ||
    !noCredentials(value) ||
    (value.configured &&
      (!hash(value.model_config_id) ||
        !id(value.model) ||
        !id(value.endpoint_host)))
  )
    invalid();
  return {
    configured: value.configured,
    model: value.model,
    endpoint_host: value.endpoint_host,
    model_config_id: value.model_config_id,
    model_config_revision: value.model_config_revision,
    daily_limit: value.daily_limit,
    used_today: value.used_today,
  };
}
