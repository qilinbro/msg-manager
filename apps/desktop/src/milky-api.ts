import { SERVICE, ServiceError } from './api';

export interface MilkyGap {
  at: number;
  reason: string;
}

export interface MilkyMonitor {
  enabled: boolean;
  state: string;
  peer_ids: string[];
  read_only: boolean;
  last_error: string;
}

export interface MilkyStatus {
  configured: boolean;
  state: string;
  account_id: string;
  nickname: string;
  milky_version: string;
  checked_at: string;
  receiving_enabled: boolean;
  sending_enabled: boolean;
  read_only: boolean;
  last_sync_at: string;
  last_sync_result: string;
  skipped: number;
  gaps: MilkyGap[];
  monitor: MilkyMonitor;
}

export interface MilkyVerification {
  connection_id: 'qq-milky';
  state: 'identity_verified';
  account_id: string;
  nickname: string;
  impl_name: string;
  impl_version: string;
  milky_version: string;
  checked_at: string;
  receiving_enabled: boolean;
  sending_enabled: false;
}

export interface MilkySyncResult {
  inserted: number;
  duplicates: number;
  conversations: number;
  skipped: number;
  next_cursor: number | null;
  source_kind: 'protocol_sync';
  sending_enabled: false;
}

const ERROR_MESSAGES: Record<string, string> = {
  MILKY_NOT_CONFIGURED: '尚未配置 Milky 协议端，请先在后台设置连接信息。',
  MILKY_STORAGE_DISABLED: '加密消息库尚未启用，无法保存同步结果。',
  MILKY_NOT_VERIFIED: '请先完成账号与协议版本核验。',
  MILKY_UNREACHABLE: '无法连接本机 Milky 协议端，请确认服务正在运行。',
  MILKY_AUTH_FAILED: '协议端鉴权失败，请检查后台配置的访问令牌。',
  MILKY_IDENTITY_MISMATCH: '协议端登录的 QQ 与预期账号不一致。',
  MILKY_LOGGED_OUT: '协议端账号当前未登录或不在线。',
  MILKY_VERSION_MISMATCH: '协议端 Milky 版本不是本产品锁定的 1.3。',
  MILKY_INVALID_CONFIG: '请求参数或协议端配置不合法。',
  MILKY_PROTOCOL_ERROR: '协议端返回的数据与 Milky 契约不匹配。',
  MILKY_BUSY: '已有 QQ Milky 操作正在进行，请稍后重试。',
  MILKY_UNAVAILABLE: '后台服务正在停止，请稍后重试。',
  INVALID_INPUT: '请检查填写的 QQ 号与条数。',
  INVALID_REQUEST: '请求格式不正确，请检查输入。',
  STORAGE_DISABLED: '加密消息库尚未启用。',
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

type Action = 'status' | 'verify' | 'sync' | 'monitor';

async function request<T>(
  token: string,
  signal: AbortSignal,
  action: Action,
  input?: Record<string, unknown>,
): Promise<T> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const mutation = action === 'sync' || action === 'monitor';
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    action === 'status' ? 10_000 : 45_000,
  );
  try {
    const response = await fetch(
      `${SERVICE.url}/api/v1/connections/qq-milky/${action}`,
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
      if (response.status === 401) {
        throw new ServiceError(
          '访问令牌无效，请重新连接本地服务。',
          'UNAUTHORIZED',
          401,
        );
      }
      const code =
        isRecord(payload) &&
        isRecord(payload.error) &&
        typeof payload.error.code === 'string'
          ? payload.error.code
          : 'MILKY_SERVICE_ERROR';
      throw new ServiceError(
        Object.hasOwn(ERROR_MESSAGES, code)
          ? ERROR_MESSAGES[code]
          : 'QQ Milky 操作未完成，请刷新状态后重试。',
        code,
        response.status,
      );
    }
    return payload as T;
  } catch (error: unknown) {
    if (timedOut) {
      throw new ServiceError(
        mutation
          ? '请求超时，后台可能已处理，请刷新状态确认结果。'
          : '读取状态超时，请重试。',
        'TIMEOUT',
      );
    }
    if (error instanceof ServiceError) throw error;
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ServiceError('请求已取消。', 'ABORTED');
    }
    throw new ServiceError(
      '无法连接本地服务，请检查后台是否已启动。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}

export function getMilkyStatus(
  token: string,
  signal: AbortSignal,
): Promise<MilkyStatus> {
  return request<MilkyStatus>(token, signal, 'status').then((value) => ({
    ...value,
    gaps: Array.isArray(value.gaps) ? value.gaps : [],
    monitor: {
      ...value.monitor,
      peer_ids: Array.isArray(value.monitor?.peer_ids)
        ? value.monitor.peer_ids
        : [],
    },
  }));
}

export function verifyMilky(
  token: string,
  signal: AbortSignal,
): Promise<MilkyVerification> {
  return request<MilkyVerification>(token, signal, 'verify');
}

export function syncMilky(
  token: string,
  signal: AbortSignal,
  peerID: string,
  limit: number,
): Promise<MilkySyncResult> {
  return request<MilkySyncResult>(token, signal, 'sync', {
    peer_id: peerID,
    limit,
  });
}

export function configureMilkyMonitor(
  token: string,
  signal: AbortSignal,
  enabled: boolean,
  peerIDs: string[],
): Promise<MilkyStatus> {
  return request<MilkyStatus>(token, signal, 'monitor', {
    enabled,
    peer_ids: peerIDs,
  });
}
