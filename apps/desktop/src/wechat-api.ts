import { SERVICE, ServiceError } from './api';

export type WechatState =
  | 'not_configured'
  | 'ready'
  | 'reading'
  | 'read'
  | 'invalid_key'
  | 'unavailable'
  | 'unsupported_schema'
  | 'changed'
  | 'not_found';

export interface WechatReadResult {
  inserted: number;
  duplicates: number;
  conversations: number;
  skipped: number;
  limited: boolean;
  read_at: string;
  read_only: true;
  sending_enabled: false;
}

export interface WechatStatus {
  configured: boolean;
  state: WechatState;
  account_id: string | null;
  read_only: true;
  last_read_at: string | null;
  last_result: WechatReadResult | null;
  sending_enabled: false;
  // null allows manual reading to keep working against a pre-monitor service.
  monitor: WechatMonitorStatus | null;
}

export interface WechatMonitorSettings {
  enabled: boolean;
  peer_ids: string[];
  interval_seconds: number;
}

export interface WechatMonitorStatus extends WechatMonitorSettings {
  state: 'disabled' | 'waiting' | 'reading' | 'error' | 'not_configured';
  last_cycle_at: string | null;
  last_error_code: string | null;
  last_result: WechatReadResult | null;
  read_only: true;
  sending_enabled: false;
}

export interface WechatSession {
  peer_id: string;
  name: string;
}

const STATES: WechatState[] = [
  'not_configured',
  'ready',
  'reading',
  'read',
  'invalid_key',
  'unavailable',
  'unsupported_schema',
  'changed',
  'not_found',
];

