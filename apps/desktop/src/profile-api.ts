import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export const PROFILE_CATEGORIES = [
  'friend',
  'family',
  'colleague',
  'client',
  'partner',
  'other',
  'unclassified',
] as const;
export type ProfileCategory = (typeof PROFILE_CATEGORIES)[number];
export const CATEGORY_NAMES: Record<ProfileCategory, string> = {
  friend: '朋友',
  family: '家人',
  colleague: '同事',
  client: '客户',
  partner: '合作伙伴',
  other: '其他',
  unclassified: '未分类',
};
export interface ProfileStatus {
  model_configured: boolean;
  search_configured: boolean;
  model_config_id: string | null;
  model: string | null;
  provider: string | null;
  endpoint_host: string | null;
  daily_limit: number;
  used_today: number;
  search_daily_limit: number;
  search_used_today: number;
  sending_enabled: false;
}
export interface ProfileSettings {
  public_name: string;
  company: string;
  role: string;
  manual_notes: string;
  category_override: ProfileCategory | '';
  auto_organize: boolean;
  public_search_enabled: boolean;
}
export interface ProfileFact {
  id: string;
  field: string;
  value: string;
  state: 'unverified' | 'confirmed' | 'rejected';
  origin: 'chat' | 'public';
  message_ids: string[];
  source_ids: string[];
}
export interface ProfileSource {
  id: string;
  url: string;
  title: string;
  snippet: string;
  content_hash: string;
  retrieved_at: string;
}
export interface ProfileJob {
  id: string;
  conversation_id: string;
  kind: string;
  state: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  attempts: number;
  next_attempt_at: string | null;
  lease_until: string | null;
  model_config_id: string;
  settings_revision: number;
  max_seq: number;
  last_error_code: string | null;
  updated_at: string;
}
export interface Profile extends ProfileSettings {
  conversation_id: string;
  contact_id: string;
  display_name: string;
  platform: string;
  model_config_id: string;
  revision: number;
  processed_seq: number;
  max_seq: number;
  analysis_total?: number;
  analysis_processed?: number;
  category: ProfileCategory;
  ai_category: ProfileCategory;
  tags: string[];
  summary: string;
  evidence_ids: string[];
  facts: ProfileFact[];
  sources: ProfileSource[];
  jobs: ProfileJob[];
  updated_at: string | null;
  document_markdown: string;
  truncated: boolean;
  ai_paused: boolean;
}
export interface ProfileSaveRequest extends ProfileSettings {
  expected_revision: number;
  confirmed: boolean;
  model_config_id: string;
}
export interface ProfileJobRequest {
  expected_revision: number;
  confirmed: true;
  model_config_id: string;
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown, max = 16000): value is string =>
  typeof value === 'string' && value.length <= max;
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const date = (value: unknown): value is string =>
  text(value, 64) && Number.isFinite(Date.parse(value));
const nullableDate = (value: unknown) => value === null || date(value);
const strings = (value: unknown, max = 500): value is string[] =>
  Array.isArray(value) &&
  value.length <= max &&
  value.every((item) => text(item));
export const isProfileCategory = (value: unknown): value is ProfileCategory =>
  typeof value === 'string' &&
  PROFILE_CATEGORIES.includes(value as ProfileCategory);
const identifier = (value: unknown): value is string =>
  text(value, 128) && /^[A-Za-z0-9_-]+$/.test(value);
const validFact = (value: unknown): value is ProfileFact =>
  record(value) &&
  identifier(value.id) &&
  text(value.field, 128) &&
  text(value.value, 4000) &&
  ['unverified', 'confirmed', 'rejected'].includes(String(value.state)) &&
  ['chat', 'public'].includes(String(value.origin)) &&
  strings(value.message_ids) &&
  strings(value.source_ids);
const validSource = (value: unknown): value is ProfileSource =>
  record(value) &&
  identifier(value.id) &&
  text(value.url, 4096) &&
  text(value.title, 1000) &&
  text(value.snippet, 8000) &&
  text(value.content_hash, 128) &&
  date(value.retrieved_at);
