import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import {
  getCollection,
  getQQVerification,
  getSystem,
  SERVICE,
  ServiceError,
} from './api';
import type { Connector, QQVerification, SystemStatus } from './api';
import Inbox from './Inbox';
import AwayDesk from './AwayDesk';
import type { AIDraftBuffer } from './ai-api';
import { useServiceSession } from './useServiceSession';
import type { ServiceSession as Session } from './useServiceSession';

type Page =
  'away' | 'overview' | 'messages' | 'contacts' | 'approvals' | 'settings';
type IconName =
  | 'overview'
  | 'messages'
  | 'contacts'
  | 'approvals'
  | 'settings'
  | 'arrow'
  | 'lock'
  | 'check'
  | 'plug'
  | 'leaf';
const NAVIGATION: { page: Page; label: string; icon: IconName }[] = [
  { page: 'away', label: '接班', icon: 'overview' },
  { page: 'overview', label: '概览', icon: 'overview' },
  { page: 'messages', label: '消息', icon: 'messages' },
  { page: 'contacts', label: '联系人', icon: 'contacts' },
  { page: 'approvals', label: '待审批', icon: 'approvals' },
];
const PAGE_TITLES: Record<Page, string> = {
  away: '接班',
  overview: '概览',
  messages: '消息',
  contacts: '联系人',
  approvals: '待审批',
  settings: '设置',
};
const PLANNED_CONNECTORS: Connector[] = [
  {
    id: 'qq-onebot',
    platform: 'qq',
    display_name: 'QQ 个人号（OneBot）',
    status: 'not_configured',
    implementation_status: 'probe_ready',
    description: '优先接入个人 QQ；当前仅支持账号核验，尚未启用收发。',
    capabilities: {
      receive_text: false,
      send_text: false,
      history_import: false,
    },
  },
  {
    id: 'weixin-clawbot',
    platform: 'weixin',
    display_name: '微信 ClawBot',
    status: 'not_configured',
    implementation_status: 'planned',
    description: 'ClawBot 联系人接入，不代表接管个人微信已有好友与聊天。',
    capabilities: {
      receive_text: false,
      send_text: false,
      history_import: false,
    },
  },
  {
    id: 'telegram-bot',
    platform: 'telegram',
    display_name: 'Telegram Bot',
    status: 'not_configured',
    implementation_status: 'planned',
    description: '等待平台连接能力接入',
    capabilities: {
      receive_text: false,
      send_text: false,
      history_import: false,
    },
  },
];

function Icon({ name, size = 20 }: { name: IconName; size?: number }) {
  const paths: Record<IconName, ReactNode> = {
    overview: (
      <>
        <rect x="3" y="3" width="7" height="7" rx="1.5" />
        <rect x="14" y="3" width="7" height="7" rx="1.5" />
        <rect x="3" y="14" width="7" height="7" rx="1.5" />
        <rect x="14" y="14" width="7" height="7" rx="1.5" />
      </>
    ),
    messages: (
      <path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z" />
    ),
    contacts: (
      <>
        <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75" />
        <circle cx="9" cy="7" r="4" />
      </>
    ),
    approvals: (
      <>
        <rect x="5" y="4" width="14" height="17" rx="2" />
        <path d="M9 4V2h6v2M9 12l2 2 4-4" />
      </>
    ),
    settings: (
      <>
        <path d="m12 3 2.3 1.2 2.6-.1 1.2 2.3 2.2 1.4-.1 2.6L21 13l-1.5 2.1-.6 2.5-2.5.7-1.9 1.8L12 19.5l-2.5.6-1.9-1.8-2.5-.7-.6-2.5L3 13l.8-2.6-.1-2.6L6 6.4l1.2-2.3 2.6.1L12 3Z" />
        <circle cx="12" cy="11.5" r="3" />
      </>
    ),
    arrow: <path d="M4 12h15m-6-6 6 6-6 6" />,
    lock: (
      <>
        <rect x="5" y="10" width="14" height="11" rx="2" />
        <path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    plug: (
      <>
        <path d="M9 3v4m6-4v4M7 7h10v4a5 5 0 0 1-10 0V7Zm5 9v5" />
      </>
    ),
    leaf: (
      <>
        <path d="M20 4C10 3 4 8 5 14c1 5 9 7 12 1 2-3 2-6 3-11Z" />
        <path d="M4 21C7 15 10 12 15 9" />
      </>
    ),
  };
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  );
}

