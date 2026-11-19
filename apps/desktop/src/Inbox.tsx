import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { Icon } from './Icon';
import { HelpDetails } from './HelpDetails';
import type { IconName } from './Icon';
import { ServiceError } from './api';
import {
  getInboxInfo,
  getConversations,
  getConversationMessages,
  updateConversation,
  importMessages,
  importTelegram,
  parseImport,
  PLATFORM_NAMES,
} from './inbox-api';
import type {
  Conversation,
  InboxInfo,
  InboxMessage,
  ImportRequest,
  ImportResult,
  Platform,
  TelegramImportRequest,
} from './inbox-api';
import './inbox.css';
import EmailPanel from './EmailPanel';
import WechatPanel from './WechatPanel';
import AIPanel from './AIPanel';
import type { AIDraftBuffer } from './ai-api';
import ProfilePanel from './ProfilePanel';
import type { AssistantHelpRequest } from './assistant-help';
import { ConversationKindSelect } from './ConversationKindSelect';
import type { ConversationKind } from './inbox-api';
import { KeywordMatch, keywordError } from './keyword-search';
import {
  CATEGORY_NAMES,
  PROFILE_CATEGORIES,
  getProfileStatus,
} from './profile-api';
import type { ProfileCategory, ProfileStatus } from './profile-api';

type View = 'all' | 'unread' | 'pinned' | 'archived';
type LoadState = 'idle' | 'loading' | 'ready' | 'error';
const PLATFORMS: Platform[] = ['qq', 'weixin', 'telegram', 'shanliao', 'email'];
const VIEWS: { value: View; name: string; icon: IconName }[] = [
  { value: 'all', name: '全部会话', icon: 'messages' },
  { value: 'unread', name: '未读', icon: 'mail' },
  { value: 'pinned', name: '置顶', icon: 'pin' },
  { value: 'archived', name: '已归档', icon: 'archive' },
];
function PlatformMark({ platform }: { platform: Platform }) {
  return (
    <span
      className={`inbox-platform-mark is-${platform}`}
      aria-label={PLATFORM_NAMES[platform]}
    >
      {PLATFORM_NAMES[platform].slice(0, 1)}
    </span>
  );
}
function dateText(value: string, detailed = false) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '时间未知';
  return date.toLocaleString(
    'zh-CN',
    detailed
      ? {
          timeZone: 'Asia/Shanghai',
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        }
      : {
          timeZone: 'Asia/Shanghai',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        },
  );
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : '操作暂时无法完成，请重试。';
}
function unique<T extends { id: string }>(items: T[]) {
  return [...new Map(items.map((item) => [item.id, item])).values()];
}

type ImportPreview =
  | { mode: 'standard'; request: ImportRequest; count: number }
  | { mode: 'telegram'; request: TelegramImportRequest; count: number };