const ERROR_MESSAGES: Record<string, string> = {
  WECHAT_NOT_CONFIGURED:
    '请按微信接入说明在本机配置数据库后重启服务。数据库密钥不在工作台中填写。',
  WECHAT_INVALID_KEY:
    '微信数据库密钥或加密格式不匹配，请检查本机配置。此次读取未保存。',
  WECHAT_UNAVAILABLE:
    '微信数据库暂时无法读取，请检查本机配置、文件访问权限和数据库状态。',
  WECHAT_TIMEOUT:
    '微信数据库读取超时，请稍后重试。读取未完成，不表示密钥或文件权限错误。',
  WECHAT_UNSUPPORTED_SCHEMA:
    '当前微信数据库结构尚未支持，请保留现有数据，等待适配后再试。',
  WECHAT_CHANGED:
    '读取期间数据库发生变化，此次未保存。请稍后重新读取会话再试。',
  WECHAT_NOT_FOUND: '所选私聊暂时无法找到，请重新读取会话列表。',
  WECHAT_BUSY: '已有微信读取正在进行，请先更新状态再试。',
  STORAGE_DISABLED: '请先启用本地加密存储，再保存微信消息。',
  STORE_UNAVAILABLE: '本地消息存储暂不可用，请更新状态后重试。',
  IMPORT_CONFLICT:
    '消息标识与已有记录存在冲突，此次未保存。请核对本机数据库来源。',
  INVALID_INPUT: '会话选择无效，请重新读取列表并选择一个私聊。',
  INVALID_REQUEST: '微信读取请求无法处理，请刷新工作台后重试。',
  NOT_IMPLEMENTED: '后台尚未更新，请更新后重启后台，再开启自动读取。',
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const isCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const validDate = (value: unknown): value is string =>
  typeof value === 'string' && Number.isFinite(Date.parse(value));
const validLabel = (value: unknown, limit: number): value is string =>
  typeof value === 'string' &&
  value.trim().length > 0 &&
  value.trim() === value &&
  Array.from(value).length <= limit &&
  !/[\u0000-\u001f\u007f]/.test(value);
const readOnly = (value: Record<string, unknown>) =>
  value.read_only === true && value.sending_enabled === false;
const isResult = (value: unknown): value is WechatReadResult =>
  isRecord(value) &&
  readOnly(value) &&
  isCount(value.inserted) &&
  isCount(value.duplicates) &&
  isCount(value.conversations) &&
  isCount(value.skipped) &&
  typeof value.limited === 'boolean' &&
  validDate(value.read_at);

const validPeer = (value: unknown): value is string =>
  validLabel(value, 128) && !/[\\/@]/.test(value);
const validMonitorSettings = (value: unknown): value is WechatMonitorSettings =>
  isRecord(value) &&
  typeof value.enabled === 'boolean' &&
  Number.isInteger(value.interval_seconds) &&
  Number(value.interval_seconds) >= 10 &&
  Number(value.interval_seconds) <= 300 &&
  Array.isArray(value.peer_ids) &&
  value.peer_ids.length <= 20 &&
  (!value.enabled || value.peer_ids.length > 0) &&
  value.peer_ids.every(validPeer) &&
  new Set(value.peer_ids).size === value.peer_ids.length;

const isMonitor = (value: unknown): value is WechatMonitorStatus =>
  isRecord(value) &&
  validMonitorSettings(value) &&
  readOnly(value) &&
  ['disabled', 'waiting', 'reading', 'error', 'not_configured'].includes(
    String(value.state),
  ) &&
  (value.last_cycle_at === null || validDate(value.last_cycle_at)) &&
  (value.last_error_code === null ||
    (typeof value.last_error_code === 'string' &&
      /^[A-Z][A-Z0-9_]{0,63}$/.test(value.last_error_code))) &&
  (value.last_result === null || isResult(value.last_result));

export function wechatMonitorError(code: string): string {
  return Object.hasOwn(ERROR_MESSAGES, code)
    ? ERROR_MESSAGES[code]
    : '后台本轮读取未完成，将按设置的频率再次检查。可先查看已保存的消息。';
}

async function request(
  token: string,
  signal: AbortSignal,
  action: 'status' | 'sessions' | 'read' | 'monitor',
  input?: Record<string, unknown>,
): Promise<unknown> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const mutation = action === 'read' || action === 'monitor';
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    action === 'status' ? 10_000 : 45_000,
  );
  try {
    const response = await fetch(
      `${SERVICE.url}/api/v1/connections/wechat-db/${action}`,
      {
        method: mutation ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(mutation ? { 'Content-Type': 'application/json' } : {}),
        },
        body: mutation ? JSON.stringify(input) : undefined,
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
          : 'WECHAT_SERVICE_ERROR';
      throw new ServiceError(
        Object.hasOwn(ERROR_MESSAGES, code)
          ? ERROR_MESSAGES[code]
          : '暂时无法读取微信数据，请更新状态后重试。',
        code,
        response.status,
      );
    }
    return payload;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        action === 'monitor'
          ? '后台读取设置的结果尚未确认，请先更新状态。工作台不会自动重试设置。'
          : action === 'read'
            ? '读取等待超时，后台可能已保存消息。请先更新状态并查看消息，再决定是否重试。'
            : '微信状态或会话读取超时，请先更新状态再试。',
        'TIMEOUT',
      );
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (error instanceof ServiceError) throw error;
    throw new ServiceError(
      action === 'monitor'
        ? '连接中断，后台设置可能已经保存。请先更新状态确认。'
        : action === 'read'
          ? '连接中断，后台可能已保存消息。请先更新状态并查看消息，再决定是否重试。'
          : '无法读取微信状态或会话，请确认本地服务正在运行。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}

export async function getWechatStatus(
  token: string,
  signal: AbortSignal,
): Promise<WechatStatus> {
  const value = await request(token, signal, 'status');
  if (
    !isRecord(value) ||
    !readOnly(value) ||
    typeof value.configured !== 'boolean' ||
    !STATES.includes(value.state as WechatState) ||
    !(value.account_id === null || validLabel(value.account_id, 128)) ||
    (value.configured && value.account_id === null) ||
    !(value.last_read_at === null || validDate(value.last_read_at)) ||
    !(value.last_result === null || isResult(value.last_result))
  )
    throw new ServiceError(
      '微信只读状态格式不匹配，请检查服务版本。',
      'INVALID_RESPONSE',
    );
  if (value.monitor !== undefined && !isMonitor(value.monitor))
    throw new ServiceError(
      '后台读取状态格式不匹配，请检查服务版本。',
      'INVALID_RESPONSE',
    );
  return {
    ...value,
    monitor: value.monitor ?? null,
  } as unknown as WechatStatus;
}

export async function getWechatSessions(
  token: string,
  signal: AbortSignal,
): Promise<WechatSession[]> {
  const value = await request(token, signal, 'sessions');
  if (
    !isRecord(value) ||
    !readOnly(value) ||
    !Array.isArray(value.items) ||
    value.items.length > 10_000 ||
    !value.items.every(
      (item) =>
        isRecord(item) &&
        validLabel(item.peer_id, 128) &&
        typeof item.name === 'string' &&
        Array.from(item.name).length <= 512,
    ) ||
    new Set(value.items.map((item) => item.peer_id)).size !== value.items.length
  )
    throw new ServiceError(
      '微信会话格式不匹配，请更新状态后重新读取。',
      'INVALID_RESPONSE',
    );
  return (value.items as WechatSession[]).map((item) => ({
    peer_id: item.peer_id,
    name:
      item.name.replace(/[\u0000-\u001f\u007f]/g, ' ').trim() || item.peer_id,
  }));
}

export async function readWechatMessages(
  token: string,
  signal: AbortSignal,
  peerID: string,
): Promise<WechatReadResult> {
  if (!validLabel(peerID, 128))
    throw new ServiceError('请先选择一个有效的微信私聊。', 'INVALID_INPUT');
  const value = await request(token, signal, 'read', { peer_id: peerID });
  if (!isResult(value))
    throw new ServiceError(
      '微信读取结果暂时无法确认，请先更新状态并查看消息。',
      'INVALID_RESPONSE',
    );
  return value;
}

export async function saveWechatMonitor(
  token: string,
  signal: AbortSignal,
  settings: WechatMonitorSettings,
): Promise<WechatMonitorStatus> {
  if (!validMonitorSettings(settings))
    throw new ServiceError(
      '请选择1至20个私聊，读取频率设为10至300秒。',
      'INVALID_INPUT',
    );
  const value = await request(token, signal, 'monitor', {
    enabled: settings.enabled,
    peer_ids: settings.peer_ids,
    interval_seconds: settings.interval_seconds,
  });
  if (!isMonitor(value))
    throw new ServiceError(
      '后台读取设置的结果尚未确认，请先更新状态。',
      'INVALID_RESPONSE',
    );
  return value;
}