function Status({ connected }: { connected: boolean }) {
  return (
    <span className={`service-status ${connected ? 'is-connected' : ''}`}>
      <span className="status-dot" />
      {connected ? '服务可访问' : '服务未连接'}
    </span>
  );
}

function Heading({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string;
  title: string;
  description: string;
}) {
  return (
    <div className="page-heading">
      <p className="eyebrow">{eyebrow}</p>
      <h1>{title}</h1>
      <p className="page-description">{description}</p>
    </div>
  );
}

function ConnectorCard({
  connector,
  verified,
}: {
  connector: Connector;
  verified: boolean;
}) {
  const connected = verified && connector.status === 'connected';
  const identityVerified = verified && connector.status === 'identity_verified';
  const labels: Record<string, string> = {
    not_configured: '尚未配置',
    configured: '待核验',
    identity_verified: '账号已核验',
    unreachable: '协议端不可达',
    auth_failed: '鉴权失败',
    identity_mismatch: '账号不匹配',
    logged_out: '账号未在线',
    protocol_error: '协议异常',
    connected: '已连接',
  };
  return (
    <article className="connector-card">
      <div
        className={`platform-symbol ${connector.platform}`}
        aria-hidden="true"
      >
        {connector.platform === 'qq'
          ? 'Q'
          : connector.platform === 'weixin'
            ? '微'
            : '↗'}
      </div>
      <div className="connector-copy">
        <h3>{connector.display_name}</h3>
        <p>{verified ? connector.description : '连接本地服务后查看接入状态'}</p>
        {identityVerified ? (
          <p className="connector-boundary">尚未启用消息接收与发送</p>
        ) : null}
      </div>
      <span
        className={`label ${connected || identityVerified ? 'label-green' : ''}`}
      >
        {verified
          ? Object.hasOwn(labels, connector.status)
            ? labels[connector.status]
            : '状态待确认'
          : '等待检查'}
      </span>
    </article>
  );
}

