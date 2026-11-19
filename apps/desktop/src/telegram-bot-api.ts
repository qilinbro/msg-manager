import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

const STATE_LABELS = {
  not_configured: '尚未配置 Bot 与授权私聊名单',
  runtime_not_configured: 'Python 适配器运行环境尚未配置',
  configured: '后台已配置，等待核验 Bot 身份',
  verifying: '正在核验 Bot 身份',
  receiving: '正在处理一次手动收件',
  identity_verified: 'Bot 身份已核验，可逐次手动收件',
  closed: '连接已关闭，暂不可收件',
  auth_failed: 'Bot 凭据验证失败，请检查后台配置',
  rate_limited: '平台暂时限流，请稍后重新核验',
  unreachable: '暂时无法连接适配器或平台',
  protocol_error: '适配器返回格式不符合约定',
  identity_mismatch: 'Bot 身份与后台预期不符，收件已禁用',
  webhook_present: 'Bot 已配置 Webhook，收件已禁用；本产品不会移除它',
  canceled: '上次操作已取消，请重新读取状态',
  permission_denied: '平台权限不足，收件不可用',
  platform_rejected: '平台拒绝了本次操作',
  unavailable: '适配器或依赖暂不可用',
  invalid_config: '后台配置无效，收件不可用',
} as const;

export type TelegramBotState = keyof typeof STATE_LABELS;
export interface TelegramBotSyncResult {
  events: number;
  imported: number;
  duplicates: number;
  unsupported: number;
  not_authorized: number;
  conversations: number;
  next_offset: string;
  partial: boolean;
  source_kind: 'telegram_bot';
  ai_queued: 0;
}
export interface TelegramBotStatus {
  connection_id: 'telegram-bot';
  runtime_configured: boolean;
  configured: boolean;
  identity_verified: boolean;
  allowed_private_chat_count: number;
  state: TelegramBotState;
  checked_at: string;
  last_sync_at: string;
  manual_receive_only: true;
  background_receiving: false;
  sending_enabled: false;
  history_import: false;
  last_result: TelegramBotSyncResult | null;
}

const ERROR_MESSAGES: Record<string, string> = {
  TELEGRAM_BOT_INVALID_REQUEST: '收件请求格式不符合约定，请刷新状态后重试。',
  TELEGRAM_BOT_HUMAN_REQUIRED:
    '此操作只能由本人在工作台手动确认，模型工具不可调用。',
  TELEGRAM_BOT_PAYLOAD_TOO_LARGE: '本次收件数据超过限制，请先核对后台结果。',
  TELEGRAM_BOT_NOT_CONFIGURED:
    '请先在后台配置 Bot 凭据、预期身份与授权私聊名单。',
  TELEGRAM_BOT_RUNTIME_NOT_CONFIGURED: '请先在后台配置 Python 适配器及其依赖。',
  TELEGRAM_BOT_VERIFICATION_REQUIRED: '请先核验 Bot 身份，再逐次授权手动收件。',
  TELEGRAM_BOT_BUSY: '已有 Bot 操作正在进行，请稍后刷新状态。',
  TELEGRAM_BOT_CLOSED: '连接已关闭，请检查后台服务后刷新状态。',
  TELEGRAM_BOT_CANCELED: '操作已取消，请刷新状态确认结果；不会自动重试。',
  TELEGRAM_BOT_CURSOR_CHANGED:
    '收件进度已有变化，请刷新状态并核对结果；不会自动重试。',
  TELEGRAM_BOT_CONFLICT: '本次收件存在数据冲突，请核对后台结果；不会自动重试。',
  TELEGRAM_BOT_STORE_UNAVAILABLE: '本地消息库暂不可用，请检查后台服务。',
  TELEGRAM_BOT_AUTH_FAILED: 'Bot 凭据验证失败，请检查后台配置。',
  TELEGRAM_BOT_RATE_LIMITED: '平台暂时限流，请稍后刷新状态并重新授权。',
  TELEGRAM_BOT_UNREACHABLE: '无法连接适配器或平台，请检查后台运行环境。',
  TELEGRAM_BOT_PROTOCOL_ERROR: '适配器返回格式不符合约定，收件已停止。',
  TELEGRAM_BOT_IDENTITY_MISMATCH: 'Bot 身份与后台预期不符，收件已禁用。',
  TELEGRAM_BOT_WEBHOOK_PRESENT:
    'Bot 已配置 Webhook，收件已禁用；本产品不会移除它。',
  TELEGRAM_BOT_PERMISSION_DENIED: '平台权限不足，无法接收本次消息。',
  TELEGRAM_BOT_PLATFORM_REJECTED: '平台拒绝了本次操作，请检查后台配置。',
  TELEGRAM_BOT_UNAVAILABLE: '适配器或依赖暂不可用，请检查后台运行环境。',
  TELEGRAM_BOT_INVALID_CONFIG:
    '后台配置无效，请核对 Bot 身份、授权名单及运行环境。',
  STORAGE_DISABLED: '加密消息库尚未启用，无法保存收件结果。',
  FORBIDDEN_SCOPE: '当前工作台没有执行此操作的权限。',
  SCOPE_DENIED: '当前工作台没有执行此操作的权限。',
  UNAUTHORIZED: '本地服务授权已失效，请重新连接。',
  DEVICE_UNAUTHORIZED: '本机授权已失效，请重新连接本地服务。',
  NOT_FOUND: '当前后台尚不支持 Telegram Bot 手动收件。',
  METHOD_NOT_ALLOWED: '当前后台不支持此收件请求，请检查服务版本。',
  CONFIG_ERROR: '本地服务地址配置无效，请检查工作台配置。',
  TIMEOUT: '请求超时，后台可能已处理；请刷新状态确认结果，不会自动重试。',
  ABORTED: '请求已取消，请刷新状态确认结果。',
  NETWORK_ERROR: '无法连接本地服务，请检查后台是否已启动。',
  INVALID_RESPONSE:
    '后台返回格式不符合收件约定；请刷新状态确认结果，不会自动重试。',
  TELEGRAM_BOT_ERROR: '本次操作未完成，请刷新状态确认结果；不会自动重试。',
};

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const exactKeys = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  keys.every((key) => Object.hasOwn(value, key));
const timestamp = (value: unknown): value is string =>
  typeof value === 'string' &&
  (value === '' ||
    (value.length <= 40 &&
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
        value,
      ) &&
      Number.isFinite(Date.parse(value))));
