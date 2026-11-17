import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

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

export interface MilkyConfig {
  url: string;
  account_id: string;
  history_limit: number;
  token_configured: boolean;
  revision: number;
  configured: boolean;
  sending_enabled: false;
  source: 'saved' | 'environment' | 'none';
}

export interface MilkyConfigInput {
  url: string;
  account_id: string;
  history_limit: number;
  access_token: string;
  expected_revision: number;
}

const ERROR_MESSAGES: Record<string, string> = {
  MILKY_NOT_CONFIGURED: '请先完成下方连接设置，再核验账号与版本。',
  MILKY_CONFIG_INVALID: '请检查本机地址、本人 QQ 号、访问令牌和历史条数。',
  MILKY_CONFIG_STALE:
    '连接设置已被其他页面更新，输入已保留；请刷新设置后核对。',
  MILKY_CONFIG_TOKEN_REQUIRED:
    '首次连接或更换地址、账号时，请填写对应的 Milky 访问令牌。',
  MILKY_CONFIG_CONFIRMATION_REQUIRED: '请核对连接信息并确认保存。',
  MILKY_CONFIG_UNAVAILABLE: '连接设置未保存，请检查本机加密存储后重试。',
  MILKY_CONFIG_BUSY: '连接正在处理，请稍后保存；输入已保留。',
  MILKY_CONFIG_STORAGE_DISABLED:
    '请先启用本机加密消息库，再保存 Milky 连接设置。',
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

type Action =
  'status' | 'verify' | 'sync' | 'monitor' | 'config' | 'config/test';

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
  const mutation =
    action === 'sync' ||
    action === 'monitor' ||
    action === 'config/test' ||
    (action === 'config' && input !== undefined);
  const method =
    action === 'config' && input !== undefined
      ? 'PATCH'
      : mutation
        ? 'POST'
        : 'GET';
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    action === 'status' ? 10_000 : 45_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/connections/qq-milky/${action}`,
      token,
      {
        method,
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
        authErrorMessage(
          code,
          Object.hasOwn(ERROR_MESSAGES, code)
            ? ERROR_MESSAGES[code]
            : 'QQ Milky 操作未完成，请刷新状态后重试。',
        ),
        code,
        response.status,
      );
    }
    return payload as T;
  } catch (error: unknown) {
    if (timedOut) {
      throw new ServiceError(
        mutation
          ? '请求超时，后台可能已处理，请刷新设置或状态核对结果，不要直接重复提交。'
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

function configResponse(value: unknown): MilkyConfig {
  if (
    !isRecord(value) ||
    Array.isArray(value) ||
    Object.hasOwn(value, 'access_token') ||
    Object.hasOwn(value, 'token') ||
    Object.keys(value).some(
      (key) =>
        ![
          'url',
          'account_id',
          'history_limit',
          'token_configured',
          'revision',
          'configured',
          'sending_enabled',
          'source',
        ].includes(key),
    ) ||
    typeof value.url !== 'string' ||
    value.url.length > 4096 ||
    typeof value.account_id !== 'string' ||
    (value.account_id !== '' &&
      !/^[1-9][0-9]{0,127}$/.test(value.account_id)) ||
    !Number.isSafeInteger(value.history_limit) ||
    Number(value.history_limit) < 1 ||
    Number(value.history_limit) > 30 ||
    !Number.isSafeInteger(value.revision) ||
    Number(value.revision) < 0 ||
    typeof value.token_configured !== 'boolean' ||
    typeof value.configured !== 'boolean' ||
    value.sending_enabled !== false ||
    !['saved', 'environment', 'none'].includes(String(value.source)) ||
    (value.configured === true &&
      (value.account_id === '' ||
        !value.token_configured ||
        !/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):([1-9][0-9]{0,4})\/?$/.test(
          value.url,
        )))
  ) {
    throw new ServiceError(
      'Milky 连接设置返回格式不正确，请更新本地服务后重试。',
      'INVALID_RESPONSE',
    );
  }
  return value as unknown as MilkyConfig;
}

function configInput(input: MilkyConfigInput): MilkyConfigInput {
  if (typeof input.url !== 'string' || typeof input.account_id !== 'string') {
    throw new ServiceError(
      '请填写本机 Milky 地址和本人 QQ 号。',
      'MILKY_CONFIG_INVALID',
    );
  }
  const url = input.url.trim();
  const target =
    /^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):([1-9][0-9]{0,4})\/?$/.exec(
      url,
    );
  if (
    !target ||
    Number(target[2]) > 65535 ||
    !/^[1-9][0-9]{0,127}$/.test(input.account_id) ||
    !Number.isInteger(input.history_limit) ||
    input.history_limit < 1 ||
    input.history_limit > 30 ||
    !Number.isSafeInteger(input.expected_revision) ||
    input.expected_revision < 0 ||
    typeof input.access_token !== 'string' ||
    input.access_token.length > 4096 ||
    (input.access_token !== '' && !/^[!-~]+$/.test(input.access_token))
  ) {
    throw new ServiceError(
      '请填写本机 HTTP 地址、本人 QQ 号、无空白的访问令牌和 1—30 条历史上限。',
      'MILKY_CONFIG_INVALID',
    );
  }
  return {
    url,
    account_id: input.account_id,
    history_limit: input.history_limit,
    access_token: input.access_token,
    expected_revision: input.expected_revision,
  };
}

export async function getMilkyConfig(
  token: string,
  signal: AbortSignal,
): Promise<MilkyConfig> {
  return configResponse(await request<unknown>(token, signal, 'config'));
}

export async function saveMilkyConfig(
  token: string,
  signal: AbortSignal,
  input: MilkyConfigInput & { confirmed: true },
): Promise<MilkyConfig> {
  if (input.confirmed !== true)
    throw new ServiceError(
      '请先核对并确认保存连接设置。',
      'MILKY_CONFIG_CONFIRMATION_REQUIRED',
    );
  return configResponse(
    await request<unknown>(token, signal, 'config', {
      ...configInput(input),
      confirmed: true,
    }),
  );
}

export async function testMilkyConfig(
  token: string,
  signal: AbortSignal,
  input: MilkyConfigInput,
): Promise<MilkyVerification> {
  const value = await request<unknown>(token, signal, 'config/test', {
    ...configInput(input),
  });
  if (
    !isRecord(value) ||
    value.connection_id !== 'qq-milky' ||
    value.state !== 'identity_verified' ||
    typeof value.account_id !== 'string' ||
    value.account_id !== input.account_id ||
    !/^[1-9][0-9]*$/.test(value.account_id) ||
    typeof value.nickname !== 'string' ||
    typeof value.impl_name !== 'string' ||
    typeof value.impl_version !== 'string' ||
    value.milky_version !== '1.3' ||
    typeof value.checked_at !== 'string' ||
    !Number.isFinite(Date.parse(value.checked_at)) ||
    typeof value.receiving_enabled !== 'boolean' ||
    value.sending_enabled !== false ||
    Object.hasOwn(value, 'access_token')
  ) {
    throw new ServiceError(
      '协议端测试返回格式不正确，尚未保存连接。',
      'INVALID_RESPONSE',
    );
  }
  return value as unknown as MilkyVerification;
}
