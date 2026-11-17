import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export interface DigestSchedule {
  enabled: boolean;
  local_time: string;
  timezone: 'Asia/Shanghai';
  revision: number;
  last_run_day: string;
  model_config_id: string;
  model_config_revision: number;
}
export interface DigestReminder {
  conversation_id: string;
  title: string;
  platform: string;
  conversation_kind: 'private' | 'group';
  priority: string;
  reason: string;
  evidence_ids: string[];
}
export interface DigestReport {
  id: string;
  day: string;
  state: string;
  summary: string;
  reminders: DigestReminder[];
  evidence_ids: string[];
  conversation_count: number;
  error_code?: string;
  generated_at: string | null;
}
export interface DigestModelConsent {
  confirmed: true;
  model_config_id: string;
  model_config_revision: number;
}
export class DigestRequestError extends ServiceError {
  readonly uncertain: boolean;
  constructor(
    message: string,
    code: string,
    status?: number,
    uncertain = false,
  ) {
    super(message, code, status);
    this.name = 'DigestRequestError';
    this.uncertain = uncertain;
  }
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown): value is string => typeof value === 'string';
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const texts = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every(text);
const validSchedule = (value: unknown): value is DigestSchedule =>
  record(value) &&
  typeof value.enabled === 'boolean' &&
  text(value.local_time) &&
  /^([01]\d|2[0-3]):[0-5]\d$/.test(value.local_time) &&
  value.timezone === 'Asia/Shanghai' &&
  count(value.revision) &&
  value.revision >= 1 &&
  text(value.last_run_day) &&
  text(value.model_config_id) &&
  count(value.model_config_revision) &&
  (!value.enabled || value.model_config_id.length > 0);
const validReport = (value: unknown): value is DigestReport =>
  record(value) &&
  text(value.id) &&
  value.id.length > 0 &&
  text(value.day) &&
  /^\d{4}-\d{2}-\d{2}$/.test(value.day) &&
  text(value.state) &&
  text(value.summary) &&
  Array.isArray(value.reminders) &&
  value.reminders.every(
    (item) =>
      record(item) &&
      text(item.conversation_id) &&
      text(item.title) &&
      text(item.platform) &&
      (item.conversation_kind === 'private' ||
        item.conversation_kind === 'group') &&
      text(item.priority) &&
      text(item.reason) &&
      texts(item.evidence_ids),
  ) &&
  texts(value.evidence_ids) &&
  count(value.conversation_count) &&
  (value.error_code === undefined || text(value.error_code)) &&
  (value.generated_at === null ||
    (text(value.generated_at) &&
      Number.isFinite(Date.parse(value.generated_at))));