const validJob = (value: unknown): value is ProfileJob =>
  record(value) &&
  identifier(value.id) &&
  identifier(value.conversation_id) &&
  text(value.kind, 64) &&
  ['queued', 'running', 'succeeded', 'failed', 'cancelled'].includes(
    String(value.state),
  ) &&
  count(value.attempts) &&
  nullableDate(value.next_attempt_at) &&
  nullableDate(value.lease_until) &&
  text(value.model_config_id, 128) &&
  count(value.settings_revision) &&
  count(value.max_seq) &&
  (value.last_error_code === null ||
    (text(value.last_error_code, 64) &&
      /^[A-Z][A-Z0-9_]*$/.test(value.last_error_code))) &&
  date(value.updated_at);
const validSettings = (value: unknown): value is ProfileSettings =>
  record(value) &&
  text(value.public_name, 256) &&
  text(value.company, 256) &&
  text(value.role, 256) &&
  text(value.manual_notes, 8000) &&
  (value.category_override === '' ||
    isProfileCategory(value.category_override)) &&
  typeof value.auto_organize === 'boolean' &&
  typeof value.public_search_enabled === 'boolean';
const validProfile = (value: unknown): value is Profile =>
  record(value) &&
  validSettings(value) &&
  identifier(value.conversation_id) &&
  identifier(value.contact_id) &&
  text(value.display_name, 512) &&
  text(value.platform, 32) &&
  text(value.model_config_id, 128) &&
  count(value.revision) &&
  count(value.processed_seq) &&
  count(value.max_seq) &&
  isProfileCategory(value.category) &&
  isProfileCategory(value.ai_category) &&
  strings(value.tags, 32) &&
  text(value.summary) &&
  strings(value.evidence_ids) &&
  Array.isArray(value.facts) &&
  value.facts.length <= 1000 &&
  value.facts.every(validFact) &&
  Array.isArray(value.sources) &&
  value.sources.length <= 500 &&
  value.sources.every(validSource) &&
  Array.isArray(value.jobs) &&
  value.jobs.length <= 500 &&
  value.jobs.every(validJob) &&
  value.jobs.every((job) => job.conversation_id === value.conversation_id) &&
  nullableDate(value.updated_at) &&
  text(value.document_markdown, 200000) &&
  typeof value.truncated === 'boolean' &&
  typeof value.ai_paused === 'boolean';

