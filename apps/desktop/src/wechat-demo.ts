import type { InboxClient } from './Inbox';
import type { WechatClient } from './WechatPanel';
import type { AIClient } from './AIPanel';
import type { AIRecord, AISettings } from './ai-api';
import type { Conversation, InboxMessage } from './inbox-api';
import type { WechatMonitorStatus, WechatReadResult } from './wechat-api';

export type WechatScenario =
  'ready' | 'unconfigured' | 'read-error' | 'no-model';

// Synthetic adapters share the production UI and never call a network client.
// No credentials or real conversation data are used or persisted by this preview.
export function createWechatDemo(scenario: WechatScenario = 'ready') {
  const names = ['陈以宁', '林然', '周屿', '许知夏'];
  const at = (minute: number) => `2026-11-15T09:${minute}:00+08:00`;
  const bodies: [string, string, string][] = [
    [
      '早上好，周五的产品分享想请你来讲 Finfold，主题还是从想法到第一条内容。',
      '好呀，分享时长和需要准备的素材发我一下？',
      '分享 15 分钟就好。请在 11 月 18 日（周三）18:00 前给我简介和一张产品截图，北京时间。',
    ],
    [
      '新版首页的方向我看过了，整体更清楚了。',
      '谢谢！你最希望再调整哪一部分？',
      '想把品牌记忆的入口再往前放一些，方便第一次使用的人找到。',
    ],
    [
      '下周有空碰一下活动合作吗？',
      '可以，我们先对一下这次合作的目标。',
      '我们主要想聊面向海外创作者的联合直播，时间你来定就好。',
    ],
    [
      '活动海报已经整理好了。',
      '收到，我晚一点看。',
      '新版已经放进共享文件夹，标题和日期都更新了。',
    ],
  ];
  const messages: Record<string, InboxMessage[]> = Object.fromEntries(
    names.map((_, index) => [
      `demo-chat-${index}`,
      bodies[index].map((body, j) => ({
        id: `demo-message-${index}-${j}`,
        conversation_id: `demo-chat-${index}`,
        external_id: `synthetic-${index}-${j}`,
        body,
        occurred_at: at(10 + index * 3 + j),
        direction: j === 1 ? 'outgoing' : 'incoming',
        read: j === 1,
        source: 'wechatdb',
      })),
    ]),
  );
  let conversations: Conversation[] = names.map((title, index) => ({
    id: `demo-chat-${index}`,
    connection_id: 'demo-wechat',
    account_id: '演示账号',
    peer_id: `wxid_demo_${index}`,
    title,
    last_message: bodies[index][2],
    last_message_at: at(12 + index * 3),
    platform: 'weixin',
    unread_count: index === 0 ? 2 : index === 2 ? 1 : 0,
    pinned: index === 0,
    archived: false,
  }));
  const configured = scenario !== 'unconfigured';
  const result: WechatReadResult = {
    inserted: 3,
    duplicates: 9,
    conversations: 3,
    skipped: 0,
    limited: false,
    read_at: at(28),
    read_only: true,
    sending_enabled: false,
  };
  let monitor: WechatMonitorStatus = {
    enabled: configured,
    peer_ids: configured ? ['wxid_demo_0', 'wxid_demo_1', 'wxid_demo_2'] : [],
    interval_seconds: 30,
    state: !configured
      ? 'not_configured'
      : scenario === 'read-error'
        ? 'error'
        : 'waiting',
    last_cycle_at: configured ? at(28) : null,
    last_error_code: scenario === 'read-error' ? 'WECHAT_DB_UNAVAILABLE' : null,
    last_result: configured ? result : null,
    read_only: true,
    sending_enabled: false,
  };
  const settings = new Map<string, AISettings>();
  const records = new Map<string, AIRecord>();
  let used = 1;
  const record = (id: string): AIRecord => ({
    id: `demo-analysis-${id}`,
    conversation_id: id,
    context_hash: `demo-context-${id}`,
    model: '合成示例',
    created_at: at(30),
    stale: false,
    intent: id === 'demo-chat-0' ? 'scheduling' : 'information_request',
    summary:
      id === 'demo-chat-0'
        ? '周五分享 Finfold，时长 15 分钟。需在 11 月 18 日 18:00（北京时间）前提交产品简介与截图。'
        : id === 'demo-chat-1'
          ? '对方认可新版首页，希望让品牌记忆的入口更容易被首次使用的人找到。'
          : id === 'demo-chat-2'
            ? '对方希望商量面向海外创作者的合作直播，具体时间尚未确定。'
            : '对方已更新活动海报的标题与日期，文件在共享文件夹中，等你查看。',
    draft:
      id === 'demo-chat-0'
        ? '收到，15 分钟的分享没问题。简介和产品截图我会整理一下，11 月 18 日 18:00 前发你。也请把活动报名链接发我，方便一起准备。'
        : id === 'demo-chat-1'
          ? '收到，我会再检查一下首次使用的路径，把品牌记忆入口做得更清楚。调整好后发你看看。'
          : id === 'demo-chat-2'
            ? '可以，联合直播的方向很合适。我先整理一下我们能分享的内容，再一起确认时间和形式。'
            : '收到，我会查看共享文件夹里的新版海报，核对标题和日期后再回复你。',
    risk_flags: id === 'demo-chat-0' ? ['commitment'] : [],
    evidence_ids: messages[id].map((item) => item.id),
    usage: { prompt_tokens: 0, completion_tokens: 0 },
    sending_enabled: false,
  });
  if (configured && scenario !== 'no-model')
    records.set('demo-chat-0', record('demo-chat-0'));
  const copy = <T>(value: T): T => structuredClone(value);
  const inbox: InboxClient = {
    async getInboxInfo() {
      return {
        storage_enabled: true,
        source_mode: 'mixed',
        import_limit: 1000,
        platforms: [
          {
            id: 'weixin',
            name: '微信',
            live_status: configured ? 'configured' : 'not_connected',
            message_count: configured ? 12 : 0,
          },
        ],
      };
    },
    async getConversations(_token, _signal, filters) {
      return {
        items: configured
          ? copy(
              conversations.filter(
                (item) =>
                  (!filters.platform || filters.platform === item.platform) &&
                  item.archived === Boolean(filters.archived) &&
                  (!filters.unread || item.unread_count > 0) &&
                  (!filters.pinned || item.pinned) &&
                  (!filters.q ||
                    `${item.title} ${messages[item.id].map((m) => m.body).join(' ')}`.includes(
                      filters.q,
                    )),
              ),
            )
          : [],
        next_cursor: null,
      };
    },
    async getConversationMessages(_token, _signal, id) {
      return { items: copy(messages[id] ?? []), next_cursor: null };
    },
    async updateConversation(_token, _signal, id, changes) {
      conversations = conversations.map((item) =>
        item.id === id
          ? {
              ...item,
              ...(changes.read === undefined
                ? {}
                : { unread_count: changes.read ? 0 : 1 }),
              ...(changes.pinned === undefined
                ? {}
                : { pinned: changes.pinned }),
              ...(changes.archived === undefined
                ? {}
                : { archived: changes.archived }),
            }
          : item,
      );
    },
  };
  const wechat: WechatClient = {
    async getWechatStatus() {
      return copy({
        configured,
        state: configured ? 'read' : 'not_configured',
        account_id: configured ? '演示账号' : null,
        read_only: true,
        last_read_at: configured ? result.read_at : null,
        last_result: configured ? result : null,
        sending_enabled: false,
        monitor,
      });
    },
    async getWechatSessions() {
      return configured
        ? names.map((name, i) => ({ name, peer_id: `wxid_demo_${i}` }))
        : [];
    },
    async readWechatMessages() {
      return copy({ ...result, inserted: 0, duplicates: 3, conversations: 1 });
    },
    async saveWechatMonitor(_token, _signal, value) {
      monitor = {
        ...monitor,
        ...value,
        state: value.enabled
          ? scenario === 'read-error'
            ? 'error'
            : 'waiting'
          : 'disabled',
      };
      return copy(monitor);
    },
  };
  const ai: AIClient = {
    async getAIStatus() {
      return {
        configured: scenario !== 'no-model',
        provider: '演示',
        model: '合成示例',
        endpoint_host: '演示空间（不连接模型）',
        sending_enabled: false,
        daily_limit: 20,
        used_today: used,
      };
    },
    async getAIContext(_token, _signal, id) {
      return {
        context_hash: `demo-context-${id}`,
        model: '合成示例',
        endpoint_host: '演示空间（不连接模型）',
        external: false,
        message_count: messages[id].length,
        memory_count: 0,
        truncated: false,
        messages: copy(messages[id]),
        memories: [],
        ...(settings.get(id) ?? { mode: 'draft_only', tone: 'natural' }),
      };
    },
    async getAnalysis(_token, _signal, id) {
      return { analysis: copy(records.get(id) ?? null), memories: [] };
    },
    async analyzeConversation(_token, _signal, id) {
      const value = record(id);
      records.set(id, value);
      used++;
      return copy(value);
    },
    async saveAIDraft(_token, _signal, id, _analysisId, body) {
      const value = records.get(id);
      if (value) records.set(id, { ...value, draft: body });
    },
    async saveAISettings(_token, _signal, id, value) {
      settings.set(id, value);
    },
    async confirmMemory() {},
    async deleteMemory() {},
  };
  return { inbox, wechat, ai };
}
