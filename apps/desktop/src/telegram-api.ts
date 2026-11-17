import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export interface TelegramStatus {
  configured: boolean;
  helper_available: boolean;
  state: string;
  requires_initialization: boolean;
  account_id: string;
  name: string;
  read_only: true;
  sending_enabled: false;
  checked_at: string;
  last_sync_at: string;
  last_error: string;
}

export interface TelegramDialog {
  peer_id: string;
  name: string;
}

export interface TelegramDialogs {
  account_id: string;
  name: string;
  read_only: true;
  sending_enabled: false;
  items: TelegramDialog[];
  limited: boolean;
}

export interface TelegramSyncResult {
  inserted: number;
  duplicates: number;
  conversations: number;
  skipped: number;
  limited: boolean;
  source_kind: 'telegram_user';
  read_only: true;
  sending_enabled: false;
}

const MESSAGES: Record<string, string> = {
  TELEGRAM_REQUIRES_INITIALIZATION:
    '尚未完成 Telegram 本机初始化，请先在本机终端登录，再刷新状态。',
  TELEGRAM_NOT_CONFIGURED:
    '尚未初始化个人 Telegram。请按下方步骤在本机终端登录，再刷新状态。',
  TELEGRAM_HELPER_UNAVAILABLE:
    'Telegram 运行组件尚未准备好。请在项目目录运行准备命令，再刷新状态。',
  TELEGRAM_NOT_VERIFIED: '请先点击“核验个人账号”，再读取会话。',
  TELEGRAM_UNREACHABLE:
    '无法连接 Telegram。请检查网络；需要代理时，在本机初始化中设置代理端口。',
  TELEGRAM_AUTH_REQUIRED: 'Telegram 授权已失效，请重新运行本机初始化。',
  TELEGRAM_IDENTITY_MISMATCH:
    '当前 Telegram 账号与保存的本人账号不一致，未导入消息。请重新核对本机授权。',
  TELEGRAM_FLOOD_WAIT:
    'Telegram 要求暂缓请求，请稍后再试；本次操作不会自动重试。',
  TELEGRAM_BUSY: 'Telegram 正在处理另一个请求，请稍后再试。',
  TELEGRAM_PROTOCOL_ERROR:
    'Telegram 组件返回格式不匹配，请更新本地组件后重试。',
  TELEGRAM_INVALID_CONFIG:
    'Telegram 本机配置不完整或无法解密，请重新运行本机初始化。',
  TELEGRAM_CONFIG_INVALID:
    'Telegram 本机配置不完整或无法解密，请使用同一 Windows 用户重新运行初始化。',
  TELEGRAM_LOGGED_OUT: 'Telegram 登录已失效，请在本机终端重新初始化。',
  TELEGRAM_AUTH_FAILED:
    'Telegram 登录或应用信息无法通过核验，请在本机终端检查后重新初始化。',
  TELEGRAM_NETWORK_ERROR:
    '无法连接 Telegram。请检查网络；需要代理时，在本机初始化中设置代理端口。',
  TELEGRAM_TIMEOUT:
    'Telegram 读取超时，本次没有自动重试。请先核对消息中心与本机状态后再操作。',
  TELEGRAM_UNSUPPORTED_PLATFORM:
    '当前个人 Telegram 连接器需要在 Windows 上准备并初始化，请检查本机运行环境。',
  TELEGRAM_PEER_NOT_FOUND: '该私聊不在本轮可读取会话中，请重新读取会话并选择。',
  TELEGRAM_PEER_NOT_SELECTED:
    '该私聊不在本轮可读取会话中，请重新读取会话并选择。',
  TELEGRAM_STORAGE_DISABLED: '请先启用本产品的加密消息库。',
  STORAGE_DISABLED: '请先启用本产品的加密消息库。',
  INVALID_INPUT: '请选择本轮读取到的私聊，条数须在 1—100 之间。',
};

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const identifier = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[1-9][0-9]{0,18}$/.test(value) &&
  BigInt(value) <= 9_223_372_036_854_775_807n;
const text = (value: unknown, max = 256): value is string =>
  typeof value === 'string' &&
  value.length <= max &&
  !/[\u0000-\u001f\u007f]/.test(value);
const count = (value: unknown): value is number =>
  Number.isSafeInteger(value) && Number(value) >= 0;
const noSecrets = (value: Record<string, unknown>) =>
  !['api_hash', 'session', 'access_token', 'phone', 'password'].some((key) =>
    Object.hasOwn(value, key),
  );

function invalid(): never {
  throw new ServiceError(
    'Telegram 返回数据不符合当前版本，请更新本地服务后重试。',
    'INVALID_RESPONSE',
  );
}

