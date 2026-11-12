import { invoke, isTauri } from '@tauri-apps/api/core';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { createAwayClient } from './away-api';
import type {
  AwayEvent,
  AwaySnapshot,
  AwayTask,
  PublicBrief,
} from './away-api';
import {
  demoClient,
  demoSender,
  resetAwayDemo,
  scheduleDemoMail,
} from './away-demo';
import type { DemoScenario } from './away-demo';
import { ServiceError } from './api';
import './away.css';

const paths = {
  desk: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="3" />
      <path d="M3 9h18m-12 0v11" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  mail: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="3" />
      <path d="m4 7 8 6 8-6" />
    </>
  ),
  arrow: <path d="M4 12h15m-6-6 6 6-6 6" />,
  diagonal: <path d="M6 18 18 6M8 6h10v10" />,
  chevron: <path d="m9 6 6 6-6 6" />,
  file: (
    <>
      <path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" />
      <path d="M14 3v6h6M8 13h8m-8 4h6" />
    </>
  ),
  check: <path d="m5 12 4 4L19 6" />,
  edit: (
    <>
      <path d="m13 6 5 5m-13 8 4-1L20 7l-4-4L5 14z" />
      <path d="M13 20h7" />
    </>
  ),
  shield: (
    <>
      <path d="M12 22s7-3 7-10V5l-7-3-7 3v7c0 7 7 10 7 10Z" />
      <path d="m9 12 2 2 4-4" />
    </>
  ),
  activity: <path d="M2 12h5l3-7 4 14 3-7h5" />,
  close: <path d="m6 6 12 12M18 6 6 18" />,
  refresh: (
    <>
      <path d="M20 9a8 8 0 1 0 0 6m0-11v5h-5" />
    </>
  ),
  brief: (
    <>
      <rect x="4" y="6" width="16" height="14" rx="3" />
      <path d="M9 6V4h6v2m-11 6h16m-9 0v2h2v-2" />
    </>
  ),
} as const;

function Icon({
  name,
  size = 18,
}: {
  name: keyof typeof paths;
  size?: number;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.55"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  );
}

function BrandMark() {
  return (
    <span className="away-brand-mark" aria-hidden="true">
      <svg
        viewBox="0 0 32 32"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.45"
      >
        <ellipse
          cx="15.5"
          cy="16"
          rx="10"
          ry="4.4"
          transform="rotate(-34 15.5 16)"
        />
        <ellipse
          cx="15.5"
          cy="16"
          rx="10"
          ry="4.4"
          transform="rotate(34 15.5 16)"
        />
        <circle cx="23.5" cy="8.9" r="2.2" fill="#a3eee0" stroke="#151e23" />
      </svg>
    </span>
  );
}

const timeZone = 'Asia/Shanghai';
const hour = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
});
const calendar = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  month: 'long',
  day: 'numeric',
});
const day = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  year: 'numeric',
  month: 'numeric',
  day: 'numeric',
});
const fmt = (value: string, format = hour) => format.format(new Date(value));
const eventNames: Record<AwayEvent['kind'], string> = {
  started: '开始接班',
  received: '收到邮件',
  task: '已创建跟进任务',
  draft: '已保存回信草稿',
  sync_error: '邮箱同步失败',
  draft_error: '草稿生成失败',
  stopped: '接班已停止',
  ended: '离席时段结束',
};
const blankBrief = (): PublicBrief => ({
  event: '',
  product: '',
  selling_points: '',
  cta: '',
});
type Panel = 'desk' | 'setup' | 'log';
type Overlay = 'evidence' | 'draft' | 'brief' | null;

function Modal({
  title,
  close,
  drawer = false,
  children,
}: {
  title: string;
  close: () => void;
  drawer?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const opener = document.activeElement;
    const dialog = ref.current;
    dialog?.showModal();
    dialog?.querySelector<HTMLElement>('[data-away-focus]')?.focus();
    return () => {
      dialog?.close();
      if (opener instanceof HTMLElement && opener.isConnected) {
        opener.focus({ preventScroll: true });
      }
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`away-modal ${drawer ? 'away-drawer' : ''}`}
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) close();
      }}
      aria-label={title}
    >
      <div className="away-modal-inner">
        <div className="away-modal-head">
          <h2>{title}</h2>
          <button
            className="away-icon-button"
            onClick={close}
            aria-label="关闭"
          >
            <Icon name="close" />
          </button>
        </div>
        {children}
      </div>
    </dialog>
  );
}

