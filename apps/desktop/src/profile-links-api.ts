import { SERVICE, ServiceError } from './api';

export interface ProfileLinkIdentity {
  conversation_id: string;
  display_name: string;
  platform: string;
  peer_id: string;
  reason:
    'confirmed' | 'same_public_name' | 'same_nickname' | 'same_platform_id';
}
export interface ProfileLinks {
  conversation_id: string;
  candidates: ProfileLinkIdentity[];
  linked: ProfileLinkIdentity[];
  document_markdown: string;
  truncated: boolean;
}
const id = (value: unknown): value is string =>
  typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const identity = (value: unknown): value is ProfileLinkIdentity =>
  record(value) &&
  id(value.conversation_id) &&
  typeof value.display_name === 'string' &&
  value.display_name.length <= 512 &&
  typeof value.platform === 'string' &&
  value.platform.length <= 32 &&
  typeof value.peer_id === 'string' &&
  value.peer_id.length <= 128 &&
  [
    'confirmed',
    'same_public_name',
    'same_nickname',
    'same_platform_id',
  ].includes(String(value.reason));

async function request(
  token: string,
  signal: AbortSignal,
  conversation: string,
  method: 'GET' | 'POST' | 'DELETE',
  target?: string,
): Promise<ProfileLinks> {
  if (
    !id(conversation) ||
    (target !== undefined && (!id(target) || target === conversation))
  )
    throw new ServiceError('请选择两份不同的联系人档案。', 'INVALID_INPUT');
  if (SERVICE.error) throw new ServiceError(SERVICE.error, 'CONFIG_ERROR');
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) controller.abort();
  let timedOut = false;
  const timer = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 20000);
  try {
    const response = await fetch(
      `${SERVICE.url}/api/v1/conversations/${conversation}/profile-links${method === 'DELETE' ? `/${target}` : ''}`,
      {
        method,
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(method === 'POST' ? { 'Content-Type': 'application/json' } : {}),
        },
        body:
          method === 'POST'
            ? JSON.stringify({
                target_conversation_id: target,
                confirmed: true,
              })
            : undefined,
        signal: controller.signal,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    );
    const value: unknown = await response.json().catch(() => null);
    if (controller.signal.aborted)
      throw new DOMException('Aborted', 'AbortError');
    if (!response.ok) {
      if (response.status === 401)
        throw new ServiceError(
          '访问令牌已失效，请重新连接。',
          'UNAUTHORIZED',
          401,
        );
      const code =
        record(value) &&
        record(value.error) &&
        typeof value.error.code === 'string'
          ? value.error.code
          : 'PROFILE_LINK_UNAVAILABLE';
      const messages: Record<string, string> = {
        PROFILE_LINK_CONFIRMATION_REQUIRED: '请先确认两份档案属于同一人。',
        PROFILE_STALE: '关联数量已达上限或档案已变化，请刷新后核对。',
        PROFILE_NOT_FOUND: '来源会话已变化，请刷新后重新选择。',
        PROFILE_INVALID_INPUT: '请选择两份不同且有效的联系人档案。',
      };
      throw new ServiceError(
        messages[code] ?? '无法处理档案关联，请刷新状态后重试。',
        code,
        response.status,
      );
    }
    if (
      !record(value) ||
      value.conversation_id !== conversation ||
      !Array.isArray(value.candidates) ||
      value.candidates.length > 20 ||
      !value.candidates.every(identity) ||
      !Array.isArray(value.linked) ||
      value.linked.length > 20 ||
      !value.linked.every(identity) ||
      typeof value.document_markdown !== 'string' ||
      value.document_markdown.length > 200000 ||
      typeof value.truncated !== 'boolean' ||
      [...value.candidates, ...value.linked].some(
        (item) => item.conversation_id === conversation,
      )
    )
      throw new ServiceError(
        '档案关联结果未通过检查，请刷新确认。',
        'INVALID_RESPONSE',
      );
    if (
      method === 'POST' &&
      !value.linked.some((item) => item.conversation_id === target)
    )
      throw new ServiceError(
        '后台未确认关联已建立，请刷新状态核对。',
        'INVALID_RESPONSE',
      );
    if (
      method === 'DELETE' &&
      value.linked.some((item) => item.conversation_id === target)
    )
      throw new ServiceError(
        '后台未确认关联已解除，请刷新状态核对。',
        'INVALID_RESPONSE',
      );
    return value as unknown as ProfileLinks;
  } catch (error: unknown) {
    if (timedOut)
      throw new ServiceError(
        method === 'GET'
          ? '读取关联状态超时。'
          : '等待超时，后台可能已处理。请刷新确认后再操作。',
        'TIMEOUT',
      );
    if (error instanceof ServiceError) throw error;
    if (controller.signal.aborted)
      throw new ServiceError('请求已取消。', 'ABORTED');
    throw new ServiceError(
      '暂时无法连接后台，请刷新关联状态确认。',
      'NETWORK_ERROR',
    );
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

export const getProfileLinks = (
  token: string,
  signal: AbortSignal,
  conversation: string,
) => request(token, signal, conversation, 'GET');
export const confirmProfileLink = (
  token: string,
  signal: AbortSignal,
  conversation: string,
  target: string,
) => request(token, signal, conversation, 'POST', target);
export const removeProfileLink = (
  token: string,
  signal: AbortSignal,
  conversation: string,
  target: string,
) => request(token, signal, conversation, 'DELETE', target);
