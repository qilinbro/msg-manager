import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { ServiceError } from './api';
import {
  getWechatSessions,
  getWechatStatus,
  readWechatMessages,
  saveWechatMonitor,
  wechatMonitorError,
} from './wechat-api';
import type {
  WechatReadResult,
  WechatSession,
  WechatStatus,
} from './wechat-api';

const MONITOR_NAMES = {
  disabled: '后台读取已暂停',
  waiting: '后台定时读取中',
  reading: '后台正在保存消息',
  error: '本轮读取未完成',
  not_configured: '等待本机配置',
};

const STATE_NAMES: Record<WechatStatus['state'], string> = {
  not_configured: '尚未配置',
  ready: '已配置，等待读取',
  reading: '后台读取中',
  read: '上次读取成功',
  invalid_key: '密钥或加密格式不匹配',
  unavailable: '数据库暂不可读',
  unsupported_schema: '数据库结构尚未支持',
  changed: '数据库发生变化',
  not_found: '所选私聊未找到',
};

const uncertainFailure = (error: unknown) =>
  error instanceof ServiceError &&
  ['TIMEOUT', 'NETWORK_ERROR', 'INVALID_RESPONSE', 'WECHAT_BUSY'].includes(
    error.code,
  );
const messageOf = (error: unknown) =>
  error instanceof ServiceError
    ? error.message
    : '暂时无法读取微信数据，请更新状态后再试。';

function resultDigest(result: WechatReadResult | null) {
  return result ? JSON.stringify(result) : null;
}

