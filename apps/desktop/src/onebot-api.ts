import { SERVICE, ServiceError } from './api';

export interface OneBotGap {
  at: number;
  reason: string;
}

export interface OneBotMonitor {
  enabled: boolean;
  state: string;
  peer_ids: string[];
  read_only: boolean;
  last_error: string;
}

export interface OneBotReceiveStatus {
  configured: boolean;
  state: string;
  account_id: string;
  checked_at: string;
  receiving: boolean;
  sending: boolean;
  last_poll_at: string;
  last_result: string;
  skipped: number;
  gaps: OneBotGap[];
  monitor: OneBotMonitor;
}

export interface OneBotSyncResult {
  inserted: number;
  duplicates: number;
  skipped: number;
  source_kind: 'onebot_sync';
  sending_enabled: false;
}

const ERRORS: Record<string, string> = {
  QQ_ONEBOT_NOT_CONFIGURED: '尚未配置 OneBot 或加密消息库。',
  QQ_ONEBOT_BUSY: '已有 OneBot 操作正在进行，请稍后重试。',
  QQ_ONEBOT_UNAVAILABLE: 'OneBot 历史读取未完成，请稍后重试。',
  INVALID_INPUT: '请检查 QQ 号与条数。',
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

async function request<T>(
  token: string,
  signal: AbortSignal,
  action: 'receive-status' | 'receive' | 'monitor',
  input?: Record<string, unknown>,
): Promise<T> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const mutation = action !== 'receive-status';
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    mutation ? 45_000 : 10_000,
  );
  try {
    const response = await fetch(
      `${SERVICE.url}/api/v1/connections/qq-onebot/${action}`,
      {
        method: mutation ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(mutation ? { 'Content-Type': 'application/json' } : {}),
        },
        body: mutation ? JSON.stringify(input) : undefined,
        signal: controller.signal,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
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
          : 'QQ_ONEBOT_SERVICE_ERROR';
      throw new ServiceError(
        ERRORS[code] ?? 'OneBot 操作未完成，请刷新状态后重试。',
        code,
        response.status,
      );
    }
    return payload as T;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        mutation
          ? '请求超时，后台可能已处理，请刷新状态确认。'
          : '读取状态超时，请重试。',
        'TIMEOUT',
      );
    if (error instanceof ServiceError) throw error;
    if (error instanceof DOMException && error.name === 'AbortError')
      throw new ServiceError('请求已取消。', 'ABORTED');
    throw new ServiceError(
      '无法连接本地服务，请检查后台是否已启动。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}

export function getOneBotReceiveStatus(token: string, signal: AbortSignal) {
  return request<OneBotReceiveStatus>(token, signal, 'receive-status');
}
export function syncOneBotMessages(
  token: string,
  signal: AbortSignal,
  peerID: string,
  count: number,
) {
  return request<OneBotSyncResult>(token, signal, 'receive', {
    peer_id: peerID,
    count,
  });
}
export function configureOneBotMonitor(
  token: string,
  signal: AbortSignal,
  enabled: boolean,
  peerIDs: string[],
) {
  return request<OneBotReceiveStatus>(token, signal, 'monitor', {
    enabled,
    peer_ids: peerIDs,
  });
}
