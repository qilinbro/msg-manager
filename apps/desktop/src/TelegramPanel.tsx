import { useEffect, useRef, useState } from 'react';
import { getSystem, ServiceError, type SystemStatus } from './api';
import {
  getTelegramDialogs,
  getTelegramStatus,
  syncTelegram,
  verifyTelegram,
  type TelegramDialogs,
  type TelegramStatus,
} from './telegram-api';
import './telegram.css';

interface Props {
  token: string;
  onAuthFailure: () => void;
  onSystemUpdate: (system: SystemStatus) => void;
}

const STATE_NAMES: Record<string, string> = {
  not_configured: '待本机初始化',
  helper_unavailable: '待准备运行组件',
  requires_initialization: '需重新完成本机初始化',
  configured: '已初始化，待核验',
  ready: '已初始化，待核验',
  verified: '个人账号已核验',
  identity_verified: '个人账号已核验',
  auth_required: '需重新登录',
  logged_out: '登录已失效，需重新初始化',
  auth_failed: '登录核验失败，需重新初始化',
  config_invalid: '本机配置无法读取',
  unsupported_platform: '需要 Windows 本机运行环境',
  network_error: '暂时无法连接 Telegram',
  timeout: '读取超时，请手动核对状态',
  identity_mismatch: '账号不一致，读取已停止',
  flood_wait: '请求受限，请稍后手动重试',
  peer_not_found: '所选私聊已不在最近范围',
  protocol_error: '组件返回格式不匹配',
  unavailable: '读取未完成，请检查本机状态',
  unreachable: '暂时无法连接 Telegram',
  busy: '正在处理',
};

