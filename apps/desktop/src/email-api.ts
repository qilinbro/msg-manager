import { SERVICE, ServiceError } from './api';

export type EmailState =
  | 'not_configured'
  | 'ready'
  | 'syncing'
  | 'synced'
  | 'auth_failed'
  | 'unreachable'
  | 'protocol_error';

export interface EmailResult {
  inserted: number;
  duplicates: number;
  skipped: number;
  limited: boolean;
}

export interface EmailStatus {
  configured: boolean;
  state: EmailState;
  account: string | null;
  folder: string | null;
  last_synced_at: string | null;
  last_result: EmailResult | null;
  sending_enabled: false;
}

export interface EmailSyncResult extends EmailResult {
  conversations: number;
  synced_at: string;
  sending_enabled: false;
}

const ERROR_MESSAGES: Record<string, string> = {
  EMAIL_NOT_CONFIGURED:
    '邮箱尚未配置，请按项目邮箱接入说明在本机设置后重启服务。',
  EMAIL_AUTH_FAILED:
    '邮箱验证失败，请检查邮箱服务是否已开启，以及本机配置的账号和授权码。',
  EMAIL_UNREACHABLE: '暂时无法访问邮箱服务，请检查网络和本机邮箱配置。',
  EMAIL_PROTOCOL_ERROR: '邮箱服务返回内容暂时无法处理，请检查邮箱设置后重试。',
  EMAIL_BUSY: '已有邮箱同步正在进行，请更新状态后再试。',
  STORAGE_DISABLED: '本地消息存储尚未启用，请先启用加密存储后再同步邮箱。',
  STORE_UNAVAILABLE: '本地消息存储暂不可用，请稍后更新状态。',
  IMPORT_CONFLICT: '邮件标识与已有记录存在冲突，本次未保存，请核对邮箱后重试。',
  INVALID_REQUEST: '邮箱同步请求无法处理，请刷新工作台后重试。',
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const isCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const nullableString = (value: unknown): value is string | null =>
  value === null || typeof value === 'string';
const validDate = (value: unknown): value is string =>
  typeof value === 'string' && Number.isFinite(Date.parse(value));
const isResult = (value: unknown): value is EmailResult =>
  isRecord(value) &&
  isCount(value.inserted) &&
  isCount(value.duplicates) &&
  isCount(value.skipped) &&
  typeof value.limited === 'boolean';

async function request(
  token: string,
  signal: AbortSignal,
  sync: boolean,
): Promise<unknown> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    sync ? 40_000 : 10_000,
  );
  try {
    const response = await fetch(
      `${SERVICE.url}/api/v1/connections/email-imap/${sync ? 'sync' : 'status'}`,
      {
        method: sync ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(sync ? { 'Content-Type': 'application/json' } : {}),
        },
        body: sync ? '{}' : undefined,
        signal: controller.signal,
        cache: 'no-store',
        credentials: 'omit',
        redirect: 'error',
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (!response.ok) {
      if (response.status === 401)
        throw new ServiceError(
          '访问令牌无效，请重新连接本地服务。',
          'UNAUTHORIZED',
          401,
        );
      const code =
        isRecord(payload) &&
        isRecord(payload.error) &&
        typeof payload.error.code === 'string'
          ? payload.error.code
          : 'EMAIL_SERVICE_ERROR';
      throw new ServiceError(
        Object.hasOwn(ERROR_MESSAGES, code)
          ? ERROR_MESSAGES[code]
          : '暂时无法处理邮箱，请稍后更新状态。',
        code,
        response.status,
      );
    }
    return payload;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        sync
          ? '同步等待超时，后台可能已保存同步结果。请先更新状态，确认后再同步。'
          : '邮箱状态读取超时，请稍后更新状态。',
        'TIMEOUT',
      );
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (error instanceof ServiceError) throw error;
    throw new ServiceError(
      sync
        ? '连接中断，后台可能已完成同步。请先更新状态并刷新消息，再决定是否重试。'
        : '无法读取邮箱状态，请确认本地服务正在运行。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}

export async function getEmailStatus(
  token: string,
  signal: AbortSignal,
): Promise<EmailStatus> {
  const value = await request(token, signal, false);
  const states: EmailState[] = [
    'not_configured',
    'ready',
    'syncing',
    'synced',
    'auth_failed',
    'unreachable',
    'protocol_error',
  ];
  if (
    !isRecord(value) ||
    typeof value.configured !== 'boolean' ||
    !states.includes(value.state as EmailState) ||
    !nullableString(value.account) ||
    !nullableString(value.folder) ||
    !(value.last_synced_at === null || validDate(value.last_synced_at)) ||
    !(value.last_result === null || isResult(value.last_result)) ||
    value.sending_enabled !== false
  )
    throw new ServiceError(
      '邮箱状态格式不匹配，请检查服务版本。',
      'INVALID_RESPONSE',
    );
  return value as unknown as EmailStatus;
}

export async function syncEmail(
  token: string,
  signal: AbortSignal,
): Promise<EmailSyncResult> {
  const value = await request(token, signal, true);
  if (
    !isResult(value) ||
    !isRecord(value) ||
    !isCount(value.conversations) ||
    !validDate(value.synced_at) ||
    value.sending_enabled !== false
  )
    throw new ServiceError(
      '同步结果暂时无法确认，请先更新状态并刷新消息。',
      'INVALID_RESPONSE',
    );
  return value as unknown as EmailSyncResult;
}