function dateText(value: string) {
  return new Date(value).toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

export default function WechatPanel({
  token,
  storageEnabled,
  revision,
  onSynced,
  onBackgroundSynced,
  onAuthFailure,
}: {
  token: string;
  storageEnabled: boolean;
  revision: number;
  onSynced: () => void;
  onBackgroundSynced: () => void;
  onAuthFailure: () => void;
}) {
  const [status, setStatus] = useState<WechatStatus | null>(null);
  const [statusLoading, setStatusLoading] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [statusRevision, setStatusRevision] = useState(0);
  const [sessions, setSessions] = useState<WechatSession[] | null>(null);
  const [peerID, setPeerID] = useState('');
  const [busy, setBusy] = useState<'sessions' | 'read' | 'monitor' | null>(
    null,
  );
  const [monitorPeers, setMonitorPeers] = useState<string[]>([]);
  const [monitorInterval, setMonitorInterval] = useState('30');
  const [monitorDirty, setMonitorDirty] = useState(false);
  const monitorDirtyRef = useRef(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const statusRequest = useRef<AbortController | null>(null);
  const operation = useRef<AbortController | null>(null);
  const statusEpoch = useRef(0);
  const mounted = useRef(false);
  const sessionsAccount = useRef<string | null>(null);
  const lastResultDigest = useRef<string | null>(null);
  const lastMonitorDigest = useRef<string | null>(null);
  const onSyncedRef = useRef(onSynced);
  onSyncedRef.current = onSynced;
  const onBackgroundSyncedRef = useRef(onBackgroundSynced);
  onBackgroundSyncedRef.current = onBackgroundSynced;

  useEffect(() => {
    mounted.current = true;
    setStatus(null);
    setStatusError(null);
    setSessions(null);
    setPeerID('');
    setBusy(null);
    setUncertain(false);
    setError(null);
    setNotice(null);
    setMonitorPeers([]);
    setMonitorInterval('30');
    setMonitorDirty(false);
    monitorDirtyRef.current = false;
    sessionsAccount.current = null;
    lastResultDigest.current = null;
    lastMonitorDigest.current = null;
    return () => {
      mounted.current = false;
      statusRequest.current?.abort();
      operation.current?.abort();
      operation.current = null;
      statusEpoch.current++;
    };
  }, [token]);

  useEffect(() => {
    let active = true;
    const refreshStatus = async (quiet: boolean) => {
      if (!active || operation.current || statusRequest.current) return;
      const controller = new AbortController();
      statusRequest.current = controller;
      const epoch = ++statusEpoch.current;
      if (!quiet) setStatusLoading(true);
      try {
        const value = await getWechatStatus(token, controller.signal);
        if (controller.signal.aborted || epoch !== statusEpoch.current) return;
        setStatus(value);
        setStatusError(null);
        if (value.state !== 'reading') setUncertain(false);
        const changedAccount = value.account_id !== sessionsAccount.current;
        if (changedAccount || !value.configured) {
          setSessions(null);
          setPeerID('');
          sessionsAccount.current = value.account_id;
        }
        if (changedAccount || !monitorDirtyRef.current) {
          setMonitorPeers(value.monitor?.peer_ids ?? []);
          setMonitorInterval(String(value.monitor?.interval_seconds ?? 30));
          setMonitorDirty(false);
          monitorDirtyRef.current = false;
        }
        const digest = resultDigest(value.last_result);
        const monitorDigest = resultDigest(value.monitor?.last_result ?? null);
        const changedMessages =
          (digest !== lastResultDigest.current &&
            (value.last_result?.inserted ?? 0) > 0) ||
          (monitorDigest !== lastMonitorDigest.current &&
            (value.monitor?.last_result?.inserted ?? 0) > 0);
        lastResultDigest.current = digest;
        lastMonitorDigest.current = monitorDigest;
        if (changedMessages) onBackgroundSyncedRef.current();
      } catch (failure: unknown) {
        if (controller.signal.aborted || epoch !== statusEpoch.current) return;
        if (failure instanceof ServiceError && failure.status === 401) {
          onAuthFailure();
          return;
        }
        setStatusError(messageOf(failure));
      } finally {
        if (epoch === statusEpoch.current) {
          statusRequest.current = null;
          setStatusLoading(false);
        }
      }
    };
    void refreshStatus(false);
    // Only cached metadata is polled. Reads are owned by the backend worker.
    const timer = window.setInterval(() => void refreshStatus(true), 5_000);
    const resume = () => {
      if (document.visibilityState === 'hidden') return;
      void refreshStatus(true);
      // Inactive tabs may miss a cycle with inserts. Reconcile product storage
      // once on return even if the latest cycle only reports duplicates.
      if (storageEnabled) onBackgroundSyncedRef.current();
    };
    window.addEventListener('focus', resume);
    document.addEventListener('visibilitychange', resume);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener('focus', resume);
      document.removeEventListener('visibilitychange', resume);
      statusRequest.current?.abort();
      statusRequest.current = null;
      statusEpoch.current++;
    };
  }, [token, revision, statusRevision, storageEnabled, onAuthFailure]);

  const invalidateStatus = () => {
    statusRequest.current?.abort();
    statusRequest.current = null;
    statusEpoch.current++;
    setStatusLoading(false);
  };
  const clearSessions = () => {
    setSessions(null);
    setPeerID('');
  };
  const beginOperation = (kind: 'sessions' | 'read') => {
    if (
      operation.current ||
      !status?.configured ||
      status.state === 'reading' ||
      uncertain
    )
      return null;
    invalidateStatus();
    const controller = new AbortController();
    operation.current = controller;
    setBusy(kind);
    setError(null);
    setNotice(null);
    return controller;
  };
  const handleFailure = (failure: unknown) => {
    if (failure instanceof ServiceError && failure.status === 401) {
      onAuthFailure();
      return;
    }
    setError(messageOf(failure));
    if (uncertainFailure(failure)) setUncertain(true);
    clearSessions();
    // Refresh only cached metadata after failure, without starting another read.
    setStatusRevision((value) => value + 1);
  };
  const finishOperation = (controller: AbortController) => {
    if (operation.current === controller) {
      operation.current = null;
      if (mounted.current) setBusy(null);
    }
  };

  const editMonitorPeers = (peers: string[]) => {
    setMonitorPeers(peers);
    setMonitorDirty(true);
    monitorDirtyRef.current = true;
  };

  const changeMonitor = async (enabled: boolean) => {
    const monitor = status?.monitor;
    if (!monitor || operation.current) return;
    invalidateStatus();
    const controller = new AbortController();
    operation.current = controller;
    setBusy('monitor');
    setError(null);
    setNotice(null);
    try {
      const value = await saveWechatMonitor(token, controller.signal, {
        enabled,
        peer_ids: enabled ? monitorPeers : monitor.peer_ids,
        interval_seconds: enabled
          ? Number(monitorInterval)
          : monitor.interval_seconds,
      });
      if (controller.signal.aborted || !mounted.current) return;
      setStatus((previous) =>
        previous ? { ...previous, monitor: value } : previous,
      );
      setMonitorPeers(value.peer_ids);
      setMonitorInterval(String(value.interval_seconds));
      setMonitorDirty(false);
      monitorDirtyRef.current = false;
      setUncertain(false);
      setNotice(
        enabled
          ? `已开启 ${value.peer_ids.length} 个私聊的后台读取。关闭页面后，后台服务仍按设置继续读取。`
          : '后台读取已暂停，已保存的消息会继续保留。',
      );
    } catch (failure: unknown) {
      if (controller.signal.aborted || !mounted.current) return;
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(messageOf(failure));
      if (uncertainFailure(failure)) setUncertain(true);
    } finally {
      finishOperation(controller);
      if (mounted.current) setStatusRevision((value) => value + 1);
    }
  };

  const loadSessions = async () => {
    const controller = beginOperation('sessions');
    if (!controller) return;
    clearSessions();
    try {
      const items = await getWechatSessions(token, controller.signal);
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatus();
      setSessions(items);
      sessionsAccount.current = status?.account_id ?? null;
      setStatus((previous) =>
        previous ? { ...previous, state: 'ready' } : previous,
      );
    } catch (failure: unknown) {
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatus();
      handleFailure(failure);
    } finally {
      finishOperation(controller);
    }
  };

  const readMessages = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!storageEnabled || !sessions?.some((item) => item.peer_id === peerID))
      return;
    const controller = beginOperation('read');
    if (!controller) return;
    try {
      const result = await readWechatMessages(token, controller.signal, peerID);
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatus();
      lastResultDigest.current = resultDigest(result);
      setStatus((previous) =>
        previous
          ? {
              ...previous,
              state: 'read',
              last_read_at: result.read_at,
              last_result: result,
            }
          : previous,
      );
      setNotice(
        `本次保存 ${result.inserted} 条，重复 ${result.duplicates} 条，跳过 ${result.skipped} 条。已保存的消息可在下方微信筛选中查看。`,
      );
      onSyncedRef.current();
    } catch (failure: unknown) {
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatus();
      handleFailure(failure);
      onSyncedRef.current();
    } finally {
      finishOperation(controller);
    }
  };

  const stopWaiting = () => {
    invalidateStatus();
    operation.current?.abort();
    operation.current = null;
    setBusy(null);
    setUncertain(true);
    setError(null);
    setNotice(
      '已停止等待，后台可能仍在读取。请先更新状态并查看消息，再决定是否重试。',
    );
    clearSessions();
    onSyncedRef.current();
  };
  const blocked =
    Boolean(busy) ||
    Boolean(statusError) ||
    statusLoading ||
    !status?.configured ||
    status.state === 'reading' ||
    uncertain;
  const visibleResult =
    status?.state === 'read' && !busy && !uncertain && !error && !statusError
      ? status.last_result
      : null;
  const monitor = status?.monitor;
  const validInterval =
    Number.isInteger(Number(monitorInterval)) &&
    Number(monitorInterval) >= 10 &&
    Number(monitorInterval) <= 300;

  return (
    <section
      className="email-panel wechat-panel"
      aria-labelledby="wechat-panel-title"
      aria-busy={Boolean(busy)}
    >
      <div className="email-panel-main">
        <div className="email-panel-heading">
          <svg
            width="23"
            height="23"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            aria-hidden="true"
          >
            <path d="M21 11.5a8.5 8.5 0 0 1-8.5 8.5 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7A8.4 8.4 0 0 1 4 11.5 8.5 8.5 0 0 1 12.5 3a8.5 8.5 0 0 1 8.5 8.5Z" />
            <path d="M8 10h9M8 14h6" />
          </svg>
          <div>
            <h2 id="wechat-panel-title">读取本机微信消息</h2>
            <p>按需保存私聊，也可以交给后台定时读取。</p>
          </div>
        </div>
        <div className="email-panel-actions">
          <button
            type="button"
            className="button button-secondary"
            onClick={loadSessions}
            disabled={blocked}
          >
            {busy === 'sessions' ? '正在读取会话…' : '读取会话列表'}
          </button>
          {busy && busy !== 'monitor' ? (
            <button
              type="button"
              className="email-text-button"
              onClick={stopWaiting}
            >
              停止等待
            </button>
          ) : null}
          <button
            type="button"
            className="email-text-button"
            onClick={() => {
              setStatusError(null);
              setStatusRevision((value) => value + 1);
            }}
            disabled={statusLoading || Boolean(busy)}
          >
            {statusLoading ? '读取状态…' : '更新状态'}
          </button>
        </div>
      </div>
      <div className="email-panel-state">
        <span
          className={`email-state-label ${status?.state === 'read' && !error && !statusError ? 'is-synced' : ''}`}
        >
          {busy === 'sessions'
            ? '正在读取会话'
            : busy === 'read'
              ? '正在读取消息'
              : busy === 'monitor'
                ? '正在保存后台读取设置'
                : statusError
                  ? '微信状态未确认'
                  : error
                    ? '此次读取未完成'
                    : status
                      ? STATE_NAMES[status.state]
                      : statusLoading
                        ? '正在读取微信状态'
                        : '微信状态未读取'}
        </span>
        {status?.account_id ? (
          <span className="email-account">本地账号：{status.account_id}</span>
        ) : null}
        <span>只读 · 发送关闭</span>
      </div>
      {status && !status.configured ? (
        <p className="email-panel-note">
          按微信接入说明，在本机配置数据库路径、账号标识与数据库密钥后重启服务。此处不填写微信密码或密钥。
        </p>
      ) : null}
      {!storageEnabled ? (
        <p className="email-panel-note">
          本地加密存储尚未启用，可以先读取会话列表，启用存储后再保存消息。
        </p>
      ) : null}
      {sessions?.length ? (
        <form className="wechat-read-form" onSubmit={readMessages}>
          <div className="wechat-chat-field">
            <label htmlFor="wechat-peer">选择私聊</label>
            <select
              id="wechat-peer"
              value={peerID}
              onChange={(event) => {
                setPeerID(event.target.value);
                setNotice(null);
                setError(null);
              }}
              disabled={blocked}
              aria-describedby="wechat-read-help"
            >
              <option value="">请选择一个私聊</option>
              {sessions.map((item) => (
                <option key={item.peer_id} value={item.peer_id}>
                  {item.name} · {item.peer_id}
                </option>
              ))}
            </select>
          </div>
          <button
            className="button button-secondary"
            type="submit"
            disabled={blocked || !storageEnabled || !peerID}
          >
            {busy === 'read' ? '正在读取并保存…' : '读取并保存消息'}
          </button>
          <button
            className="email-text-button"
            type="button"
            disabled={
              !monitor ||
              Boolean(busy) ||
              !peerID ||
              monitorPeers.includes(peerID) ||
              monitorPeers.length >= 20
            }
            onClick={() => editMonitorPeers([...monitorPeers, peerID])}
          >
            加入后台读取
          </button>
        </form>
      ) : sessions ? (
        <p className="email-panel-note" role="status">
          当前数据库没有可读取的私聊。首版不读取群聊或媒体消息。
        </p>
      ) : null}
      <section
        className="wechat-monitor"
        aria-labelledby="wechat-monitor-title"
      >
        <div className="wechat-monitor-heading">
          <h3 id="wechat-monitor-title">后台自动读取</h3>
          <span
            className={`email-state-label ${monitor?.enabled ? 'is-synced' : ''}`}
          >
            {monitor ? MONITOR_NAMES[monitor.state] : '后台需要更新'}
          </span>
        </div>
        {!monitor ? (
          <p className="email-panel-note">
            {statusLoading && !status
              ? '正在确认后台能力…'
              : '更新后重启后台，再开启自动读取。已有的手动读取可继续使用。'}
          </p>
        ) : (
          <>
            <p className="email-panel-note">
              {monitor.enabled
                ? `当前每 ${monitor.interval_seconds} 秒读取 ${monitor.peer_ids.length} 个私聊。`
                : '先读取会话列表，选择私聊并点击“加入后台读取”，再开启。最多选择20个私聊。'}
              关闭页面后继续运行；电脑需保持开机，后台服务需保持运行。
            </p>
            {monitorPeers.length ? (
              <ul
                className="wechat-monitor-peers"
                aria-label="已选择的后台读取私聊"
              >
                {monitorPeers.map((id) => (
                  <li key={id}>
                    <span>
                      {sessions?.find((item) => item.peer_id === id)?.name ??
                        id}
                    </span>
                    <button
                      type="button"
                      className="email-text-button"
                      aria-label={`移除私聊 ${id}`}
                      disabled={Boolean(busy)}
                      onClick={() =>
                        editMonitorPeers(
                          monitorPeers.filter((item) => item !== id),
                        )
                      }
                    >
                      移除
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
            <div className="wechat-monitor-controls">
              <label htmlFor="wechat-monitor-interval">
                读取间隔（秒）
                <input
                  id="wechat-monitor-interval"
                  type="number"
                  min="10"
                  max="300"
                  step="1"
                  value={monitorInterval}
                  disabled={Boolean(busy)}
                  aria-describedby="wechat-monitor-help"
                  onChange={(event) => {
                    setMonitorInterval(event.target.value);
                    setMonitorDirty(true);
                    monitorDirtyRef.current = true;
                  }}
                />
              </label>
              <button
                type="button"
                className="button button-primary"
                disabled={
                  Boolean(busy) ||
                  !storageEnabled ||
                  !status?.configured ||
                  Boolean(statusError) ||
                  uncertain ||
                  !monitorPeers.length ||
                  !validInterval ||
                  (monitor.enabled && !monitorDirty)
                }
                onClick={() => void changeMonitor(true)}
              >
                {busy === 'monitor'
                  ? '正在保存…'
                  : monitor.enabled
                    ? '保存并继续读取'
                    : '开启后台读取'}
              </button>
              <button
                type="button"
                className="button button-secondary"
                disabled={Boolean(busy) || !monitor.enabled}
                onClick={() => void changeMonitor(false)}
              >
                暂停后台读取
              </button>
            </div>
            <p className="email-panel-note" id="wechat-monitor-help">
              间隔10至300秒。
              {monitorDirty
                ? '选择或频率已修改，保存后才会生效。'
                : '设置保存在本机，重启后台后恢复已开启的读取。'}
            </p>
            {monitor.last_cycle_at ? (
              <p className="email-last-result">
                上次后台检查{' '}
                <time dateTime={monitor.last_cycle_at}>
                  {dateText(monitor.last_cycle_at)}
                </time>
                {monitor.last_result
                  ? ` · 新增 ${monitor.last_result.inserted} 条 · 重复 ${monitor.last_result.duplicates} 条`
                  : ''}
                {monitor.last_result?.limited
                  ? ' · 已达到读取窗口上限，可能存在未读取记录'
                  : ''}
              </p>
            ) : null}
            {monitor.last_error_code ? (
              <p className="email-feedback is-error" role="status">
                {wechatMonitorError(monitor.last_error_code)}
              </p>
            ) : null}
          </>
        )}
      </section>
      <p className="email-panel-note" id="wechat-read-help">
        读取微信 Windows 4.x 本机已有的私聊文本，不发送消息、不调用AI。
        每次读取最近一批记录；暂停、离线或消息量超过读取窗口时，可能无法补齐全部历史。
      </p>
      {visibleResult ? (
        <p className="email-last-result">
          最近一次成功读取{' '}
          <time dateTime={visibleResult.read_at}>
            {dateText(visibleResult.read_at)}
          </time>
          {' · '}保存 {visibleResult.inserted} 条 · 重复{' '}
          {visibleResult.duplicates} 条 · 跳过 {visibleResult.skipped} 条
          {visibleResult.limited ? ' · 本次已达到读取上限' : ''}
        </p>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {notice ? (
          <p className="email-feedback" role="status">
            {notice}
          </p>
        ) : null}
        {uncertain ? (
          <p className="email-panel-note" role="status">
            本次处理结果尚未确认，请先更新状态。工作台不会自动重试。
          </p>
        ) : null}
        {statusError || error ? (
          <p className="email-feedback is-error" role="alert">
            {statusError ?? error}
          </p>
        ) : null}
      </div>
    </section>
  );
}