const ERRORS: Record<string, string> = {
  AI_NOT_CONFIGURED: '请先配置模型并刷新目标，再明确授权摘要数据范围。',
  DIGEST_MODEL_CHANGED:
    '模型目标或版本已变化。请重新读取状态并核对，旧同意不会自动用于新目标。',
  DIGEST_STALE: '每日计划已有变化。请重新读取实际计划，再决定是否保存。',
  DIGEST_INVALID: '请选择有效时间；每日计划统一使用 Asia/Shanghai 时区。',
  DIGEST_CONFIRMATION_REQUIRED:
    '请明确核对并同意本次摘要的数据范围和模型目标。',
  AI_QUOTA_EXCEEDED:
    '今日共享模型额度或模型服务配额已达限制。请先读取最近摘要。',
  AI_BUSY: '模型正在处理其他请求，本次未开始。请先读取最近摘要状态。',
  DIGEST_TIMEOUT: '摘要等待超时，结果需核实。请读取最近摘要，不要重复提交。',
  DIGEST_INTERRUPTED:
    '摘要处理已中断，结果需核实。请读取最近摘要，不要重复提交。',
  AI_TIMEOUT: '模型响应超时，可能已处理并计费。请先读取最近摘要。',
  AI_INVALID_OUTPUT: '模型结果或证据无效，本次摘要未成功。请读取保存的状态。',
  STORAGE_DISABLED: '每日摘要需要先启用本地加密消息库。',
};
async function request(
  token: string,
  path: string,
  method: 'GET' | 'PATCH' | 'POST',
  signal?: AbortSignal,
  body?: unknown,
): Promise<unknown> {
  if (SERVICE.error)
    throw new DigestRequestError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) controller.abort();
  let timedOut = false;
  const timer = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    method === 'POST' ? 125_000 : 10_000,
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
      const code =
        record(value) && record(value.error) && text(value.error.code)
          ? value.error.code
          : 'DIGEST_UNAVAILABLE';
      throw new DigestRequestError(
        authErrorMessage(
          code,
          response.status === 401
            ? '摘要请求未获授权。请核对本地服务状态；未知错误不会移除本地业务授权。'
            : Object.hasOwn(ERRORS, code)
              ? ERRORS[code]
              : '每日摘要暂不可用。请先读取实际计划及最近摘要，不会自动重试。',
        ),
        code,
        response.status,
        method !== 'GET' && (response.status >= 500 || response.status === 408),
      );
    }
    return value;
  } catch (failure: unknown) {
    if (failure instanceof ServiceError) throw failure;
    throw new DigestRequestError(
      method === 'GET'
        ? signal?.aborted
          ? '已停止读取摘要状态。'
          : '无法读取摘要状态，请检查本地服务后重新读取。'
        : `${signal?.aborted ? '已停止本地等待' : timedOut ? '等待超时' : '连接中断'}；不代表后台未处理，可能已计费或保存。请先读取实际计划和最近摘要，不要重复提交。`,
      signal?.aborted ? 'LOCAL_ABORT' : timedOut ? 'TIMEOUT' : 'NETWORK_ERROR',
      undefined,
      method !== 'GET',
    );
  } finally {
    window.clearTimeout(timer);
    signal?.removeEventListener('abort', abort);
  }
}
const requireConsent = (input: DigestModelConsent) => {
  if (
    input.confirmed !== true ||
    !text(input.model_config_id) ||
    !input.model_config_id ||
    !count(input.model_config_revision)
  )
    throw new DigestRequestError(
      '请先核对并同意当前模型目标、配置版本及数据范围。',
      'CONSENT_REQUIRED',
    );
};
export const getDigestSchedule = async (
  token: string,
  signal?: AbortSignal,
): Promise<DigestSchedule> => {
  const value = await request(token, 'digest/schedule', 'GET', signal);
  if (!validSchedule(value))
    throw new DigestRequestError(
      '摘要设置返回格式无效，未将其视为已授权计划。',
      'INVALID_RESPONSE',
    );
  return value;
};
export const saveDigestSchedule = async (
  token: string,
  input: {
    enabled: boolean;
    local_time: string;
    expected_revision: number;
    confirmed: true;
    model_config_id?: string;
    model_config_revision?: number;
  },
  signal?: AbortSignal,
): Promise<DigestSchedule> => {
  if (
    typeof input.enabled !== 'boolean' ||
    input.confirmed !== true ||
    !count(input.expected_revision) ||
    input.expected_revision < 1 ||
    !/^([01]\d|2[0-3]):[0-5]\d$/.test(input.local_time)
  )
    throw new DigestRequestError(
      '请核对每日计划、时间和当前版本。',
      'INVALID_REQUEST',
    );
  if (input.enabled) requireConsent(input as DigestModelConsent);
  const value = await request(token, 'digest/schedule', 'PATCH', signal, {
    enabled: input.enabled,
    local_time: input.local_time,
    timezone: 'Asia/Shanghai',
    expected_revision: input.expected_revision,
    confirmed: true,
    ...(input.enabled
      ? {
          model_config_id: input.model_config_id,
          model_config_revision: input.model_config_revision,
        }
      : {}),
  });
  if (!validSchedule(value))
    throw new DigestRequestError(
      '摘要设置保存返回无效，保存结果未知。请读取实际计划，不要重复提交。',
      'INVALID_RESPONSE',
      undefined,
      true,
    );
  return value;
};
export const getLatestDigest = async (
  token: string,
  signal?: AbortSignal,
): Promise<DigestReport | null> => {
  const value = await request(token, 'digest/latest', 'GET', signal);
  if (
    !record(value) ||
    value.sending_enabled !== false ||
    (value.report !== null && !validReport(value.report))
  )
    throw new DigestRequestError('摘要结果返回格式无效。', 'INVALID_RESPONSE');
  return value.report as DigestReport | null;
};
export const runDigestNow = async (
  token: string,
  input: DigestModelConsent,
  signal?: AbortSignal,
): Promise<DigestReport> => {
  requireConsent(input);
  const value = await request(token, 'digest/run', 'POST', signal, {
    confirmed: true,
    model_config_id: input.model_config_id,
    model_config_revision: input.model_config_revision,
  });
  if (
    !record(value) ||
    value.sending_enabled !== false ||
    !validReport(value.report)
  )
    throw new DigestRequestError(
      '摘要返回格式无效，运行结果未知。请读取最近摘要，不要重复提交。',
      'INVALID_RESPONSE',
      undefined,
      true,
    );
  return value.report;
};
