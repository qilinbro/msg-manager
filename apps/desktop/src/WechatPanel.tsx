import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { ServiceError } from './api';
import {
  getWechatSessions,
  getWechatStatus,
  readWechatMessages,
} from './wechat-api';
import type {
  WechatReadResult,
  WechatSession,
  WechatStatus,
} from './wechat-api';

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
  onAuthFailure,
}: {
  token: string;
  storageEnabled: boolean;
  revision: number;
  onSynced: () => void;
  onAuthFailure: () => void;
}) {
  const [status, setStatus] = useState<WechatStatus | null>(null);
  const [statusLoading, setStatusLoading] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [statusRevision, setStatusRevision] = useState(0);
  const [sessions, setSessions] = useState<WechatSession[] | null>(null);
  const [peerID, setPeerID] = useState('');
  const [busy, setBusy] = useState<'sessions' | 'read' | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const statusRequest = useRef<AbortController | null>(null);
  const operation = useRef<AbortController | null>(null);
  const statusEpoch = useRef(0);
  const mounted = useRef(false);
  const sessionsAccount = useRef<string | null>(null);
  const lastResultDigest = useRef<string | null>(null);
  const onSyncedRef = useRef(onSynced);
  onSyncedRef.current = onSynced;

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
    sessionsAccount.current = null;
    lastResultDigest.current = null;
    return () => {
      mounted.current = false;
      statusRequest.current?.abort();
      operation.current?.abort();
      operation.current = null;
      statusEpoch.current++;
    };
  }, [token]);

  useEffect(() => {
    const controller = new AbortController();
    statusRequest.current = controller;
    const epoch = ++statusEpoch.current;
    setStatusLoading(true);
    getWechatStatus(token, controller.signal)
      .then((value) => {
        if (controller.signal.aborted || epoch !== statusEpoch.current) return;
        setStatus(value);
        setStatusError(null);
        if (value.state !== 'reading') setUncertain(false);
        if (value.account_id !== sessionsAccount.current || !value.configured) {
          setSessions(null);
          setPeerID('');
          sessionsAccount.current = value.account_id;
        }
        const digest = resultDigest(value.last_result);
        if (digest && digest !== lastResultDigest.current) {
          lastResultDigest.current = digest;
          onSyncedRef.current();
        }
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted || epoch !== statusEpoch.current) return;
        if (failure instanceof ServiceError && failure.status === 401) {
          onAuthFailure();
          return;
        }
        setStatusError(messageOf(failure));
      })
      .finally(() => {
        if (epoch === statusEpoch.current) {
          statusRequest.current = null;
          setStatusLoading(false);
        }
      });
    return () => controller.abort();
  }, [token, revision, statusRevision, onAuthFailure]);

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
            <p>选择一个私聊，将最近一批文本保存到本地消息中心。</p>
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
          {busy ? (
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
        </form>
      ) : sessions ? (
        <p className="email-panel-note" role="status">
          当前数据库没有可读取的私聊。首版不读取群聊或媒体消息。
        </p>
      ) : null}
      <p className="email-panel-note" id="wechat-read-help">
        按需读取微信 Windows 4.x 的本地私聊文本，不会持续监听。
        只包含本机已有、当前支持的记录，不代表完整聊天历史。
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
