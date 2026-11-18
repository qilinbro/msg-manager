import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export type ReceiveChannel = 'email-imap' | 'telegram-user';
export interface ReceiveMonitorStatus {
  enabled: boolean;
  interval_seconds: number;
  state: string;
  last_success_at: string | null;
  last_error_code: string | null;
  read_only: true;
  sending_enabled: false;
  peer_ids?: string[];
  expected_account_id?: string | null;
  baseline_mode?: string;
  skipped?: number;
  skipped_count?: number;
  missing_uid_count?: number;
  last_gap_reason?: string | null;
  limited?: boolean;
  retry_not_before?: string | null;
}
export interface ReceiveMonitorSettings {
  enabled: boolean;
  interval_seconds?: number;
  peer_ids?: string[];
  expected_account_id?: string;
}
const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const code = (value: unknown): value is string =>
  typeof value === 'string' && /^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(value);
const identifier = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[1-9][0-9]{0,18}$/.test(value) &&
  BigInt(value) <= 9_223_372_036_854_775_807n;
const interval = (value: unknown): value is number =>
  Number.isInteger(value) && Number(value) >= 30 && Number(value) <= 900;
const count = (value: unknown) =>
  Number.isSafeInteger(value) && Number(value) >= 0;
const date = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length <= 64 &&
  /^\d{4}-\d{2}-\d{2}T.+(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
  Number.isFinite(Date.parse(value));

const ERROR_MESSAGES: Record<string, string> = {
  EMAIL_NOT_CONFIGURED: '请先在本机配置邮箱，再刷新状态。',
  EMAIL_AUTH_FAILED: '请检查邮箱 IMAP 授权及本机配置，确认后再开启。',
  EMAIL_UNREACHABLE: '邮箱暂不可达，请检查网络和本机邮箱配置。',
  EMAIL_UIDVALIDITY_CHANGED:
    '邮箱记录代次已变化，请核对后重新开启；缺口会保留记录。',
  EMAIL_IDENTITY_CHANGED: '邮箱账号已变化，请核对本机配置后重新开启。',
  TELEGRAM_NOT_VERIFIED: '请先核验 Telegram 本人账号并读取私聊列表。',
  TELEGRAM_IDENTITY_MISMATCH: 'Telegram 账号已变化，请重新核验并选择私聊。',
  TELEGRAM_PEER_NOT_SELECTED:
    '请重新读取当前账号的私聊列表，并明确选择要接收的私聊。',
  TELEGRAM_PEER_NOT_FOUND: '所选私聊已不可用，请重新读取列表并核对选择。',
  TELEGRAM_LOGGED_OUT: 'Telegram 登录已失效，请在本机重新初始化后核验。',
  TELEGRAM_AUTH_REQUIRED: 'Telegram 登录已失效，请在本机重新初始化后核验。',
  TELEGRAM_AUTH_FAILED: '请检查 Telegram 本机授权，重新初始化后核验。',
  TELEGRAM_FLOOD_WAIT:
    'Telegram 要求等待，后台会按服务状态处理；请勿反复提交。',
  TELEGRAM_REQUIRES_INITIALIZATION: '请先在本机完成 Telegram 初始化。',
  TELEGRAM_NETWORK_ERROR: 'Telegram 暂不可达，请检查网络和本机代理配置。',
  TELEGRAM_BUSY: 'Telegram 正在处理另一项操作，请刷新状态后再保存。',
  STORAGE_DISABLED: '请先启用本地加密消息存储。',
  STORE_UNAVAILABLE: '本地加密消息库暂不可用，请检查后台后刷新状态。',
  INVALID_REQUEST: '请检查接收间隔及私聊选择，再保存设置。',
};

export function receiveMonitorError(value: string | null): string | null {
  return value
    ? (ERROR_MESSAGES[value] ??
        '上次后台接收未完成，请刷新本机状态并核对账号、网络和存储配置。')
    : null;
}

function validate(
  channel: ReceiveChannel,
  value: unknown,
): ReceiveMonitorStatus {
  if (
    !record(value) ||
    typeof value.enabled !== 'boolean' ||
    !interval(value.interval_seconds) ||
    !code(value.state) ||
    !(value.last_success_at === null || date(value.last_success_at)) ||
    !(
      value.retry_not_before === undefined ||
      value.retry_not_before === null ||
      date(value.retry_not_before)
    ) ||
    !(value.last_error_code === null || code(value.last_error_code)) ||
    value.read_only !== true ||
    value.sending_enabled !== false ||
    (value.baseline_mode !== undefined && !code(value.baseline_mode)) ||
    ['skipped', 'skipped_count', 'missing_uid_count'].some(
      (key) => value[key] !== undefined && !count(value[key]),
    ) ||
    !(
      value.last_gap_reason === undefined ||
      value.last_gap_reason === null ||
      code(value.last_gap_reason)
    ) ||
    (value.limited !== undefined && typeof value.limited !== 'boolean') ||
    [
      'password',
      'token',
      'api_hash',
      'session',
      'cookies',
      'authorization',
    ].some((key) => Object.hasOwn(value, key)) ||
    (channel === 'telegram-user' &&
      (!Array.isArray(value.peer_ids) ||
        value.peer_ids.length > 10 ||
        !value.peer_ids.every(identifier) ||
        new Set(value.peer_ids).size !== value.peer_ids.length ||
        !(
          value.expected_account_id === undefined ||
          value.expected_account_id === null ||
          value.expected_account_id === '' ||
          identifier(value.expected_account_id)
        )))
  )
    throw new ServiceError(
      '后台接收状态格式不匹配，请更新服务后刷新；手动读取仍可使用。',
      'INVALID_RESPONSE',
    );
  return value as unknown as ReceiveMonitorStatus;
}

async function request(
  channel: ReceiveChannel,
  token: string,
  signal: AbortSignal,
  settings?: ReceiveMonitorSettings,
): Promise<ReceiveMonitorStatus> {
  if (!['email-imap', 'telegram-user'].includes(channel))
    throw new ServiceError('接收渠道无效。', 'INVALID_REQUEST');
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
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
    settings ? 40_000 : 10_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/connections/${channel}/monitor`,
      token,
      {
        method: settings ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(settings ? { 'Content-Type': 'application/json' } : {}),
        },
        body: settings ? JSON.stringify(settings) : undefined,
        signal: controller.signal,
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (!response.ok) {
      if ([404, 405, 501].includes(response.status))
        throw new ServiceError(
          '当前后台版本暂不支持自动接收；更新并重启服务后可再次检查，手动读取仍可使用。',
          'MONITOR_UNSUPPORTED',
          response.status,
        );
      if (response.status === 401)
        throw new ServiceError(
          '本地服务授权已失效，请重新连接。',
          'UNAUTHORIZED',
          401,
        );
      const errorCode =
        record(payload) && record(payload.error) && code(payload.error.code)
          ? payload.error.code
          : 'MONITOR_UNAVAILABLE';
      throw new ServiceError(
        authErrorMessage(
          errorCode,
          receiveMonitorError(errorCode) ??
            '后台接收设置未保存，请刷新后核对。',
        ),
        errorCode,
        response.status,
      );
    }
    return validate(channel, payload);
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        settings
          ? '设置等待超时，后台可能已保存。请刷新状态核对，不要直接重复提交。'
          : '读取后台接收状态超时，请稍后刷新。',
        'TIMEOUT',
      );
    if (error instanceof ServiceError) throw error;
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    throw new ServiceError(
      settings
        ? '连接中断，设置可能已保存。请刷新状态核对后再操作。'
        : '无法读取后台接收状态，请确认本地服务正在运行。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

export const getReceiveMonitor = (
  channel: ReceiveChannel,
  token: string,
  signal: AbortSignal,
) => request(channel, token, signal);
export function saveReceiveMonitor(
  channel: ReceiveChannel,
  token: string,
  signal: AbortSignal,
  settings: ReceiveMonitorSettings,
) {
  if (settings.enabled === false)
    return request(channel, token, signal, { enabled: false });
  if (settings.enabled !== true || !interval(settings.interval_seconds ?? 60))
    throw new ServiceError('读取间隔须为 30—900 秒的整数。', 'INVALID_REQUEST');
  if (channel === 'telegram-user') {
    if (
      !identifier(settings.expected_account_id) ||
      !Array.isArray(settings.peer_ids) ||
      settings.peer_ids.length < 1 ||
      settings.peer_ids.length > 10 ||
      !settings.peer_ids.every(identifier) ||
      new Set(settings.peer_ids).size !== settings.peer_ids.length
    )
      throw new ServiceError(
        '请先核验账号，再明确选择当前列表中的 1—10 个私聊。',
        'INVALID_REQUEST',
      );
    return request(channel, token, signal, {
      enabled: true,
      interval_seconds: settings.interval_seconds ?? 60,
      expected_account_id: settings.expected_account_id,
      peer_ids: [...settings.peer_ids],
    });
  }
  return request(channel, token, signal, {
    enabled: true,
    interval_seconds: settings.interval_seconds ?? 60,
  });
}