async function request(
  token: string,
  signal: AbortSignal,
  action: 'status' | 'verify' | 'dialogs' | 'sync',
  input?: Record<string, unknown>,
): Promise<unknown> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  const mutation = action === 'verify' || action === 'sync';
  let timedOut = false;
  const timer = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    action === 'status' ? 10_000 : 45_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/connections/telegram-user/${action}`,
      token,
      {
        method: mutation ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(action === 'sync' ? { 'Content-Type': 'application/json' } : {}),
        },
        body: action === 'sync' ? JSON.stringify(input) : undefined,
        signal: controller.signal,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    );
    const value: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (!response.ok) {
      if (response.status === 401)
        throw new ServiceError(
          '本地服务授权已失效，请重新连接。',
          'UNAUTHORIZED',
          401,
        );
      const code =
        record(value) &&
        record(value.error) &&
        typeof value.error.code === 'string'
          ? value.error.code
          : 'TELEGRAM_UNAVAILABLE';
      throw new ServiceError(
        authErrorMessage(
          code,
          MESSAGES[code] ?? 'Telegram 操作未完成，请刷新状态后重试。',
        ),
        code,
        response.status,
      );
    }
    return value;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        action === 'sync'
          ? '同步超时，后台可能已保存结果。请先核对消息中心，不要直接重复提交。'
          : 'Telegram 请求超时，请检查网络后重试。',
        'TIMEOUT',
      );
    if (error instanceof ServiceError) throw error;
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    throw new ServiceError(
      '无法连接本地服务，请确认后台正在运行。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

function status(value: unknown): TelegramStatus {
  if (
    !record(value) ||
    !noSecrets(value) ||
    typeof value.configured !== 'boolean' ||
    typeof value.helper_available !== 'boolean' ||
    !text(value.state, 64) ||
    typeof value.requires_initialization !== 'boolean' ||
    !(value.account_id === '' || identifier(value.account_id)) ||
    !text(value.name) ||
    value.read_only !== true ||
    value.sending_enabled !== false ||
    !text(value.checked_at) ||
    !text(value.last_sync_at) ||
    !text(value.last_error) ||
    (value.state === 'identity_verified' &&
      (!value.configured ||
        !value.helper_available ||
        value.requires_initialization ||
        !identifier(value.account_id)))
  )
    invalid();
  return value as unknown as TelegramStatus;
}

export async function getTelegramStatus(
  token: string,
  signal: AbortSignal,
): Promise<TelegramStatus> {
  return status(await request(token, signal, 'status'));
}

export async function verifyTelegram(
  token: string,
  signal: AbortSignal,
): Promise<TelegramStatus> {
  const next = status(await request(token, signal, 'verify'));
  if (next.state !== 'identity_verified' || !identifier(next.account_id))
    invalid();
  return next;
}

export async function getTelegramDialogs(
  token: string,
  signal: AbortSignal,
): Promise<TelegramDialogs> {
  const value = await request(token, signal, 'dialogs');
  if (
    !record(value) ||
    !noSecrets(value) ||
    !identifier(value.account_id) ||
    !text(value.name) ||
    value.read_only !== true ||
    value.sending_enabled !== false ||
    typeof value.limited !== 'boolean' ||
    !Array.isArray(value.items) ||
    value.items.length > 100 ||
    value.items.some(
      (item) =>
        !record(item) ||
        !identifier(item.peer_id) ||
        !text(item.name) ||
        !noSecrets(item),
    ) ||
    new Set(value.items.map((item) => item.peer_id)).size !== value.items.length
  )
    invalid();
  return value as unknown as TelegramDialogs;
}

export async function syncTelegram(
  token: string,
  signal: AbortSignal,
  peerID: string,
  expectedAccountID: string,
  limit = 50,
): Promise<TelegramSyncResult> {
  if (
    !identifier(peerID) ||
    !identifier(expectedAccountID) ||
    !Number.isInteger(limit) ||
    limit < 1 ||
    limit > 100
  )
    throw new ServiceError(
      '请选择私聊，条数须在 1—100 之间。',
      'INVALID_INPUT',
    );
  const value = await request(token, signal, 'sync', {
    expected_account_id: expectedAccountID,
    peer_id: peerID,
    limit,
  });
  if (
    !record(value) ||
    !noSecrets(value) ||
    !count(value.inserted) ||
    !count(value.duplicates) ||
    !count(value.conversations) ||
    !count(value.skipped) ||
    typeof value.limited !== 'boolean' ||
    value.source_kind !== 'telegram_user' ||
    value.read_only !== true ||
    value.sending_enabled !== false
  )
    invalid();
  return value as unknown as TelegramSyncResult;
}