const offset = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^(?:0|[1-9]\d{0,18})$/.test(value) &&
  (value.length < 19 || value <= '9223372036854775807');

function syncResult(value: unknown): value is TelegramBotSyncResult {
  return (
    record(value) &&
    exactKeys(value, [
      'events',
      'imported',
      'duplicates',
      'unsupported',
      'not_authorized',
      'conversations',
      'next_offset',
      'partial',
      'source_kind',
      'ai_queued',
    ]) &&
    count(value.events) &&
    value.events <= 100 &&
    count(value.imported) &&
    count(value.duplicates) &&
    count(value.unsupported) &&
    count(value.not_authorized) &&
    count(value.conversations) &&
    value.imported +
      value.duplicates +
      value.unsupported +
      value.not_authorized ===
      value.events &&
    value.conversations <= value.imported + value.duplicates &&
    offset(value.next_offset) &&
    value.partial === (value.unsupported > 0 || value.not_authorized > 0) &&
    value.source_kind === 'telegram_bot' &&
    value.ai_queued === 0
  );
}
function status(value: unknown): value is TelegramBotStatus {
  return (
    record(value) &&
    exactKeys(value, [
      'connection_id',
      'runtime_configured',
      'configured',
      'identity_verified',
      'allowed_private_chat_count',
      'state',
      'checked_at',
      'last_sync_at',
      'manual_receive_only',
      'background_receiving',
      'sending_enabled',
      'history_import',
      'last_result',
    ]) &&
    value.connection_id === 'telegram-bot' &&
    typeof value.runtime_configured === 'boolean' &&
    typeof value.configured === 'boolean' &&
    typeof value.identity_verified === 'boolean' &&
    count(value.allowed_private_chat_count) &&
    typeof value.state === 'string' &&
    Object.hasOwn(STATE_LABELS, value.state) &&
    timestamp(value.checked_at) &&
    timestamp(value.last_sync_at) &&
    value.manual_receive_only === true &&
    value.background_receiving === false &&
    value.sending_enabled === false &&
    value.history_import === false &&
    (value.last_result === null || syncResult(value.last_result)) &&
    (!value.identity_verified ||
      (value.configured &&
        value.runtime_configured &&
        value.allowed_private_chat_count > 0))
  );
}

function safeMessage(code: string): string {
  return authErrorMessage(
    code,
    Object.hasOwn(ERROR_MESSAGES, code)
      ? ERROR_MESSAGES[code]
      : ERROR_MESSAGES.TELEGRAM_BOT_ERROR,
  );
}
export function telegramBotErrorMessage(failure: unknown): string {
  return safeMessage(
    failure instanceof ServiceError ? failure.code : 'TELEGRAM_BOT_ERROR',
  );
}
export function isTelegramBotAuthFailure(failure: unknown): boolean {
  return (
    failure instanceof ServiceError &&
    failure.status === 401 &&
    (failure.code === 'UNAUTHORIZED' || failure.code === 'DEVICE_UNAUTHORIZED')
  );
}
export function telegramBotStateLabel(state: TelegramBotState): string {
  return STATE_LABELS[state];
}

