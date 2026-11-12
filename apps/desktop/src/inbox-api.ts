import { SERVICE, ServiceError } from './api';

export type Platform = 'qq' | 'weixin' | 'telegram' | 'shanliao';
export const PLATFORM_NAMES: Record<Platform, string> = {
  qq: 'QQ',
  weixin: '微信',
  telegram: 'Telegram',
  shanliao: '闪聊',
};
export interface InboxInfo {
  storage_enabled: boolean;
  source_mode: 'import';
  platforms: {
    id: Platform;
    name: string;
    live_status: 'not_connected' | 'probe_only' | 'unsupported';
    message_count: number;
  }[];
  import_limit: number;
}
export interface Conversation {
  id: string;
  connection_id: string;
  platform: Platform;
  account_id: string;
  peer_id: string;
  title: string;
  last_message: string;
  last_message_at: string;
  unread_count: number;
  pinned: boolean;
  archived: boolean;
}
export interface InboxMessage {
  id: string;
  conversation_id: string;
  external_id: string;
  direction: 'incoming' | 'outgoing';
  body: string;
  occurred_at: string;
  read: boolean;
}
export interface PageResult<T> {
  items: T[];
  next_cursor: string | null;
}
export interface ImportMessage {
  external_id: string;
  peer_id: string;
  peer_name: string;
  direction: 'incoming' | 'outgoing';
  body: string;
  occurred_at: string;
}
export interface ImportRequest {
  platform: Platform;
  account_id: string;
  account_name: string;
  messages: ImportMessage[];
}
export interface ImportResult {
  inserted: number;
  duplicates: number;
  conversations: number;
  skipped?: number;
}