export function TelegramPanel({ token, onAuthFailure, onSystemUpdate }: Props) {
  const [status, setStatus] = useState<TelegramStatus | null>(null);
  const [dialogs, setDialogs] = useState<TelegramDialogs | null>(null);
  const [peerID, setPeerID] = useState('');
  const [limit, setLimit] = useState('50');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const pending = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  const gate = useRef(false);
  const callbacks = useRef({ onAuthFailure, onSystemUpdate });
  callbacks.current = { onAuthFailure, onSystemUpdate };

  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    pending.current = controller;
    gate.current = !!token;
    setStatus(null);
    setDialogs(null);
    setPeerID('');
    setError(null);
    setNotice(null);
    setBusy(token ? 'status' : null);
    if (token)
      void getTelegramStatus(token, controller.signal)
        .then((next) => {
          if (!controller.signal.aborted && pending.current === controller)
            setStatus(next);
        })
        .catch((failure: unknown) => {
          if (controller.signal.aborted || pending.current !== controller)
            return;
          if (
            failure instanceof ServiceError &&
            failure.code === 'UNAUTHORIZED'
          )
            callbacks.current.onAuthFailure();
          else
            setError(
              failure instanceof ServiceError
                ? failure.message
                : '无法读取 Telegram 状态，请重试。',
            );
        })
        .finally(() => {
          if (controller.signal.aborted || pending.current !== controller)
            return;
          gate.current = false;
          setBusy(null);
        });
    return () => {
      mounted.current = false;
      controller.abort();
      pending.current?.abort();
    };
  }, [token]);

  const run = async (
    name: string,
    work: (
      controller: AbortController,
      current: () => boolean,
    ) => Promise<void>,
  ) => {
    if (!token || gate.current) return;
    const controller = new AbortController();
    pending.current?.abort();
    pending.current = controller;
    gate.current = true;
    setBusy(name);
    setError(null);
    setNotice(null);
    const current = () =>
      mounted.current &&
      !controller.signal.aborted &&
      pending.current === controller;
    try {
      await work(controller, current);
    } catch (failure: unknown) {
      if (!current()) return;
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
        callbacks.current.onAuthFailure();
      else {
        if (
          failure instanceof ServiceError &&
          [
            'TELEGRAM_IDENTITY_MISMATCH',
            'TELEGRAM_AUTH_REQUIRED',
            'TELEGRAM_NOT_VERIFIED',
            'TELEGRAM_NOT_CONFIGURED',
            'TELEGRAM_INVALID_CONFIG',
            'TELEGRAM_UNREACHABLE',
            'TELEGRAM_PEER_NOT_SELECTED',
            'TELEGRAM_REQUIRES_INITIALIZATION',
            'TELEGRAM_CONFIG_INVALID',
            'TELEGRAM_LOGGED_OUT',
            'TELEGRAM_AUTH_FAILED',
            'TELEGRAM_UNSUPPORTED_PLATFORM',
            'TELEGRAM_NETWORK_ERROR',
            'TELEGRAM_TIMEOUT',
            'TELEGRAM_PROTOCOL_ERROR',
            'TELEGRAM_PEER_NOT_FOUND',
            'TELEGRAM_FLOOD_WAIT',
            'TELEGRAM_UNAVAILABLE',
          ].includes(failure.code)
        ) {
          setDialogs(null);
          setPeerID('');
          setStatus(null);
        }
        setError(
          failure instanceof ServiceError
            ? failure.message
            : '操作未完成，请刷新状态后重试。',
        );
      }
    } finally {
      if (current()) {
        gate.current = false;
        setBusy(null);
      }
    }
  };

  const refresh = () =>
    void run('status', async (controller, current) => {
      const next = await getTelegramStatus(token, controller.signal);
      if (!current()) return;
      setStatus(next);
      if (
        !next.configured ||
        next.state !== 'identity_verified' ||
        next.account_id !== status?.account_id
      ) {
        setDialogs(null);
        setPeerID('');
      }
    });
  const verify = () =>
    void run('verify', async (controller, current) => {
      const next = await verifyTelegram(token, controller.signal);
      if (!current()) return;
      setStatus(next);
      setDialogs(null);
      setPeerID('');
      setNotice(
        `已核验本人账号：${next.name || next.account_id}。尚未读取私聊或开启后台接收。`,
      );
      const system = await getSystem(token, controller.signal);
      if (current()) callbacks.current.onSystemUpdate(system);
    });
  const readDialogs = () =>
    void run('dialogs', async (controller, current) => {
      const next = await getTelegramDialogs(token, controller.signal);
      if (!current()) return;
      if (!status?.account_id || next.account_id !== status.account_id)
        throw new ServiceError(
          '账号状态已变化，请重新核验后读取会话。',
          'TELEGRAM_IDENTITY_MISMATCH',
        );
      setDialogs(next);
      setPeerID('');
      setNotice(
        next.limited
          ? '已读取最近范围内的个人私聊，列表可能不完整。请选择一名联系人。'
          : `已读取 ${next.items.length} 个个人私聊。请选择一名联系人。`,
      );
    });
  const sync = () =>
    void run('sync', async (controller, current) => {
      if (
        !dialogs ||
        status?.state !== 'identity_verified' ||
        dialogs.account_id !== status.account_id
      )
        throw new ServiceError(
          '账号状态已变化，请重新核验后读取会话。',
          'TELEGRAM_IDENTITY_MISMATCH',
        );
      if (!dialogs?.items.some((item) => item.peer_id === peerID)) return;
      const result = await syncTelegram(
        token,
        controller.signal,
        peerID,
        dialogs.account_id,
        Number(limit),
      );
      if (!current()) return;
      setNotice(
        `同步完成：新增 ${result.inserted} 条，重复 ${result.duplicates} 条，跳过 ${result.skipped} 条。${result.limited ? '仅处理本次最近记录窗口。' : ''}可到消息页查看。`,
      );
      const next = await getTelegramStatus(token, controller.signal);
      if (current()) {
        setStatus(next);
        if (
          next.state !== 'identity_verified' ||
          next.account_id !== dialogs?.account_id
        ) {
          setDialogs(null);
          setPeerID('');
        }
      }
    });
  const verified = status?.state === 'identity_verified';
  const disabled = !token || busy !== null;
  const selected = dialogs?.items.find((item) => item.peer_id === peerID);

  return (
    <section
      className="settings-panel telegram-settings"
      aria-labelledby="telegram-title"
      aria-busy={busy !== null}
    >
      <div className="section-heading">
        <h2 id="telegram-title">Telegram 个人账号</h2>
        <span className="label">只读接入</span>
      </div>
      <p>
        使用你自己的 Telegram
        账号读取明确选择的私聊。首次登录在本机终端完成，页面不收集手机号、验证码、两步验证密码或
        API Hash。
      </p>
      <ol className="telegram-setup-steps">
        <li>
          <h3>1. 申请自己的应用信息</h3>
          <p>
            登录{' '}
            <a href="https://my.telegram.org" target="_blank" rel="noreferrer">
              Telegram 开发者页面 ↗
            </a>
            ，进入 API development tools，创建应用并保管 API ID / API Hash。
          </p>
        </li>
        <li>
          <h3>2. 在本机完成一次初始化</h3>
          <p>
            在项目目录的 PowerShell
            中依次运行。按提示填写应用信息并登录本人账号；需要代理时，填写本机
            SOCKS5 端口。
          </p>
          <pre className="telegram-commands">
            <code>
              {
                '.\\scripts\\prepare-telegram.ps1\n.\\scripts\\initialize-telegram.ps1'
              }
            </code>
          </pre>
          <p className="input-help">
            登录会话由当前 Windows
            用户加密保存。不要把应用密钥、验证码、密码或会话文件发到聊天里。重新初始化后刷新本页状态即可。
          </p>
        </li>
        <li>
          <h3>3. 核验并选择私聊</h3>
          <p>
            先核验账号，再读取会话列表，选择联系人同步最近文本。首版没有自动收件、发送、群聊或完整历史补齐。
          </p>
        </li>
      </ol>
      {!token ? <p className="input-help">请先连接上方的本地服务。</p> : null}
      {status && !status.helper_available ? (
        <p className="input-help">
          运行组件尚未准备好，请先运行第 2 步的准备命令。
        </p>
      ) : null}
      {status?.helper_available && !status.configured ? (
        <p className="input-help">运行组件已就绪，尚未完成个人账号初始化。</p>
      ) : null}
      {status?.configured && status.requires_initialization ? (
        <p className="input-help">
          请在本机终端重新运行初始化命令，再刷新状态。
        </p>
      ) : null}
      <dl className="telegram-status">
        <div>
          <dt>本机状态</dt>
          <dd>
            {status
              ? (STATE_NAMES[status.state] ?? '请核验或刷新状态')
              : token
                ? '尚未读取'
                : '等待本地服务'}
          </dd>
        </div>
        <div>
          <dt>已核验账号</dt>
          <dd>
            {status?.account_id
              ? `${status.name || 'Telegram 用户'} · ${status.account_id}`
              : '尚未核验'}
          </dd>
        </div>
        <div>
          <dt>最近同步</dt>
          <dd>{status?.last_sync_at || '尚未同步'}</dd>
        </div>
      </dl>
      <div className="form-actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={refresh}
          disabled={disabled}
        >
          {busy === 'status' ? '读取中…' : '刷新本机状态'}
        </button>
        <button
          type="button"
          className="button button-secondary"
          onClick={verify}
          disabled={
            disabled ||
            !status?.configured ||
            !status?.helper_available ||
            status.requires_initialization
          }
        >
          {busy === 'verify' ? '正在核验…' : '核验个人账号'}
        </button>
        <button
          type="button"
          className="button button-secondary"
          onClick={readDialogs}
          disabled={disabled || !verified}
        >
          {busy === 'dialogs' ? '正在读取…' : '读取私聊列表'}
        </button>
      </div>
      <div className="form-field">
        <label htmlFor="telegram-private-peer">选择要同步的私聊</label>
        <select
          id="telegram-private-peer"
          value={peerID}
          disabled={disabled || !dialogs || dialogs.items.length === 0}
          onChange={(event) => setPeerID(event.target.value)}
        >
          <option value="">
            {dialogs ? '请选择一名联系人' : '先读取私聊列表'}
          </option>
          {dialogs?.items.map((dialog) => (
            <option key={dialog.peer_id} value={dialog.peer_id}>
              {dialog.name || dialog.peer_id} · {dialog.peer_id}
            </option>
          ))}
        </select>
      </div>
      <div className="form-field">
        <label htmlFor="telegram-sync-limit">本次最近记录条数</label>
        <input
          id="telegram-sync-limit"
          type="number"
          min={1}
          max={100}
          step={1}
          value={limit}
          disabled={disabled}
          onChange={(event) => setLimit(event.target.value)}
        />
      </div>
      <p className="input-help">
        仅将选定私聊的文本保存到本产品加密消息中心，不更改 Telegram
        已读状态。图片、文件与其他不支持的记录会跳过；已有授权的 AI
        整理可能随后处理新增记录。
      </p>
      <div className="form-actions">
        <button
          type="button"
          className="button button-primary"
          onClick={sync}
          disabled={
            disabled ||
            !verified ||
            !selected ||
            !Number.isInteger(Number(limit)) ||
            Number(limit) < 1 ||
            Number(limit) > 100
          }
        >
          {busy === 'sync' ? '正在同步…' : '同步选定私聊'}
        </button>
      </div>
      <div aria-live="polite" aria-atomic="true">
        {error ? (
          <p className="form-message error-message" role="alert">
            {error}
          </p>
        ) : null}
        {notice ? (
          <p className="form-message success-message">{notice}</p>
        ) : null}
      </div>
    </section>
  );
}