// Status/results contain only bounded metadata, never raw platform updates.
// Bound the stream before parsing; checking JSON size afterwards is too late.
const RESPONSE_BYTE_LIMIT = 16_384;
async function responseJSON(
  response: Response,
  signal: AbortSignal,
): Promise<unknown> {
  const invalid = () =>
    new ServiceError(safeMessage('INVALID_RESPONSE'), 'INVALID_RESPONSE');
  const length = response.headers.get('content-length');
  if (
    length !== null &&
    /^\d+$/.test(length) &&
    Number(length) > RESPONSE_BYTE_LIMIT
  ) {
    void response.body?.cancel().catch(() => {});
    throw invalid();
  }
  const reader = response.body?.getReader();
  if (!reader) return null;
  let bytes = 0;
  let text = '';
  try {
    const decoder = new TextDecoder('utf-8', { fatal: true });
    for (;;) {
      signal.throwIfAborted();
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > RESPONSE_BYTE_LIMIT) throw invalid();
      text += decoder.decode(value, { stream: true });
    }
    text += decoder.decode();
    return JSON.parse(text);
  } catch (failure: unknown) {
    void reader.cancel().catch(() => {});
    if (failure instanceof ServiceError || signal.aborted) throw failure;
    // Invalid JSON/UTF-8 is never exposed. A 401 can still revoke local auth;
    // successful responses must pass the strict DTO validators below.
    return null;
  } finally {
    reader.releaseLock();
  }
}

type Action = 'status' | 'verify' | 'sync';
async function request(
  action: Action,
  token: string,
  signal: AbortSignal,
): Promise<unknown> {
  if (SERVICE.error)
    throw new ServiceError(safeMessage('CONFIG_ERROR'), 'CONFIG_ERROR');
  const deadline = AbortSignal.timeout(action === 'status' ? 10_000 : 45_000);
  const combined = AbortSignal.any([signal, deadline]);
  const mutation = action !== 'status';
  try {
    combined.throwIfAborted();
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/connections/telegram-bot/${action}`,
      token,
      {
        method: mutation ? 'POST' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(mutation ? { 'Content-Type': 'application/json' } : {}),
        },
        ...(mutation
          ? {
              body: JSON.stringify(
                action === 'verify' ? {} : { consent_receive: true },
              ),
            }
          : {}),
        signal: combined,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    );
    combined.throwIfAborted();
    const payload = await responseJSON(response, combined);
    combined.throwIfAborted();
    if (!response.ok) {
      const candidate =
        record(payload) && record(payload.error) ? payload.error.code : null;
      const code =
        response.status === 401
          ? candidate === 'DEVICE_UNAUTHORIZED'
            ? 'DEVICE_UNAUTHORIZED'
            : 'UNAUTHORIZED'
          : typeof candidate === 'string' &&
              (Object.hasOwn(ERROR_MESSAGES, candidate) ||
                authErrorMessage(candidate, '') !== '')
            ? candidate
            : 'TELEGRAM_BOT_ERROR';
      // No upstream message, URL, exception cause, Bot identity or chat ID is echoed.
      throw new ServiceError(safeMessage(code), code, response.status);
    }
    if (response.status !== 200)
      throw new ServiceError(
        safeMessage('INVALID_RESPONSE'),
        'INVALID_RESPONSE',
      );
    return payload;
  } catch (failure: unknown) {
    if (signal.aborted)
      throw new ServiceError(safeMessage('ABORTED'), 'ABORTED');
    if (deadline.aborted)
      throw new ServiceError(safeMessage('TIMEOUT'), 'TIMEOUT');
    if (failure instanceof ServiceError) throw failure;
    throw new ServiceError(safeMessage('NETWORK_ERROR'), 'NETWORK_ERROR');
  }
}
export async function getTelegramBotStatus(
  token: string,
  signal: AbortSignal,
): Promise<TelegramBotStatus> {
  const value = await request('status', token, signal);
  if (!status(value))
    throw new ServiceError(safeMessage('INVALID_RESPONSE'), 'INVALID_RESPONSE');
  return value;
}
export async function verifyTelegramBot(
  token: string,
  signal: AbortSignal,
): Promise<TelegramBotStatus> {
  const value = await request('verify', token, signal);
  if (
    !status(value) ||
    !value.identity_verified ||
    value.state !== 'identity_verified'
  )
    throw new ServiceError(safeMessage('INVALID_RESPONSE'), 'INVALID_RESPONSE');
  return value;
}
export async function syncTelegramBot(
  token: string,
  signal: AbortSignal,
  consentReceive: true,
): Promise<TelegramBotSyncResult> {
  if (consentReceive !== true)
    throw new ServiceError(
      safeMessage('TELEGRAM_BOT_INVALID_REQUEST'),
      'TELEGRAM_BOT_INVALID_REQUEST',
    );
  const value = await request('sync', token, signal);
  if (!syncResult(value))
    throw new ServiceError(safeMessage('INVALID_RESPONSE'), 'INVALID_RESPONSE');
  return value;
}