export interface TelegramImportRequest {
  account_id: string;
  account_name: string;
  export: Record<string, unknown>;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const isString = (value: unknown): value is string => typeof value === 'string';
const isCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
export const isPlatform = (value: unknown): value is Platform =>
  isString(value) && Object.hasOwn(PLATFORM_NAMES, value);

const ERROR_MESSAGES: Record<string, string> = {
  STORAGE_DISABLED: '本地消息存储尚未启用，请在设置中查看配置状态。',
  INVALID_REQUEST: '提交内容不符合导入格式，请检查字段、账号和消息时间。',
  PAYLOAD_TOO_LARGE: '文件或消息内容超过限制，请拆分后重新导入。',
  CONVERSATION_NOT_FOUND: '这个会话已不存在，请刷新会话列表。',
  IMPORT_CONFLICT: '部分消息标识与已有内容冲突，请核对来源后重新导入。',
  STORE_UNAVAILABLE: '本地消息存储暂不可用，请稍后刷新。',
  INVALID_CURSOR: '分页位置已失效，请刷新列表后重试。',
};

async function request(
  path: string,
  token: string,
  signal: AbortSignal,
  method: 'GET' | 'POST' | 'PATCH' = 'GET',
  body?: unknown,
): Promise<unknown> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  let timedOut = false;
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  const timer = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 15_000);
  try {
    const response = await fetch(`${SERVICE.url}/api/v1/${path}`, {
      method,
      headers: {
        Accept: 'application/json',
        Authorization: `Bearer ${token}`,
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    });
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
        isString(payload.error.code)
          ? payload.error.code
          : 'SERVICE_ERROR';
      const message = Object.hasOwn(ERROR_MESSAGES, code)
        ? ERROR_MESSAGES[code]
        : response.status === 413
          ? ERROR_MESSAGES.PAYLOAD_TOO_LARGE
          : '暂时无法完成操作，请刷新后重试。';
      throw new ServiceError(message, code, response.status);
    }
    return payload;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        method === 'GET'
          ? '读取超时，请确认本地服务可用后重试。'
          : '等待超时，后台可能已处理，请先刷新确认结果。',
        'TIMEOUT',
      );
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (error instanceof ServiceError) throw error;
    throw new ServiceError(
      method === 'GET'
        ? '无法读取本地服务，请检查连接后重试。'
        : '连接中断，后台可能已处理，请先刷新确认结果。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

function invalidResponse(): never {
  throw new ServiceError(
    '消息服务返回的格式不匹配，请检查服务版本后重试。',
    'INVALID_RESPONSE',
  );
}
function conversation(value: unknown): value is Conversation {
  return (
    isRecord(value) &&
    [
      'id',
      'connection_id',
      'account_id',
      'peer_id',
      'title',
      'last_message',
      'last_message_at',
    ].every((key) => isString(value[key])) &&
    isPlatform(value.platform) &&
    isCount(value.unread_count) &&
    typeof value.pinned === 'boolean' &&
    typeof value.archived === 'boolean'
  );
}
function message(value: unknown): value is InboxMessage {
  return (
    isRecord(value) &&
    ['id', 'conversation_id', 'external_id', 'body', 'occurred_at'].every(
      (key) => isString(value[key]),
    ) &&
    ['incoming', 'outgoing'].includes(String(value.direction)) &&
    typeof value.read === 'boolean'
  );
}
function page<T>(
  value: unknown,
  validate: (item: unknown) => item is T,
): PageResult<T> {
  if (
    !isRecord(value) ||
    !Array.isArray(value.items) ||
    !value.items.every(validate) ||
    !(value.next_cursor === null || isString(value.next_cursor))
  )
    invalidResponse();
  return value as unknown as PageResult<T>;
}

export async function getInboxInfo(
  token: string,
  signal: AbortSignal,
): Promise<InboxInfo> {
  const value = await request('inbox', token, signal);
  if (
    !isRecord(value) ||
    typeof value.storage_enabled !== 'boolean' ||
    value.source_mode !== 'import' ||
    !isCount(value.import_limit) ||
    !Array.isArray(value.platforms) ||
    !value.platforms.every(
      (item) =>
        isRecord(item) &&
        isPlatform(item.id) &&
        isString(item.name) &&
        ['not_connected', 'probe_only', 'unsupported'].includes(
          String(item.live_status),
        ) &&
        isCount(item.message_count),
    )
  )
    invalidResponse();
  return value as unknown as InboxInfo;
}

export async function getConversations(
  token: string,
  signal: AbortSignal,
  filters: {
    platform?: Platform;
    q?: string;
    unread?: boolean;
    pinned?: boolean;
    archived: boolean;
    cursor?: string;
  },
): Promise<PageResult<Conversation>> {
  const params = new URLSearchParams({
    limit: '50',
    archived: String(filters.archived),
  });
  if (filters.platform) params.set('platform', filters.platform);
  if (filters.q) params.set('q', filters.q);
  if (filters.unread) params.set('unread', 'true');
  if (filters.pinned) params.set('pinned', 'true');
  if (filters.cursor) params.set('cursor', filters.cursor);
  return page(
    await request(`conversations?${params}`, token, signal),
    conversation,
  );
}

export async function getConversationMessages(
  token: string,
  signal: AbortSignal,
  id: string,
  cursor?: string,
): Promise<PageResult<InboxMessage>> {
  const params = new URLSearchParams({ limit: '50' });
  if (cursor) params.set('cursor', cursor);
  return page(
    await request(
      `conversations/${encodeURIComponent(id)}/messages?${params}`,
      token,
      signal,
    ),
    message,
  );
}

export async function updateConversation(
  token: string,
  signal: AbortSignal,
  id: string,
  changes: { read?: boolean; pinned?: boolean; archived?: boolean },
): Promise<void> {
  const value = await request(
    `conversations/${encodeURIComponent(id)}`,
    token,
    signal,
    'PATCH',
    changes,
  );
  if (!isRecord(value) || value.ok !== true) invalidResponse();
}

export async function importMessages(
  token: string,
  signal: AbortSignal,
  data: ImportRequest,
): Promise<ImportResult> {
  const value = await request('imports/messages', token, signal, 'POST', data);
  if (
    !isRecord(value) ||
    !isCount(value.inserted) ||
    !isCount(value.duplicates) ||
    !isCount(value.conversations)
  )
    invalidResponse();
  return value as unknown as ImportResult;
}

export async function importTelegram(
  token: string,
  signal: AbortSignal,
  data: TelegramImportRequest,
): Promise<ImportResult> {
  const value = await request('imports/telegram', token, signal, 'POST', data);
  if (
    !isRecord(value) ||
    !isCount(value.inserted) ||
    !isCount(value.duplicates) ||
    !isCount(value.conversations) ||
    !isCount(value.skipped)
  )
    invalidResponse();
  return value as unknown as ImportResult;
}

const byteLength = (value: string) => new TextEncoder().encode(value).length;
export function parseImport(
  value: unknown,
  defaults: Omit<ImportRequest, 'messages'>,
  limit: number,
): ImportRequest {
  const source: unknown = Array.isArray(value)
    ? { ...defaults, messages: value }
    : value;
  if (
    !isRecord(source) ||
    !isPlatform(source.platform) ||
    !isString(source.account_id) ||
    !source.account_id.trim() ||
    Array.from(source.account_id).length > 128 ||
    !isString(source.account_name) ||
    Array.from(source.account_name).length > 128 ||
    !Array.isArray(source.messages)
  )
    throw new Error(
      '请提供平台、账号、账号名称和 messages 数组，或填写来源信息后上传消息数组。',
    );
  if (
    source.messages.length === 0 ||
    source.messages.length > Math.min(1000, limit)
  )
    throw new Error(
      `每次需要 1 至 ${Math.min(1000, limit)} 条消息，请拆分文件。`,
    );
  const messages = source.messages.map(
    (item: unknown, index): ImportMessage => {
      if (!isRecord(item)) throw new Error(`第 ${index + 1} 条消息格式无效。`);
      const idsValid = ['external_id', 'peer_id'].every(
        (key) =>
          isString(item[key]) &&
          item[key].trim() &&
          Array.from(item[key] as string).length <= 128,
      );
      if (
        !idsValid ||
        !isString(item.peer_name) ||
        Array.from(item.peer_name).length > 128 ||
        !['incoming', 'outgoing'].includes(String(item.direction)) ||
        !isString(item.body) ||
        !item.body.trim() ||
        byteLength(item.body) > 16_000 ||
        !isString(item.occurred_at) ||
        !/^\d{4}-\d{2}-\d{2}T.+(?:Z|[+-]\d{2}:\d{2})$/.test(item.occurred_at) ||
        !Number.isFinite(Date.parse(item.occurred_at))
      )
        throw new Error(
          `第 ${index + 1} 条消息字段不完整或超过限制。请检查标识、方向、正文和含时区的 ISO 时间。`,
        );
      return {
        external_id: item.external_id as string,
        peer_id: item.peer_id as string,
        peer_name: item.peer_name,
        direction: item.direction as ImportMessage['direction'],
        body: item.body,
        occurred_at: item.occurred_at,
      };
    },
  );
  const result: ImportRequest = {
    platform: source.platform,
    account_id: source.account_id,
    account_name: source.account_name,
    messages,
  };
  if (byteLength(JSON.stringify(result)) > 8 * 1024 * 1024)
    throw new Error('导入内容超过 8 MiB，请拆分文件。');
  return result;
}
