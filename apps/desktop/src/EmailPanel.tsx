import { useEffect, useRef, useState } from 'react';
import { ServiceError } from './api';
import { getEmailStatus, syncEmail } from './email-api';
import type { EmailResult, EmailStatus } from './email-api';

const STATE_NAMES: Record<EmailStatus['state'], string> = {
  not_configured: '尚未配置',
  ready: '已配置，等待同步',
  syncing: '后台同步中',
  synced: '上次同步成功',
  auth_failed: '邮箱验证失败',
  unreachable: '邮箱暂不可达',
  protocol_error: '邮箱同步异常',
};
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
function messageOf(error: unknown) {
  return error instanceof ServiceError
    ? error.message
    : '暂时无法处理邮箱，请更新状态后重试。';
}

function resultDigest(syncedAt: string | null, result: EmailResult | null) {
  if (!syncedAt || !result) return null;
  return JSON.stringify([
    syncedAt,
    result.inserted,
    result.duplicates,
    result.skipped,
    result.limited,
  ]);
}

export default function EmailPanel({
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
  const [status, setStatus] = useState<EmailStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [syncError, setSyncError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const [statusRevision, setStatusRevision] = useState(0);
  const pendingSync = useRef<AbortController | null>(null);
  const pendingStatus = useRef<AbortController | null>(null);
  const readEpoch = useRef(0);
  const lastResultDigest = useRef<string | null>(null);
  const mounted = useRef(true);
  const onSyncedRef = useRef(onSynced);
  onSyncedRef.current = onSynced;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      pendingSync.current?.abort();
      pendingStatus.current?.abort();
      readEpoch.current++;
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    pendingStatus.current = controller;
    const epoch = ++readEpoch.current;
    setLoading(true);
    getEmailStatus(token, controller.signal)
      .then((value) => {
        if (controller.signal.aborted || epoch !== readEpoch.current) return;
        setStatus(value);
        setStatusError(null);
        if (value.state !== 'syncing') setUncertain(false);
        const digest = resultDigest(value.last_synced_at, value.last_result);
        if (digest && digest !== lastResultDigest.current) {
          lastResultDigest.current = digest;
          onSyncedRef.current();
        }
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted || epoch !== readEpoch.current) return;
        if (failure instanceof ServiceError && failure.status === 401) {
          onAuthFailure();
          return;
        }
        setStatusError(messageOf(failure));
      })
      .finally(() => {
        if (epoch === readEpoch.current) {
          pendingStatus.current = null;
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [token, revision, statusRevision, onAuthFailure]);
  const invalidateStatusRead = () => {
    pendingStatus.current?.abort();
    pendingStatus.current = null;
    readEpoch.current++;
    setLoading(false);
  };
  const refreshStatus = () => {
    setStatusError(null);
    setStatusRevision((value) => value + 1);
  };
  const startSync = async () => {
    if (
      !storageEnabled ||
      !status?.configured ||
      syncing ||
      pendingSync.current ||
      status.state === 'syncing' ||
      uncertain
    )
      return;
    const controller = new AbortController();
    invalidateStatusRead();
    pendingSync.current = controller;
    setSyncing(true);
    setSyncError(null);
    setNotice(null);
    try {
      const result = await syncEmail(token, controller.signal);
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatusRead();
      lastResultDigest.current = resultDigest(result.synced_at, result);
      setStatus((previous) =>
        previous
          ? {
              ...previous,
              state: 'synced',
              last_synced_at: result.synced_at,
              last_result: result,
            }
          : previous,
      );
      setNotice(
        `本次保存 ${result.inserted} 封，重复 ${result.duplicates} 封，跳过 ${result.skipped} 封。${result.limited ? '本次仅处理最近一批邮件，并未完整收取历史。' : ''}`,
      );
      onSyncedRef.current();
    } catch (failure: unknown) {
      if (controller.signal.aborted || !mounted.current) return;
      invalidateStatusRead();
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setSyncError(messageOf(failure));
      if (
        failure instanceof ServiceError &&
        ['TIMEOUT', 'NETWORK_ERROR', 'INVALID_RESPONSE', 'EMAIL_BUSY'].includes(
          failure.code,
        )
      )
        setUncertain(true);
      // Read only the backend's local status; this never starts another IMAP request.
      setStatusRevision((value) => value + 1);
      onSyncedRef.current();
    } finally {
      if (pendingSync.current === controller) {
        pendingSync.current = null;
        if (mounted.current) setSyncing(false);
      }
    }
  };
  const stopWaiting = () => {
    invalidateStatusRead();
    pendingSync.current?.abort();
    pendingSync.current = null;
    setSyncing(false);
    setUncertain(true);
    setNotice(
      '已停止等待，后台可能仍在处理邮件。请先更新状态并查看消息，再决定是否重试。',
    );
    onSyncedRef.current();
  };
  const backendBusy = status?.state === 'syncing';
  return (
    <section
      className="email-panel"
      aria-labelledby="email-panel-title"
      aria-busy={syncing}
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
            <rect x="3" y="5" width="18" height="14" rx="2" />
            <path d="m3 7 9 6 9-6" />
          </svg>
          <div>
            <h2 id="email-panel-title">邮箱，也整理在这里</h2>
            <p>按需收取最近邮件到本地，保留原邮箱状态。暂不支持发信。</p>
          </div>
        </div>
        <div className="email-panel-actions">
          <button
            className="button button-secondary"
            onClick={startSync}
            disabled={
              loading ||
              !storageEnabled ||
              !status?.configured ||
              syncing ||
              backendBusy ||
              uncertain
            }
          >
            {syncing || backendBusy ? '正在同步…' : '同步邮箱'}
          </button>
          {syncing ? (
            <button className="email-text-button" onClick={stopWaiting}>
              停止等待
            </button>
          ) : null}
          <button
            className="email-text-button"
            onClick={refreshStatus}
            disabled={loading}
          >
            {loading ? '读取状态…' : '更新状态'}
          </button>
        </div>
      </div>
      <div className="email-panel-state">
        <span
          className={`email-state-label ${status?.state === 'synced' ? 'is-synced' : ''}`}
        >
          {status
            ? STATE_NAMES[status.state]
            : loading
              ? '正在读取邮箱状态'
              : '邮箱状态未读取'}
        </span>
        {status?.account ? (
          <span className="email-account">{status.account}</span>
        ) : null}
        {status?.folder ? <span>文件夹：{status.folder}</span> : null}
        {status?.last_synced_at ? (
          <span>
            最近成功同步{' '}
            <time dateTime={status.last_synced_at}>
              {dateText(status.last_synced_at)}
            </time>
          </span>
        ) : null}
      </div>
      {!storageEnabled ? (
        <p className="email-panel-note">
          先启用本地加密存储，再同步邮箱中的邮件。
        </p>
      ) : status && !status.configured ? (
        <p className="email-panel-note">
          请按项目的邮箱接入说明，在本机配置账号与授权码后重启服务。工作台不会收集你的邮箱密码。
        </p>
      ) : (
        <p className="email-panel-note">
          同步是一次主动读取，不会持续自动抓取。已读、置顶和归档仅在本地工作台生效。
        </p>
      )}
      {status?.last_result ? (
        <p className="email-last-result">
          上次保存 {status.last_result.inserted} 封 · 重复{' '}
          {status.last_result.duplicates} 封 · 跳过 {status.last_result.skipped}{' '}
          封{status.last_result.limited ? ' · 最近一批，未包含全部历史' : ''}
        </p>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {notice ? (
          <p className="email-feedback" role="status">
            {notice}
          </p>
        ) : null}
        {statusError ? (
          <p className="email-feedback is-error" role="alert">
            {statusError}
          </p>
        ) : null}
        {syncError ? (
          <p className="email-feedback is-error" role="alert">
            {syncError}
          </p>
        ) : null}
      </div>
    </section>
  );
}
