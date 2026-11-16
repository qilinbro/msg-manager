import { SERVICE, ServiceError } from './api';

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
const messages: Record<string, string> = {
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
async function request(
  token: string,
  signal: AbortSignal,
  input?: ModelConfigSave,
): Promise<ModelConfig> {
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
    const response = await fetch(`${SERVICE.url}/api/v1/ai/model-config`, {
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
    });
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
          : 'AI_CONFIG_UNAVAILABLE';
      throw new ServiceError(
        messages[code] ?? '无法读取或保存模型配置，请刷新后核对。',
        code,
        response.status,
      );
    }
    if (!valid(payload))
      throw new ServiceError(
        '模型配置响应不完整，请检查后台版本。',
        'INVALID_RESPONSE',
      );
    return payload;
  } catch (error) {
    if (error instanceof ServiceError) throw error;
    if (timedOut)
      throw new ServiceError(
        input
          ? '保存超时，后台可能已处理；请刷新确认，不要立即重复提交。'
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
  request(token, signal);
export const saveModelConfig = (
  token: string,
  signal: AbortSignal,
  input: ModelConfigSave,
) => request(token, signal, input);
