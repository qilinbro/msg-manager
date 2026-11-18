import { useCallback, useEffect, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { getCollection, SERVICE, ServiceError } from './api';
import type { Connector, SystemStatus } from './api';
import Inbox from './Inbox';
import { MilkyPanel } from './MilkyPanel';
import { AIModelPanel } from './AIModelPanel';
import { DigestPanel } from './DigestPanel';
import { AssistantPanel } from './AssistantPanel';
import { FeishuPanel } from './FeishuPanel';
import type { FeishuRecoveryCache } from './FeishuPanel';
import type { AssistantWorkspace } from './AssistantPanel';
import type { AIDraftBuffer } from './ai-api';
import { useServiceSession } from './useServiceSession';
import type { ServiceSession as Session } from './useServiceSession';

type Page =
  | 'overview'
  | 'messages'
  | 'contacts'
  | 'approvals'
  | 'assistant'
  | 'feishu'
  | 'settings';
type IconName =
  | 'overview'
  | 'messages'
  | 'contacts'
  | 'approvals'
  | 'settings'
  | 'arrow'
  | 'lock'
  | 'check'
  | 'plug';
const NAVIGATION: { page: Page; label: string; icon: IconName }[] = [
  { page: 'overview', label: '概览', icon: 'overview' },
  { page: 'messages', label: '消息', icon: 'messages' },
  { page: 'contacts', label: '联系人', icon: 'contacts' },
  { page: 'approvals', label: '待审批', icon: 'approvals' },
  { page: 'assistant', label: 'Agent 助手', icon: 'messages' },
  { page: 'feishu', label: '飞书早晚报', icon: 'approvals' },
];
const PAGE_TITLES: Record<Page, string> = {
  overview: '概览',
  messages: '消息',
  contacts: '联系人',
  approvals: '待审批',
  assistant: 'Agent 助手',
  feishu: '飞书早晚报',
  settings: '设置',
};
const PLANNED_CONNECTORS: Connector[] = [
  {
    id: 'qq-milky',
    platform: 'qq',
    display_name: 'QQ 个人号（Milky）',
    status: 'not_configured',
    implementation_status: 'manual_sync_ready',
    description:
      '自有 Milky 1.3 适配器：账号核验、好友私聊历史同步与显式开启的后台接收；发送保持关闭。',
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

function Status({ state }: { state: Session['status'] }) {
  const labels = {
    idle: '服务未连接',
    connecting: '正在连接服务…',
    connected: '服务可访问',
    error: '服务连接异常',
  };
  return (
    <span className="service-status" role="status">
      {labels[state]}
    </span>
  );
}

function Heading({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="page-heading">
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
      <div className="connector-copy">
        <h3>{connector.display_name}</h3>
        <p>{verified ? connector.description : '连接本地服务后查看接入状态'}</p>
        {identityVerified ? (
          <p className="connector-boundary">尚未启用消息接收与发送</p>
        ) : null}
      </div>
      <span
        className={`label ${connected || identityVerified ? 'label-neutral' : ''}`}
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
  return (
    <>
      <Heading
        title="概览"
        description="查看平台接入与存储状态，继续管理消息和联系人。"
      />
      <section
        className="overview-summary"
        aria-labelledby="workspace-state-title"
      >
        <div className="section-heading">
          <h2 id="workspace-state-title">工作台状态</h2>
          <button
            type="button"
            className="button button-primary"
            onClick={system?.storage.configured ? onMessages : onSettings}
          >
            {system?.storage.configured
              ? '打开消息'
              : system
                ? '查看配置'
                : '连接本地服务'}
          </button>
        </div>
        <dl className="overview-facts">
          <div>
            <dt>消息存储</dt>
            <dd>
              {system
                ? system.storage.configured
                  ? '已配置'
                  : '尚未配置'
                : '尚未读取'}
            </dd>
          </div>
          <div>
            <dt>后台自动化</dt>
            <dd>
              {system
                ? system.automation.paused
                  ? '已暂停'
                  : '运行中'
                : '尚未读取'}
            </dd>
          </div>
          <div>
            <dt>通用自动发送</dt>
            <dd>
              {system
                ? system.automation.auto_send_enabled
                  ? '已启用'
                  : '已关闭'
                : '默认关闭'}
            </dd>
          </div>
        </dl>
        <p className="section-footnote">
          {system?.storage.configured
            ? '可导入已有记录。实时接收需单独配置；仅飞书本人早晚报支持独立授权后的摘要发送。'
            : system
              ? '在后台配置加密消息存储后，即可导入并整理已有记录。'
              : '在设置中授权本地服务后读取实际状态。主令牌仅保留在当前页面内存中。'}
        </p>
      </section>
      <section aria-labelledby="platform-title">
        <div className="section-heading">
          <h2 id="platform-title">平台接入</h2>
          <button
            type="button"
            className="button button-quiet"
            onClick={onSettings}
          >
            连接设置
          </button>
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
          {system
            ? '状态来自当前服务。账号核验、历史导入与持续接收是不同能力。'
            : '以下为接入目录，连接服务后查看真实配置与可用能力。'}
        </p>
      </section>
    </>
  );
}

const COLLECTION_COPY = {
  messages: {
    title: '消息',
    description: '在一个地方，接住来自不同平台的对话。',
    emptyTitle: '第一条消息，还在路上。',
    emptyDescription:
      '平台尚未接入，当前没有消息。连接能力完成并获得授权后，新消息会出现在这里。',
  },
  contacts: {
    title: '联系人',
    description: '每个人的故事，都有独立的位置。',
    emptyTitle: '这里将记住你在意的人。',
    emptyDescription:
      '当前没有联系人。联系人管理和聊天导入接入后，可以在这里整理关系与记忆。',
  },
  approvals: {
    title: '待审批',
    description: '查看待审批记录。审批工作流尚未接入。',
    emptyTitle: '暂无待审批记录',
    emptyDescription:
      '此处暂未接入回复审批工作流。会话 AI 草稿在消息详情中管理，Agent 工具确认在助手页面中完成。',
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
      <Heading title={copy.title} description={copy.description} />
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

function Settings({
  session,
  activeToken,
  digestUnresolved,
  tokenDraft,
  hasToken,
  storageAvailable,
  rememberDevice,
  remembered,
  deviceExpiresAt,
  persistenceNotice,
  forgetting,
  onRememberChange,
  onDraftChange,
  onConnect,
  onClear,
  onCancel,
  onSystemUpdate,
  onAuthFailure,
}: {
  session: Session;
  activeToken: string;
  digestUnresolved: Set<string>;
  tokenDraft: string;
  hasToken: boolean;
  storageAvailable: boolean;
  rememberDevice: boolean;
  remembered: boolean;
  deviceExpiresAt: string | null;
  persistenceNotice: string | null;
  forgetting: boolean;
  onRememberChange: (value: boolean) => void;
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
        title="连接与设置"
        description="管理本机授权、平台连接、模型与每日摘要。"
      />
      <section
        className="settings-panel"
        aria-labelledby="service-settings-title"
      >
        <div className="settings-section-header">
          <div>
            <h2 id="service-settings-title">本地服务</h2>
            <p>首次使用主令牌授权，再读取本地服务的运行状态。</p>
          </div>
        </div>
        <form onSubmit={onConnect} className="connection-form">
          <label htmlFor="service-token">主令牌</label>
          <input
            id="service-token"
            name="service-token"
            type="password"
            value={tokenDraft}
            onChange={(event) => onDraftChange(event.target.value)}
            placeholder={
              remembered
                ? '已使用本机授权；仅更换连接时输入主令牌'
                : '输入本地服务启动时配置的主令牌'
            }
            autoComplete="off"
            spellCheck={false}
            disabled={connecting || forgetting}
            aria-describedby="token-description"
          />
          <p id="token-description" className="input-help">
            <Icon name="lock" size={14} />
            <span>
              {remembered
                ? `已记住此浏览器，关闭页面后可自动恢复。${deviceExpiresAt ? `有效期至 ${new Date(deviceExpiresAt).toLocaleDateString('zh-CN')}。` : '授权最长 90 天。'}`
                : '未记住时，连接仅在当前页面内存中保留；刷新后需重新连接。'}
              {!storageAvailable
                ? ' 无法清理旧版连接记录，请手动清除此站点数据；不会读取或恢复旧令牌。'
                : ''}
            </span>
          </p>
          {!remembered || tokenDraft.trim() ? (
            <div className="remember-device">
              <label className="remember-device-choice">
                <input
                  type="checkbox"
                  checked={rememberDevice}
                  onChange={(event) => onRememberChange(event.target.checked)}
                  disabled={connecting || forgetting}
                  aria-describedby="remember-description"
                />
                <span>记住这台可信任的电脑（90 天）</span>
              </label>
              <p id="remember-description">
                浏览器保存不可导出的签名密钥，而非主令牌。请勿在共享电脑上勾选；清除站点数据或更换地址后需重新授权。
              </p>
            </div>
          ) : null}
          <div className="form-actions">
            <button
              className="button button-primary"
              type="submit"
              disabled={
                connecting ||
                forgetting ||
                (!tokenDraft.trim() && !hasToken) ||
                Boolean(SERVICE.error)
              }
            >
              {connecting
                ? '正在连接…'
                : hasToken
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
            ) : null}
            <button
              className="button button-quiet"
              type="button"
              onClick={onClear}
              disabled={forgetting || (!tokenDraft && !hasToken)}
            >
              {forgetting
                ? '正在忘记…'
                : remembered
                  ? '忘记本机并断开'
                  : '清除令牌与连接'}
            </button>
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
            {persistenceNotice ? (
              <p className="connection-notice">{persistenceNotice}</p>
            ) : null}
          </div>
        </form>
      </section>
      <MilkyPanel
        key={`milky-${activeToken}`}
        token={activeToken}
        onSystemUpdate={onSystemUpdate}
        onAuthFailure={onAuthFailure}
      />
      <AIModelPanel
        key={`model:${activeToken}`}
        token={activeToken}
        onAuthFailure={onAuthFailure}
      />
      <DigestPanel
        key={`digest:${activeToken}`}
        token={activeToken}
        unresolvedIdentities={digestUnresolved}
        onAuthFailure={onAuthFailure}
      />
      <section className="settings-panel" aria-labelledby="automation-title">
        <div className="section-heading">
          <div>
            <h2 id="automation-title">自动化权限</h2>
          </div>
          <span className="label">只读状态</span>
        </div>
        <div className="settings-row">
          <div>
            <h3>通用自动发送</h3>
            <p>
              默认关闭，通用会话发送尚未开放；飞书本人早晚报使用独立授权与计划。
            </p>
          </div>
          <span className="label label-neutral">
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
            <p>
              暂停状态约束后台分析与整理；平台接收需单独开启。飞书本人早晚报需在独立页面授权。
            </p>
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
          当前支持加密消息管理、记录导入、已实现平台的接收、AI
          分析与草稿、档案和摘要。
          各平台以实际配置及状态为准；账号核验不等于持续接收，通用自动发送保持关闭。飞书本人早晚报默认关闭，需独立授权。
          微信 ClawBot 仅针对 Bot 联系人，不接管个人微信已有好友或聊天。
          后台服务独立于工作台窗口；休眠或关机后不承诺继续运行。
        </p>
      </details>
    </>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>('overview');
  const [aiDrafts] = useState(() => new Map<string, AIDraftBuffer>());
  const [assistantWorkspaces] = useState(
    () => new Map<string, AssistantWorkspace>(),
  );
  const [digestUnresolved] = useState(() => new Set<string>());
  const [feishuRecoveries] = useState<FeishuRecoveryCache>(() => new Map());
  const {
    session,
    tokenDraft,
    setTokenDraft,
    activeToken,
    hasCredential,
    storageAvailable,
    rememberDevice,
    setRememberDevice,
    remembered,
    deviceExpiresAt,
    persistenceNotice,
    forgetting,
    connect,
    clear,
    cancel,
    onAuthFailure,
    onSystemUpdate,
  } = useServiceSession(aiDrafts);
  useEffect(() => {
    // Only the stable business identity owns this in-memory assistant workspace.
    // Device bearer renewal does not change activeToken or remount business panels.
    for (const identity of assistantWorkspaces.keys()) {
      if (identity !== activeToken) assistantWorkspaces.delete(identity);
    }
    for (const identity of digestUnresolved) {
      if (identity !== activeToken) digestUnresolved.delete(identity);
    }
    for (const identity of feishuRecoveries.keys()) {
      if (identity !== activeToken) feishuRecoveries.delete(identity);
    }
  }, [activeToken, assistantWorkspaces, digestUnresolved, feishuRecoveries]);
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
          aria-label="消息工作台概览"
        >
          消息工作台
        </a>
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
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
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
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <div className="breadcrumb">
            <span>工作空间</span>
            <span aria-hidden="true">/</span>
            <strong>{PAGE_TITLES[page]}</strong>
          </div>
          <Status state={session.status} />
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
              digestUnresolved={digestUnresolved}
              tokenDraft={tokenDraft}
              hasToken={hasCredential}
              storageAvailable={storageAvailable}
              rememberDevice={rememberDevice}
              remembered={remembered}
              deviceExpiresAt={deviceExpiresAt}
              persistenceNotice={persistenceNotice}
              forgetting={forgetting}
              onRememberChange={setRememberDevice}
              onDraftChange={setTokenDraft}
              onConnect={connect}
              onClear={clear}
              onCancel={cancel}
              onSystemUpdate={onSystemUpdate}
              onAuthFailure={onAuthFailure}
            />
          ) : page === 'feishu' ? (
            <FeishuPanel
              key={`feishu:${activeToken}`}
              token={activeToken}
              recoveryCache={feishuRecoveries}
              onSettings={onSettings}
              onAuthFailure={onAuthFailure}
            />
          ) : page === 'assistant' ? (
            <AssistantPanel
              key={`assistant:${activeToken}`}
              token={activeToken}
              cache={assistantWorkspaces}
              onSettings={onSettings}
              onAuthFailure={onAuthFailure}
            />
          ) : page === 'contacts' ? (
            <Inbox
              key={`profiles:${activeToken}`}
              token={activeToken}
              aiDrafts={aiDrafts}
              onSettings={onSettings}
              onAuthFailure={onAuthFailure}
              profilesOnly
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
      </div>
    </div>
  );
}
