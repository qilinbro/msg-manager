import { SERVICE, ServiceError } from './api';

export type AwaySession = {
  id: string;
  status: 'running' | 'ended' | 'stopped';
  started_at: string;
  ends_at: string;
  stopped_at?: string;
  sender: string;
};
export type AwayTask = {
  id: string;
  title: string;
  requirements: string[];
  deadline?: string;
  deadline_label?: string;
  evidence?: string;
  draft?: string;
  draft_failed?: boolean;
  done?: boolean;
  subject: string;
  sender: string;
  received_at: string;
  body: string;
};
export type AwayEvent = {
  at: string;
  kind:
    | 'started'
    | 'received'
    | 'task'
    | 'draft'
    | 'sync_error'
    | 'draft_error'
    | 'stopped'
    | 'ended';
  detail?: string;
};
export type AwaySnapshot = {
  account?: string;
  model?: string;
  handoff_enabled: boolean;
  session?: AwaySession;
  tasks: AwayTask[];
  events: AwayEvent[];
};
export type PublicBrief = {
  event: string;
  product: string;
  selling_points: string;
  cta: string;
};
export type AwayStart = { minutes: number; sender: string; consent: boolean };
export interface AwayClient {
  get(signal?: AbortSignal): Promise<AwaySnapshot>;
  start(input: AwayStart): Promise<AwaySnapshot>;
  stop(id: string): Promise<AwaySnapshot>;
  updateTask(
    sessionId: string,
    taskId: string,
    patch: { draft?: string; done?: boolean },
  ): Promise<AwaySnapshot>;
  handoff(brief: PublicBrief): Promise<{ url: string }>;
}

export function createAwayClient(token: string): AwayClient {
  async function request<T>(
    path: string,
    method = 'GET',
    body?: object,
    signal?: AbortSignal,
  ): Promise<T> {
    if (SERVICE.error) throw new Error(SERVICE.error);
    const response = await fetch(`${SERVICE.url}/api/v1/away-sessions${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
      signal,
    });
    const data = await response.json();
    if (!response.ok) {
      throw new ServiceError(
        data.error?.message ?? '暂时无法连接接班服务。',
        data.error?.code ?? 'AWAY_ERROR',
        response.status,
      );
    }
    return data as T;
  }
  return {
    get: (signal) => request('', 'GET', undefined, signal),
    start: (input) => request('', 'POST', input),
    stop: (id) => request(`/${encodeURIComponent(id)}/stop`, 'POST'),
    updateTask: (sessionId, taskId, patch) =>
      request(
        `/${encodeURIComponent(sessionId)}/tasks/${encodeURIComponent(taskId)}`,
        'PATCH',
        patch,
      ),
    handoff: (brief) => request('/finfold', 'POST', { brief, confirmed: true }),
  };
}
