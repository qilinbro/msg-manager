import { SERVICE, ServiceError } from './api';

export interface AIStatus {
  configured: boolean;
  provider: string | null;
  model: string | null;
  endpoint_host: string | null;
  sending_enabled: false;
  daily_limit: number;
  used_today: number;
}
export interface AIEvidence {
  id: string;
  body: string;
  direction: string;
  occurred_at: string;
}
export interface AIMemory {
  id: string;
  content: string;
  verification_state: 'unverified' | 'confirmed';
  evidence_ids: string[];
  created_at: string;
}
export interface AIContext {
  context_hash: string;
  model: string | null;
  endpoint_host: string | null;
  external: boolean;
  message_count: number;
  memory_count: number;
  truncated: boolean;
  messages: AIEvidence[];
  memories: AIMemory[];
  mode: 'draft_only' | 'paused';
  tone: 'natural' | 'casual' | 'professional';
}
export interface AIRecord {
  id: string;
  conversation_id: string;
  context_hash: string;
  model: string;
  created_at: string;
  stale: boolean;
  intent: string;
  summary: string;
  draft: string;
  risk_flags: string[];
  evidence_ids: string[];
  usage: { prompt_tokens: number; completion_tokens: number };
  sending_enabled: false;
}
export interface AIDraftBuffer {
  analysis: AIRecord;
  body: string;
}
export interface AIEnvelope {
  analysis: AIRecord | null;
  memories: AIMemory[];
}
export interface AISettings {
  mode: AIContext['mode'];
  tone: AIContext['tone'];
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const string = (value: unknown): value is string => typeof value === 'string';
const strings = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every(string);
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const nullableString = (value: unknown): value is string | null =>
  value === null || string(value);
const date = (value: unknown): value is string =>
  string(value) && Number.isFinite(Date.parse(value));

const ERRORS: Record<string, string> = {
  AI_NOT_CONFIGURED: '模型尚未配置，请按项目 AI 接入说明在本机设置后重启服务。',
  AI_BUSY: '已有 AI 分析正在进行，请先刷新结果，稍后再试。',
  AI_PAUSED: '当前会话的 AI 处理已暂停，恢复后再开始分析。',
  AI_QUOTA_EXCEEDED:
    'AI 调用额度或请求频率已达到限制，请核对本地额度与模型服务状态。',
  AI_AUTH_FAILED: '模型验证失败，请检查本机后台配置的模型密钥。',
  AI_INVALID_OUTPUT: '模型未返回可核对的有效结果，本次没有生成草稿，请重试。',
  AI_UNAVAILABLE: '模型服务暂时不可用，请稍后再试。',
  AI_TIMEOUT: '模型处理超时，请先刷新结果，确认后再决定是否重试。',
  AI_CONFIRMATION_REQUIRED: '请先核对本次提交的会话范围，并确认后再开始分析。',
  AI_CONTEXT_INVALID:
    '当前会话没有可分析内容，或内容超过模型输入限制，请核对会话后重试。',
  STALE_CONTEXT: '当前会话或记忆已有变化，请重新预览分析范围后再继续。',
  STORAGE_DISABLED: '本地加密存储尚未启用，请先完成存储配置。',
  STORE_UNAVAILABLE: '本地消息存储暂不可用，请稍后刷新。',
  CONVERSATION_NOT_FOUND: '当前会话或记录已不存在，请刷新后查看。',
  INVALID_REQUEST: '提交内容不符合当前版本，请刷新后重新操作。',
  PAYLOAD_TOO_LARGE: '提交内容过长，请缩短内容后重试。',
  NOT_FOUND: '当前服务版本未提供这项 AI 操作，请检查服务版本。',
};

async function request(
  path: string,
  token: string,
  signal: AbortSignal,
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE' = 'GET',
  body?: unknown,
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
    method === 'POST' ? 35_000 : 10_000,
  );
  try {
    const response = await fetch(`${SERVICE.url}/api/v1/${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
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
          '访问令牌已失效，请重新连接本地服务。',
          'UNAUTHORIZED',
          401,
        );
      const code =
        record(payload) && record(payload.error) && string(payload.error.code)
          ? payload.error.code
          : 'AI_SERVICE_ERROR';
      throw new ServiceError(
        Object.hasOwn(ERRORS, code)
          ? ERRORS[code]
          : '暂时无法完成 AI 操作，请刷新后重试。',
        code,
        response.status,
      );
    }
    return payload;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        method === 'GET'
          ? '读取 AI 数据超时，请确认本地服务可用后刷新。'
          : '等待超时，模型可能已处理并产生费用。请先刷新结果，确认后再决定是否重试。',
        'TIMEOUT',
      );
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (error instanceof ServiceError) throw error;
    throw new ServiceError(
      method === 'GET'
        ? '无法读取 AI 数据，请检查本地服务后刷新。'
        : '连接中断，后台可能已处理，请先刷新结果再决定是否重试。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}
function invalid(): never {
  throw new ServiceError(
    'AI 数据格式暂时无法确认，请检查服务版本并刷新。',
    'INVALID_RESPONSE',
  );
}
function memory(value: unknown): value is AIMemory {
  return (
    record(value) &&
    string(value.id) &&
    string(value.content) &&
    ['unverified', 'confirmed'].includes(String(value.verification_state)) &&
    strings(value.evidence_ids) &&
    date(value.created_at)
  );
}
function evidence(value: unknown): value is AIEvidence {
  return (
    record(value) &&
    ['id', 'body', 'direction'].every((key) => string(value[key])) &&
    date(value.occurred_at)
  );
}
function analysis(value: unknown): value is AIRecord {
  return (
    record(value) &&
    [
      'id',
      'conversation_id',
      'context_hash',
      'model',
      'intent',
      'summary',
      'draft',
    ].every((key) => string(value[key])) &&
    date(value.created_at) &&
    typeof value.stale === 'boolean' &&
    strings(value.risk_flags) &&
    strings(value.evidence_ids) &&
    record(value.usage) &&
    count(value.usage.prompt_tokens) &&
    count(value.usage.completion_tokens) &&
    value.sending_enabled === false
  );
}
const conversationPath = (id: string) =>
  `conversations/${encodeURIComponent(id)}`;
export async function getAIStatus(
  token: string,
  signal: AbortSignal,
): Promise<AIStatus> {
  const value = await request('ai/status', token, signal);
  if (
    !record(value) ||
    typeof value.configured !== 'boolean' ||
    !nullableString(value.provider) ||
    !nullableString(value.model) ||
    !nullableString(value.endpoint_host) ||
    value.sending_enabled !== false ||
    !count(value.daily_limit) ||
    !count(value.used_today)
  )
    invalid();
  return value as unknown as AIStatus;
}
export async function getAIContext(
  token: string,
  signal: AbortSignal,
  id: string,
): Promise<AIContext> {
  const value = await request(
    `${conversationPath(id)}/ai-context`,
    token,
    signal,
  );
  if (
    !record(value) ||
    !string(value.context_hash) ||
    !nullableString(value.model) ||
    !nullableString(value.endpoint_host) ||
    typeof value.external !== 'boolean' ||
    !count(value.message_count) ||
    !count(value.memory_count) ||
    typeof value.truncated !== 'boolean' ||
    !Array.isArray(value.messages) ||
    !value.messages.every(evidence) ||
    !Array.isArray(value.memories) ||
    !value.memories.every(memory) ||
    !['draft_only', 'paused'].includes(String(value.mode)) ||
    !['natural', 'casual', 'professional'].includes(String(value.tone))
  )
    invalid();
  return value as unknown as AIContext;
}
export async function getAnalysis(
  token: string,
  signal: AbortSignal,
  id: string,
): Promise<AIEnvelope> {
  const value = await request(
    `${conversationPath(id)}/analysis`,
    token,
    signal,
  );
  if (
    !record(value) ||
    !(value.analysis === null || analysis(value.analysis)) ||
    !Array.isArray(value.memories) ||
    !value.memories.every(memory)
  )
    invalid();
  return value as unknown as AIEnvelope;
}
export async function analyzeConversation(
  token: string,
  signal: AbortSignal,
  id: string,
  contextHash: string,
): Promise<AIRecord> {
  const value = await request(
    `${conversationPath(id)}/analyze`,
    token,
    signal,
    'POST',
    { context_hash: contextHash, confirmed: true },
  );
  if (!analysis(value) || value.conversation_id !== id) invalid();
  return value;
}
async function mutation(
  path: string,
  token: string,
  signal: AbortSignal,
  method: 'PATCH' | 'DELETE',
  body?: unknown,
): Promise<void> {
  const value = await request(path, token, signal, method, body);
  if (!record(value) || value.ok !== true) invalid();
}
export const saveAIDraft = (
  token: string,
  signal: AbortSignal,
  id: string,
  analysisID: string,
  body: string,
) =>
  mutation(`${conversationPath(id)}/draft`, token, signal, 'PATCH', {
    analysis_id: analysisID,
    body,
  });
export const confirmMemory = (
  token: string,
  signal: AbortSignal,
  id: string,
  memoryID: string,
) =>
  mutation(
    `${conversationPath(id)}/memories/${encodeURIComponent(memoryID)}`,
    token,
    signal,
    'PATCH',
    { verification_state: 'confirmed' },
  );
export const deleteMemory = (
  token: string,
  signal: AbortSignal,
  id: string,
  memoryID: string,
) =>
  mutation(
    `${conversationPath(id)}/memories/${encodeURIComponent(memoryID)}`,
    token,
    signal,
    'DELETE',
  );
export async function saveAISettings(
  token: string,
  signal: AbortSignal,
  id: string,
  settings: AISettings,
): Promise<void> {
  const value = await request(
    `${conversationPath(id)}/ai-settings`,
    token,
    signal,
    'PATCH',
    settings,
  );
  if (record(value) && value.ok === true) return;
  if (
    !record(value) ||
    value.mode !== settings.mode ||
    value.tone !== settings.tone
  )
    invalid();
}
