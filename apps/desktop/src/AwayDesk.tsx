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
  finishDemoReplay,
  isDemoReplay,
  startDemoReplay,
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
  play: <path d="m9 5 11 7-11 7Z" />,
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
  wide = false,
  children,
}: {
  title: string;
  close: () => void;
  drawer?: boolean;
  wide?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const opener = document.activeElement;
    const scrollPosition = { left: window.scrollX, top: window.scrollY };
    const dialog = ref.current;
    dialog?.showModal();
    dialog?.querySelector<HTMLElement>('[data-away-focus]')?.focus();
    dialog?.querySelector('mark')?.scrollIntoView({ block: 'nearest' });
    return () => {
      dialog?.close();
      window.scrollTo({ ...scrollPosition, behavior: 'instant' });
      if (opener instanceof HTMLElement && opener.isConnected) {
        opener.focus({ preventScroll: true });
      }
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`away-modal ${drawer ? 'away-drawer' : wide ? 'away-modal-wide' : ''}`}
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

const replaySteps = [
  {
    kind: 'started',
    title: '你离开了',
    detail: '14:00 · 窗口关闭，接班开始。',
    icon: 'clock',
  },
  {
    kind: 'received',
    title: '收到活动邀约',
    detail: '14:12 · Makers Week 发来产品展示邀请。',
    icon: 'mail',
  },
  {
    kind: 'task',
    title: '提取截止时间和交付要求',
    detail: '识别出 18:00 截止，并列好两项交付要求。',
    icon: 'file',
  },
  {
    kind: 'draft',
    title: '准备好英文回信',
    detail: '14:13 · 回信留在草稿，等你回来审阅。',
    icon: 'edit',
  },
] as const;

function Replay({ events }: { events: AwayEvent[] }) {
  const active = Math.max(0, events.length - 1);
  return (
    <section className="away-priority away-replay" aria-label="离席演示">
      <div>
        <span className="away-priority-label">
          <span />
          30 分钟离席 · 7 秒回放
        </span>
        <h2>
          你离开后，
          <br />
          接班开始工作。
        </h2>
        <p role="status">{replaySteps[active]?.detail}</p>
      </div>
      <ol>
        {replaySteps.map(({ kind, title, icon }, i) => (
          <li
            key={kind}
            className={
              i === active ? 'is-current' : i < active ? 'is-complete' : ''
            }
          >
            <span>
              <Icon name={i < active ? 'check' : icon} size={17} />
            </span>
            {title}
          </li>
        ))}
      </ol>
    </section>
  );
}

function ActivitySummary({
  events,
  showLog,
}: {
  events: AwayEvent[];
  showLog: () => void;
}) {
  if (!events.length) return null;
  const recent = events
    .filter(({ kind }) => kind !== 'ended' && kind !== 'stopped')
    .slice(-4);
  return (
    <section className="away-activity-summary" aria-label="离席期间">
      <div className="away-activity-head">
        <h2>离席期间</h2>
        <button className="away-text-button" onClick={showLog}>
          完整记录
          <Icon name="chevron" size={14} />
        </button>
      </div>
      <ol>
        {recent.map(({ at, kind }, i) => (
          <li key={at + kind + i}>
            <span className={kind.endsWith('_error') ? 'is-warning' : ''} />
            <time dateTime={at}>{fmt(at)}</time>
            <strong>{eventNames[kind]}</strong>
          </li>
        ))}
      </ol>
    </section>
  );
}

function Handover({
  task,
  tasks,
  selectTask,
  openEvidence,
  reviewDraft,
  makeBrief,
  toggleDone,
  busy,
}: {
  task: AwayTask;
  tasks: AwayTask[];
  selectTask: (id: string) => void;
  openEvidence: () => void;
  reviewDraft: () => void;
  makeBrief?: () => void;
  toggleDone?: () => void;
  busy: boolean;
}) {
  const deadline = task.deadline;
  const today =
    deadline && day.format(new Date(deadline)) === day.format(new Date());
  const draftPreview = task.draft?.trim();
  return (
    <>
      {tasks.length > 1 && (
        <label className="away-task-picker">
          跟进事项
          <select
            value={task.id}
            onChange={(event) => selectTask(event.target.value)}
          >
            {tasks.map((item) => (
              <option key={item.id} value={item.id}>
                {item.done ? '已完成 · ' : ''}
                {item.title}
              </option>
            ))}
          </select>
        </label>
      )}
      <div className="away-handover">
        <section
          className="away-priority away-reveal"
          key={task.id}
          aria-label="任务详情"
        >
          <div className="away-priority-copy">
            <div className="away-card-topline">
              <span className="away-eyebrow">01 / 下一件要紧事</span>
              <span className="away-priority-label">
                <span />
                {task.done ? '已完成' : '待跟进'}
              </span>
            </div>
            <p className="away-task-origin">
              <Icon name="mail" size={14} />
              {task.sender}
            </p>
            <h2>{task.title}</h2>
            {task.requirements.length > 0 ? (
              <ul className="away-deliverables" aria-label="交付要求">
                {task.requirements.map((item, i) => (
                  <li key={i}>
                    <span className="away-item-number">
                      {String(i + 1).padStart(2, '0')}
                    </span>
                    {item}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="away-priority-note">
                请查看原文，确认需要交付的内容。
              </p>
            )}
          </div>
          <button
            type="button"
            className="away-deadline"
            onClick={openEvidence}
            aria-label={
              deadline
                ? `核对截止时间 ${fmt(deadline)} 原文`
                : '查看截止时间原文'
            }
          >
            <span className="away-deadline-label">
              <Icon name="clock" size={15} />
              {deadline
                ? `${today ? '今天' : fmt(deadline, calendar)}截止`
                : '截止时间'}
            </span>
            <strong className={deadline ? undefined : 'is-unknown'}>
              {deadline ? fmt(deadline) : '待确认'}
            </strong>
            <span className="away-deadline-link">
              {deadline ? '北京时间 · 查看原文' : '查看邮件原文'}
              <Icon name="diagonal" size={13} />
            </span>
          </button>
        </section>
        <section className="away-next away-reveal" aria-label="下一步">
          <div className="away-card-topline">
            <span className="away-eyebrow">02 / 你只需确认</span>
            <span className="away-next-icon">
              <Icon name="edit" size={17} />
            </span>
          </div>
          <div className="away-next-copy">
            <h2>
              {task.draft
                ? '英文回信已备好'
                : task.draft_failed
                  ? '回信需手动补写'
                  : '英文回信'}
            </h2>
            <div className="away-letter-meta">
              <span>RE: {task.subject}</span>
              <span>草稿</span>
            </div>
            <p className="away-letter" lang={draftPreview ? 'en' : undefined}>
              {draftPreview ??
                (task.draft_failed
                  ? '任务和原文已保留。'
                  : '可以根据原文撰写回信。')}
            </p>
          </div>
          <div className="away-draft-footer">
            <span>
              <Icon name="shield" size={13} />
              仅保存，未发送
            </span>
          </div>
          <button
            className="away-button primary away-review"
            onClick={reviewDraft}
          >
            {task.draft ? '审阅回信' : '撰写回信'}
            <Icon name="arrow" size={17} />
          </button>
        </section>
      </div>
      <div className="away-task-actions">
        <span>
          <Icon name="shield" size={15} />
          仅保存草稿
        </span>
        {makeBrief && (
          <button className="away-text-button" onClick={makeBrief}>
            <Icon name="brief" size={15} />
            制作推广 Brief
          </button>
        )}
        {toggleDone && (
          <button
            className="away-text-button"
            onClick={toggleDone}
            disabled={busy}
          >
            <Icon name="check" size={15} />
            {task.done ? '恢复待办' : '标记完成'}
          </button>
        )}
      </div>
    </>
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
  const [sender, setSender] = useState<string>();
  const [consent, setConsent] = useState(false);
  const [draft, setDraft] = useState('');
  const [copyNotice, setCopyNotice] = useState('');
  const draftInput = useRef<HTMLTextAreaElement>(null);
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
    const timer = window.setInterval(refresh, demo ? 325 : 3500);
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
  const senderInput = sender ?? (demo ? demoSender : (session?.sender ?? ''));
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
  const replaying = demo && isDemoReplay(snapshot);
  const sessionWindow =
    session &&
    `${fmt(session.started_at)}–${fmt(session.stopped_at ?? session.ends_at)}`;
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
      client.start({ minutes, sender: senderInput.trim(), consent }),
    );
    if (next) {
      setSelection('');
      draftEdits.current.clear();
      setConsent(false);
      setDelivery(false);
      show(demo ? 'log' : 'desk');
    }
  }
  function editDraft(item: AwayTask) {
    setSelection(item.id);
    setDraft(draftEdits.current.get(item.id) ?? item.draft ?? '');
    setCopyNotice('');
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
      <header className="away-nav">
        <div className="away-brand">
          <BrandMark />
          <div>
            <strong>接班</strong>
            <span>AWAY DESK</span>
          </div>
        </div>
        <span className="away-nav-label">WORKSPACE</span>
        <nav aria-label="接班导航">
          <button
            aria-current={panel === 'desk' ? 'page' : undefined}
            onClick={() => show('desk')}
          >
            <Icon name="desk" size={17} />
            交接台
          </button>
          <button
            aria-current={panel === 'setup' ? 'page' : undefined}
            onClick={() => show('setup')}
          >
            <Icon name="clock" size={17} />
            离席授权
          </button>
          <button
            aria-current={panel === 'log' ? 'page' : undefined}
            onClick={() => show('log')}
          >
            <Icon name="activity" size={17} />
            运行记录
          </button>
        </nav>
        <div className={`away-agent ${running ? 'is-running' : ''}`}>
          <div className="away-orbit" aria-hidden="true">
            <i />
            <i />
            <i />
            <span>↗</span>
            <b />
          </div>
          <span className="away-eyebrow">WHILE YOU’RE AWAY</span>
          <strong>{running ? '我在接班。' : '随时，接你的班。'}</strong>
          <p>收好消息，理清下一步。</p>
        </div>
        <div className="away-nav-foot">
          {demo && <span className="away-demo-label">演示数据</span>}
          {onBack && (
            <button
              className="away-icon-button away-return"
              onClick={onBack}
              aria-label="返回关系工作台"
              title="返回关系工作台"
            >
              <Icon name="arrow" size={18} />
            </button>
          )}
        </div>
      </header>

      <main className="away-main" id="away-main" tabIndex={-1}>
        <div className="away-topbar">
          <span>
            接班工作空间 <i>/</i>{' '}
            {panel === 'desk'
              ? '交接台'
              : panel === 'setup'
                ? '离席授权'
                : '运行记录'}
          </span>
          <span>
            <Icon name="shield" size={14} />
            {demo ? '交互预览 · 合成邮件' : '本机处理 · 草稿待确认'}
          </span>
        </div>
        <header className="away-head">
          <div>
            <p className="away-context">
              <span className={running ? 'is-live' : ''} />
              {replaying
                ? '离席回放'
                : running
                  ? '接班中'
                  : session?.status === 'stopped'
                    ? '已停止'
                    : session
                      ? '本次离席'
                      : '接班'}
              {sessionWindow && <time>{sessionWindow}</time>}
            </p>
            <h1>
              {panel === 'setup' ? (
                '离席授权'
              ) : panel === 'log' ? (
                '运行记录'
              ) : replaying ? (
                '离席回放'
              ) : pending > 0 ? (
                <>
                  <span className="away-title-part">你不在，</span>
                  <span className="away-title-part">事情在推进。</span>
                </>
              ) : task ? (
                '待办已处理'
              ) : (
                '交接台'
              )}
            </h1>
            <p className="away-head-description">
              {panel === 'setup'
                ? '选好关注的人，剩下的交给接班。'
                : panel === 'log'
                  ? '每一步都有时间，每个结果都有来处。'
                  : replaying
                    ? '用 7 秒，看见一次完整交接。'
                    : pending > 0
                      ? `${pending} 件待跟进 · ${task?.draft ? '任务已整理，回信等你确认。' : '先核对要求，再准备回信。'}`
                      : '只关注重要来信，把时间留给手头的事。'}
            </p>
          </div>
          <div className="away-head-actions">
            {demo && (replaying || !running) && (
              <button
                className="away-button away-play"
                disabled={busy}
                onClick={() => {
                  operation.current += 1;
                  setSelection('');
                  draftEdits.current.clear();
                  setDelivery(false);
                  setSnapshot(
                    replaying ? finishDemoReplay() : startDemoReplay(),
                  );
                  show('desk');
                }}
              >
                <Icon name={replaying ? 'arrow' : 'play'} size={16} />
                {replaying ? '跳到结果' : '一键演示'}
              </button>
            )}
            {connected && !demo && panel !== 'setup' && !running && (
              <button className="away-button" onClick={() => show('setup')}>
                <Icon name="clock" size={16} />
                开始接班
              </button>
            )}
            {running && session && !replaying && (
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
          replaying ? (
            <Replay events={snapshot?.events ?? []} />
          ) : (
            <>
              {task ? (
                <Handover
                  task={task}
                  tasks={tasks}
                  selectTask={setSelection}
                  openEvidence={() => open('evidence')}
                  reviewDraft={() => editDraft(task)}
                  makeBrief={snapshot?.handoff_enabled ? makeBrief : undefined}
                  toggleDone={
                    session
                      ? () =>
                          void change(
                            () =>
                              client.updateTask(session.id, task.id, {
                                done: !task.done,
                              }),
                            task.done ? '已恢复待办。' : '已标记完成。',
                          )
                      : undefined
                  }
                  busy={busy}
                />
              ) : (
                <section className="away-card away-empty">
                  <Icon name="mail" size={32} />
                  <h2>
                    {!snapshot && !syncError
                      ? '正在读取交接'
                      : running
                        ? '正在等候新邮件'
                        : session
                          ? '这次没有需要跟进的邮件'
                          : '还没有接班记录'}
                  </h2>
                  {running ? (
                    <p>可以关闭窗口，后台会在授权时间内继续检查邮箱。</p>
                  ) : snapshot && !session ? (
                    <button
                      className="away-button primary"
                      onClick={() => show('setup')}
                    >
                      设置离席授权
                      <Icon name="arrow" size={16} />
                    </button>
                  ) : null}
                </section>
              )}
              <ActivitySummary
                events={snapshot?.events ?? []}
                showLog={() => show('log')}
              />
            </>
          )
        ) : panel === 'setup' ? (
          <section className="away-card away-form-card">
            <div className="away-card-heading">
              <h2>本次接班</h2>
              <Icon name="shield" />
            </div>
            {running ? (
              <p className="away-muted">
                {replaying
                  ? '回放结束后可以设置离席授权。'
                  : '接班正在运行，停止后可以修改授权。'}
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
                    value={senderInput}
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
            {demo && running && !replaying && (
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
        <footer className="away-workspace-footer">
          <span>AWAY DESK / 接班</span>
          <span>你做决定，AI 做准备。</span>
        </footer>
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
        <Modal title="编辑英文回信" close={() => open(null)} wide>
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
                ref={draftInput}
                value={draft}
                data-away-focus
                onChange={(e) => {
                  setDraft(e.target.value);
                  draftEdits.current.set(task.id, e.target.value);
                }}
                rows={10}
                maxLength={8000}
                required
              />
            </label>
            {error && (
              <div className="away-message warning" role="alert">
                {error}
              </div>
            )}
            {copyNotice && (
              <p className="away-muted" role="status">
                {copyNotice}
              </p>
            )}
            <div className="away-modal-actions">
              <button
                type="button"
                className="away-button"
                disabled={!draft.trim()}
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(draft.trim());
                    setCopyNotice('已复制回信。');
                  } catch {
                    draftInput.current?.focus();
                    draftInput.current?.select();
                    setCopyNotice('已选中回信，可手动复制。');
                  }
                }}
              >
                复制回信
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