const ERRORS: Record<string, string> = {
  PROFILE_INPUT_TOO_LARGE:
    '存在超过单批处理预算的消息，任务已停在该条记录；请拆分记录后重试，后续消息尚未标记为已分析。',
  AI_NOT_CONFIGURED: '模型尚未配置，请在本机后台配置后重启服务。',
  AI_PROFILE_NOT_CONFIGURED: '模型尚未配置，请在本机后台配置后重启服务。',
  AI_PAUSED: '该联系人的 AI 已暂停，请在 AI 管理中恢复后再整理或检索。',
  SEARCH_NOT_CONFIGURED: '公开检索尚未配置，请在本机后台配置搜索服务。',
  PROFILE_SEARCH_NOT_CONFIGURED: '公开检索尚未配置，请在本机后台配置搜索服务。',
  AI_QUOTA_EXCEEDED: '模型额度已达到上限，请核对设置或稍后再试。',
  SEARCH_QUOTA_EXCEEDED: '公开检索额度已达到上限，请稍后再试。',
  STALE_CONTEXT: '档案已有变化，请刷新并核对当前设置后再操作。',
  PROFILE_CONFLICT: '档案已有变化，当前输入已保留，请重新载入设置后再保存。',
  PROFILE_REVISION_CONFLICT:
    '档案已有变化，当前输入已保留，请重新载入设置后再保存。',
  AI_CONFIRMATION_REQUIRED: '请先核对发送范围，并明确确认此次授权。',
  PROFILE_CONFIRMATION_REQUIRED: '请先核对发送范围，并明确确认此次授权。',
  AI_CONSENT_REQUIRED: '模型或处理范围已有变化，请重新核对后明确授权。',
  PROFILE_STALE: '档案或授权已有变化，输入已保留，请刷新并核对当前版本。',
  PROFILE_INVALID_INPUT: '请核对档案字段、分类和公开搜索身份后再提交。',
  PROFILE_PUBLIC_NAME_REQUIRED:
    '请明确填写用于公开搜索的姓名，不能使用平台账号代替。',
  PROFILE_SEARCH_NOT_AUTHORIZED:
    '请先保存公开搜索姓名，并明确开启此人的公开资料检索。',
  PROFILE_UNAVAILABLE: '个人档案暂时不可用，请稍后刷新状态。',
  PROFILE_CANCELLED: '任务已停止，已保存的档案仍可查看。',
  PROFILE_TIMEOUT: '后台处理超时，请先刷新档案和任务状态确认。',
  AI_AUTH_FAILED: '模型验证失败，请检查本机后台的模型配置。',
  AI_INVALID_OUTPUT: '模型结果未通过格式或依据校验，本次未写入新档案。',
  AI_INVALID_INPUT: '当前档案或聊天内容不满足模型处理要求，请核对后重试。',
  AI_TIMEOUT: '模型处理超时，请先刷新任务状态确认结果。',
  AI_UNAVAILABLE: '模型服务暂时不可用，请稍后查看任务状态。',
  SEARCH_AUTH_FAILED: '搜索服务验证失败，请检查本机后台的搜索配置。',
  SEARCH_INVALID_RESULT: '搜索结果未通过来源校验，本次未加入新资料。',
  SEARCH_TIMEOUT: '搜索等待超时，请先刷新后台任务状态。',
  SEARCH_UNAVAILABLE: '公开搜索暂时不可用，请稍后查看任务状态。',
  MODEL_CONFIG_CHANGED: '模型配置已变化，请刷新后重新确认发送范围。',
  AI_CONFIG_CHANGED: '模型配置已变化，请刷新后重新确认发送范围。',
  INVALID_REQUEST: '请核对姓名、公司、分类和授权设置后再提交。',
  INVALID_INPUT: '请核对姓名、公司、分类和授权设置后再提交。',
  CONVERSATION_NOT_FOUND: '这个会话已不存在，请刷新消息列表。',
  PROFILE_NOT_FOUND: '档案或候选信息已变化，请刷新后核对。',
  STORAGE_DISABLED: '本地加密存储尚未启用，请先连接已配置的后台。',
  STORE_UNAVAILABLE: '本地存储暂时不可用，请稍后刷新。',
};
export function profileErrorMessage(error: unknown): string {
  return error instanceof ServiceError
    ? error.message
    : '暂时无法读取个人档案，请刷新后再试。';
}
export function profileJobError(code: string | null): string {
  return code && Object.hasOwn(ERRORS, code)
    ? ERRORS[code]
    : '任务未完成，请检查后台配置并刷新状态。';
}
export function safeProfileSourceURL(value: string): string | null {
  try {
    const url = new URL(value);
    const host = url.hostname.toLowerCase();
    if (
      !['http:', 'https:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      !host.includes('.') ||
      host.endsWith('.localhost') ||
      host.endsWith('.local') ||
      host.endsWith('.internal') ||
      /^[\d.]+$/.test(host) ||
      host.includes(':')
    )
      return null;
    return url.href;
  } catch {
    return null;
  }
}

function invalid(): never {
  throw new ServiceError(
    '个人档案数据格式与当前页面不匹配，请更新并重启后台后刷新。',
    'INVALID_RESPONSE',
  );
}
function scope(id: string) {
  if (!identifier(id))
    throw new ServiceError('会话或记录标识无效，请刷新列表。', 'INVALID_INPUT');
  return `conversations/${encodeURIComponent(id)}/profile`;
}
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
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const timeout = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 15000);
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/${path}`,
      token,
      {
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
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (!response.ok) {
      if (response.status === 401)
        throw new ServiceError(
          '服务令牌已失效，请重新连接。',
          'UNAUTHORIZED',
          401,
        );
      const code =
        record(payload) && record(payload.error) && text(payload.error.code, 64)
          ? payload.error.code
          : 'PROFILE_SERVICE_ERROR';
      if (
        response.status === 501 ||
        (response.status === 404 &&
          !['CONVERSATION_NOT_FOUND', 'PROFILE_NOT_FOUND'].includes(code))
      )
        throw new ServiceError(
          '后台尚未提供个人档案，请更新代码并重启后台；原消息功能仍可使用。',
          'PROFILE_UNSUPPORTED',
          response.status,
        );
      throw new ServiceError(
        authErrorMessage(
          code,
          Object.hasOwn(ERRORS, code)
            ? ERRORS[code]
            : '暂时无法完成档案操作，请刷新档案与任务状态后再试。',
        ),
        code,
        response.status,
      );
    }
    return payload;
  } catch (failure: unknown) {
    if (timedOut)
      throw new ServiceError(
        method === 'GET'
          ? '档案读取超时，请确认后台运行后刷新。'
          : '等待超时，后台可能已保存或接收任务，请先刷新档案和任务状态确认。',
        'TIMEOUT',
      );
    if (controller.signal.aborted)
      throw new DOMException('请求已取消', 'AbortError');
    if (failure instanceof ServiceError) throw failure;
    throw new ServiceError(
      method === 'GET'
        ? '无法读取个人档案，请确认本地服务正在运行。'
        : '连接中断，后台可能已保存或接收任务，请先刷新档案和任务状态确认。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', abort);
  }
}
export async function getProfileStatus(
  token: string,
  signal: AbortSignal,
): Promise<ProfileStatus> {
  const value = await request('ai/profile-status', token, signal);
  if (
    !record(value) ||
    typeof value.model_configured !== 'boolean' ||
    typeof value.search_configured !== 'boolean' ||
    ![
      value.model_config_id,
      value.model,
      value.provider,
      value.endpoint_host,
    ].every((item) => item === null || text(item, 512)) ||
    (value.model_configured && !value.model_config_id) ||
    ![
      value.daily_limit,
      value.used_today,
      value.search_daily_limit,
      value.search_used_today,
    ].every(count) ||
    value.sending_enabled !== false
  )
    invalid();
  return value as unknown as ProfileStatus;
}
function checkedProfile(value: unknown, id: string): Profile {
  if (!validProfile(value) || value.conversation_id !== id) invalid();
  return value;
}
export async function getProfile(
  token: string,
  signal: AbortSignal,
  id: string,
): Promise<Profile> {
  return checkedProfile(await request(scope(id), token, signal), id);
}
export async function saveProfile(
  token: string,
  signal: AbortSignal,
  id: string,
  input: ProfileSaveRequest,
): Promise<Profile> {
  if (
    !validSettings(input) ||
    !count(input.expected_revision) ||
    typeof input.confirmed !== 'boolean' ||
    !text(input.model_config_id, 128)
  )
    invalid();
  const body: ProfileSaveRequest = {
    expected_revision: input.expected_revision,
    public_name: input.public_name,
    company: input.company,
    role: input.role,
    manual_notes: input.manual_notes,
    category_override: input.category_override,
    auto_organize: input.auto_organize,
    public_search_enabled: input.public_search_enabled,
    confirmed: input.confirmed,
    model_config_id: input.model_config_id,
  };
  return checkedProfile(
    await request(scope(id), token, signal, 'PATCH', body),
    id,
  );
}
async function enqueue(
  token: string,
  signal: AbortSignal,
  id: string,
  kind: 'generate' | 'enrich',
  input: ProfileJobRequest,
): Promise<ProfileJob> {
  if (
    !count(input.expected_revision) ||
    input.confirmed !== true ||
    !text(input.model_config_id, 128) ||
    !input.model_config_id
  )
    invalid();
  const value = await request(`${scope(id)}/${kind}`, token, signal, 'POST', {
    expected_revision: input.expected_revision,
    confirmed: true,
    model_config_id: input.model_config_id,
  });
  if (!validJob(value) || value.conversation_id !== id) invalid();
  return value;
}
export const generateProfile = (
  token: string,
  signal: AbortSignal,
  id: string,
  input: ProfileJobRequest,
) => enqueue(token, signal, id, 'generate', input);
export const enrichProfile = (
  token: string,
  signal: AbortSignal,
  id: string,
  input: ProfileJobRequest,
) => enqueue(token, signal, id, 'enrich', input);
export async function setProfileFactState(
  token: string,
  signal: AbortSignal,
  id: string,
  factID: string,
  state: 'confirmed' | 'rejected',
  expectedRevision: number,
): Promise<Profile> {
  if (
    !identifier(factID) ||
    !['confirmed', 'rejected'].includes(state) ||
    !count(expectedRevision)
  )
    invalid();
  return checkedProfile(
    await request(
      `${scope(id)}/facts/${encodeURIComponent(factID)}`,
      token,
      signal,
      'PATCH',
      { state, expected_revision: expectedRevision },
    ),
    id,
  );
}
