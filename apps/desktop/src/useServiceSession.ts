import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { getSystem, SERVICE, ServiceError } from './api';
import type { SystemStatus } from './api';
import type { AIDraftBuffer } from './ai-api';
import {
  clearServiceSession,
  loadServiceSession,
  saveServiceSession,
} from './service-session';

export type ServiceSession = {
  status: 'idle' | 'connecting' | 'connected' | 'error';
  system: SystemStatus | null;
  error: string | null;
  checkedAt: string | null;
};

const INITIAL_SESSION: ServiceSession = {
  status: 'idle',
  system: null,
  error: null,
  checkedAt: null,
};

const checkedAt = () =>
  new Date().toLocaleTimeString('zh-CN', {
    hour12: false,
    timeZone: 'Asia/Shanghai',
  });

export function useServiceSession(aiDrafts: Map<string, AIDraftBuffer>) {
  const [saved] = useState(() => loadServiceSession(SERVICE.url));
  const [session, setSession] = useState<ServiceSession>(INITIAL_SESSION);
  const [tokenDraft, setTokenDraft] = useState(saved.token ?? '');
  const [activeToken, setActiveToken] = useState('');
  const [storageAvailable, setStorageAvailable] = useState(saved.available);
  const [authenticationEpoch, setAuthenticationEpoch] = useState(0);
  const authenticatedGeneration = useRef(0);
  const active = useRef('');
  const candidate = useRef(saved.token ?? '');
  const system = useRef<SystemStatus | null>(null);
  const pending = useRef<{
    controller: AbortController;
    token: string;
  } | null>(null);
  const generation = useRef(0);
  const mounted = useRef(false);

  const invalidate = useCallback(
    (token: string) => {
      if (
        !mounted.current ||
        !token ||
        (active.current !== token && candidate.current !== token)
      )
        return;
      generation.current++;
      pending.current?.controller.abort();
      pending.current = null;
      active.current = '';
      candidate.current = '';
      system.current = null;
      setAuthenticationEpoch(++authenticatedGeneration.current);
      aiDrafts.clear();
      setStorageAvailable(clearServiceSession(SERVICE.url, token));
      setActiveToken('');
      setTokenDraft((previous) => (previous.trim() === token ? '' : previous));
      setSession({
        ...INITIAL_SESSION,
        status: 'error',
        error: '令牌无效或已失效，请在设置中重新连接。',
      });
    },
    [aiDrafts],
  );

  const verify = useCallback(
    async (token: string, manual = false) => {
      if (!mounted.current || !token || SERVICE.error) return;
      if (pending.current?.token === token) return;
      if (!manual && pending.current) return;

      const previousToken = active.current;
      pending.current?.controller.abort();
      if (candidate.current !== token) {
        // A user-supplied replacement must never restore an older credential
        // after refresh while its verification is still pending.
        setStorageAvailable(clearServiceSession(SERVICE.url));
      }
      candidate.current = token;
      if (previousToken !== token) {
        setAuthenticationEpoch(++authenticatedGeneration.current);
        active.current = '';
        system.current = null;
        aiDrafts.clear();
        setActiveToken('');
      }
      const controller = new AbortController();
      const epoch = ++generation.current;
      pending.current = { controller, token };
      if (!active.current) {
        setSession({ ...INITIAL_SESSION, status: 'connecting' });
      } else {
        // Revalidation is a metadata refresh, not a disconnect/reconnect.
        setSession((previous) => ({ ...previous, error: null }));
      }
      const current = () =>
        mounted.current &&
        !controller.signal.aborted &&
        generation.current === epoch &&
        pending.current?.controller === controller;
      try {
        const result = await getSystem(token, controller.signal);
        if (!current()) return;
        active.current = token;
        candidate.current = token;
        system.current = result;
        setActiveToken(token);
        setStorageAvailable(saveServiceSession(SERVICE.url, token));
        setSession({
          status: 'connected',
          system: result,
          error: null,
          checkedAt: checkedAt(),
        });
      } catch (failure: unknown) {
        if (!current()) return;
        if (failure instanceof ServiceError && failure.status === 401) {
          invalidate(token);
          return;
        }
        const message =
          failure instanceof ServiceError &&
          !['NETWORK_ERROR', 'TIMEOUT'].includes(failure.code) &&
          failure.status !== 503
            ? failure.message
            : '服务暂时无法访问。连接信息已保留，返回页面时会自动重试。';
        setSession((previous) => ({
          ...previous,
          status: 'error',
          system: system.current,
          error: message,
        }));
      } finally {
        if (pending.current?.controller === controller) pending.current = null;
      }
    },
    [aiDrafts, invalidate],
  );

  useEffect(() => {
    mounted.current = true;
    const restore = () => {
      if (candidate.current) void verify(candidate.current);
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') restore();
    };
    restore();
    window.addEventListener('focus', restore);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      mounted.current = false;
      generation.current++;
      pending.current?.controller.abort();
      pending.current = null;
      window.removeEventListener('focus', restore);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [verify]);

  // Each child callback is bound to the token that rendered it. A late 401
  // from an old page/request cannot erase a newer successful connection.
  const onAuthFailure = useCallback(() => {
    if (
      mounted.current &&
      activeToken &&
      active.current === activeToken &&
      authenticatedGeneration.current === authenticationEpoch
    )
      invalidate(activeToken);
  }, [activeToken, authenticationEpoch, invalidate]);

  const onSystemUpdate = useCallback(
    (result: SystemStatus) => {
      if (
        !mounted.current ||
        !activeToken ||
        active.current !== activeToken ||
        authenticatedGeneration.current !== authenticationEpoch
      )
        return;
      system.current = result;
      setSession((previous) => ({
        ...previous,
        status: 'connected',
        system: result,
        error: null,
        checkedAt: checkedAt(),
      }));
    },
    [activeToken, authenticationEpoch],
  );

  const clear = useCallback(() => {
    generation.current++;
    pending.current?.controller.abort();
    pending.current = null;
    active.current = '';
    candidate.current = '';
    system.current = null;
    setAuthenticationEpoch(++authenticatedGeneration.current);
    aiDrafts.clear();
    setStorageAvailable(clearServiceSession(SERVICE.url));
    setTokenDraft('');
    setActiveToken('');
    setSession(INITIAL_SESSION);
  }, [aiDrafts]);

  const cancel = useCallback(() => {
    generation.current++;
    pending.current?.controller.abort();
    pending.current = null;
    setSession((previous) =>
      previous.status === 'connecting' ? INITIAL_SESSION : previous,
    );
  }, []);

  const connect = useCallback(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      return verify(tokenDraft.trim(), true);
    },
    [tokenDraft, verify],
  );

  return {
    session,
    tokenDraft,
    setTokenDraft,
    activeToken,
    storageAvailable,
    connect,
    clear,
    cancel,
    onAuthFailure,
    onSystemUpdate,
  };
}