function Overview({
  session,
  onSettings,
  onMessages,
}: {
  session: Session;
  onSettings: () => void;
  onMessages: () => void;
}) {
  const system = session.system;
  const connectors = system?.connectors ?? PLANNED_CONNECTORS;
  const connectedCount = system
    ? connectors.filter((item) => item.status === 'connected').length
    : null;
  return (
    <>
      <Heading
        eyebrow="YOUR RELATIONSHIPS, IN CONTEXT"
        title="让每段关系，都有上下文。"
        description="把分散的消息与重要的约定，慢慢整理成有记忆的联系。"
      />
      <section className="welcome-panel" aria-labelledby="welcome-title">
        <div className="welcome-copy">
          <span className="small-label">
            <span className="little-line" />{' '}
            {system ? '一步一步，建立连接' : '从这里开始'}
          </span>
          <h2 id="welcome-title">
            {system ? (
              <>
                服务已连接，
                <br />
                {system.storage.configured
                  ? '开始整理消息。'
                  : '下一步配置消息存储。'}
              </>
            ) : (
              <>
                先连接，
                <br />
                再让沟通连续起来。
              </>
            )}
          </h2>
          <p>
            {system ? (
              <>
                {system.storage.configured
                  ? '统一消息中心已准备好。'
                  : '消息存储尚未配置。'}
                <br className="desktop-break" />
                {system.storage.configured
                  ? '导入已有记录，按平台查看和整理会话。'
                  : '启用加密存储后，即可导入已有记录。'}
              </>
            ) : (
              <>
                你的工作台已经准备好。连接本地服务后，
                <br className="desktop-break" />
                就可以查看平台接入与消息状态。
              </>
            )}
          </p>
          <button
            className="button button-primary"
            onClick={system?.storage.configured ? onMessages : onSettings}
          >
            {system?.storage.configured
              ? '打开消息中心'
              : system
                ? '查看配置状态'
                : '连接本地服务'}{' '}
            <Icon name="arrow" size={17} />
          </button>
        </div>
        <div className="relationship-art" aria-hidden="true">
          <span className="art-orbit orbit-outer" />
          <span className="art-orbit orbit-inner" />
          <div className="art-center">
            <Icon name="leaf" size={44} />
          </div>
          <span className="art-node node-one">
            <Icon name="messages" size={22} />
          </span>
          <span className="art-node node-two">
            <Icon name="contacts" size={23} />
          </span>
          <span className="art-node node-three">
            <Icon name="check" size={19} />
          </span>
          <span className="art-small-dot" />
          <span className="art-caption">每一次联系，值得被记住。</span>
        </div>
      </section>
      <section className="status-grid" aria-label="当前工作台状态">
        <article className="status-item">
          <span className="status-item-label">本地服务</span>
          <div className="status-item-value">
            {system ? '可访问' : '未连接'}
            <span className={`inline-dot ${system ? 'green' : ''}`} />
          </div>
          <p>{system ? '已读取服务的实时状态' : '在设置中输入令牌以连接'}</p>
        </article>
        <article className="status-item">
          <span className="status-item-label">社交平台</span>
          <div className="status-item-value">
            {connectedCount === null ? '待检查' : `${connectedCount} 个已连接`}
          </div>
          <p>{system ? '仅显示平台实际接入状态' : '尚未读取平台连接状态'}</p>
        </article>
        <article className="status-item">
          <span className="status-item-label">自动发送</span>
          <div className="status-item-value">
            {system?.automation.auto_send_enabled
              ? '已启用'
              : system
                ? '已关闭'
                : '默认关闭'}
            <Icon name="lock" size={17} />
          </div>
          <p>每次授权，都由你决定</p>
        </article>
      </section>
      <div className="overview-bottom">
        <section aria-labelledby="platform-title">
          <div className="section-heading">
            <div>
              <p className="eyebrow">CONNECTIONS</p>
              <h2 id="platform-title">平台接入</h2>
            </div>
            <span className="subtle-note">
              {system ? '来自当前服务' : '计划接入'}
            </span>
          </div>
          <div className="connector-list">
            {connectors.map((connector) => (
              <ConnectorCard
                key={connector.id}
                connector={connector}
                verified={Boolean(system)}
              />
            ))}
          </div>
          <p className="section-footnote">
            已导入记录可在消息中心统一管理；平台在线连接单独验证。
          </p>
        </section>
        <aside className="principle-panel">
          <Icon name="leaf" size={23} />
          <h2>
            关系的决定权，
            <br />
            始终在你。
          </h2>
          <p>
            重要安排留给你确认，日常回复遵循明确授权。每次处理都应有迹可循。
          </p>
          <div className="principle-rule" />
          <span>记忆有来源 · 操作有边界</span>
        </aside>
      </div>
    </>
  );
}

const COLLECTION_COPY = {
  messages: {
    eyebrow: 'INBOX',
    title: '消息',
    description: '在一个地方，接住来自不同平台的对话。',
    emptyTitle: '第一条消息，还在路上。',
    emptyDescription:
      '平台尚未接入，当前没有消息。连接能力完成并获得授权后，新消息会出现在这里。',
    icon: 'messages' as IconName,
  },
  contacts: {
    eyebrow: 'PEOPLE',
    title: '联系人',
    description: '每个人的故事，都有独立的位置。',
    emptyTitle: '这里将记住你在意的人。',
    emptyDescription:
      '当前没有联系人。联系人管理和聊天导入接入后，可以在这里整理关系与记忆。',
    icon: 'contacts' as IconName,
  },
  approvals: {
    eyebrow: 'YOUR DECISIONS',
    title: '待审批',
    description: '重要的回复，等你看过再决定。',
    emptyTitle: '暂时没有需要确认的回复。',
    emptyDescription:
      '回复生成与审批能力尚未接入。未来涉及承诺、金额或重要安排的消息，会留在这里等待你确认。',
    icon: 'approvals' as IconName,
  },
};

