import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ChangeEvent, FormEvent, ReactNode } from 'react';
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

type View = 'all' | 'unread' | 'pinned' | 'archived';
type LoadState = 'idle' | 'loading' | 'ready' | 'error';
type Glyph =
  | 'mail'
  | 'search'
  | 'arrow'
  | 'back'
  | 'import'
  | 'pin'
  | 'archive'
  | 'check'
  | 'close'
  | 'refresh';
const PLATFORMS: Platform[] = ['qq', 'weixin', 'telegram', 'shanliao'];
const VIEWS: { value: View; name: string; icon: Glyph }[] = [
  { value: 'all', name: '全部会话', icon: 'mail' },
  { value: 'unread', name: '未读', icon: 'mail' },
  { value: 'pinned', name: '置顶', icon: 'pin' },
  { value: 'archived', name: '已归档', icon: 'archive' },
];
function Symbol({ name, size = 18 }: { name: Glyph; size?: number }) {
  const paths: Record<Glyph, ReactNode> = {
    mail: (
      <>
        <rect x="3" y="5" width="18" height="14" rx="2" />
        <path d="m3 7 9 6 9-6" />
      </>
    ),
    search: (
      <>
        <circle cx="10" cy="10" r="6" />
        <path d="m15 15 5 5" />
      </>
    ),
    arrow: <path d="M5 12h14m-5-5 5 5-5 5" />,
    back: <path d="M19 12H5m5-5-5 5 5 5" />,
    import: (
      <>
        <path d="M12 3v12m-4-4 4 4 4-4M4 15v5h16v-5" />
      </>
    ),
    pin: (
      <>
        <path d="m9 3 6 0-1 5 4 4v2H6v-2l4-4-1-5ZM12 14v7" />
      </>
    ),
    archive: (
      <>
        <rect x="3" y="3" width="18" height="5" rx="1" />
        <path d="M5 8v12h14V8M10 12h4" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    refresh: (
      <>
        <path d="M20 11a8 8 0 1 0-1 5M20 4v7h-7" />
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
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  );
}
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
  const [error, setError] = useState<string | null>(null);
  const pending = useRef<AbortController | null>(null);
  const readVersion = useRef(0);
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
      const response =
        preview.mode === 'standard'
          ? await importMessages(token, controller.signal, preview.request)
          : await importTelegram(token, controller.signal, preview.request);
      if (controller.signal.aborted) return;
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
          <h2 id="import-heading">把已有消息，整理到一起</h2>
          <p>仅导入你有权使用的记录；这不会向任何联系人发送消息。</p>
        </div>
        <button
          className="inbox-icon-button"
          onClick={onClose}
          title="关闭导入"
          aria-label="关闭导入"
        >
          <Symbol name="close" />
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
            <span>
              最多 8 MiB、{Math.min(limit, 1000)} 条记录；正文每条最多 16,000
              UTF-8 字节。
            </span>
          </label>
        </fieldset>
        {format === 'standard' ? (
          <details className="inbox-format-help">
            <summary>查看统一导入格式</summary>
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
              outgoing；时间必须包含时区。相同平台、账号、消息标识用于检查重复，文件不会交给模型。
            </p>
          </details>
        ) : (
          <p className="inbox-form-note">
            仅支持 Telegram Desktop 单个私聊的 JSON
            导出。附件、服务消息及不支持记录的跳过数量，会在导入结果中列出。
          </p>
        )}
        {!preview && state !== 'done' ? (
          <button
            type="submit"
            className="button button-secondary"
            disabled={locked || !file}
          >
            {state === 'reading' ? '正在检查文件…' : '检查文件与来源'}
            <Symbol name="arrow" size={16} />
          </button>
        ) : null}
      </form>
      {preview ? (
        <div className="inbox-import-preview">
          <h3>确认本次导入</h3>
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
          <div className="inbox-inline-actions">
            <button
              className="button button-primary"
              onClick={submit}
              disabled={state === 'importing'}
            >
              {state === 'importing' ? '正在导入…' : '确认导入'}
              <Symbol name="import" size={16} />
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
        {result ? (
          <p className="inbox-feedback is-success">
            已导入 {result.inserted} 条，重复 {result.duplicates} 条，涉及{' '}
            {result.conversations} 个会话。
            {result.skipped !== undefined
              ? `跳过 ${result.skipped} 条附件或不支持记录。`
              : ''}
          </p>
        ) : null}
      </div>
    </section>
  );
}

export default function Inbox({
  token,
  onSettings,
  onAuthFailure,
}: {
  token: string;
  onSettings: () => void;
  onAuthFailure: () => void;
}) {
  const [info, setInfo] = useState<InboxInfo | null>(null);
  const [infoState, setInfoState] = useState<LoadState>('idle');
  const [infoError, setInfoError] = useState<string | null>(null);
  const [platform, setPlatform] = useState<Platform | undefined>();
  const [view, setView] = useState<View>('all');
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [listState, setListState] = useState<LoadState>('idle');
  const [listError, setListError] = useState<string | null>(null);
  const [listCursor, setListCursor] = useState<string | null>(null);
  const [listMore, setListMore] = useState(false);
  const [selected, setSelected] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState<InboxMessage[]>([]);
  const [messageState, setMessageState] = useState<LoadState>('idle');
  const [messageError, setMessageError] = useState<string | null>(null);
  const [messageCursor, setMessageCursor] = useState<string | null>(null);
  const [historyMore, setHistoryMore] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [revision, setRevision] = useState(0);
  const listRequest = useRef<AbortController | null>(null);
  const historyRequest = useRef<AbortController | null>(null);
  const actionRequest = useRef<AbortController | null>(null);
  const messageViewport = useRef<HTMLDivElement | null>(null);
  const scrollMode = useRef<
    { kind: 'bottom' } | { kind: 'prepend'; height: number }
  >({ kind: 'bottom' });
  const enabled = Boolean(token && info?.storage_enabled);
  const selectedId = selected?.id;
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
    setListCursor(null);
    setMessageCursor(null);
    setRevision((value) => value + 1);
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
    listRequest.current?.abort();
    setConversations([]);
    setListCursor(null);
    setListMore(false);
    setListError(null);
    if (!enabled) {
      setListState('idle');
      return;
    }
    const controller = new AbortController();
    listRequest.current = controller;
    setListState('loading');
    getConversations(token, controller.signal, {
      platform,
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
            ? (data.items.find((item) => item.id === current.id) ?? null)
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
  }, [enabled, token, platform, query, view, revision, onAuthFailure]);
  useEffect(() => {
    historyRequest.current?.abort();
    setMessages([]);
    setMessageCursor(null);
    setHistoryMore(false);
    setMessageError(null);
    if (!enabled || !selectedId) {
      setMessageState('idle');
      return;
    }
    const controller = new AbortController();
    historyRequest.current = controller;
    setMessageState('loading');
    scrollMode.current = { kind: 'bottom' };
    getConversationMessages(token, controller.signal, selectedId)
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
  }, [enabled, token, selectedId, revision, onAuthFailure]);
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
        q: query,
        unread: view === 'unread',
        pinned: view === 'pinned',
        archived: view === 'archived',
        cursor: listCursor,
      });
      if (controller.signal.aborted) return;
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
        setNotice('数据已变化，已重新读取第一页。');
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
      );
      if (controller.signal.aborted) return;
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
        setNotice('消息已变化，已重新读取最新一页。');
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
      setNotice(
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
      );
      refresh();
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (authFailed(failure)) {
        onAuthFailure();
        return;
      }
      setNotice(messageOf(failure));
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
  return (
    <div className="inbox-root">
      <div className="inbox-title-row">
        <div>
          <h1>所有消息，在一处。</h1>
          <p>跨平台整理会话，把重要的联系留在视线里。</p>
        </div>
        <div className="inbox-header-actions">
          <button
            className="inbox-icon-button"
            aria-label="刷新消息中心"
            title="刷新消息中心"
            onClick={refresh}
            disabled={!token || infoState === 'loading'}
          >
            <Symbol name="refresh" />
          </button>
          <button
            className="button button-primary"
            onClick={() => setImportOpen((value) => !value)}
            disabled={!enabled}
          >
            <Symbol name="import" size={16} />
            导入消息
          </button>
        </div>
      </div>
      <div className="inbox-source-note">
        <span className="inbox-status-dot" />
        <span>
          当前使用本地导入记录。平台实时接收与发送尚未启用，导入不会发出消息。
        </span>
      </div>
      {notice ? (
        <div className="inbox-notice" role="status">
          <p>{notice}</p>
          <button
            className="inbox-icon-button"
            title="关闭提示"
            aria-label="关闭提示"
            onClick={() => setNotice(null)}
          >
            <Symbol name="close" size={15} />
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
          <Symbol name="mail" size={34} />
          <h2>先连接你的消息工作台</h2>
          <p>连接本地服务后，就能导入和整理来自四个平台的消息。</p>
          <button className="button button-secondary" onClick={onSettings}>
            前往连接设置
            <Symbol name="arrow" size={16} />
          </button>
        </section>
      ) : infoState === 'error' ? (
        <section className="inbox-start">
          <h2>消息中心暂时无法读取</h2>
          <p role="alert">{infoError}</p>
          <button className="button button-secondary" onClick={refresh}>
            重新读取
          </button>
        </section>
      ) : !info ? (
        <section className="inbox-start" aria-busy="true">
          <h2>正在读取消息中心…</h2>
          <p>正在检查平台来源与本地存储状态。</p>
        </section>
      ) : !info.storage_enabled ? (
        <section className="inbox-start">
          <Symbol name="archive" size={34} />
          <h2>为消息准备一个本地空间</h2>
          <p>消息存储尚未启用。先在设置中查看配置状态，再导入你已有的对话。</p>
          <button className="button button-secondary" onClick={onSettings}>
            查看设置
            <Symbol name="arrow" size={16} />
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
                  <Symbol name={item.icon} size={16} />
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
            <p className="inbox-filter-footnote">
              消息来自已导入记录；账号核验与实时接入状态分别显示。
            </p>
            <div className="inbox-live-states">
              {info.platforms.map((item) => (
                <span key={item.id}>
                  {PLATFORM_NAMES[item.id]}
                  <span>
                    {item.live_status === 'probe_only'
                      ? '仅账号核验'
                      : item.live_status === 'unsupported'
                        ? '暂不支持实时接入'
                        : '实时未连接'}
                  </span>
                </span>
              ))}
            </div>
          </aside>
          <section className="inbox-conversations" aria-label="会话列表">
            <div className="inbox-list-header">
              <label className="inbox-search">
                <Symbol name="search" size={17} />
                <span className="inbox-sr-only">搜索会话或消息</span>
                <input
                  type="search"
                  placeholder="搜索联系人、消息内容"
                  value={search}
                  onChange={(event) => {
                    setSearch(event.target.value);
                    setSelected(null);
                  }}
                  autoComplete="off"
                />
              </label>
              <div className="inbox-list-summary">
                <strong>
                  {platform ? PLATFORM_NAMES[platform] : '全部平台'} ·{' '}
                  {VIEWS.find((item) => item.value === view)?.name}
                </strong>
                <span>
                  {listState === 'ready'
                    ? `${conversations.length} 个会话`
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
                  <button className="button button-secondary" onClick={refresh}>
                    重试
                  </button>
                </div>
              ) : conversations.length === 0 ? (
                <div className="inbox-list-empty">
                  <Symbol name="mail" size={29} />
                  <h3>
                    {query
                      ? '没有找到相关会话'
                      : view === 'unread'
                        ? '暂时没有未读会话'
                        : view === 'archived'
                          ? '还没有归档的会话'
                          : view === 'pinned'
                            ? '还没有置顶的会话'
                            : '第一段对话，等待你整理'}
                  </h3>
                  <p>
                    {query
                      ? '试试其他关键词，或切换平台来源。'
                      : view === 'all'
                        ? '导入已有消息后，会话将按时间汇集在这里。'
                        : '可以切换到全部会话，继续查看已有记录。'}
                  </p>
                  {view === 'all' && !query ? (
                    <button
                      className="inbox-text-button"
                      onClick={() => setImportOpen(true)}
                    >
                      导入消息
                      <Symbol name="arrow" size={15} />
                    </button>
                  ) : (
                    <button
                      className="inbox-text-button"
                      onClick={() => {
                        setSearch('');
                        setQuery('');
                        setView('all');
                        setPlatform(undefined);
                      }}
                    >
                      查看全部会话
                    </button>
                  )}
                </div>
              ) : (
                <ul className="inbox-conversation-list">
                  {conversations.map((item) => (
                    <li key={item.id}>
                      <button
                        className={`inbox-conversation ${selected?.id === item.id ? 'selected' : ''}`}
                        onClick={() => {
                          setSelected(item);
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
                            <strong>{item.title || item.peer_id}</strong>
                            <time dateTime={item.last_message_at}>
                              {dateText(item.last_message_at)}
                            </time>
                          </span>
                          <span className="inbox-conversation-preview">
                            {item.last_message || '暂无文本消息'}
                          </span>
                          <span className="inbox-conversation-meta">
                            <span>
                              {PLATFORM_NAMES[item.platform]} ·{' '}
                              {item.account_id}
                            </span>
                            {item.pinned ? (
                              <span className="inbox-pin-label">
                                <Symbol name="pin" size={11} />
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
                <header className="inbox-detail-header">
                  <button
                    className="inbox-icon-button inbox-back"
                    onClick={() => setSelected(null)}
                    aria-label="返回会话列表"
                    title="返回会话列表"
                  >
                    <Symbol name="back" />
                  </button>
                  <PlatformMark platform={selected.platform} />
                  <div>
                    <h2>{selected.title || selected.peer_id}</h2>
                    <p>
                      {PLATFORM_NAMES[selected.platform]} ·{' '}
                      {selected.account_id} · 导入记录
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
                    <Symbol name="check" size={15} />
                    {selected.unread_count > 0 ? '标为已读' : '标为未读'}
                  </button>
                  <button
                    onClick={() => change({ pinned: !selected.pinned })}
                    disabled={actionBusy}
                  >
                    <Symbol name="pin" size={15} />
                    {selected.pinned ? '取消置顶' : '置顶'}
                  </button>
                  <button
                    onClick={() => change({ archived: !selected.archived })}
                    disabled={actionBusy}
                  >
                    <Symbol name="archive" size={15} />
                    {selected.archived ? '移回收件箱' : '归档'}
                  </button>
                </div>
                <div
                  className="inbox-history"
                  ref={messageViewport}
                  aria-busy={messageState === 'loading'}
                >
                  {messageState === 'loading' ? (
                    <p className="inbox-history-note" role="status">
                      正在读取消息…
                    </p>
                  ) : messageState === 'error' ? (
                    <div className="inbox-history-note">
                      <p role="alert">{messageError}</p>
                      <button className="inbox-text-button" onClick={refresh}>
                        重新读取
                      </button>
                    </div>
                  ) : (
                    <>
                      {messageCursor ? (
                        <button
                          className="inbox-load-more"
                          disabled={historyMore}
                          onClick={olderMessages}
                        >
                          {historyMore ? '正在读取更早消息…' : '加载更早消息'}
                        </button>
                      ) : (
                        <p className="inbox-history-note">
                          {messages.length
                            ? '已显示当前保存的最早记录'
                            : '这个会话还没有消息'}
                        </p>
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
                                : selected.title || selected.peer_id}
                            </span>
                            <time dateTime={item.occurred_at}>
                              {dateText(item.occurred_at, true)}
                            </time>
                          </div>
                          <p>{item.body}</p>
                        </article>
                      ))}
                      {messageError ? (
                        <p className="inbox-inline-error" role="alert">
                          {messageError}
                        </p>
                      ) : null}
                    </>
                  )}
                </div>
                <footer className="inbox-detail-footer">
                  <Symbol name="archive" size={15} />
                  <span>这是本地导入的历史记录，当前不提供消息发送。</span>
                </footer>
              </>
            ) : (
              <div className="inbox-detail-empty">
                <div className="inbox-detail-emblem">
                  <Symbol name="mail" size={31} />
                </div>
                <h2>
                  打开一段对话，
                  <br />
                  接上它的上下文。
                </h2>
                <p>
                  在左侧选择会话，查看消息与来源。
                  <br />
                  不同平台统一整理，账号依然各自清楚。
                </p>
                <span>QQ · 微信 · Telegram · 闪聊</span>
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
