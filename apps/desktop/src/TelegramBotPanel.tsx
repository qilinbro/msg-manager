import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { HelpDetails } from './HelpDetails';
import {
  getTelegramBotStatus,
  isTelegramBotAuthFailure,
  syncTelegramBot,
  telegramBotErrorMessage,
  telegramBotStateLabel,
  verifyTelegramBot,
  type TelegramBotStatus,
} from './telegram-bot-api';

interface Props {
  token: string;
  onAuthFailure: () => void;
}
type Action = 'status' | 'verify' | 'sync';

export function TelegramBotPanel({ token, onAuthFailure }: Props) {
  const id = useId();
  const [snapshot, setSnapshot] = useState<{
    token: string;
    value: TelegramBotStatus;
  } | null>(null);
  const [busy, setBusy] = useState<Action | null>(null);
  const [consent, setConsent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const pending = useRef<AbortController | null>(null);
  const receiveConsent = useRef(false);
  const status = snapshot?.token === token ? snapshot.value : null;

  const run = useCallback(
    async (action: Action) => {
      // The synchronous ref also blocks double clicks before React rerenders.
      if (!token || pending.current) return;
      receiveConsent.current = false;
      setConsent(false);
      const controller = new AbortController();
      pending.current = controller;
      const current = () =>
        pending.current === controller && !controller.signal.aborted;
      setBusy(action);
      setError(null);
      setNotice(null);
      try {
        if (action === 'sync') {
          const result = await syncTelegramBot(token, controller.signal, true);
          if (!current()) return;
          setSnapshot((previous) =>
            previous?.token === token
              ? { token, value: { ...previous.value, last_result: result } }
              : null,
          );
          setNotice(
            `本次手动收件新增 ${result.imported} 条、重复 ${result.duplicates} 条；未创建 AI 任务。`,
          );
        } else {
          const next = await (action === 'verify'
            ? verifyTelegramBot(token, controller.signal)
            : getTelegramBotStatus(token, controller.signal));
          if (!current()) return;
          setSnapshot({ token, value: next });
          if (action === 'verify')
            setNotice('Bot 身份已核验；只允许逐次手动收件，不代表持续在线。');
        }
      } catch (failure: unknown) {
        if (!current()) return;
        // A failed or uncertain operation must not leave a usable stale identity.
        setSnapshot(null);
        if (isTelegramBotAuthFailure(failure)) {
          onAuthFailure();
          return;
        }
        setError(telegramBotErrorMessage(failure));
      } finally {
        if (current()) {
          pending.current = null;
          setBusy(null);
        }
      }
    },
    [token, onAuthFailure],
  );

  useEffect(() => {
    pending.current?.abort();
    pending.current = null;
    receiveConsent.current = false;
    setSnapshot(null);
    setConsent(false);
    setError(null);
    setNotice(null);
    setBusy(null);
    if (token) void run('status');
    return () => {
      // Abort whichever request is active, not just the initial status read.
      pending.current?.abort();
      pending.current = null;
      receiveConsent.current = false;
    };
  }, [token, run]);

  const canVerify = Boolean(
    status?.configured &&
    status.runtime_configured &&
    status.allowed_private_chat_count > 0 &&
    !['closed', 'verifying', 'receiving'].includes(status.state),
  );
  const canReceive = Boolean(
    canVerify &&
    status?.identity_verified &&
    status.state === 'identity_verified',
  );
  const onSync = () => {
    if (!canReceive || !receiveConsent.current || pending.current) return;
    // Consume before the request, including failures and uncertain outcomes.
    void run('sync');
  };
  const result = status?.last_result;
  const stateLabel = !token
    ? '先连接本地服务，才能查看 Bot 状态。'
    : busy === 'status'
      ? '正在读取后台状态…'
      : busy === 'verify'
        ? '正在核验 Bot 身份…'
        : busy === 'sync'
          ? '正在处理本次手动收件…'
          : status
            ? telegramBotStateLabel(status.state)
            : '状态未确认，请刷新状态后继续。';

  return (
    <section className="settings-panel" aria-labelledby={`${id}-title`}>
      <div className="settings-section-header">
        <h2 id={`${id}-title`}>Telegram Bot</h2>
        <span className="subtle-note">仅手动收件</span>
      </div>
      <p className="input-help" id={`${id}-scope`}>
        仅接收后台授权名单内、Bot
        可见的私聊纯文本；不是个人账号接管，也不代表实时在线。
        后台接收、发送、历史导入与自动 AI 分析均关闭。
      </p>
      <div className="connection-form" aria-busy={busy !== null}>
        <p role="status" aria-live="polite" aria-atomic="true">
          {stateLabel}
        </p>
        {status ? (
          <p className="input-help">
            后台授权私聊：{status.allowed_private_chat_count}{' '}
            个；不展示身份或名单明细。
          </p>
        ) : null}
        <div className="form-actions">
          <button
            type="button"
            className="button button-secondary"
            disabled={!token || busy !== null}
            onClick={() => void run('status')}
          >
            {busy === 'status' ? '正在读取…' : '刷新状态'}
          </button>
          <button
            type="button"
            className="button button-secondary"
            disabled={!canVerify || busy !== null}
            onClick={() => {
              if (canVerify) void run('verify');
            }}
          >
            {busy === 'verify' ? '正在核验…' : '核验 Bot 身份'}
          </button>
        </div>
        <label className="remember-device-choice">
          <input
            type="checkbox"
            checked={consent && canReceive}
            disabled={!canReceive || busy !== null}
            aria-describedby={`${id}-scope ${id}-consent-help`}
            onChange={(event) => {
              if (!canReceive || pending.current) return;
              receiveConsent.current = event.target.checked;
              setConsent(event.target.checked);
            }}
          />
          <span>我同意本次手动接收授权私聊纯文本（仅此次有效）</span>
        </label>
        <p className="input-help" id={`${id}-consent-help`}>
          每次请求都会清除勾选。失败或超时后先刷新状态核对结果，不会自动重试。
        </p>
        <div className="form-actions">
          <button
            type="button"
            className="button button-secondary"
            disabled={!canReceive || !consent || busy !== null}
            onClick={onSync}
          >
            {busy === 'sync' ? '正在收件…' : '手动收件一次'}
          </button>
        </div>
        {error ? (
          <p className="form-message error-message" role="alert">
            {error}
          </p>
        ) : null}
        {notice ? (
          <p className="form-message success-message" role="status">
            {notice}
          </p>
        ) : null}
        {result ? (
          <HelpDetails label="最近一次收件统计">
            <p>
              检查 {result.events} 项，新增 {result.imported} 条，重复{' '}
              {result.duplicates} 条， 不支持 {result.unsupported}{' '}
              项，未授权或非私聊 {result.not_authorized} 项， 涉及{' '}
              {result.conversations} 个会话。AI 任务为 0。
            </p>
            {result.partial ? (
              <p>
                本批包含未接收项目，仅保存符合授权与类型要求的消息，不补齐历史。
              </p>
            ) : null}
          </HelpDetails>
        ) : null}
      </div>
      <HelpDetails label="后台配置与收件限制">
        <p>
          Bot Token、预期 Bot ID、授权私聊名单与 Python 适配器依赖仅由后台配置，
          工作台不提供这些凭据或名单的输入，也不返回其明细。
          缺少配置或依赖、身份不符、已有 Webhook 时不会收件，也不会自动移除
          Webhook。
        </p>
        <p>
          本功能不导入个人账号聊天历史，不启动后台轮询，不发送或回复消息，
          不会把接收内容自动加入 AI 分析队列。再次收件仍需重新勾选并点击。
        </p>
      </HelpDetails>
    </section>
  );
}