function ImportPanel({
  token,
  limit,
  onClose,
  onImported,
  onAuthFailure,
}: {
  token: string;
  limit: number;
  onClose: () => void;
  onImported: () => void;
  onAuthFailure: () => void;
}) {
  const [platform, setPlatform] = useState<Platform>('qq');
  const [format, setFormat] = useState<'standard' | 'telegram'>('standard');
  const [account, setAccount] = useState('');
  const [accountName, setAccountName] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [state, setState] = useState<
    'editing' | 'reading' | 'importing' | 'done'
  >('editing');
  const [result, setResult] = useState<ImportResult | null>(null);
  const [analyzeImported, setAnalyzeImported] = useState(false);
  const [analysisNotice, setAnalysisNotice] = useState<{
    queued: number;
    text: string;
  } | null>(null);
  const [modelStatus, setModelStatus] = useState<ProfileStatus | null>(null);
  const [modelError, setModelError] = useState<string | null>(null);
  const [modelRefresh, setModelRefresh] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef<AbortController | null>(null);
  const readVersion = useRef(0);
  useEffect(() => {
    const controller = new AbortController();
    setModelStatus(null);
    setModelError(null);
    setAnalyzeImported(false);
    void getProfileStatus(token, controller.signal).then(
      (value) => {
        if (!controller.signal.aborted) setModelStatus(value);
      },
      (failure: unknown) => {
        if (controller.signal.aborted) return;
        if (failure instanceof ServiceError && failure.status === 401)
          onAuthFailure();
        else setModelError('无法确认模型配置；你仍可取消AI整理后仅导入记录。');
      },
    );
    return () => controller.abort();
  }, [token, modelRefresh, onAuthFailure]);
  useEffect(
    () => () => {
      readVersion.current++;
      pending.current?.abort();
    },
    [],
  );
  const resetPreview = () => {
    readVersion.current++;
    setPreview(null);
    setError(null);
    setResult(null);
    setAnalysisNotice(null);
    setAnalyzeImported(false);
    setState('editing');
  };

  const prepare = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!file) {
      setError('请先选择需要导入的 JSON 文件。');
      return;
    }
    const version = ++readVersion.current;
    setError(null);
    setPreview(null);
    setState('reading');
    try {
      if (file.size > 8 * 1024 * 1024)
        throw new Error('文件超过 8 MiB，请拆分后导入。');
      let source: unknown;
      try {
        source = JSON.parse(await file.text());
      } catch {
        throw new Error('文件不是有效的 JSON，请检查导出格式。');
      }
      if (version !== readVersion.current) return;
      const isTelegram =
        typeof source === 'object' &&
        source !== null &&
        !Array.isArray(source) &&
        (source as Record<string, unknown>).type === 'personal_chat';
      if (format === 'telegram' || isTelegram) {
        const data = source as Record<string, unknown>;
        if (!isTelegram || !Array.isArray(data.messages))
          throw new Error(
            '请上传 Telegram Desktop 导出的单个 personal_chat JSON，暂不支持完整账号或群聊导出。',
          );
        if (
          !/^[1-9]\d{0,127}$/.test(account) ||
          Array.from(accountName).length > 128
        )
          throw new Error(
            '请填写本人 Telegram 数字账号 ID 和不超过 128 字的账号名称，不能使用用户名推测身份。',
          );
        if (
          data.messages.length === 0 ||
          data.messages.length > Math.min(limit, 1000)
        )
          throw new Error(
            `文件需要包含 1 至 ${Math.min(limit, 1000)} 条原始记录，请拆分后导入。`,
          );
        setFormat('telegram');
        setPlatform('telegram');
        setPreview({
          mode: 'telegram',
          request: {
            account_id: account,
            account_name: accountName,
            export: data,
          },
          count: data.messages.length,
        });
      } else {
        const request = parseImport(
          source,
          {
            platform,
            account_id: account.trim(),
            account_name: accountName.trim(),
          },
          limit,
        );
        setPlatform(request.platform);
        setAccount(request.account_id);
        setAccountName(request.account_name);
        setPreview({
          mode: 'standard',
          request,
          count: request.messages.length,
        });
      }
    } catch (failure: unknown) {
      if (version === readVersion.current) setError(messageOf(failure));
    } finally {
      if (version === readVersion.current) setState('editing');
    }
  };
  const submit = async () => {
    if (!preview || state === 'importing') return;
    const controller = new AbortController();
    pending.current = controller;
    setError(null);
    setState('importing');
    try {
      if (
        analyzeImported &&
        (!modelStatus?.model_configured || !modelStatus.model_config_id)
      ) {
        throw new Error('请先配置并检查模型，或取消AI整理后仅导入记录。');
      }
      const consent =
        analyzeImported && modelStatus?.model_config_id
          ? {
              ai_organize: {
                confirmed: true as const,
                model_config_id: modelStatus.model_config_id,
              },
            }
          : {};
      const response =
        preview.mode === 'standard'
          ? await importMessages(token, controller.signal, {
              ...preview.request,
              ...consent,
            })
          : await importTelegram(token, controller.signal, {
              ...preview.request,
              ...consent,
            });
      if (controller.signal.aborted) return;
      if (analyzeImported) {
        setAnalysisNotice({
          queued: response.ai_queued ?? 0,
          text: `已保存本次导入及 ${response.ai_queued ?? 0} 个会话的后台分析任务。后台会分批读取完整历史，结果可在联系人档案查看；额度用尽会等待次日继续。`,
        });
      }
      setResult(response);
      setPreview(null);
      setFile(null);
      setState('done');
      onImported();
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(messageOf(failure));
      setState('editing');
    } finally {
      if (pending.current === controller) pending.current = null;
    }
  };
  const cancel = () => {
    pending.current?.abort();
    pending.current = null;
    setState('editing');
    setPreview(null);
    onImported();
    setError(
      '已取消等待，后台可能已处理导入。请先刷新列表确认，再决定是否重试。',
    );
  };
  const locked = state === 'importing' || state === 'reading';
  return (
    <section
      className="inbox-import"
      aria-labelledby="import-heading"
      aria-busy={locked}
    >
      <div className="inbox-import-heading">
        <div>
          <h2 id="import-heading" className="icon-label">
            <Icon name="import" size={20} />
            导入已有消息
          </h2>
        </div>
        <button
          className="inbox-icon-button"
          onClick={() => {
            if (!pending.current) onClose();
          }}
          disabled={state === 'importing'}
          title="关闭导入"
          aria-label="关闭导入"
        >
          <Icon name="close" />
        </button>
      </div>
      <form onSubmit={prepare}>
        <fieldset disabled={locked} className="inbox-import-fields">
          <legend className="inbox-sr-only">导入来源</legend>
          <label>
            文件格式
            <select
              value={format}
              onChange={(event) => {
                resetPreview();
                const mode = event.target.value as typeof format;
                setFormat(mode);
                if (mode === 'telegram') setPlatform('telegram');
              }}
            >
              <option value="standard">统一 JSON 格式</option>
              <option value="telegram">Telegram Desktop 导出</option>
            </select>
          </label>
          <label>
            来源平台
            <select
              value={platform}
              disabled={format === 'telegram'}
              onChange={(event) => {
                resetPreview();
                setPlatform(event.target.value as Platform);
              }}
            >
              {PLATFORMS.map((item) => (
                <option key={item} value={item}>
                  {PLATFORM_NAMES[item]}
                </option>
              ))}
            </select>
          </label>
          <label>
            {format === 'telegram'
              ? '本人 Telegram 数字账号 ID'
              : '本人账号 ID'}
            <input
              value={account}
              onChange={(event) => {
                resetPreview();
                setAccount(event.target.value);
              }}
              autoComplete="off"
              placeholder={
                format === 'telegram'
                  ? '填写数字 ID，不是用户名'
                  : '用于区分不同账号'
              }
            />
          </label>
          <label>
            账号名称
            <input
              value={accountName}
              onChange={(event) => {
                resetPreview();
                setAccountName(event.target.value);
              }}
              autoComplete="off"
              placeholder="便于识别的名称"
            />
          </label>
          <label className="inbox-file-field">
            JSON 文件
            <input
              type="file"
              accept=".json,application/json"
              onChange={(event: ChangeEvent<HTMLInputElement>) => {
                resetPreview();
                setFile(event.target.files?.[0] ?? null);
              }}
            />
          </label>
        </fieldset>
        <HelpDetails label="导入说明与格式" className="inbox-format-help">
          <p>仅导入你有权使用的记录；这不会向任何联系人发送消息。</p>
          <p>
            最多 8 MiB、{Math.min(limit, 1000)} 条记录；正文每条最多 16,000
            UTF-8 字节。
          </p>
          {format === 'standard' ? (
            <>
              <p>
                上传 messages 数组时使用上方来源；也可上传包含
                platform、account_id、account_name、messages
                的完整对象，确认页会展示文件内来源。
              </p>
              <pre>
                {
                  '[{\n  "external_id": "原平台消息ID",\n  "peer_id": "对方账号ID",\n  "peer_name": "联系人名称",\n  "direction": "incoming",\n  "body": "消息正文",\n  "occurred_at": "2026-11-12T08:00:00+08:00"\n}]'
                }
              </pre>
              <p>
                direction 仅支持 incoming /
                outgoing；时间必须包含时区。相同平台、账号、消息标识用于检查重复。原始文件只在本机解析，只有明确勾选AI整理才提交会话内容。
              </p>
            </>
          ) : (
            <p className="inbox-form-note">
              仅支持 Telegram Desktop 单个私聊的 JSON
              导出。附件、服务消息及不支持记录的跳过数量，会在导入结果中列出。
            </p>
          )}
        </HelpDetails>
        {!preview && state !== 'done' ? (
          <button
            type="submit"
            className="button button-secondary"
            disabled={locked || !file}
          >
            {state === 'reading' ? '正在检查文件…' : '检查文件与来源'}
            <Icon name="arrow" size={16} />
          </button>
        ) : null}
      </form>
      {preview ? (
        <div className="inbox-import-preview">
          <h3 className="icon-label">
            <Icon name="eye" />
            确认本次导入
          </h3>
          <dl>
            <div>
              <dt>来源</dt>
              <dd>
                {
                  PLATFORM_NAMES[
                    preview.mode === 'telegram'
                      ? 'telegram'
                      : preview.request.platform
                  ]
                }{' '}
                · {preview.request.account_name || '未命名账号'}
              </dd>
            </div>
            <div>
              <dt>本人账号</dt>
              <dd>{preview.request.account_id}</dd>
            </div>
            <div>
              <dt>{preview.mode === 'telegram' ? '原始记录' : '消息数量'}</dt>
              <dd>{preview.count} 条</dd>
            </div>
            <div>
              <dt>保存方式</dt>
              <dd>本地导入，不开启实时连接</dd>
            </div>
          </dl>
          <p className="inbox-form-note">
            分析模型：
            {modelStatus?.model_configured
              ? `${modelStatus.provider ?? '兼容接口'} · ${modelStatus.model ?? ''} · ${modelStatus.endpoint_host ?? ''}`
              : '尚未配置或未确认'}
            。
            <button
              className="email-text-button"
              type="button"
              disabled={locked}
              onClick={() => setModelRefresh((value) => value + 1)}
            >
              检查模型状态
            </button>
          </p>
          {modelError ? (
            <p className="inbox-inline-error">{modelError}</p>
          ) : null}
          <label className="inbox-consent-row">
            <input
              type="checkbox"
              checked={analyzeImported}
              disabled={locked || !modelStatus?.model_configured}
              onChange={(event) => setAnalyzeImported(event.target.checked)}
            />
            <span>
              允许上述模型分批读取本次会话的昵称、平台账号ID、聊天文本和已保存档案，分析完整历史并继续整理这些会话的新记录。
              分类、摘要和事实候选写入对应联系人档案，新事实仍需核实；可在档案中暂停。
            </span>
          </label>
          <div className="inbox-inline-actions">
            <button
              className="button button-primary"
              onClick={submit}
              disabled={state === 'importing'}
            >
              {state === 'importing' ? '正在导入…' : '确认导入'}
              <Icon name="import" />
            </button>
            {state === 'importing' ? (
              <button className="button button-quiet" onClick={cancel}>
                取消等待
              </button>
            ) : (
              <button
                className="button button-quiet"
                onClick={() => setPreview(null)}
              >
                返回修改
              </button>
            )}
          </div>
        </div>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {error ? (
          <p className="inbox-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
        {result || analysisNotice ? (
          <HelpDetails label="导入结果">
            {result ? (
              <p className="inbox-feedback is-success">
                已导入 {result.inserted} 条，重复 {result.duplicates} 条，涉及{' '}
                {result.conversations} 个会话。
                {result.skipped !== undefined
                  ? `跳过 ${result.skipped} 条附件或不支持记录。`
                  : ''}
              </p>
            ) : null}
            {analysisNotice ? (
              <p className="inbox-feedback is-success">{analysisNotice.text}</p>
            ) : null}
          </HelpDetails>
        ) : null}
        {analysisNotice && analysisNotice.queued > 0 ? (
          <p className="inbox-feedback" role="status">
            AI 整理已排队：{analysisNotice.queued} 个会话；进度见联系人档案。
          </p>
        ) : null}
      </div>
    </section>
  );
}

export default function Inbox({
  token,
  aiDrafts,
  onSettings,
  onAuthFailure,
  onAssistantHelp,
  profilesOnly = false,
}: {
  token: string;
  aiDrafts: Map<string, AIDraftBuffer>;
  onSettings: () => void;
  onAuthFailure: () => void;
  onAssistantHelp: (request: AssistantHelpRequest) => void;
  profilesOnly?: boolean;
}) {
  const [info, setInfo] = useState<InboxInfo | null>(null);
  const [infoState, setInfoState] = useState<LoadState>('idle');
  const [infoError, setInfoError] = useState<string | null>(null);
  const [platform, setPlatform] = useState<Platform | undefined>();
  const [category, setCategory] = useState<ProfileCategory | undefined>();
  const [view, setView] = useState<View>('all');
  const [kind, setKind] = useState<ConversationKind | undefined>('private');
  const [searchScope, setSearchScope] = useState<'list' | 'current'>('list');
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [messageSearch, setMessageSearch] = useState('');
  const [messageQuery, setMessageQuery] = useState('');
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [listState, setListState] = useState<LoadState>('idle');
  const [listError, setListError] = useState<string | null>(null);
  const [listCursor, setListCursor] = useState<string | null>(null);
  const [listMore, setListMore] = useState(false);
  const [selected, setSelected] = useState<Conversation | null>(null);
  const canSearchCurrent = Boolean(selected && !profilesOnly);
  const searchCurrent = canSearchCurrent && searchScope === 'current';
  const searchValue = searchCurrent ? messageSearch : search;
  const searchError = keywordError(searchValue);
  const [aiOpen, setAiOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(profilesOnly);
  const [messages, setMessages] = useState<InboxMessage[]>([]);
  const [messageState, setMessageState] = useState<LoadState>('idle');
  const [messageError, setMessageError] = useState<string | null>(null);
  const [messageCursor, setMessageCursor] = useState<string | null>(null);
  const [historyMore, setHistoryMore] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [notice, setNotice] = useState<{
    text: string;
    kind: 'info' | 'success' | 'error';
  } | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [revision, setRevision] = useState(0);
  const [backgroundPending, setBackgroundPending] = useState(false);
  const [wechatAttention, setWechatAttention] = useState(false);
  const [emailAttention, setEmailAttention] = useState(false);
  const backgroundRequest = useRef<AbortController | null>(null);
  const listExpanded = useRef(false);
  const historyExpanded = useRef(false);
  const listRequest = useRef<AbortController | null>(null);
  const historyRequest = useRef<AbortController | null>(null);
  const actionRequest = useRef<AbortController | null>(null);
  const messageViewport = useRef<HTMLDivElement | null>(null);
  const detailHeader = useRef<HTMLElement | null>(null);
  const selectedTrigger = useRef<HTMLButtonElement | null>(null);
  const searchField = useRef<HTMLInputElement | null>(null);
  const previousSelectedId = useRef<string | undefined>(undefined);
  const restoreListFocus = useRef(false);
  const scrollMode = useRef<
    { kind: 'bottom' } | { kind: 'prepend'; height: number }
  >({ kind: 'bottom' });
  const enabled = Boolean(token && info?.storage_enabled);
  const selectedId = selected?.id;
  const backgroundView = useRef({
    aiOpen,
    profileOpen,
    actionBusy,
    historyMore,
    listMore,
  });
  backgroundView.current = {
    aiOpen,
    profileOpen,
    actionBusy,
    historyMore,
    listMore,
  };
  const backgroundContext = JSON.stringify([
    token,
    platform,
    category,
    kind,
    query,
    messageQuery,
    view,
    selectedId,
    revision,
  ]);
  const backgroundContextRef = useRef(backgroundContext);
  backgroundContextRef.current = backgroundContext;
  useEffect(
    () => () => {
      backgroundRequest.current?.abort();
      backgroundRequest.current = null;
    },
    [
      token,
      platform,
      category,
      kind,
      query,
      messageQuery,
      view,
      selectedId,
      revision,
    ],
  );
  useLayoutEffect(() => {
    if (selectedId) detailHeader.current?.focus({ preventScroll: true });
    else if (previousSelectedId.current && restoreListFocus.current) {
      if (selectedTrigger.current?.isConnected)
        selectedTrigger.current.focus({ preventScroll: true });
      else searchField.current?.focus({ preventScroll: true });
    }
    previousSelectedId.current = selectedId;
    restoreListFocus.current = false;
  }, [selectedId]);
  useEffect(
    () => () => {
      listRequest.current?.abort();
      historyRequest.current?.abort();
      actionRequest.current?.abort();
    },
    [],
  );
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(search.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [search]);
  useEffect(() => {
    const timer = window.setTimeout(
      () => setMessageQuery(messageSearch.trim()),
      250,
    );
    return () => window.clearTimeout(timer);
  }, [messageSearch]);
  useLayoutEffect(() => {
    const viewport = messageViewport.current;
    if (!viewport) return;
    viewport.scrollTop =
      scrollMode.current.kind === 'prepend'
        ? viewport.scrollTop + viewport.scrollHeight - scrollMode.current.height
        : viewport.scrollHeight;
    scrollMode.current = { kind: 'bottom' };
  }, [messages]);
  const authFailed = (failure: unknown) =>
    failure instanceof ServiceError && failure.status === 401;
  const refresh = () => {
    setBackgroundPending(false);
    setListCursor(null);
    setMessageCursor(null);
    setRevision((value) => value + 1);
  };
  const refreshBackground = async (profileChange = false) => {
    if (
      !enabled ||
      keywordError(query) ||
      keywordError(messageQuery) ||
      backgroundRequest.current
    )
      return;
    const controller = new AbortController();
    backgroundRequest.current = controller;
    const atLatest = () => {
      const viewport = messageViewport.current;
      return (
        !historyExpanded.current &&
        !backgroundView.current.aiOpen &&
        !backgroundView.current.profileOpen &&
        !backgroundView.current.actionBusy &&
        !backgroundView.current.historyMore &&
        (!viewport ||
          viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <
            48)
      );
    };
    const updateList =
      listState === 'ready' &&
      !listExpanded.current &&
      !backgroundView.current.listMore &&
      !backgroundView.current.actionBusy;
    const updateMessages =
      !profilesOnly &&
      !profileChange &&
      Boolean(selectedId) &&
      messageState === 'ready' &&
      atLatest();
    if (
      !updateList ||
      (!profilesOnly && !profileChange && selectedId && !updateMessages)
    )
      setBackgroundPending(true);
    try {
      const [infoResult, listResult, messageResult] = await Promise.allSettled([
        getInboxInfo(token, controller.signal),
        updateList
          ? getConversations(token, controller.signal, {
              platform,
              conversation_kind: kind,
              category,
              q: query,
              unread: view === 'unread',
              pinned: view === 'pinned',
              archived: view === 'archived',
            })
          : Promise.resolve(null),
        updateMessages && selectedId
          ? getConversationMessages(
              token,
              controller.signal,
              selectedId,
              undefined,
              messageQuery,
            )
          : Promise.resolve(null),
      ]);
      if (
        controller.signal.aborted ||
        backgroundContextRef.current !== backgroundContext
      )
        return;
      for (const result of [infoResult, listResult, messageResult]) {
        if (result.status === 'rejected') {
          if (authFailed(result.reason)) {
            onAuthFailure();
            return;
          }
          setBackgroundPending(true);
        }
      }
      if (infoResult.status === 'fulfilled') setInfo(infoResult.value);
      if (
        listResult.status === 'fulfilled' &&
        listResult.value &&
        !listExpanded.current &&
        !backgroundView.current.listMore &&
        !backgroundView.current.actionBusy
      ) {
        const data = listResult.value;
        setConversations(data.items);
        setListCursor(data.next_cursor);
        setSelected((current) =>
          current
            ? (data.items.find((item) => item.id === current.id) ?? current)
            : null,
        );
      } else if (listResult.status === 'fulfilled' && listResult.value)
        setBackgroundPending(true);
      if (messageResult.status === 'fulfilled' && messageResult.value) {
        if (atLatest()) {
          scrollMode.current = { kind: 'bottom' };
          setMessages(messageResult.value.items);
          setMessageCursor(messageResult.value.next_cursor);
        } else setBackgroundPending(true);
      }
    } finally {
      if (backgroundRequest.current === controller)
        backgroundRequest.current = null;
    }
  };
  useEffect(() => {
    if (!token) return;
    const controller = new AbortController();
    setInfoState('loading');
    setInfoError(null);
    getInboxInfo(token, controller.signal)
      .then((data) => {
        if (controller.signal.aborted) return;
        setInfo(data);
        setInfoState('ready');
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted) return;
        if (authFailed(failure)) {
          onAuthFailure();
          return;
        }
        setInfo(null);
        setInfoState('error');
        setInfoError(messageOf(failure));
      });
    return () => controller.abort();
  }, [token, revision, onAuthFailure]);
  useEffect(() => {
    listExpanded.current = false;
    listRequest.current?.abort();
    setConversations([]);
    setListCursor(null);
    setListMore(false);
    setListError(null);
    if (!enabled) {
      setListState('idle');
      return;
    }
    const invalidKeyword = keywordError(query);
    if (invalidKeyword) {
      setListState('error');
      setListError(invalidKeyword);
      return;
    }
    const controller = new AbortController();
    listRequest.current = controller;
    setListState('loading');
    getConversations(token, controller.signal, {
      platform,
      conversation_kind: kind,
      category,
      q: query,
      unread: view === 'unread',
      pinned: view === 'pinned',
      archived: view === 'archived',
    })
      .then((data) => {
        if (controller.signal.aborted) return;
        setConversations(data.items);
        setListCursor(data.next_cursor);
        setListState('ready');
        setSelected((current) =>
          current
            ? (data.items.find((item) => item.id === current.id) ??
              (backgroundView.current.aiOpen ||
              backgroundView.current.profileOpen
                ? current
                : null))
            : null,
        );
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted) return;
        if (authFailed(failure)) {
          onAuthFailure();
          return;
        }
        setListState('error');
        setListError(messageOf(failure));
      });
    return () => controller.abort();
  }, [
    enabled,
    token,
    platform,
    category,
    kind,
    query,
    view,
    revision,
    onAuthFailure,
  ]);
  useEffect(() => {
    historyExpanded.current = false;
    historyRequest.current?.abort();
    setMessages([]);
    setMessageCursor(null);
    setHistoryMore(false);
    setMessageError(null);
    if (!enabled || !selectedId || profilesOnly) {
      setMessageState('idle');
      return;
    }
    const invalidKeyword = keywordError(messageQuery);
    if (invalidKeyword) {
      setMessageState('error');
      setMessageError(invalidKeyword);
      return;
    }
    const controller = new AbortController();
    historyRequest.current = controller;
    setMessageState('loading');
    scrollMode.current = { kind: 'bottom' };
    getConversationMessages(
      token,
      controller.signal,
      selectedId,
      undefined,
      messageQuery,
    )
      .then((data) => {
        if (controller.signal.aborted) return;
        setMessages(data.items);
        setMessageCursor(data.next_cursor);
        setMessageState('ready');
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted) return;
        if (authFailed(failure)) {
          onAuthFailure();
          return;
        }
        setMessageState('error');
        setMessageError(messageOf(failure));
      });
    return () => controller.abort();
  }, [
    enabled,
    token,
    selectedId,
    messageQuery,
    revision,
    profilesOnly,
    onAuthFailure,
  ]);
  const moreConversations = async () => {
    if (!enabled || !listCursor || listMore) return;
    const controller = new AbortController();
    listRequest.current?.abort();
    listRequest.current = controller;
    setListMore(true);
    setListError(null);
    try {
      const data = await getConversations(token, controller.signal, {
        platform,
        conversation_kind: kind,
        category,
        q: query,
        unread: view === 'unread',
        pinned: view === 'pinned',
        archived: view === 'archived',
        cursor: listCursor,
      });
      if (controller.signal.aborted) return;
      listExpanded.current = true;
      setConversations((current) => unique([...current, ...data.items]));
      setListCursor(data.next_cursor);
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (authFailed(failure)) {
        onAuthFailure();
        return;
      }
      if (
        failure instanceof ServiceError &&
        failure.code === 'INVALID_CURSOR'
      ) {
        refresh();
        setNotice({ kind: 'info', text: '数据已变化，已重新读取第一页。' });
      } else setListError(messageOf(failure));
    } finally {
      if (!controller.signal.aborted) setListMore(false);
    }
  };
  const olderMessages = async () => {
    if (!enabled || !selectedId || !messageCursor || historyMore) return;
    const controller = new AbortController();
    historyRequest.current?.abort();
    historyRequest.current = controller;
    setHistoryMore(true);
    setMessageError(null);
    try {
      const data = await getConversationMessages(
        token,
        controller.signal,
        selectedId,
        messageCursor,
        messageQuery,
      );
      if (controller.signal.aborted) return;
      historyExpanded.current = true;
      scrollMode.current = {
        kind: 'prepend',
        height: messageViewport.current?.scrollHeight ?? 0,
      };
      setMessages((current) => unique([...data.items, ...current]));
      setMessageCursor(data.next_cursor);
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (authFailed(failure)) {
        onAuthFailure();
        return;
      }
      if (
        failure instanceof ServiceError &&
        failure.code === 'INVALID_CURSOR'
      ) {
        refresh();
        setNotice({ kind: 'info', text: '消息已变化，已重新读取最新一页。' });
      } else setMessageError(messageOf(failure));
    } finally {
      if (!controller.signal.aborted) setHistoryMore(false);
    }
  };
  const change = async (changes: {
    read?: boolean;
    pinned?: boolean;
    archived?: boolean;
  }) => {
    if (!selected || actionBusy) return;
    const current = selected;
    const controller = new AbortController();
    actionRequest.current = controller;
    setActionBusy(true);
    setNotice(null);
    try {
      await updateConversation(token, controller.signal, current.id, changes);
      if (controller.signal.aborted) return;
      if (changes.archived !== undefined)
        setSelected((latest) => (latest?.id === current.id ? null : latest));
      setNotice({
        kind: 'success',
        text:
          changes.read !== undefined
            ? changes.read
              ? '已标为已读。'
              : '已标为未读。'
            : changes.pinned !== undefined
              ? changes.pinned
                ? '会话已置顶。'
                : '已取消置顶。'
              : changes.archived
                ? '会话已归档，可在“已归档”中查看。'
                : '会话已移回收件箱。',
      });
      refresh();
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (authFailed(failure)) {
        onAuthFailure();
        return;
      }
      setNotice({ kind: 'error', text: messageOf(failure) });
    } finally {
      if (actionRequest.current === controller) {
        actionRequest.current = null;
        setActionBusy(false);
      }
    }
  };
  const selectPlatform = (value: Platform | undefined) => {
    setPlatform(value);
    setSelected(null);
    setNotice(null);
  };
  const selectView = (value: View) => {
    setView(value);
    setSelected(null);
    setNotice(null);
  };
  const discoveryID = profilesOnly ? 'contacts' : 'inbox';
  const searchLabel = searchCurrent
    ? '搜索当前会话'
    : profilesOnly
      ? '搜索联系人'
      : '搜索会话与消息';
  const applySearch = (value: string) => {
    if (searchCurrent) setMessageQuery(value);
    else setQuery(value);
  };
  const changeSearch = (value: string) => {
    if (searchCurrent) setMessageSearch(value);
    else {
      setSearch(value);
      if (value) setSelected(null);
    }
  };
  const askAssistant = () =>
    onAssistantHelp({
      source: profilesOnly ? '联系人检索' : '消息检索',
      prompt: [
        '请按以下条件查找已保存的会话。若需读取新来源或写入，请先确认。',
        '所选条件（仅供查询）：',
        `会话类型：${kind === 'group' ? '群聊' : kind === 'private' ? '私聊' : '全部'}`,
        `平台：${platform ? PLATFORM_NAMES[platform] : '全部'}`,
        `关系分类：${category ? CATEGORY_NAMES[category] : '全部'}`,
        `会话视图：${VIEWS.find((item) => item.value === view)?.name}`,
        `列表关键词：${query ? JSON.stringify(query) : '无'}`,
        `所选会话 ID：${selected ? JSON.stringify(selected.id) : '无'}`,
        `正文关键词：${selected && messageQuery ? JSON.stringify(messageQuery) : '无'}`,
      ].join('\n'),
    });
  return (
    <div className="inbox-root">
      <div className="inbox-title-row">
        <div>
          <h1 className="icon-label">
            <Icon name={profilesOnly ? 'contacts' : 'messages'} size={22} />
            {profilesOnly ? '联系人' : '消息'}
          </h1>
        </div>
        <div className="inbox-header-actions">
          <button
            className="inbox-icon-button"
            aria-label={backgroundPending ? '查看最新消息' : '刷新消息中心'}
            title={backgroundPending ? '查看最新消息' : '刷新消息中心'}
            onClick={refresh}
            disabled={!token || infoState === 'loading'}
          >
            <Icon name="refresh" />
          </button>
          <button
            className="button button-primary"
            onClick={() => setImportOpen(true)}
            disabled={!enabled}
          >
            <Icon name="import" />
            导入消息
          </button>
        </div>
      </div>
      <section
        className="inbox-discovery"
        aria-label={profilesOnly ? '联系人查找' : '消息查找'}
      >
        <form
          className="inbox-keyword-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (enabled && !searchError) applySearch(searchValue.trim());
          }}
        >
          <div className="inbox-keyword-row">
            <ConversationKindSelect
              id={`${discoveryID}-kind`}
              value={kind ?? 'all'}
              includeAll
              onChange={(value) => {
                setKind(value === 'all' ? undefined : value);
                setSelected(null);
                setNotice(null);
              }}
            />
            {canSearchCurrent ? (
              <select
                id="inbox-search-scope"
                aria-label="搜索范围"
                value={searchScope}
                onChange={(event) =>
                  setSearchScope(
                    event.target.value === 'current' ? 'current' : 'list',
                  )
                }
              >
                <option value="list">会话列表</option>
                <option value="current">当前会话</option>
              </select>
            ) : null}
            <div className="inbox-keyword-field">
              <Icon name="search" size={16} />
              <input
                id={`${discoveryID}-keyword`}
                type="search"
                ref={searchField}
                aria-label={searchLabel}
                placeholder={searchCurrent ? '搜索正文' : '名称或消息正文'}
                value={searchValue}
                onChange={(event) => changeSearch(event.target.value)}
                autoComplete="off"
                aria-invalid={Boolean(searchError)}
                aria-describedby={
                  searchError ? `${discoveryID}-keyword-error` : undefined
                }
              />
              {searchValue ? (
                <button
                  className="inbox-icon-button"
                  type="button"
                  aria-label="清除搜索关键词"
                  title="清除搜索关键词"
                  onClick={() => {
                    changeSearch('');
                    applySearch('');
                    searchField.current?.focus({ preventScroll: true });
                  }}
                >
                  <Icon name="close" />
                </button>
              ) : null}
            </div>
            <button
              type="button"
              className="button button-secondary"
              onClick={askAssistant}
            >
              <Icon name="assistant" />
              Agent 帮我
            </button>
          </div>
          {searchError ? (
            <p
              id={`${discoveryID}-keyword-error`}
              className="inbox-keyword-error"
              role="alert"
            >
              {searchError}
            </p>
          ) : null}
        </form>
        {token && !profilesOnly ? (
          <details className="inbox-sources">
            <summary>
              消息来源
              <span className="inbox-source-attention" aria-live="polite">
                {wechatAttention ? ' · 微信需检查' : ''}
                {emailAttention ? ' · 邮箱需检查' : ''}
              </span>
            </summary>
            <WechatPanel
              key={`wechat:${token}`}
              token={token}
              storageEnabled={enabled}
              revision={revision}
              onSynced={refresh}
              onBackgroundSynced={() => void refreshBackground()}
              onAuthFailure={onAuthFailure}
              onAssistantHelp={onAssistantHelp}
              onAttentionChange={setWechatAttention}
            />
            <EmailPanel
              key={`email:${token}`}
              token={token}
              storageEnabled={enabled}
              revision={revision}
              onSynced={refresh}
              onAuthFailure={onAuthFailure}
              onAttentionChange={setEmailAttention}
            />
          </details>
        ) : null}
      </section>
      {backgroundPending ? (
        <div className="inbox-notice" role="status">
          <p>后台已有新记录，可刷新查看。</p>
        </div>
      ) : null}
      {notice?.kind === 'success' ? (
        <HelpDetails label="会话操作记录">
          <div className="inbox-notice" role="status">
            <p>{notice.text}</p>
            <button
              className="inbox-icon-button"
              title="关闭提示"
              aria-label="关闭提示"
              onClick={() => setNotice(null)}
            >
              <Icon name="close" size={15} />
            </button>
          </div>
        </HelpDetails>
      ) : notice ? (
        <div
          className="inbox-notice"
          role={notice.kind === 'error' ? 'alert' : 'status'}
        >
          <p>{notice.text}</p>
          <button
            className="inbox-icon-button"
            title="关闭提示"
            aria-label="关闭提示"
            onClick={() => setNotice(null)}
          >
            <Icon name="close" size={15} />
          </button>
        </div>
      ) : null}
      {importOpen && enabled ? (
        <ImportPanel
          key={token}
          token={token}
          limit={info?.import_limit ?? 1000}
          onClose={() => {
            setImportOpen(false);
            refresh();
          }}
          onImported={refresh}
          onAuthFailure={onAuthFailure}
        />
      ) : null}
      {!token ? (
        <section className="inbox-start">
          <h2>先连接你的消息工作台</h2>
          <p>连接本地服务后，就能导入社交记录，按需读取微信并整理邮件。</p>
          <button className="button button-secondary" onClick={onSettings}>
            前往连接设置
            <Icon name="arrow" size={16} />
          </button>
        </section>
      ) : infoState === 'error' ? (
        <section className="inbox-start">
          <h2>消息中心暂时无法读取</h2>
          <p role="alert">{infoError}</p>
        </section>
      ) : !info ? (
        <section className="inbox-start" aria-busy="true">
          <h2>正在读取消息中心…</h2>
          <p>正在检查平台来源与本地存储状态。</p>
        </section>
      ) : !info.storage_enabled ? (
        <section className="inbox-start">
          <h2>消息存储尚未启用</h2>
          <p>消息存储尚未启用。先在设置中查看配置状态，再导入你已有的对话。</p>
          <button className="button button-secondary" onClick={onSettings}>
            查看设置
            <Icon name="arrow" size={16} />
          </button>
        </section>
      ) : (
        <div className={`inbox-layout ${selected ? 'has-selection' : ''}`}>
          <aside className="inbox-filters" aria-label="会话筛选">
            <h2>收件箱</h2>
            <div className="inbox-view-filters">
              {VIEWS.map((item) => (
                <button
                  key={item.value}
                  className={view === item.value ? 'active' : ''}
                  aria-pressed={view === item.value}
                  onClick={() => selectView(item.value)}
                >
                  <Icon name={item.icon} size={16} />
                  <span>{item.name}</span>
                </button>
              ))}
            </div>
            <h3>平台来源</h3>
            <button
              className={`inbox-platform-filter ${platform === undefined ? 'active' : ''}`}
              onClick={() => selectPlatform(undefined)}
              aria-pressed={platform === undefined}
            >
              <span>全部平台</span>
              <span className="inbox-count">
                {info.platforms.reduce(
                  (sum, item) => sum + item.message_count,
                  0,
                )}
              </span>
            </button>
            {PLATFORMS.map((item) => {
              const status = info.platforms.find(
                (source) => source.id === item,
              );
              return (
                <button
                  key={item}
                  className={`inbox-platform-filter ${platform === item ? 'active' : ''}`}
                  onClick={() => selectPlatform(item)}
                  aria-pressed={platform === item}
                >
                  <PlatformMark platform={item} />
                  <span>{PLATFORM_NAMES[item]}</span>
                  <span className="inbox-count">
                    {status?.message_count ?? 0}
                  </span>
                </button>
              );
            })}
            <label className="inbox-category-filter">
              关系分类
              <select
                value={category ?? ''}
                onChange={(event) => {
                  setCategory(
                    (event.target.value || undefined) as
                      ProfileCategory | undefined,
                  );
                  if (
                    !backgroundView.current.aiOpen &&
                    !backgroundView.current.profileOpen
                  )
                    setSelected(null);
                  setNotice(null);
                }}
              >
                <option value="">全部分类</option>
                {PROFILE_CATEGORIES.map((item) => (
                  <option key={item} value={item}>
                    {CATEGORY_NAMES[item]}
                  </option>
                ))}
              </select>
            </label>
            <div className="inbox-live-states">
              {info.platforms.map((item) => (
                <span key={item.id}>
                  {PLATFORM_NAMES[item.id]}
                  <span>
                    {item.live_status === 'probe_only'
                      ? '仅账号核验'
                      : item.live_status === 'unsupported'
                        ? '暂不支持实时接入'
                        : item.live_status === 'configured'
                          ? item.id === 'weixin'
                            ? '已配置·可后台读取'
                            : '已配置·手动同步'
                          : '实时未连接'}
                  </span>
                </span>
              ))}
            </div>
          </aside>
          <section className="inbox-conversations" aria-label="会话列表">
            <div className="inbox-list-header">
              <div className="inbox-list-summary">
                <strong>
                  {platform ? PLATFORM_NAMES[platform] : '全部平台'} ·{' '}
                  {VIEWS.find((item) => item.value === view)?.name}
                </strong>
                <span>
                  {listState === 'ready'
                    ? platform === 'email'
                      ? `${conversations.length} 封邮件`
                      : `${conversations.length} ${profilesOnly ? '位联系人' : '个会话'}`
                    : '正在读取'}
                </span>
              </div>
            </div>
            <div
              className="inbox-conversation-scroll"
              aria-busy={listState === 'loading'}
            >
              {listState === 'loading' ? (
                <p className="inbox-list-message" role="status">
                  正在读取会话…
                </p>
              ) : listState === 'error' ? (
                <div className="inbox-list-message">
                  <p role="alert">{listError}</p>
                </div>
              ) : conversations.length === 0 ? (
                <div className="inbox-list-empty">
                  <h3>
                    {query
                      ? '没有找到相关会话'
                      : view === 'unread'
                        ? '暂时没有未读会话'
                        : view === 'archived'
                          ? '还没有归档的会话'
                          : view === 'pinned'
                            ? '还没有置顶的会话'
                            : '暂无会话记录'}
                  </h3>
                  {view !== 'all' || platform || category ? (
                    <button
                      className="inbox-text-button"
                      onClick={() => {
                        setSearch('');
                        setQuery('');
                        setView('all');
                        setPlatform(undefined);
                        setCategory(undefined);
                      }}
                    >
                      重置筛选
                    </button>
                  ) : null}
                </div>
              ) : (
                <ul className="inbox-conversation-list">
                  {conversations.map((item) => (
                    <li key={item.id}>
                      <button
                        className={`inbox-conversation ${selected?.id === item.id ? 'selected' : ''}`}
                        onClick={(event) => {
                          selectedTrigger.current = event.currentTarget;
                          setAiOpen(false);
                          setProfileOpen(profilesOnly);
                          setSelected(item);
                          setMessageSearch(query);
                          setMessageQuery(query);
                          setNotice(null);
                        }}
                        aria-current={
                          selected?.id === item.id ? 'true' : undefined
                        }
                      >
                        <span className="inbox-avatar">
                          {(item.title || item.peer_id).slice(0, 1)}
                          <span
                            className={`inbox-avatar-source is-${item.platform}`}
                          >
                            {PLATFORM_NAMES[item.platform].slice(0, 1)}
                          </span>
                        </span>
                        <span className="inbox-conversation-copy">
                          <span className="inbox-conversation-top">
                            <strong>
                              <KeywordMatch
                                text={item.title || item.peer_id}
                                keyword={query}
                              />
                            </strong>
                            <time dateTime={item.last_message_at}>
                              {dateText(item.last_message_at)}
                            </time>
                          </span>
                          <span className="inbox-conversation-preview">
                            <KeywordMatch
                              text={item.last_message || '暂无文本消息'}
                              keyword={query}
                            />
                          </span>
                          {query ? (
                            <span className="inbox-match-preview">
                              {item.matched_message ? (
                                <>
                                  <span>正文命中：</span>
                                  <KeywordMatch
                                    text={item.matched_message}
                                    keyword={query}
                                  />
                                </>
                              ) : (
                                '名称或资料匹配 · 打开核对正文'
                              )}
                            </span>
                          ) : null}
                          <span className="inbox-conversation-meta">
                            <span className="inbox-category-label">
                              {CATEGORY_NAMES[item.category ?? 'unclassified']}
                            </span>
                            <span>
                              {PLATFORM_NAMES[item.platform]} ·{' '}
                              {item.platform === 'email'
                                ? item.account_name || '邮箱账号'
                                : item.account_id}
                            </span>
                            {item.pinned ? (
                              <span className="inbox-pin-label">
                                <Icon name="pin" size={11} />
                                置顶
                              </span>
                            ) : null}
                            {item.unread_count > 0 ? (
                              <span
                                className="inbox-unread-badge"
                                aria-label={`${item.unread_count} 条未读`}
                              >
                                {item.unread_count > 99
                                  ? '99+'
                                  : item.unread_count}
                              </span>
                            ) : null}
                          </span>
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {listError && listState !== 'error' ? (
                <p className="inbox-inline-error" role="alert">
                  {listError}
                </p>
              ) : null}
              {listCursor ? (
                <button
                  className="inbox-load-more"
                  onClick={moreConversations}
                  disabled={listMore}
                >
                  {listMore ? '正在读取…' : '加载更多会话'}
                </button>
              ) : null}
            </div>
          </section>
          <section className="inbox-detail" aria-label="会话详情">
            {selected ? (
              <>
                <header
                  className="inbox-detail-header"
                  ref={detailHeader}
                  tabIndex={-1}
                >
                  <button
                    className="inbox-icon-button inbox-back"
                    onClick={() => {
                      restoreListFocus.current = true;
                      setSelected(null);
                    }}
                    aria-label="返回会话列表"
                    title="返回会话列表"
                  >
                    <Icon name="back" />
                  </button>
                  <PlatformMark platform={selected.platform} />
                  <div>
                    <h2>{selected.title || selected.peer_id}</h2>
                    <p>
                      {PLATFORM_NAMES[selected.platform]} ·{' '}
                      {selected.platform === 'email'
                        ? selected.account_name || '邮箱账号'
                        : selected.account_id}{' '}
                      ·{' '}
                      {selected.platform === 'email'
                        ? '本地邮件记录'
                        : '已入库消息'}
                    </p>
                  </div>
                </header>
                <div
                  className="inbox-conversation-actions"
                  aria-label="管理会话"
                  aria-busy={actionBusy}
                >
                  <button
                    onClick={() => change({ read: selected.unread_count > 0 })}
                    disabled={actionBusy}
                  >
                    <Icon
                      name={selected.unread_count > 0 ? 'mail-read' : 'mail'}
                    />
                    {selected.unread_count > 0 ? '标为已读' : '标为未读'}
                  </button>
                  <button
                    onClick={() => change({ pinned: !selected.pinned })}
                    disabled={actionBusy}
                  >
                    <Icon name={selected.pinned ? 'unpin' : 'pin'} />
                    {selected.pinned ? '取消置顶' : '置顶'}
                  </button>
                  <button
                    onClick={() => change({ archived: !selected.archived })}
                    disabled={actionBusy}
                  >
                    <Icon name={selected.archived ? 'unarchive' : 'archive'} />
                    {selected.archived ? '移回收件箱' : '归档'}
                  </button>
                  {!profilesOnly ? (
                    <button
                      onClick={() => setAiOpen((value) => !value)}
                      aria-expanded={aiOpen}
                      aria-controls={`ai-management-${selected.id}`}
                    >
                      <Icon name="settings" />
                      AI 管理
                    </button>
                  ) : null}
                  {!profilesOnly ? (
                    <button
                      onClick={() => setProfileOpen((value) => !value)}
                      aria-label="个人档案"
                      aria-expanded={profileOpen}
                      aria-controls={`personal-profile-${selected.id}`}
                    >
                      <Icon name="user" />
                      档案
                    </button>
                  ) : null}
                </div>
                <div
                  className="inbox-history"
                  ref={messageViewport}
                  aria-busy={messageState === 'loading'}
                >
                  {profileOpen ? (
                    <ProfilePanel
                      key={`${token}:${selected.id}`}
                      token={token}
                      conversationId={selected.id}
                      displayName={selected.title || selected.peer_id}
                      onAuthFailure={onAuthFailure}
                      onChanged={() => void refreshBackground(true)}
                    />
                  ) : null}
                  {profilesOnly ? null : messageState === 'loading' ? (
                    <p className="inbox-history-note" role="status">
                      正在读取消息…
                    </p>
                  ) : messageState === 'error' ? (
                    <div className="inbox-history-note">
                      <p role="alert">{messageError}</p>
                    </div>
                  ) : (
                    <>
                      {messageQuery && !searchCurrent ? (
                        <p className="inbox-history-note" role="status">
                          正文包含「{messageQuery}」
                        </p>
                      ) : null}
                      {messageCursor ? (
                        <button
                          className="inbox-load-more"
                          disabled={historyMore}
                          onClick={olderMessages}
                        >
                          {historyMore ? '正在读取更早消息…' : '加载更早消息'}
                        </button>
                      ) : (
                        <div className="inbox-history-note" role="status">
                          <p>
                            {messages.length
                              ? messageQuery
                                ? '已显示最早的正文匹配记录'
                                : '已显示当前保存的最早记录'
                              : messageQuery
                                ? '当前会话没有匹配的正文。'
                                : '这个会话还没有消息'}
                          </p>
                        </div>
                      )}
                      {messages.map((item) => (
                        <article
                          className={`inbox-message is-${item.direction}`}
                          key={item.id}
                        >
                          <div className="inbox-message-caption">
                            <span>
                              {item.direction === 'outgoing'
                                ? '我 · 历史记录'
                                : selected.platform === 'email'
                                  ? item.email?.from_name ||
                                    item.email?.from_address ||
                                    '邮件记录'
                                  : selected.title || selected.peer_id}
                            </span>
                            <time dateTime={item.occurred_at}>
                              {dateText(item.occurred_at, true)}
                            </time>
                          </div>
                          {item.email ? (
                            <dl className="email-message-metadata">
                              <div>
                                <dt>主题</dt>
                                <dd>{item.email.subject || '无主题'}</dd>
                              </div>
                              <div>
                                <dt>发件人</dt>
                                <dd>
                                  {item.email.from_name
                                    ? `${item.email.from_name} · `
                                    : ''}
                                  {item.email.from_address || '未提供'}
                                </dd>
                              </div>
                              {item.email.to.length > 0 ? (
                                <div>
                                  <dt>收件人</dt>
                                  <dd>{item.email.to.join('、')}</dd>
                                </div>
                              ) : null}
                            </dl>
                          ) : null}
                          <p>
                            <KeywordMatch
                              text={item.body}
                              keyword={messageQuery}
                            />
                          </p>
                        </article>
                      ))}
                      {messageError ? (
                        <p className="inbox-inline-error" role="alert">
                          {messageError}
                        </p>
                      ) : null}
                      {aiOpen ? (
                        <div id={`ai-management-${selected.id}`}>
                          <AIPanel
                            key={`${token}:${selected.id}`}
                            token={token}
                            conversationId={selected.id}
                            draftCache={aiDrafts}
                            messages={messages}
                            onAuthFailure={onAuthFailure}
                          />
                        </div>
                      ) : null}
                    </>
                  )}
                </div>
                <footer className="inbox-detail-footer">
                  <HelpDetails label="本地消息说明">
                    <p className="icon-label">
                      <Icon name="lock" />
                      <span>
                        {selected.platform === 'email'
                          ? '邮件正文保存在本地；已读、置顶和归档不改变原邮箱，暂不支持发信。'
                          : '本地已入库消息；来源以记录为准，当前不提供消息发送。'}
                      </span>
                    </p>
                  </HelpDetails>
                </footer>
              </>
            ) : (
              <div className="inbox-detail-empty">
                <h2>{profilesOnly ? '选择联系人' : '选择会话'}</h2>
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
