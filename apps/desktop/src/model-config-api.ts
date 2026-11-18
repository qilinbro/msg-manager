import { SERVICE, ServiceError } from './api';
import { authorizedFetch, authErrorMessage } from './device-auth';

export interface ModelConfig {
  base_url: string;
  model: string;
  daily_limit: number;
  key_configured: boolean;
  revision: number;
  configured: boolean;
  model_config_id: string;
  sending_enabled: false;
}
export interface ModelConfigSave {
  expected_revision: number;
  base_url: string;
  model: string;
  api_key: string;
  daily_limit: number;
  confirmed: true;
}
export interface ModelPreset {
  id: string;
  display_name: string;
  base_url: string;
  api_key_required: boolean;
  implementation_status: 'protocol_supported' | 'template_only' | 'unsupported';
  source_url: string;
  source_ref: string;
  source_license: string;
}
export interface ModelPresets {
  protocol: 'openai_chat_completions';
  model_id_required: true;
  live_verification_performed: false;
  items: ModelPreset[];
}
function exactObject(
  value: unknown,
  keys: string[],
): value is Record<string, unknown> {
  return (
    !!value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).length === keys.length &&
    keys.every((key) => Object.hasOwn(value, key))
  );
}
function boundedText(
  value: unknown,
  min: number,
  max: number,
): value is string {
  return (
    typeof value === 'string' &&
    value.length >= min &&
    value.length <= max &&
    !/[\u0000-\u001f\u007f]/u.test(value)
  );
}
function validPreset(value: unknown): value is ModelPreset {
  if (
    !exactObject(value, [
      'id',
      'display_name',
      'base_url',
      'api_key_required',
      'implementation_status',
      'source_url',
      'source_ref',
      'source_license',
    ])
  )
    return false;
  const v = value;
  if (
    !boundedText(v.id, 1, 64) ||
    !/^[a-z][a-z0-9_-]*$/u.test(v.id) ||
    !boundedText(v.display_name, 1, 80) ||
    !boundedText(v.base_url, 0, 2048) ||
    typeof v.api_key_required !== 'boolean' ||
    !['protocol_supported', 'template_only', 'unsupported'].includes(
      v.implementation_status as string,
    ) ||
    !boundedText(v.source_ref, 40, 40) ||
    !/^[a-f0-9]{40}$/u.test(v.source_ref) ||
    !boundedText(v.source_url, 1, 2048) ||
    !boundedText(v.source_license, 1, 128)
  )
    return false;
  try {
    const source = new URL(v.source_url);
    const parts = source.pathname.split('/');
    if (
      source.protocol !== 'https:' ||
      source.host !== 'github.com' ||
      source.username ||
      source.password ||
      /[\\%?#]/u.test(v.source_url) ||
      !parts[1] ||
      !parts[2] ||
      !['blob', 'tree'].includes(parts[3]) ||
      parts[4] !== v.source_ref
    )
      return false;
    if (v.base_url !== '') {
      const base = new URL(v.base_url);
      const loopback =
        base.protocol === 'http:' &&
        /^http:\/\/(?:127\.0\.0\.1|\[::1\]):[1-9][0-9]{0,4}(?:\/|$)/u.test(
          v.base_url,
        );
      if (
        base.username ||
        base.password ||
        /[\\%?#]/u.test(v.base_url) ||
        (base.protocol !== 'https:' && !loopback)
      )
        return false;
    }
    return true;
  } catch {
    return false;
  }
}
function validPresets(value: unknown): value is ModelPresets {
  if (
    !exactObject(value, [
      'protocol',
      'model_id_required',
      'live_verification_performed',
      'items',
    ]) ||
    value.protocol !== 'openai_chat_completions' ||
    value.model_id_required !== true ||
    value.live_verification_performed !== false ||
    !Array.isArray(value.items) ||
    value.items.length < 1 ||
    value.items.length > 16 ||
    !value.items.every(validPreset)
  )
    return false;
  return (
    new Set(value.items.map((item) => item.id)).size === value.items.length
  );
}
const messages: Record<string, string> = {
  AI_PRESETS_INVALID: '模型预设不接受查询参数。',
  AI_PRESETS_UNAVAILABLE: '暂时无法读取模型预设，请重试。',
  AI_CONFIG_INVALID: '模型设置无效，请核对服务地址、模型、密钥及每日额度。',
  AI_CONFIG_KEY_REQUIRED: '更换服务地址时，请重新填写该服务的密钥。',
  AI_CONFIG_STALE: '模型设置已有更新，输入已保留，请刷新并核对后再保存。',
  AI_CONFIG_BUSY: '当前模型仍在处理，请稍后保存；输入已保留。',
  AI_CONFIG_UNAVAILABLE: '设置未保存，请检查加密存储后重试。',
  AI_CONFIG_CONFIRMATION_REQUIRED: '请核对模型目标并勾选确认后保存。',
  STORAGE_DISABLED: '请先启用本地加密存储，再保存模型设置。',
};
function valid(value: unknown): value is ModelConfig {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.base_url === 'string' &&
    v.base_url.length <= 2048 &&
    typeof v.model === 'string' &&
    v.model.length <= 128 &&
    typeof v.daily_limit === 'number' &&
    Number.isSafeInteger(v.daily_limit) &&
    v.daily_limit >= 1 &&
    v.daily_limit <= 1000 &&
    typeof v.revision === 'number' &&
    Number.isSafeInteger(v.revision) &&
    v.revision >= 0 &&
    typeof v.configured === 'boolean' &&
    typeof v.key_configured === 'boolean' &&
    typeof v.model_config_id === 'string' &&
    v.model_config_id.length <= 128 &&
    v.sending_enabled === false &&
    !Object.hasOwn(v, 'api_key')
  );
}
async function request<T>(
  token: string,
  signal: AbortSignal,
  resource: 'model-config' | 'model-presets',
  validate: (value: unknown) => value is T,
  input?: ModelConfigSave,
): Promise<T> {
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const stop = () => controller.abort();
  signal.addEventListener('abort', stop, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const timeout = window.setTimeout(
    () => {
      timedOut = true;
      controller.abort();
    },
    input ? 55_000 : 12_000,
  );
  try {
    const response = await authorizedFetch(
      `${SERVICE.url}/api/v1/ai/${resource}`,
      token,
      {
        method: input ? 'PATCH' : 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(input ? { 'Content-Type': 'application/json' } : {}),
        },
        body: input
          ? JSON.stringify({
              expected_revision: input.expected_revision,
              base_url: input.base_url,
              model: input.model,
              daily_limit: input.daily_limit,
              api_key: input.api_key,
              confirmed: input.confirmed,
            })
          : undefined,
        signal: controller.signal,
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
      },
    );
    const payload: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('aborted', 'AbortError');
    if (response.status === 401)
      throw new ServiceError(
        '访问令牌已失效，请重新连接。',
        'UNAUTHORIZED',
        401,
      );
    if (!response.ok) {
      const code =
        payload &&
        typeof payload === 'object' &&
        'error' in payload &&
        payload.error &&
        typeof payload.error === 'object' &&
        'code' in payload.error &&
        typeof payload.error.code === 'string'
          ? payload.error.code
          : resource === 'model-presets'
            ? 'AI_PRESETS_UNAVAILABLE'
            : 'AI_CONFIG_UNAVAILABLE';
      throw new ServiceError(
        authErrorMessage(
          code,
          messages[code] ??
            (resource === 'model-presets'
              ? '暂时无法读取模型预设，请重试。'
              : '无法读取或保存模型配置，请刷新后核对。'),
        ),
        code,
        response.status,
      );
    }
    if (!validate(payload))
      throw new ServiceError(
        resource === 'model-presets'
          ? '模型预设响应无效，请检查后台版本。'
          : '模型配置响应不完整，请检查后台版本。',
        'INVALID_RESPONSE',
      );
    return payload;
  } catch (error) {
    if (error instanceof ServiceError) throw error;
    if (timedOut)
      throw new ServiceError(
        input
          ? '保存超时，后台可能已处理；请刷新确认，不要立即重复提交。'
          : resource === 'model-presets'
            ? '读取模型预设超时，请重试。'
            : '读取模型设置超时，请重试。',
        'TIMEOUT',
      );
    if (signal.aborted) throw new ServiceError('请求已取消。', 'ABORTED');
    throw new ServiceError(
      '无法连接本地服务，请刷新后确认设置。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timeout);
    signal.removeEventListener('abort', stop);
  }
}
export const getModelConfig = (token: string, signal: AbortSignal) =>
  request(token, signal, 'model-config', valid);
export const saveModelConfig = (
  token: string,
  signal: AbortSignal,
  input: ModelConfigSave,
) => request(token, signal, 'model-config', valid, input);
export const getModelPresets = (token: string, signal: AbortSignal) =>
  request(token, signal, 'model-presets', validPresets);
