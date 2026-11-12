import type { AwayClient, AwaySnapshot, AwayTask } from './away-api';

const storageKey = 'away-desk-demo-v1';
const sampleSender = 'maya@makersweek.example';
const account = 'partnerships@finfold.example';
const evidence =
  'Please send the public description and a 60-second demo video by 18:00 Beijing time today.';
const sampleBody = `Hi Joey,

We’re putting together the Makers Week product showcase and would love to include Finfold.

${evidence}

Please also confirm whether you are available for a short live Q&A. We’ll share the final schedule once all teams have responded.

Thanks,
Maya
Makers Week`;
const sampleDraft = `Hi Maya,

Thanks for inviting Finfold to the Makers Week showcase. I’ve noted the public description and 60-second demo, due by 18:00 Beijing time.

I’ll confirm our availability for the live Q&A separately once I’ve checked the team’s schedule.

Best,
Joey`;
const clock = (value: number) => new Date(value).toISOString();
const shanghaiDay = (time: number) => clock(time + 8 * 3600000).slice(0, 10);
export type DemoScenario =
  'standard' | 'unclear' | 'sync_error' | 'draft_error';
type DemoState = {
  snapshot: AwaySnapshot;
  deliveryAt?: number;
  scenario?: DemoScenario;
};
let memory: DemoState | undefined;

function makeTask(time: number): AwayTask {
  return {
    id: 'makers-week',
    title: '提交 Makers Week 活动素材',
    requirements: ['一段可公开的产品介绍', '一支 60 秒产品演示视频'],
    deadline: `${shanghaiDay(time)}T18:00:00+08:00`,
    evidence,
    draft: sampleDraft,
    subject: 'Makers Week showcase — materials due today',
    sender: sampleSender,
    received_at: clock(time),
    body: sampleBody,
  };
}

function initialState(): DemoState {
  const date = shanghaiDay(Date.now());
  const at = (time: string) => `${date}T${time}:00+08:00`;
  const task = makeTask(Date.parse(at('14:12')));
  return {
    snapshot: {
      account,
      model: 'DeepSeek · 演示配置',
      handoff_enabled: true,
      session: {
        id: 'demo-first',
        status: 'ended',
        started_at: at('14:00'),
        ends_at: at('14:30'),
        sender: sampleSender,
      },
      tasks: [task],
      events: [
        { at: at('14:00'), kind: 'started' },
        { at: at('14:12'), kind: 'received', detail: task.subject },
        { at: at('14:12'), kind: 'task', detail: task.title },
        { at: at('14:13'), kind: 'draft', detail: '英文回信已保存' },
        { at: at('14:30'), kind: 'ended' },
      ],
    },
  };
}

function save(state: DemoState) {
  memory = state;
  try {
    localStorage.setItem(storageKey, JSON.stringify(state));
  } catch {
    /* Preview may run as a local file. */
  }
  return state.snapshot;
}

function read(): DemoState {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved) return JSON.parse(saved) as DemoState;
  } catch {
    /* The session can still run in memory. */
  }
  return memory ?? initialState();
}

function advance() {
  const state = read();
  const { snapshot, deliveryAt, scenario } = state;
  const session = snapshot.session;
  if (session?.status !== 'running') return snapshot;
  const now = Date.now();
  const end = Date.parse(session.ends_at);
  if (deliveryAt && deliveryAt <= now && deliveryAt < end) {
    delete state.deliveryAt;
    if (scenario === 'sync_error') {
      snapshot.events.push({
        at: clock(deliveryAt),
        kind: 'sync_error',
        detail: '演示邮箱暂时无法访问。',
      });
    } else {
      const task = makeTask(deliveryAt);
      if (scenario === 'unclear') {
        task.deadline = undefined;
        task.evidence = undefined;
        task.body = task.body.replace(
          evidence,
          'Please send the public description and a 60-second demo video tomorrow afternoon.',
        );
        task.draft = task.draft?.replace(
          ', due by 18:00 Beijing time',
          '. Could you confirm the submission deadline and time zone?',
        );
      }
      if (scenario === 'draft_error') {
        task.draft = undefined;
        task.draft_failed = true;
      }
      snapshot.tasks.push(task);
      snapshot.events.push(
        { at: clock(deliveryAt), kind: 'received', detail: task.subject },
        { at: clock(deliveryAt + 250), kind: 'task', detail: task.title },
        task.draft_failed
          ? {
              at: clock(deliveryAt + 500),
              kind: 'draft_error',
              detail: '模型暂未返回草稿，待办已保存。',
            }
          : {
              at: clock(deliveryAt + 500),
              kind: 'draft',
              detail: '英文回信已保存',
            },
      );
    }
  }
  if (now >= end) {
    session.status = 'ended';
    snapshot.events.push({ at: session.ends_at, kind: 'ended' });
  }
  return save(state);
}

export const demoSender = sampleSender;
export const demoClient: AwayClient = {
  async get() {
    return advance();
  },
  async start(input) {
    const now = Date.now();
    return save({
      snapshot: {
        account,
        model: 'DeepSeek · 演示配置',
        handoff_enabled: true,
        session: {
          id: 'demo-' + now,
          status: 'running',
          started_at: clock(now),
          ends_at: clock(now + input.minutes * 60000),
          sender: input.sender,
        },
        tasks: [],
        events: [{ at: clock(now), kind: 'started' }],
      },
    });
  },
  async stop(id) {
    const snapshot = advance();
    if (snapshot.session?.id === id && snapshot.session.status === 'running') {
      const at = clock(Date.now());
      snapshot.session = {
        ...snapshot.session,
        status: 'stopped',
        stopped_at: at,
      };
      snapshot.events.push({ at, kind: 'stopped' });
    }
    return save({ snapshot });
  },
  async updateTask(sessionId, taskId, patch) {
    const state = read();
    const task = state.snapshot.tasks.find((item) => item.id === taskId);
    if (task && state.snapshot.session?.id === sessionId)
      Object.assign(task, patch);
    return save(state);
  },
  async handoff() {
    return { url: '' };
  },
};

export function scheduleDemoMail(scenario: DemoScenario) {
  const state = read();
  if (state.snapshot.session?.status === 'running' && !state.deliveryAt) {
    save({ ...state, deliveryAt: Date.now() + 2500, scenario });
  }
}

export function resetAwayDemo() {
  return save(initialState());
}
