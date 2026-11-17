import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export interface DigestSchedule {
  enabled: boolean;
  local_time: string;
  timezone: 'Asia/Shanghai';
  revision: number;
  last_run_day: string;
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

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const date = (value: unknown) =>
  value === null || (typeof value === 'string' && value.length <= 64);
const validSchedule = (value: unknown): value is DigestSchedule =>
  record(value) &&
  typeof value.enabled === 'boolean' &&
  typeof value.local_time === 'string' &&
  /^([01]\d|2[0-3]):[0-5]\d$/.test(value.local_time) &&
  value.timezone === 'Asia/Shanghai' &&
  typeof value.revision === 'number' &&
  Number.isSafeInteger(value.revision) &&
  value.revision >= 1 &&
  typeof value.last_run_day === 'string';
const validReport = (value: unknown): value is DigestReport =>
  record(value) &&
  typeof value.id === 'string' &&
  typeof value.day === 'string' &&
  typeof value.state === 'string' &&
  typeof value.summary === 'string' &&
  Array.isArray(value.reminders) &&
  value.reminders.every(
    (item) => record(item) && typeof item.title === 'string',
  ) &&
  Array.isArray(value.evidence_ids) &&
  Number.isSafeInteger(value.conversation_count) &&
  date(value.generated_at);

async function request<T>(
  token: string,
  path: string,
  method: 'GET' | 'PATCH' | 'POST',
  body?: unknown,
): Promise<T> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const response = await authorizedFetch(
    `${SERVICE.url}/api/v1/${path}`,
    token,
    {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/json',
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    },
  );
  const value: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    if (response.status === 401)
      throw new ServiceError(
        '访问令牌已失效，请重新连接。',
        'UNAUTHORIZED',
        401,
      );
    const code =
      record(value) &&
      record(value.error) &&
      typeof value.error.code === 'string'
        ? value.error.code
        : 'DIGEST_UNAVAILABLE';
    throw new ServiceError(
      authErrorMessage(
        code,
        code === 'AI_NOT_CONFIGURED'
          ? '请先配置模型。'
          : '每日摘要暂时不可用，请稍后重试。',
      ),
      code,
      response.status,
    );
  }
  return value as T;
}

export const getDigestSchedule = async (
  token: string,
  signal?: AbortSignal,
) => {
  void signal;
  const value = await request<unknown>(token, 'digest/schedule', 'GET');
  if (!validSchedule(value))
    throw new ServiceError('摘要设置返回格式无效。', 'INVALID_RESPONSE');
  return value;
};
export const saveDigestSchedule = async (
  token: string,
  input: {
    enabled: boolean;
    local_time: string;
    expected_revision: number;
    confirmed: true;
  },
) => {
  const value = await request<unknown>(token, 'digest/schedule', 'PATCH', {
    ...input,
    timezone: 'Asia/Shanghai',
  });
  if (!validSchedule(value))
    throw new ServiceError('摘要设置保存结果无效。', 'INVALID_RESPONSE');
  return value;
};
export const getLatestDigest = async (token: string) => {
  const value = await request<unknown>(token, 'digest/latest', 'GET');
  if (!record(value) || (value.report !== null && !validReport(value.report)))
    throw new ServiceError('摘要结果返回格式无效。', 'INVALID_RESPONSE');
  return value.report as DigestReport | null;
};
export const runDigestNow = async (token: string) => {
  const value = await request<unknown>(token, 'digest/run', 'POST', {
    confirmed: true,
  });
  if (!validReport(value))
    throw new ServiceError('摘要结果返回格式无效。', 'INVALID_RESPONSE');
  return value;
};