function CollectionView({
  page,
  token,
  onSettings,
  onAuthFailure,
}: {
  page: keyof typeof COLLECTION_COPY;
  token: string;
  onSettings: () => void;
  onAuthFailure: () => void;
}) {
  const copy = COLLECTION_COPY[page];
  const [result, setResult] = useState<{
    status: 'idle' | 'loading' | 'ready' | 'error';
    count: number;
    error: string | null;
  }>({ status: 'idle', count: 0, error: null });
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!token) return;
    const controller = new AbortController();
    setResult({ status: 'loading', count: 0, error: null });
    getCollection(page, token, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted)
          setResult({ status: 'ready', count: data.items.length, error: null });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof ServiceError && error.status === 401)
          onAuthFailure();
        setResult({
          status: 'error',
          count: 0,
          error: error instanceof Error ? error.message : '读取失败，请重试。',
        });
      });
    return () => controller.abort();
  }, [page, token, retry, onAuthFailure]);
  const loading = token && result.status === 'loading';
  const error = token && result.status === 'error';
  return (
    <>
      <Heading
        eyebrow={copy.eyebrow}
        title={copy.title}
        description={copy.description}
      />
      <section className="collection-surface" aria-busy={Boolean(loading)}>
        <div className="collection-toolbar">
          <span>{copy.title}列表</span>
          <span className="subtle-note">
            {token && result.status === 'ready'
              ? `${result.count} 条记录`
              : '尚未读取'}
          </span>
        </div>
        <div className="empty-state">
          <div className={`empty-icon ${loading ? 'is-loading' : ''}`}>
            <Icon name={copy.icon} size={32} />
          </div>
          <p className="eyebrow">
            {loading
              ? 'CONNECTING'
              : error
                ? 'CONNECTION NEEDED'
                : 'A LITTLE SPACE FOR WHAT MATTERS'}
          </p>
          <h2>
            {loading
              ? '正在读取…'
              : error
                ? '暂时无法读取'
                : !token
                  ? '先连接你的本地服务。'
                  : result.count > 0
                    ? `已读取 ${result.count} 条记录`
                    : copy.emptyTitle}
          </h2>
          <p role={error ? 'alert' : undefined}>
            {loading
              ? '请稍候，正在获取当前列表。'
              : error
                ? result.error
                : !token
                  ? '连接后才能查看真实的消息、联系人与待审批状态。'
                  : result.count > 0
                    ? '服务已返回记录，详细列表展示将在对应功能接入后开放。'
                    : copy.emptyDescription}
          </p>
          {!loading ? (
            <button
              className="button button-secondary"
              onClick={
                error ? () => setRetry((value) => value + 1) : onSettings
              }
            >
              {error ? '重新读取' : '查看连接设置'}
              <Icon name="arrow" size={16} />
            </button>
          ) : null}
        </div>
      </section>
    </>
  );
}