function Timeline({ events }: { events: AwayEvent[] }) {
  if (!events.length)
    return <p className="away-muted">接班开始后，处理记录会出现在这里。</p>;
  return (
    <ol className="away-timeline">
      {events.map((event, i) => (
        <li key={event.at + '-' + i}>
          <time dateTime={event.at}>{fmt(event.at)}</time>
          <span
            className={`away-timeline-dot ${event.kind.endsWith('_error') ? 'is-warning' : ''}`}
          />
          <div>
            <strong>{eventNames[event.kind]}</strong>
            {event.detail && <p>{event.detail}</p>}
          </div>
        </li>
      ))}
    </ol>
  );
}

export default function AwayDesk({
  demo = false,
  token = '',
  onSettings,
  onAuthFailure,
  onBack,
}: {
  demo?: boolean;
  token?: string;
  onSettings?: () => void;
  onAuthFailure?: () => void;
  onBack?: () => void;
}) {
  const client = useMemo(
    () => (demo ? demoClient : createAwayClient(token)),
    [demo, token],
  );
  const [snapshot, setSnapshot] = useState<AwaySnapshot | null>(null);
  const [panel, setPanel] = useState<Panel>('desk');
  const [overlay, setOverlay] = useState<Overlay>(null);
  const [selection, setSelection] = useState('');
  const [minutes, setMinutes] = useState(30);
  const [sender, setSender] = useState(demo ? demoSender : '');
  const [consent, setConsent] = useState(false);
  const [draft, setDraft] = useState('');
  const draftEdits = useRef(new Map<string, string>());
  const [demoScenario, setDemoScenario] = useState<DemoScenario>('standard');
  const [brief, setBrief] = useState<PublicBrief>(blankBrief);
  const [publicConfirmed, setPublicConfirmed] = useState(false);
  const [demoBriefReady, setDemoBriefReady] = useState(false);
  const [error, setError] = useState('');
  const [syncError, setSyncError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [delivery, setDelivery] = useState(false);
  const operation = useRef(0);

  useEffect(() => {
    if (!demo && !token) return;
    const controller = new AbortController();
    let reading = false;
    async function refresh() {
      if (reading) return;
      reading = true;
      const version = operation.current;
      try {
        const next = await client.get(controller.signal);
        if (!controller.signal.aborted && version === operation.current) {
          setSnapshot(next);
          setSyncError('');
        }
      } catch (reason) {
        if (!controller.signal.aborted) {
          if (reason instanceof ServiceError && reason.status === 401)
            onAuthFailure?.();
          setSyncError(
            reason instanceof Error ? reason.message : '无法读取接班状态。',
          );
        }
      } finally {
        reading = false;
      }
    }
    void refresh();
    const timer = window.setInterval(refresh, demo ? 800 : 3500);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [client, demo, token, onAuthFailure]);

  async function change(action: () => Promise<AwaySnapshot>, message?: string) {
    setError('');
    setBusy(true);
    operation.current += 1;
    try {
      const next = await action();
      setSnapshot(next);
      if (message) setNotice(message);
      return next;
    } catch (reason) {
      if (reason instanceof ServiceError && reason.status === 401)
        onAuthFailure?.();
      setError(reason instanceof Error ? reason.message : '操作失败，请重试。');
    } finally {
      operation.current += 1;
      setBusy(false);
    }
  }

  const session = snapshot?.session;
  const running = session?.status === 'running';
  const tasks = [...(snapshot?.tasks ?? [])].sort(
    (a, b) =>
      Number(Boolean(a.done)) - Number(Boolean(b.done)) ||
      (a.deadline ? Date.parse(a.deadline) : Infinity) -
        (b.deadline ? Date.parse(b.deadline) : Infinity) ||
      Date.parse(a.received_at) - Date.parse(b.received_at),
  );
  const task = tasks.find((item) => item.id === selection) ?? tasks[0];
  const pending = tasks.filter((item) => !item.done).length;
  const hasDeadline = Boolean(task?.deadline);
  const today =
    task?.deadline &&
    day.format(new Date(task.deadline)) === day.format(new Date());
  const status = running
    ? '接班中'
    : session
      ? session.status === 'stopped'
        ? '已停止'
        : '已交接'
      : '待开始';
  const connected = demo || Boolean(token);

  function show(next: Panel) {
    setPanel(next);
    setError('');
    setNotice('');
  }
  function open(next: Overlay) {
    if (next === 'evidence' && task) setSelection(task.id);
    setError('');
    setNotice('');
    setOverlay(next);
  }
  async function start(event: FormEvent) {
    event.preventDefault();
    const next = await change(() =>
      client.start({ minutes, sender: sender.trim(), consent }),
    );
    if (next) {
      setSelection('');
      draftEdits.current.clear();
      setConsent(false);
      setDelivery(false);
      show('log');
    }
  }
  function editDraft(item: AwayTask) {
    setSelection(item.id);
    setDraft(draftEdits.current.get(item.id) ?? item.draft ?? '');
    open('draft');
  }
  function makeBrief() {
    setBrief(
      demo
        ? {
            event: 'Makers Week 产品展示',
            product: 'Finfold · 为出海小团队准备多平台内容的 AI 工作台',
            selling_points: '一次输入，即可获得适合 LinkedIn 和 X 的内容草稿。',
            cta: '访问 https://www.finfold.app 了解产品',
          }
        : blankBrief(),
    );
    setPublicConfirmed(false);
    setDemoBriefReady(false);
    open('brief');
  }
  async function sendBrief(event: FormEvent) {
    event.preventDefault();
    if (demo) {
      setDemoBriefReady(true);
      return;
    }
    setBusy(true);
    setError('');
    const native = isTauri();
    const popup = native ? null : window.open('about:blank', '_blank');
    if (popup) popup.opener = null;
    try {
      const { url } = await client.handoff(brief);
      const target = new URL(url);
      if (
        target.protocol !== 'https:' ||
        !['www.finfold.app', 'staging.finfold.app'].includes(target.host) ||
        target.pathname !== '/workbench/import' ||
        !/^#[A-Za-z0-9_-]{43}$/.test(target.hash) ||
        target.search ||
        target.username ||
        target.password
      )
        throw new Error('Finfold 返回的导入链接无效。');
      if (native) await invoke('open_finfold_handoff', { url: target.href });
      else if (popup) popup.location.replace(target.href);
      else window.location.assign(target.href);
      open(null);
      setNotice('已打开 Finfold 创作台。');
    } catch (reason) {
      popup?.close();
      setError(reason instanceof Error ? reason.message : '交接失败，请重试。');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="away-shell">
      <a className="skip-link" href="#away-main">
        跳到主要内容
      </a>
      <aside className="away-nav">
        <div className="away-brand">
          <BrandMark />
          <div>
            <strong>接班</strong>
            <span>Away Desk</span>
          </div>
        </div>
        <nav aria-label="接班导航">
          <button
            aria-current={panel === 'desk' ? 'page' : undefined}
            onClick={() => show('desk')}
          >
            <Icon name="desk" />
            交接台
            {pending > 0 && <span className="away-nav-count">{pending}</span>}
          </button>
          <button
            aria-current={panel === 'setup' ? 'page' : undefined}
            onClick={() => show('setup')}
          >
            <Icon name="clock" />
            离席授权
          </button>
          <button
            aria-current={panel === 'log' ? 'page' : undefined}
            onClick={() => show('log')}
          >
            <Icon name="activity" />
            运行记录
          </button>
        </nav>
        <div className="away-nav-foot">
          <span>
            <Icon name="shield" size={16} />
            {demo ? '演示数据' : '本地工作空间'}
          </span>
          {demo && (
            <button
              className="away-reset"
              onClick={() => {
                setSnapshot(resetAwayDemo());
                setSelection('');
                draftEdits.current.clear();
                setDelivery(false);
                show('desk');
              }}
              aria-label="重置演示"
              title="重置演示"
            >
              <Icon name="refresh" size={16} />
            </button>
          )}
          {onBack && (
            <button
              className="away-reset away-return"
              onClick={onBack}
              aria-label="返回关系工作台"
              title="返回关系工作台"
            >
              <Icon name="arrow" size={16} />
            </button>
          )}
        </div>
      </aside>

      <main className="away-main" id="away-main" tabIndex={-1}>
        <header className="away-head">
          <div>
            <h1>
              {panel === 'desk'
                ? '交接台'
                : panel === 'setup'
                  ? '离席授权'
                  : '运行记录'}
            </h1>
            <p>
              {panel === 'desk'
                ? '离席期间的重要来信和下一步。'
                : panel === 'setup'
                  ? '选择接班时段和允许处理的邮件。'
                  : '查看后台收到邮件、创建任务和备稿的时间。'}
            </p>
          </div>
          <div className="away-head-actions">
            <span className="away-status">
              <span className={session ? 'is-active' : ''} />
              {status}
            </span>
            {connected && panel !== 'setup' && !running && (
              <button
                className="away-button primary"
                onClick={() => show('setup')}
              >
                <Icon name="clock" size={16} />
                开始接班
              </button>
            )}
            {running && session && (
              <button
                className="away-button"
                disabled={busy}
                onClick={() =>
                  void change(() => client.stop(session.id), '已停止接班。')
                }
              >
                停止接班
              </button>
            )}
          </div>
        </header>

        {notice && (
          <div className="away-message" role="status">
            {notice}
          </div>
        )}
        {(syncError || (error && !overlay)) && (
          <div className="away-message warning" role="alert">
            {syncError || error}
          </div>
        )}
        {!connected ? (
          <div className="away-card away-empty">
            <Icon name="shield" size={30} />
            <h2>连接本地服务</h2>
            <p>连接后可以设置离席授权并查看接班结果。</p>
            <button className="away-button primary" onClick={onSettings}>
              前往设置
              <Icon name="arrow" size={16} />
            </button>
          </div>
        ) : panel === 'desk' ? (
          <>
            <div className="away-summary">
              <div>
                <span className="away-summary-icon">
                  <Icon name="activity" />
                </span>
                <strong>
                  {session
                    ? running
                      ? '后台正在接班'
                      : '本次接班'
                    : '还没有接班记录'}
                </strong>
                {session && (
                  <span className="away-summary-time">
                    {fmt(session.started_at)} –{' '}
                    {fmt(session.stopped_at ?? session.ends_at)} · 北京时间
                  </span>
                )}
              </div>
              <button className="away-text-button" onClick={() => show('log')}>
                查看运行记录
                <Icon name="chevron" size={15} />
              </button>
            </div>

            <div className="away-grid">
              <section className="away-card away-queue" aria-label="待办">
                <div className="away-card-heading">
                  <h2>待办</h2>
                  <span className="away-count">{pending}</span>
                </div>
                {tasks.length ? (
                  <ul>
                    {tasks.map((item) => (
                      <li key={item.id}>
                        <button
                          className={`away-task-row ${task?.id === item.id ? 'is-selected' : ''}`}
                          aria-pressed={task?.id === item.id}
                          onClick={() => setSelection(item.id)}
                        >
                          <span className="away-mail-icon">
                            <Icon
                              name={item.done ? 'check' : 'mail'}
                              size={17}
                            />
                          </span>
                          <span className="away-row-body">
                            <strong>{item.title}</strong>
                            <small>
                              {item.done
                                ? '已完成'
                                : item.deadline
                                  ? `截止 ${fmt(item.deadline)}`
                                  : '截止时间待确认'}
                            </small>
                          </span>
                          <Icon name="chevron" size={15} />
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="away-queue-empty">暂无需要跟进的邮件。</p>
                )}
                {tasks.length > 0 &&
                  snapshot?.events.some((event) => event.kind === 'draft') && (
                    <div className="away-queue-activity">
                      <h3>后台已完成</h3>
                      <span>
                        <Icon name="check" size={15} />
                        已创建跟进任务
                      </span>
                      <span>
                        <Icon name="check" size={15} />
                        已保存回信草稿
                      </span>
                    </div>
                  )}
                {session && (
                  <div className="away-queue-foot">
                    <Icon name="shield" size={16} />
                    <span>邮箱只读 · 回信仅保存为草稿</span>
                  </div>
                )}
              </section>

              {task ? (
                <section
                  className="away-card away-task-detail"
                  aria-label="任务详情"
                >
                  <div className="away-task-top">
                    <span
                      className={`away-chip ${task.done ? 'done' : hasDeadline ? 'urgent' : ''}`}
                    >
                      <span />
                      {task.done
                        ? '已完成'
                        : hasDeadline
                          ? '有截止时间'
                          : '待确认'}
                    </span>
                    <button
                      className="away-text-button"
                      onClick={() => open('evidence')}
                    >
                      <Icon name="file" size={16} />
                      查看原文
                    </button>
                  </div>
                  <h2>{task.title}</h2>
                  <div className="away-due-row">
                    <button
                      className="away-due"
                      onClick={() => open('evidence')}
                      aria-label={
                        task.deadline
                          ? `核对截止时间 ${fmt(task.deadline)} 原文`
                          : '查看截止时间原文'
                      }
                    >
                      <Icon name="clock" size={20} />
                      {task.deadline ? (
                        <>
                          <span>
                            {today ? '今天' : fmt(task.deadline, calendar)}
                          </span>
                          <strong>{fmt(task.deadline)}</strong>
                        </>
                      ) : (
                        <strong className="away-due-unknown">
                          截止时间待确认
                        </strong>
                      )}
                      <Icon name="diagonal" size={14} />
                    </button>
                    {task.deadline && <span>北京时间</span>}
                  </div>
                  <div className="away-requirements">
                    <h3>交付要求</h3>
                    {task.requirements.length ? (
                      <ul>
                        {task.requirements.map((item, i) => (
                          <li key={i}>
                            <Icon name="check" size={16} />
                            {item}
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p className="away-muted">请查看原文确认交付内容。</p>
                    )}
                  </div>
                  <div className="away-reply">
                    <div>
                      <span className="away-reply-icon">
                        <Icon name="edit" size={19} />
                      </span>
                      <span>
                        <strong>英文回信</strong>
                        <small>
                          {task.draft
                            ? '草稿已就绪，等你确认'
                            : task.draft_failed
                              ? '生成失败，可以手动补写'
                              : '可以在此撰写草稿'}
                        </small>
                      </span>
                    </div>
                    <button
                      className="away-button primary"
                      onClick={() => editDraft(task)}
                    >
                      编辑回信
                      <Icon name="arrow" size={16} />
                    </button>
                  </div>
                  <div className="away-detail-foot">
                    {snapshot?.handoff_enabled && (
                      <button className="away-text-button" onClick={makeBrief}>
                        <Icon name="brief" size={16} />
                        制作推广 Brief
                      </button>
                    )}
                    {session && (
                      <button
                        className="away-text-button"
                        disabled={busy}
                        onClick={() =>
                          void change(
                            () =>
                              client.updateTask(session.id, task.id, {
                                done: !task.done,
                              }),
                            task.done ? '已恢复待办。' : '已标记完成。',
                          )
                        }
                      >
                        <Icon name="check" size={16} />
                        {task.done ? '恢复待办' : '标记完成'}
                      </button>
                    )}
                  </div>
                </section>
              ) : (
                <section className="away-card away-empty">
                  <Icon name="mail" size={32} />
                  <h2>{running ? '正在等候新邮件' : '离席结果会出现在这里'}</h2>
                  <p>
                    {running
                      ? '关闭界面后，后台仍会在授权时间内检查邮箱。'
                      : '开启接班后，系统会将符合条件的新邮件整理为待办。'}
                  </p>
                </section>
              )}
            </div>
          </>
        ) : panel === 'setup' ? (
          <section className="away-card away-form-card">
            <div className="away-card-heading">
              <h2>本次接班</h2>
              <Icon name="shield" />
            </div>
            {running ? (
              <p className="away-muted">
                当前有一次接班正在运行。请先停止，再修改授权。
              </p>
            ) : (
              <form className="away-form" onSubmit={start}>
                <div className="away-fields">
                  <label>
                    接班时长
                    <select
                      value={minutes}
                      onChange={(e) => setMinutes(Number(e.target.value))}
                    >
                      <option value="15">15 分钟</option>
                      <option value="30">30 分钟</option>
                      <option value="60">1 小时</option>
                      <option value="120">2 小时</option>
                    </select>
                  </label>
                  <label>
                    监听邮箱
                    <input value={snapshot?.account || '未配置'} readOnly />
                  </label>
                </div>
                <label>
                  关注的发件人
                  <input
                    type="email"
                    value={sender}
                    onChange={(e) => setSender(e.target.value)}
                    placeholder="name@example.com"
                    required
                    maxLength={254}
                  />
                </label>
                {demo && (
                  <label>
                    示例场景
                    <select
                      value={demoScenario}
                      onChange={(event) =>
                        setDemoScenario(event.target.value as DemoScenario)
                      }
                    >
                      <option value="standard">标准邀约</option>
                      <option value="unclear">截止时间不明确</option>
                      <option value="sync_error">邮箱同步失败</option>
                      <option value="draft_error">草稿生成失败</option>
                    </select>
                  </label>
                )}
                {!snapshot?.account || !snapshot.model ? (
                  <p className="away-muted">
                    请在本地后台配置只读邮箱和模型后再开始。
                  </p>
                ) : null}
                <div className="away-permissions">
                  <strong>授权范围</strong>
                  <p>
                    只分析此发件人在接班期间的新来信。将邮件主题和纯文本正文提供给{' '}
                    <b>{snapshot?.model || '未配置的模型'}</b>
                    ，用于创建本地任务和英文草稿。
                  </p>
                  <label className="away-checkbox">
                    <input
                      type="checkbox"
                      checked={consent}
                      onChange={(e) => setConsent(e.target.checked)}
                      required
                    />
                    我确认此范围和模型处理授权
                  </label>
                </div>
                <button
                  className="away-button primary"
                  disabled={
                    busy || !snapshot?.account || !snapshot.model || !consent
                  }
                >
                  <Icon name="clock" size={17} />
                  {busy ? '正在确认邮箱…' : '开始接班'}
                </button>
              </form>
            )}
          </section>
        ) : (
          <section className="away-card away-form-card">
            <div className="away-card-heading">
              <h2>本次接班记录</h2>
              <Icon name="activity" />
            </div>
            {session && (
              <p className="away-muted">
                {fmt(session.started_at)} –{' '}
                {fmt(session.stopped_at ?? session.ends_at)} · 北京时间
              </p>
            )}
            {demo && running && (
              <div className="away-demo-delivery">
                <button
                  className="away-button"
                  disabled={delivery || tasks.length > 0}
                  onClick={() => {
                    scheduleDemoMail(demoScenario);
                    setDelivery(true);
                  }}
                >
                  <Icon name="mail" />
                  {delivery ? '已安排示例邮件' : '投递示例邮件'}
                </button>
                <span>可关窗后再打开查看结果。</span>
              </div>
            )}
            <Timeline events={snapshot?.events ?? []} />
            {tasks.length > 0 && (
              <button
                className="away-button primary"
                onClick={() => show('desk')}
              >
                打开交接台
                <Icon name="arrow" size={16} />
              </button>
            )}
          </section>
        )}
      </main>

      {overlay === 'evidence' && task && (
        <Modal title="邮件原文" close={() => open(null)} drawer>
          <div className="away-source">
            <span className="away-mail-icon">
              <Icon name="mail" />
            </span>
            <div>
              <h3>{task.subject}</h3>
              <p>
                {task.sender} · {fmt(task.received_at)}
              </p>
            </div>
          </div>
          <div className="away-source-body">
            {task.evidence && task.body.includes(task.evidence) ? (
              <>
                {task.body.slice(0, task.body.indexOf(task.evidence))}
                <mark>{task.evidence}</mark>
                {task.body.slice(
                  task.body.indexOf(task.evidence) + task.evidence.length,
                )}
              </>
            ) : (
              task.body
            )}
          </div>
        </Modal>
      )}

      {overlay === 'draft' && task && session && (
        <Modal title="编辑英文回信" close={() => open(null)}>
          <form
            className="away-form"
            onSubmit={async (event) => {
              event.preventDefault();
              if (
                await change(
                  () =>
                    client.updateTask(session.id, task.id, {
                      draft: draft.trim(),
                    }),
                  '回信草稿已保存。',
                )
              ) {
                draftEdits.current.delete(task.id);
                setOverlay(null);
              }
            }}
          >
            <label>
              回信草稿
              <textarea
                value={draft}
                data-away-focus
                onChange={(e) => {
                  setDraft(e.target.value);
                  draftEdits.current.set(task.id, e.target.value);
                }}
                rows={9}
                maxLength={8000}
                required
              />
            </label>
            {error && (
              <div className="away-message warning" role="alert">
                {error}
              </div>
            )}
            <div className="away-modal-actions">
              <button
                type="button"
                className="away-button"
                onClick={() => open(null)}
              >
                暂不保存
              </button>
              <button
                className="away-button primary"
                disabled={busy || !draft.trim()}
              >
                <Icon name="check" size={16} />
                {busy ? '正在保存…' : '保存草稿'}
              </button>
            </div>
          </form>
        </Modal>
      )}

      {overlay === 'brief' && (
        <Modal
          title={
            demoBriefReady ? 'Finfold 创作台 · 接入预览' : '制作推广 Brief'
          }
          close={() => open(null)}
        >
          {demoBriefReady ? (
            <div className="away-form">
              <p className="away-muted">
                演示预览 · 已确认的素材会填入创作台。
              </p>
              <div className="away-preview-brief">
                {Object.values(brief).join('\n\n')}
              </div>
              <div className="away-platforms">
                <span>LinkedIn</span>
                <span>X / Twitter</span>
              </div>
              <button
                className="away-button"
                onClick={() => setDemoBriefReady(false)}
              >
                返回修改
              </button>
            </div>
          ) : (
            <form className="away-form" onSubmit={sendBrief}>
              <p className="away-muted">
                下列字段会进入 Finfold 创作台。请只填写可以公开的内容。
              </p>
              {(
                [
                  ['event', '公开活动信息'],
                  ['product', '产品介绍'],
                  ['selling_points', '公开卖点'],
                  ['cta', '行动引导'],
                ] as const
              ).map(([key, label]) => (
                <label key={key}>
                  {label}
                  <input
                    required
                    value={brief[key]}
                    onChange={(e) => {
                      setBrief({ ...brief, [key]: e.target.value });
                      setPublicConfirmed(false);
                    }}
                    maxLength={key === 'selling_points' ? 1200 : 300}
                  />
                </label>
              ))}
              <label className="away-checkbox">
                <input
                  type="checkbox"
                  required
                  checked={publicConfirmed}
                  onChange={(e) => setPublicConfirmed(e.target.checked)}
                />
                我确认这些字段可以公开
              </label>
              {error && (
                <div className="away-message warning" role="alert">
                  {error}
                </div>
              )}
              <div className="away-modal-actions">
                <button
                  className="away-button primary"
                  disabled={busy || !publicConfirmed}
                >
                  {busy ? '正在打开 Finfold…' : '在 Finfold 创作'}
                  <Icon name="diagonal" size={16} />
                </button>
              </div>
            </form>
          )}
        </Modal>
      )}
    </div>
  );
}
