import { authorizedFetch, authErrorMessage } from './device-auth';

export interface Connector {
  id: string;
  display_name: string;
  platform: string;
  status: string;
  implementation_status: string;
  capabilities: {
    receive_text: boolean;
    send_text: boolean;
    history_import: boolean;
  };
  description: string;
}

export interface SystemStatus {
  version: string;
  stage: string;
  workspace_id: string;
  automation: { paused: boolean; auto_send_enabled: boolean };
  storage: { configured: boolean; driver: string };
  connectors: Connector[];
}

export interface Collection {
  items: unknown[];
  next_cursor: string | null;
}

export class ServiceError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly status?: number,
  ) {
    super(message);
    this.name = 'ServiceError';
  }
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

function isConnector(value: unknown): value is Connector {
  return (
    record(value) &&
    typeof value.id === 'string' &&
    typeof value.display_name === 'string' &&
    typeof value.platform === 'string' &&
    typeof value.status === 'string' &&
    typeof value.implementation_status === 'string' &&
    typeof value.description === 'string' &&
    record(value.capabilities) &&
    typeof value.capabilities.receive_text === 'boolean' &&
    typeof value.capabilities.send_text === 'boolean' &&
    typeof value.capabilities.history_import === 'boolean'
  );
}

function isSystem(value: unknown): value is SystemStatus {
  return (
    record(value) &&
    typeof value.version === 'string' &&
    typeof value.stage === 'string' &&
    typeof value.workspace_id === 'string' &&
    record(value.automation) &&
    typeof value.automation.paused === 'boolean' &&
    typeof value.automation.auto_send_enabled === 'boolean' &&
    record(value.storage) &&
    typeof value.storage.configured === 'boolean' &&
    typeof value.storage.driver === 'string' &&
    Array.isArray(value.connectors) &&
    value.connectors.every(isConnector)
  );
}

export function serviceConfig(): { url: string; error: string | null } {
  const configured: string =
    import.meta.env.VITE_SOCIALD_URL || 'http://127.0.0.1:17890';
  try {
    const url = new URL(configured);
    if (
      url.protocol !== 'http:' ||
      !['localhost', '127.0.0.1'].includes(url.hostname) ||
      url.port !== '17890' ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      url.pathname !== '/'
    ) {
      throw new Error('invalid local service URL');
    }
    return { url: url.origin, error: null };
  } catch {
    return {
      url: '',
      error:
        '服务地址配置无效，当前仅支持 http://127.0.0.1:17890 或 http://localhost:17890。',
    };
  }
}

export const SERVICE = serviceConfig();

async function request(
  path: string,
  token: string,
  signal: AbortSignal,
): Promise<unknown> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  let timedOut = false;
  const cancel = () => controller.abort();
  signal.addEventListener('abort', cancel, { once: true });
  if (signal.aborted) controller.abort();
  const timeout = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 10_000);
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/${path}`,
      token,
      {
        method: 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
        },
        signal: controller.signal,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (!response.ok) {
      if (response.status === 401) {
        throw new ServiceError(
          '令牌无效或已失效，请重新输入服务令牌。',
          'UNAUTHORIZED',
          401,
        );
      }
      const code =
        record(payload) &&
        record(payload.error) &&
        typeof payload.error.code === 'string'
          ? payload.error.code
          : 'SERVICE_ERROR';
      throw new ServiceError(
        authErrorMessage(
          code,
          response.status === 503
            ? '服务暂未就绪，请稍后重试。'
            : '服务暂时无法完成读取，请稍后重试。',
        ),
        code,
        response.status,
      );
    }
    return payload;
  } catch (error) {
    if (timedOut)
      throw new ServiceError('连接超时，请确认本地服务正在运行。', 'TIMEOUT');
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (error instanceof ServiceError) throw error;
    throw new ServiceError(
      '无法连接本地服务，请确认服务已启动，再重新连接。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', cancel);
  }
}

export async function getSystem(
  token: string,
  signal: AbortSignal,
): Promise<SystemStatus> {
  const payload = await request('system', token, signal);
  if (!isSystem(payload))
    throw new ServiceError(
      '服务返回的数据格式不匹配，请检查版本。',
      'INVALID_RESPONSE',
    );
  return payload;
}

export async function getCollection(
  path: 'messages' | 'contacts' | 'approvals' | 'reports',
  token: string,
  signal: AbortSignal,
): Promise<Collection> {
  const payload = await request(path, token, signal);
  if (
    !record(payload) ||
    !Array.isArray(payload.items) ||
    !(payload.next_cursor === null || typeof payload.next_cursor === 'string')
  ) {
    throw new ServiceError(
      '服务返回的数据格式不匹配，请检查版本。',
      'INVALID_RESPONSE',
    );
  }
  return payload as unknown as Collection;
}