function QQSettings({
  token,
  onSystemUpdate,
  onAuthFailure,
}: {
  token: string;
  onSystemUpdate: (system: SystemStatus) => void;
  onAuthFailure: () => void;
}) {
  const [result, setResult] = useState<QQVerification | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef<AbortController | null>(null);
  useEffect(() => {
    setResult(null);
    setError(null);
    setBusy(false);
    return () => {
      pending.current?.abort();
      pending.current = null;
    };
  }, [token]);
  const cancel = () => {
    pending.current?.abort();
    pending.current = null;
    setBusy(false);
    setResult(null);
    setError(null);
  };
  const verify = async () => {
    if (!token) return;
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setResult(null);
    setError(null);
    setBusy(true);
    try {
      const verified = await getQQVerification(token, controller.signal);
      const system = await getSystem(token, controller.signal);
      if (controller.signal.aborted) return;
      onSystemUpdate(system);
      setResult(verified);
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(
        failure instanceof Error
          ? failure.message
          : 'QQ 账号核验失败，请重试。',
      );
      try {
        const system = await getSystem(token, controller.signal);
        if (!controller.signal.aborted) onSystemUpdate(system);
      } catch (refreshError: unknown) {
        if (
          !controller.signal.aborted &&
          refreshError instanceof ServiceError &&
          refreshError.status === 401
        )
          onAuthFailure();
      }
    } finally {
      if (pending.current === controller) {
        pending.current = null;
        setBusy(false);
      }
    }
  };
  return (
    <section
      className="settings-panel qq-settings"
      aria-labelledby="qq-settings-title"
      aria-busy={busy}
    >
      <div className="section-heading">
        <div>
          <p className="eyebrow">PERSONAL ACCOUNT · PRIORITY</p>
          <h2 id="qq-settings-title">QQ 个人号</h2>
        </div>
        <span className="label">OneBot v11</span>
      </div>
      <p className="qq-intro">
        通过已登录的个人 QQ
        协议端核验账号。当前只识别账号与在线状态，不接收聊天、不导入历史，也不发送消息。
      </p>
      <div className="qq-configuration">
        <p>在启动本地服务前，由技术开发配置：</p>
        <dl>
          <div>
            <dt>
              <code>QQ_ONEBOT_URL</code>
            </dt>
            <dd>本机 OneBot 服务地址</dd>
          </div>
          <div>
            <dt>
              <code>QQ_ONEBOT_ACCESS_TOKEN</code>
            </dt>
            <dd>协议端令牌，仅保留在服务端环境</dd>
          </div>
          <div>
            <dt>
              <code>QQ_ONEBOT_ACCOUNT_ID</code>
            </dt>
            <dd>预期登录的 QQ 号</dd>
          </div>
        </dl>
        <p>QQ 登录在协议端完成。工作台不收集 QQ 密码或协议端令牌。</p>
      </div>
      <div className="form-actions">
        <button
          type="button"
          className="button button-primary"
          onClick={verify}
          disabled={!token || busy}
        >
          {busy ? '正在核验…' : '核验 QQ 账号'}
          {!busy ? <Icon name="arrow" size={16} /> : null}
        </button>
        {busy ? (
          <button
            type="button"
            className="button button-quiet"
            onClick={cancel}
          >
            取消核验
          </button>
        ) : null}
      </div>
      {!token ? (
        <p className="input-help">请先连接上方的本地服务，再核验 QQ 账号。</p>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {error ? (
          <p className="form-message error-message" role="alert">
            {error}
          </p>
        ) : null}
        {result ? (
          <div className="qq-result">
            <div className="qq-result-heading">
              <Icon name="check" size={18} />
              <strong>账号已核验，尚未启用收发</strong>
            </div>
            <dl>
              <div>
                <dt>QQ 账号</dt>
                <dd>{result.account_id}</dd>
              </div>
              <div>
                <dt>昵称</dt>
                <dd>{result.nickname || '未提供昵称'}</dd>
              </div>
              <div>
                <dt>本次在线状态</dt>
                <dd>在线</dd>
              </div>
              <div>
                <dt>核验时间</dt>
                <dd>
                  {new Date(result.checked_at).toLocaleString('zh-CN', {
                    hour12: false,
                    timeZone: 'Asia/Shanghai',
                  })}
                </dd>
              </div>
            </dl>
            <p>
              此结果仅确认本次身份检查成功。消息接收、历史导入与发送仍未启用。
            </p>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function Settings({
  session,
  activeToken,
  tokenDraft,
  hasToken,
  storageAvailable,
  onDraftChange,
  onConnect,
  onClear,
  onCancel,
  onSystemUpdate,
  onAuthFailure,
}: {
  session: Session;
  activeToken: string;
  tokenDraft: string;
  hasToken: boolean;
  storageAvailable: boolean;
  onDraftChange: (value: string) => void;
  onConnect: (event: FormEvent<HTMLFormElement>) => void;
  onClear: () => void;
  onCancel: () => void;
  onSystemUpdate: (system: SystemStatus) => void;
  onAuthFailure: () => void;
}) {
  const connecting = session.status === 'connecting';
  return (
    <>
      <Heading
        eyebrow="PREFERENCES"
        title="连接与设置"
        description="把服务连接好，再一步步接入你的消息与记忆。"
      />
      <section
        className="settings-panel"
        aria-labelledby="service-settings-title"
      >
        <div className="settings-section-header">
          <div className="settings-symbol">
            <Icon name="plug" size={23} />
          </div>
          <div>
            <h2 id="service-settings-title">本地服务</h2>
            <p>使用服务启动时配置的访问令牌，读取当前运行状态。</p>
          </div>
          <Status connected={session.status === 'connected'} />
        </div>
        <form onSubmit={onConnect} className="connection-form">
          <label htmlFor="service-token">访问令牌</label>
          <input
            id="service-token"
            name="service-token"
            type="password"
            value={tokenDraft}
            onChange={(event) => onDraftChange(event.target.value)}
            placeholder="输入本地服务令牌"
            autoComplete="off"
            spellCheck={false}
            disabled={connecting}
            aria-describedby="token-description"
          />
          <p id="token-description" className="input-help">
            <Icon name="lock" size={14} />
            {storageAvailable
              ? '连接信息仅在本标签页会话内保留，刷新后自动验证，可随时清除。'
              : '当前浏览器无法保存会话，连接仅在本窗口内存中保留；刷新后需重新连接。'}
          </p>
          <div className="form-actions">
            <button
              className="button button-primary"
              type="submit"
              disabled={
                connecting || !tokenDraft.trim() || Boolean(SERVICE.error)
              }
            >
              {connecting
                ? '正在连接…'
                : session.status === 'connected'
                  ? '重新检查连接'
                  : '连接服务'}
              {!connecting ? <Icon name="arrow" size={16} /> : null}
            </button>
            {connecting ? (
              <button
                className="button button-quiet"
                type="button"
                onClick={onCancel}
              >
                取消连接
              </button>
            ) : (
              <button
                className="button button-quiet"
                type="button"
                onClick={onClear}
                disabled={!tokenDraft && !hasToken}
              >
                清除令牌与连接
              </button>
            )}
          </div>
          <div aria-live="polite" aria-atomic="true">
            {SERVICE.error || session.error ? (
              <p className="form-message error-message" role="alert">
                {SERVICE.error ?? session.error}
              </p>
            ) : session.status === 'connected' ? (
              <p className="form-message success-message">
                <Icon name="check" size={16} />
                服务可访问，状态已更新。
                {session.checkedAt ? `检查于 ${session.checkedAt}` : ''}
              </p>
            ) : null}
          </div>
        </form>
      </section>
      <QQSettings
        key={activeToken}
        token={activeToken}
        onSystemUpdate={onSystemUpdate}
        onAuthFailure={onAuthFailure}
      />
      <section className="settings-panel" aria-labelledby="automation-title">
        <div className="section-heading">
          <div>
            <p className="eyebrow">AUTOMATION</p>
            <h2 id="automation-title">自动化权限</h2>
          </div>
          <span className="label">只读状态</span>
        </div>
        <div className="settings-row">
          <div>
            <h3>自动发送</h3>
            <p>默认关闭，需在后续功能中逐联系人明确授权。</p>
          </div>
          <span className="label label-green">
            {session.system
              ? session.system.automation.auto_send_enabled
                ? '已启用'
                : '已关闭'
              : '默认关闭'}
          </span>
        </div>
        <div className="settings-row">
          <div>
            <h3>后台自动化</h3>
            <p>当前仅读取状态与核验 QQ 身份，不执行收发或审批操作。</p>
          </div>
          <span className="subtle-note">
            {session.system
              ? session.system.automation.paused
                ? '已暂停'
                : '运行中'
              : '等待检查'}
          </span>
        </div>
      </section>
      <details className="development-details">
        <summary>开发状态与服务信息</summary>
        <dl>
          <div>
            <dt>服务地址</dt>
            <dd>
              <code>{SERVICE.url || '配置无效'}</code>
            </dd>
          </div>
          <div>
            <dt>工作台版本</dt>
            <dd>0.1.0 · 统一消息中心</dd>
          </div>
          <div>
            <dt>后台版本</dt>
            <dd>{session.system?.version ?? '尚未读取'}</dd>
          </div>
          <div>
            <dt>持久化存储</dt>
            <dd>
              {session.system
                ? session.system.storage.configured
                  ? '已配置'
                  : '尚未配置'
                : '尚未读取'}
            </dd>
          </div>
        </dl>
        <p>
          接班让重要来信在你离席期间变成可核对的任务和待审回信。
          在指定发件人和授权时段内，独立后台可以持续检查邮箱；关闭窗口后，电脑和后台仍需保持运行。
          统一消息中心支持加密存储、手动 AI 整理与选定微信私聊的后台只读。 QQ
          OneBot 当前仅核验身份。回信保存为草稿，发送由你决定。 真实 QQ
          邮箱三轮验收与原生桌面验收尚待完成。
        </p>
      </details>
    </>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>('overview');
  const [aiDrafts] = useState(() => new Map<string, AIDraftBuffer>());
  const {
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
  } = useServiceSession(aiDrafts);
  useEffect(() => {
    const warnUnsaved = (event: BeforeUnloadEvent) => {
      if (!aiDrafts.size) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warnUnsaved);
    return () => window.removeEventListener('beforeunload', warnUnsaved);
  }, [aiDrafts]);
  const onSettings = useCallback(() => setPage('settings'), []);
  if (page === 'away') {
    return (
      <AwayDesk
        key={activeToken}
        token={activeToken}
        onSettings={onSettings}
        onAuthFailure={onAuthFailure}
        onBack={() => setPage('overview')}
      />
    );
  }
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      <aside className="sidebar">
        <a
          className="brand"
          href="#main-content"
          onClick={() => setPage('overview')}
          aria-label="Social Intelligence OS 概览"
        >
          <span className="brand-symbol">
            <Icon name="leaf" size={25} />
          </span>
          <span>
            Social Intelligence
            <span className="brand-subtitle">关系工作台</span>
          </span>
        </a>
        <span className="sidebar-section-label">个人工作空间</span>
        <nav aria-label="工作台导航">
          {NAVIGATION.map((item) => (
            <button
              key={item.page}
              className={`nav-item ${page === item.page ? 'active' : ''}`}
              aria-label={item.label}
              title={item.label}
              aria-current={page === item.page ? 'page' : undefined}
              onClick={() => setPage(item.page)}
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
              {page === item.page ? <span className="nav-active-dot" /> : null}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="sidebar-note">
            <span className="note-mark">“</span>
            <p>
              让日常联系轻一点，
              <br />
              让重要的人近一点。
            </p>
          </div>
          <button
            className={`nav-item ${page === 'settings' ? 'active' : ''}`}
            aria-label="设置"
            title="设置"
            aria-current={page === 'settings' ? 'page' : undefined}
            onClick={onSettings}
          >
            <Icon name="settings" />
            <span>设置</span>
          </button>
          <div className="workspace-owner">
            <span className="owner-avatar">我</span>
            <div>
              <strong>我的工作空间</strong>
              <span>个人 · 初始设置</span>
            </div>
            <Icon name="lock" size={15} />
          </div>
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <div className="breadcrumb">
            <span>工作空间</span>
            <span aria-hidden="true">/</span>
            <strong>{PAGE_TITLES[page]}</strong>
          </div>
          <Status connected={session.status === 'connected'} />
        </header>
        <main id="main-content" className="main-content" tabIndex={-1}>
          {page === 'overview' ? (
            <Overview
              session={session}
              onSettings={onSettings}
              onMessages={() => setPage('messages')}
            />
          ) : page === 'settings' ? (
            <Settings
              session={session}
              activeToken={activeToken}
              tokenDraft={tokenDraft}
              hasToken={Boolean(activeToken)}
              storageAvailable={storageAvailable}
              onDraftChange={setTokenDraft}
              onConnect={connect}
              onClear={clear}
              onCancel={cancel}
              onSystemUpdate={onSystemUpdate}
              onAuthFailure={onAuthFailure}
            />
          ) : page === 'messages' ? (
            <Inbox
              key={activeToken}
              token={activeToken}
              aiDrafts={aiDrafts}
              onSettings={onSettings}
              onAuthFailure={onAuthFailure}
            />
          ) : (
            <CollectionView
              key={page}
              page={page}
              token={activeToken}
              onSettings={onSettings}
              onAuthFailure={onAuthFailure}
            />
          )}
        </main>
        <footer className="workspace-footer">
          <span>Social Intelligence OS</span>
          <span>由你建立关系，由 AI 延续上下文。</span>
        </footer>
      </div>
    </div>
  );
}
