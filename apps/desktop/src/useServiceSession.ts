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
import {
  DeviceAuthError,
  deviceAuthAvailable,
  forgetDevice,
  isDeviceHandle,
  loadDeviceSession,
  pairDevice,
  subscribeDeviceForget,
} from './device-auth';

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
const TAB_ONLY =
  '未记住此浏览器；连接仅在当前页面内存中保留，刷新后需重新连接。';
const checkedAt = () =>
  new Date().toLocaleTimeString('zh-CN', {
    hour12: false,
    timeZone: 'Asia/Shanghai',
  });

export function useServiceSession(aiDrafts: Map<string, AIDraftBuffer>) {
  const [saved] = useState(() => loadServiceSession(SERVICE.url));
  const [session, setSession] = useState<ServiceSession>(INITIAL_SESSION);
  const [tokenDraft, setTokenDraft] = useState(
    saved.token && !isDeviceHandle(saved.token) ? saved.token : '',
  );
  const [activeToken, setActiveToken] = useState('');
  const [storageAvailable, setStorageAvailable] = useState(saved.available);
  const [authenticationEpoch, setAuthenticationEpoch] = useState(0);
  const [rememberDevice, setRememberDevice] = useState(true);
  const [deviceExpiresAt, setDeviceExpiresAt] = useState<string | null>(null);
  const [persistenceNotice, setPersistenceNotice] = useState<string | null>(
    null,
  );
  const [forgetting, setForgetting] = useState(false);
  const remember = useRef(rememberDevice);
  remember.current = rememberDevice;
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
  const explicitlyCleared = useRef(false);
  const restoring = useRef<Promise<void> | null>(null);
  const clearing = useRef(false);

  const invalidate = useCallback(
    (token: string, forgetLocal = true) => {
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
      explicitlyCleared.current = true;
      system.current = null;
      setAuthenticationEpoch(++authenticatedGeneration.current);
      aiDrafts.clear();
      setStorageAvailable(clearServiceSession(SERVICE.url, token));
      setActiveToken('');
      setDeviceExpiresAt(null);
      setPersistenceNotice(null);
      setTokenDraft((previous) => (previous.trim() === token ? '' : previous));
      setSession({
        ...INITIAL_SESSION,
        status: 'error',
        error: isDeviceHandle(token)
          ? '本机授权已失效或已被忘记，请在设置中输入主令牌重新连接。'
          : '令牌无效或已失效，请在设置中重新连接。',
      });
      if (forgetLocal && isDeviceHandle(token))
        void forgetDevice(SERVICE.url, token, false);
    },
    [aiDrafts],
  );

  const verify = useCallback(
    async (token: string, manual = false) => {
      if (!mounted.current || !token || SERVICE.error || clearing.current)
        return;
      if (pending.current?.token === token) return;
      if (!manual && pending.current) return;

      const previousToken = active.current;
      const previousCandidate = candidate.current;
      pending.current?.controller.abort();
      if (previousCandidate !== token) {
        // A replacement must never restore an older credential while its own
        // verification is pending, including a durable device descriptor.
        setStorageAvailable(clearServiceSession(SERVICE.url));
      }
      candidate.current = token;
      explicitlyCleared.current = false;
      if (previousToken !== token) {
        setAuthenticationEpoch(++authenticatedGeneration.current);
        active.current = '';
        system.current = null;
        aiDrafts.clear();
        setActiveToken('');
        setDeviceExpiresAt(null);
      }
      const controller = new AbortController();
      const epoch = ++generation.current;
      pending.current = { controller, token };
      if (!active.current) {
        setSession({ ...INITIAL_SESSION, status: 'connecting' });
      } else {
        // Revalidation is a metadata refresh, not a disconnect/reconnect. Access
        // renewal in the shared transport never changes the public React key.
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
        let verifiedToken = token;
        if (manual && !isDeviceHandle(token)) {
          setPersistenceNotice(null);
          if (!remember.current) {
            setPersistenceNotice(TAB_ONLY);
          } else if (!deviceAuthAvailable()) {
            setPersistenceNotice(
              '当前环境不支持安全保存本机密钥，未记住此浏览器；本次仅连接当前页面，刷新后需重新连接。',
            );
          } else {
            try {
              const paired = await pairDevice(
                SERVICE.url,
                token,
                controller.signal,
              );
              if (!current()) {
                void forgetDevice(SERVICE.url, paired.handle);
                return;
              }
              verifiedToken = paired.handle;
              setDeviceExpiresAt(paired.expiresAt);
              setPersistenceNotice(
                '已记住此浏览器。主令牌不会长期保存，可随时忘记本机。',
              );
              setTokenDraft((previous) =>
                previous.trim() === token ? '' : previous,
              );
            } catch (failure) {
              if (!current()) return;
              if (failure instanceof DeviceAuthError && failure.status === 401)
                throw new ServiceError(
                  '主令牌已失效，请重新连接。',
                  'UNAUTHORIZED',
                  401,
                );
              // Unsupported/old backend, unavailable encrypted store and denied
              // browser storage must not prevent a verified tab-only connection.
              setPersistenceNotice(
                '未能记住本次连接（服务版本、存储或网络暂不可用）；本次主令牌仅在当前页面内存中保留。如无可用的已有本机授权，刷新后需重新连接。',
              );
            }
          }
        } else if (isDeviceHandle(token)) {
          const device = await loadDeviceSession(SERVICE.url);
          if (!current()) return;
          if (device?.handle !== token)
            throw new ServiceError('本机授权已失效。', 'UNAUTHORIZED', 401);
          setDeviceExpiresAt(device.expiresAt);
        }
        // A failed replacement retains the previous durable grant. Only an
        // explicit opt-out retires it here; successful pairing owns replacement.
        if (
          manual &&
          !remember.current &&
          !isDeviceHandle(verifiedToken) &&
          deviceAuthAvailable()
        ) {
          const previousDevice = isDeviceHandle(previousCandidate)
            ? previousCandidate
            : (await loadDeviceSession(SERVICE.url).catch(() => null))?.handle;
          if (!current()) return;
          if (previousDevice) void forgetDevice(SERVICE.url, previousDevice);
        }
        if (!current()) return;
        active.current = verifiedToken;
        candidate.current = verifiedToken;
        system.current = result;
        setActiveToken(verifiedToken);
        setStorageAvailable(saveServiceSession(SERVICE.url, verifiedToken));
        setSession({
          status: 'connected',
          system: result,
          error: null,
          checkedAt: checkedAt(),
        });
      } catch (failure: unknown) {
        if (!current()) return;
        if (
          (failure instanceof ServiceError ||
            failure instanceof DeviceAuthError) &&
          failure.status === 401
        ) {
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
      if (candidate.current) {
        void verify(candidate.current);
      } else if (
        !explicitlyCleared.current &&
        !restoring.current &&
        deviceAuthAvailable()
      ) {
        const epoch = generation.current;
        const operation = loadDeviceSession(SERVICE.url)
          .then((device) => {
            if (
              !mounted.current ||
              generation.current !== epoch ||
              candidate.current ||
              explicitlyCleared.current
            )
              return;
            if (device) {
              candidate.current = device.handle;
              setDeviceExpiresAt(device.expiresAt);
              void verify(device.handle);
            }
          })
          .catch(() => {
            if (mounted.current && generation.current === epoch)
              setPersistenceNotice(
                '暂时无法读取已记住的本机授权；可检查存储权限后重试，或输入主令牌连接。',
              );
          })
          .finally(() => {
            if (restoring.current === operation) restoring.current = null;
          });
        restoring.current = operation;
      }
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') restore();
    };
    const unsubscribe = deviceAuthAvailable()
      ? subscribeDeviceForget((serviceURL, handle) => {
          if (serviceURL === SERVICE.url) invalidate(handle, false);
        })
      : () => {};
    restore();
    window.addEventListener('focus', restore);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      mounted.current = false;
      generation.current++;
      pending.current?.controller.abort();
      pending.current = null;
      restoring.current = null;
      unsubscribe();
      window.removeEventListener('focus', restore);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [verify, invalidate]);

  // Each child callback is bound to both the public credential identity and its
  // authenticated epoch. An old 401 cannot clear a newer connection, including
  // a clear + reconnect with the same manually entered master token.
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
    if (clearing.current) return;
    clearing.current = true;
    const previous = active.current || candidate.current;
    const epoch = ++generation.current;
    pending.current?.controller.abort();
    pending.current = null;
    active.current = '';
    candidate.current = '';
    explicitlyCleared.current = true;
    system.current = null;
    setAuthenticationEpoch(++authenticatedGeneration.current);
    aiDrafts.clear();
    setStorageAvailable(clearServiceSession(SERVICE.url));
    setTokenDraft('');
    setActiveToken('');
    setDeviceExpiresAt(null);
    setPersistenceNotice(null);
    setSession(INITIAL_SESSION);
    if (isDeviceHandle(previous) || deviceAuthAvailable()) {
      setForgetting(true);
      const operation = isDeviceHandle(previous)
        ? forgetDevice(SERVICE.url, previous)
        : loadDeviceSession(SERVICE.url).then((device) => {
            if (generation.current !== epoch || !device) return null;
            return forgetDevice(SERVICE.url, device.handle);
          });
      return operation
        .then((result) => {
          if (!mounted.current || generation.current !== epoch) return;
          setPersistenceNotice(
            !result
              ? '已清除当前页面连接。'
              : !result.forgotten
                ? '已断开，但无法确认本机密钥已删除。请在浏览器中清除此站点数据；离线时也无法确认后台撤销。'
                : result.revoked
                  ? '已忘记此浏览器，并确认后台授权已撤销。'
                  : '已删除本机密钥。未能确认后台授权撤销（可能离线）；不会再使用此密钥自动连接。',
          );
        })
        .catch(() => {
          if (mounted.current && generation.current === epoch)
            setPersistenceNotice(
              '已断开，但无法读取本机授权。请清除此站点数据，以确认删除本机密钥。',
            );
        })
        .finally(() => {
          clearing.current = false;
          if (mounted.current) setForgetting(false);
        });
    }
    clearing.current = false;
    setForgetting(false);
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
      if (forgetting) return;
      return verify(
        tokenDraft.trim() || active.current || candidate.current,
        true,
      );
    },
    [tokenDraft, verify, forgetting],
  );

  return {
    session,
    tokenDraft,
    setTokenDraft,
    activeToken,
    hasCredential: Boolean(activeToken || candidate.current),
    storageAvailable,
    rememberDevice,
    setRememberDevice,
    remembered: isDeviceHandle(activeToken || candidate.current),
    deviceExpiresAt,
    persistenceNotice,
    forgetting,
    connect,
    clear,
    cancel,
    onAuthFailure,
    onSystemUpdate,
  };
}
