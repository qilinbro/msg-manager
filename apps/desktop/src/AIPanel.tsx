import { useEffect, useRef, useState } from 'react';
import { ServiceError } from './api';
import {
  analyzeConversation,
  confirmMemory,
  deleteMemory,
  getAIContext,
  getAIStatus,
  getAnalysis,
  saveAIDraft,
  saveAISettings,
} from './ai-api';
import type {
  AIContext,
  AIDraftBuffer,
  AIEvidence,
  AIEnvelope,
  AIMemory,
  AISettings,
  AIStatus,
} from './ai-api';
import type { InboxMessage } from './inbox-api';
import './ai.css';

const INTENTS: Record<string, string> = {
  greeting: '日常问候',
  information_request: '信息咨询',
  scheduling: '时间与安排',
  commitment: '承诺与决定',
  payment: '金钱与付款',
  emotional_conflict: '情感与关系沟通',
  other: '其他消息',
};
const RISKS: Record<string, string> = {
  payment: '涉及金钱',
  commitment: '涉及承诺',
  sensitive: '涉及敏感信息',
  emotional_conflict: '涉及关系冲突',
  uncertain: '存在不确定信息',
};
function dateText(value: string) {
  return new Date(value).toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}
function messageOf(value: unknown) {
  return value instanceof ServiceError
    ? value.message
    : '暂时无法完成 AI 操作，请刷新后重试。';
}
function Evidence({
  ids,
  sources,
}: {
  ids: string[];
  sources: Map<string, AIEvidence>;
}) {
  return (
    <details className="ai-evidence">
      <summary>核对引用的消息 · {ids.length} 条</summary>
      {ids.length ? (
        ids.map((id, index) => {
          const item = sources.get(id);
          return (
            <blockquote key={id}>
              <div>
                <strong>引用 {index + 1}</strong>
                {item ? (
                  <time dateTime={item.occurred_at}>
                    {dateText(item.occurred_at)}
                  </time>
                ) : null}
              </div>
              <p>
                {item
                  ? item.body
                  : '这条来源不在当前已加载范围内。请查看该会话的更早记录后核对。'}
              </p>
              <code>{id}</code>
            </blockquote>
          );
        })
      ) : (
        <p>未提供可显示的消息引用，请自行核对内容。</p>
      )}
    </details>
  );
}

export default function AIPanel({
  token,
  conversationId,
  messages,
  draftCache,
  onAuthFailure,
}: {
  token: string;
  conversationId: string;
  messages: InboxMessage[];
  draftCache: Map<string, AIDraftBuffer>;
  onAuthFailure: () => void;
}) {
  const [status, setStatus] = useState<AIStatus | null>(null);
  const [context, setContext] = useState<AIContext | null>(null);
  const [data, setData] = useState<AIEnvelope>({
    analysis: null,
    memories: [],
  });
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [readError, setReadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<
    'preview' | 'analyze' | 'save' | 'memory' | 'settings' | null
  >(null);
  const [preview, setPreview] = useState<AIContext | null>(null);
  const [consent, setConsent] = useState(false);
  const [draft, setDraft] = useState('');
  const [dirty, setDirty] = useState(false);
  const [mode, setMode] = useState<AISettings['mode']>('draft_only');
  const [tone, setTone] = useState<AISettings['tone']>('natural');
  const pendingRead = useRef<AbortController | null>(null);
  const pendingAction = useRef<AbortController | null>(null);
  const readEpoch = useRef(0);
  const dirtyRef = useRef(false);
  const mounted = useRef(true);
  const draftField = useRef<HTMLTextAreaElement | null>(null);
  const panel = useRef<HTMLElement | null>(null);
  const analysis = data.analysis;
  const stale = Boolean(
    analysis &&
    (analysis.stale ||
      (context && context.context_hash !== analysis.context_hash)),
  );
  const locked = Boolean(busy);
  const sources = new Map<string, AIEvidence>();
  for (const item of messages)
    sources.set(item.id, {
      id: item.id,
      body: item.body,
      direction: item.direction,
      occurred_at: item.occurred_at,
    });
  for (const item of context?.messages ?? []) sources.set(item.id, item);
  for (const item of preview?.messages ?? []) sources.set(item.id, item);

  useEffect(() => {
    mounted.current = true;
    panel.current?.scrollIntoView({ block: 'start' });
    panel.current?.focus({ preventScroll: true });
    return () => {
      mounted.current = false;
      pendingRead.current?.abort();
      pendingAction.current?.abort();
      readEpoch.current++;
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    pendingRead.current?.abort();
    pendingRead.current = controller;
    const epoch = ++readEpoch.current;
    setLoading(true);
    setReadError(null);
    Promise.all([
      getAIStatus(token, controller.signal),
      getAIContext(token, controller.signal, conversationId),
      getAnalysis(token, controller.signal, conversationId),
    ])
      .then(([nextStatus, nextContext, nextData]) => {
        if (controller.signal.aborted || epoch !== readEpoch.current) return;
        if (
          nextData.analysis &&
          nextData.analysis.conversation_id !== conversationId
        )
          throw new ServiceError(
            '分析结果不属于当前会话，尚未显示。',
            'INVALID_RESPONSE',
          );
        setStatus(nextStatus);
        setContext(nextContext);
        const buffered = draftCache.get(conversationId);
        setData({
          ...nextData,
          analysis: buffered
            ? {
                ...buffered.analysis,
                stale:
                  buffered.analysis.stale ||
                  nextData.analysis?.stale === true ||
                  nextData.analysis?.id !== buffered.analysis.id ||
                  nextContext.context_hash !== buffered.analysis.context_hash,
              }
            : nextData.analysis,
        });
        setMode(nextContext.mode);
        setTone(nextContext.tone);
        setDraft(buffered?.body ?? nextData.analysis?.draft ?? '');
        dirtyRef.current = Boolean(buffered);
        setDirty(Boolean(buffered));
        setPreview((current) => {
          if (current && current.context_hash !== nextContext.context_hash) {
            setConsent(false);
            return null;
          }
          return current;
        });
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted || epoch !== readEpoch.current) return;
        if (failure instanceof ServiceError && failure.status === 401) {
          onAuthFailure();
          return;
        }
        setReadError(messageOf(failure));
      })
      .finally(() => {
        if (epoch === readEpoch.current) {
          pendingRead.current = null;
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [token, conversationId, revision, onAuthFailure, draftCache]);

  const invalidateRead = () => {
    pendingRead.current?.abort();
    pendingRead.current = null;
    readEpoch.current++;
    setLoading(false);
  };
  const refresh = () => {
    setPreview(null);
    setConsent(false);
    setRevision((value) => value + 1);
  };
  const run = async (
    kind: NonNullable<typeof busy>,
    operation: (signal: AbortSignal) => Promise<void>,
  ) => {
    if (pendingAction.current) return;
    invalidateRead();
    const controller = new AbortController();
    pendingAction.current = controller;
    setBusy(kind);
    setError(null);
    setNotice(null);
    try {
      await operation(controller.signal);
    } catch (failure: unknown) {
      if (controller.signal.aborted || !mounted.current) return;
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(messageOf(failure));
      if (kind !== 'preview') refresh();
    } finally {
      if (pendingAction.current === controller) {
        pendingAction.current = null;
        if (mounted.current) setBusy(null);
      }
    }
  };
  const prepare = () => {
    if (!status?.configured || context?.mode === 'paused' || dirtyRef.current)
      return;
    void run('preview', async (signal) => {
      const next = await getAIContext(token, signal, conversationId);
      if (signal.aborted || !mounted.current) return;
      setContext(next);
      setPreview(next);
      setConsent(false);
    });
  };
  const analyze = () => {
    if (
      !preview ||
      !consent ||
      dirtyRef.current ||
      preview.mode === 'paused' ||
      !status?.configured
    )
      return;
    const hash = preview.context_hash;
    setPreview(null);
    setConsent(false);
    void run('analyze', async (signal) => {
      const result = await analyzeConversation(
        token,
        signal,
        conversationId,
        hash,
      );
      if (signal.aborted || !mounted.current) return;
      invalidateRead();
      draftCache.delete(conversationId);
      dirtyRef.current = false;
      setData((current) => ({ ...current, analysis: result }));
      setDraft(result.draft);
      setDirty(false);
      setNotice(
        '分析已保存。摘要、风险判断与草稿均为 AI 建议，请核对引用后再使用。',
      );
      refresh();
    });
  };
  const stopWaiting = () => {
    pendingAction.current?.abort();
    pendingAction.current = null;
    invalidateRead();
    setBusy(null);
    setNotice(
      '已停止等待，后台可能已处理并产生费用。请先刷新结果，再决定是否重试。',
    );
    refresh();
  };
  const saveDraft = () => {
    if (
      !analysis ||
      stale ||
      !draft.trim() ||
      new TextEncoder().encode(draft).length > 6000
    )
      return;
    const analysisID = analysis.id,
      body = draft;
    void run('save', async (signal) => {
      await saveAIDraft(token, signal, conversationId, analysisID, body);
      if (signal.aborted || !mounted.current) return;
      dirtyRef.current = false;
      setDirty(false);
      draftCache.delete(conversationId);
      setData((current) => ({
        ...current,
        analysis:
          current.analysis?.id === analysisID
            ? { ...current.analysis, draft: body }
            : current.analysis,
      }));
      setNotice('草稿已保存到本地，没有发送。');
      refresh();
    });
  };
  const copyDraft = async () => {
    if (!draft || (stale && !dirty)) return;
    try {
      await navigator.clipboard.writeText(draft);
      if (mounted.current)
        setNotice(
          stale
            ? '已复制未保存修改；对应上下文已过时，请核对新消息和事实后再使用。'
            : '草稿已复制，请核对内容后自行使用。',
        );
    } catch {
      draftField.current?.focus();
      draftField.current?.select();
      setNotice('未能自动复制，已选中草稿，可手动复制。');
    }
  };
  const changeMemory = (item: AIMemory, remove: boolean) => {
    if (dirtyRef.current) return;
    void run('memory', async (signal) => {
      if (remove) await deleteMemory(token, signal, conversationId, item.id);
      else await confirmMemory(token, signal, conversationId, item.id);
      if (signal.aborted || !mounted.current) return;
      setNotice(
        remove
          ? '这条记忆已删除。'
          : '记忆已由你确认，之后可作为当前会话的上下文使用。',
      );
      refresh();
    });
  };
  const saveSettings = () => {
    if (dirtyRef.current) return;
    const settings = { mode, tone };
    void run('settings', async (signal) => {
      await saveAISettings(token, signal, conversationId, settings);
      if (signal.aborted || !mounted.current) return;
      setNotice(
        settings.mode === 'paused'
          ? '当前会话已暂停新的 AI 分析，已有结果仍可查看。'
          : '当前会话的 AI 偏好已保存。',
      );
      refresh();
    });
  };
  return (
    <section
      className="ai-panel"
      ref={panel}
      tabIndex={-1}
      aria-labelledby="ai-panel-title"
      aria-busy={Boolean(busy || loading)}
    >
      <header className="ai-panel-heading">
        <div>
          <h3 id="ai-panel-title">AI 管理</h3>
          <p>只处理当前会话，先核对，再使用。</p>
        </div>
        <span className="ai-mode-label">仅分析与草稿</span>
      </header>
      <div className="ai-provider-line">
        <span>
          {loading
            ? '正在读取状态…'
            : status?.configured
              ? `${status.provider || '模型服务'} · ${status.model}`
              : '模型尚未配置'}
        </span>
        {status ? (
          <span>
            今日 {status.used_today} / {status.daily_limit} 次
          </span>
        ) : null}
      </div>
      {!status?.configured && !loading ? (
        <p className="ai-muted">
          请按项目 AI 接入说明在本机配置模型服务和密钥。工作台不收集模型密钥。
        </p>
      ) : null}
      <div className="ai-settings">
        <label>
          处理状态
          <select
            value={mode}
            onChange={(event) =>
              setMode(event.target.value as AISettings['mode'])
            }
            disabled={locked || loading || dirty}
          >
            <option value="draft_only">允许分析与草稿</option>
            <option value="paused">暂停新的 AI 处理</option>
          </select>
        </label>
        <label>
          回复语气
          <select
            value={tone}
            onChange={(event) =>
              setTone(event.target.value as AISettings['tone'])
            }
            disabled={locked || loading || dirty}
          >
            <option value="natural">自然</option>
            <option value="casual">轻松</option>
            <option value="professional">专业简洁</option>
          </select>
        </label>
        <button
          className="ai-text-button"
          onClick={saveSettings}
          disabled={
            locked ||
            loading ||
            dirty ||
            !context ||
            (mode === context.mode && tone === context.tone)
          }
        >
          {busy === 'settings' ? '保存中…' : '保存偏好'}
        </button>
      </div>
      {context?.mode === 'paused' ? (
        <p className="ai-muted">
          此会话已暂停 AI 处理。恢复并保存偏好后，可以重新预览分析范围。
        </p>
      ) : null}
      <div className="ai-toolbar">
        <button
          className="button button-secondary"
          onClick={prepare}
          disabled={
            locked ||
            loading ||
            !status?.configured ||
            context?.mode === 'paused' ||
            !context?.message_count ||
            dirty
          }
        >
          {busy === 'preview' ? '正在读取范围…' : '预览本次分析范围'}
        </button>
        <button
          className="ai-text-button"
          onClick={refresh}
          disabled={locked || loading}
        >
          刷新结果
        </button>
        {busy === 'analyze' ? (
          <button className="ai-text-button" onClick={stopWaiting}>
            停止等待
          </button>
        ) : null}
      </div>
      {dirty ? (
        <p className="ai-muted">
          有尚未保存的修改。切换会话或关闭面板后仍保留在本次工作台会话内；断开连接或关闭应用会清除。请先保存或还原，再分析或修改记忆与偏好。
        </p>
      ) : null}
      {preview ? (
        <div className="ai-preview">
          <h4>确认这次处理的内容</h4>
          <p>
            将向{' '}
            <strong>{preview.endpoint_host || '本机配置的模型服务'}</strong>{' '}
            提交当前会话的 <strong>{preview.message_count} 条消息</strong>和{' '}
            <strong>{preview.memory_count} 条已确认记忆</strong>。
            {preview.external
              ? '内容将离开本机交由该模型服务处理。'
              : '请求将提交到配置的本机模型服务。'}
          </p>
          {preview.truncated ? (
            <p className="ai-muted">
              本次只选择了预算内的近期内容，不包含此会话的全部历史。
            </p>
          ) : null}
          <details className="ai-context-details">
            <summary>查看本次消息与记忆</summary>
            {preview.messages.map((item, index) => (
              <blockquote key={item.id}>
                <div>
                  <strong>
                    消息 {index + 1} ·{' '}
                    {item.direction === 'outgoing' ? '我' : '对方'}
                  </strong>
                  <time dateTime={item.occurred_at}>
                    {dateText(item.occurred_at)}
                  </time>
                </div>
                <p>{item.body}</p>
              </blockquote>
            ))}
            {preview.memories.map((item) => (
              <p className="ai-context-memory" key={item.id}>
                已确认记忆：{item.content}
              </p>
            ))}
          </details>
          <label className="ai-consent">
            <input
              type="checkbox"
              checked={consent}
              onChange={(event) => setConsent(event.target.checked)}
              disabled={locked}
            />
            <span>
              我已核对以上范围，同意将这些内容提交给显示的模型服务，仅生成分析与草稿。
            </span>
          </label>
          <div className="ai-inline-actions">
            <button
              className="button button-primary"
              onClick={analyze}
              disabled={
                !consent || locked || dirty || preview.mode === 'paused'
              }
            >
              确认并开始分析
            </button>
            <button
              className="ai-text-button"
              onClick={() => {
                setPreview(null);
                setConsent(false);
              }}
            >
              暂不分析
            </button>
          </div>
        </div>
      ) : null}
      {busy === 'analyze' ? (
        <p className="ai-running" role="status">
          正在阅读当前会话并整理建议，请稍候。分析不会发送任何消息。
        </p>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {notice ? (
          <p className="ai-feedback" role="status">
            {notice}
          </p>
        ) : null}
        {readError ? (
          <p className="ai-feedback is-error" role="alert">
            {readError}
          </p>
        ) : null}
        {error ? (
          <p className="ai-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
      </div>
      {analysis ? (
        <div className="ai-result">
          <div className="ai-result-heading">
            <h4>会话建议</h4>
            <span>
              {INTENTS[analysis.intent] || '其他消息'} ·{' '}
              {dateText(analysis.created_at)}
            </span>
          </div>
          {stale ? (
            <p className="ai-stale" role="status">
              会话或记忆已变化，这份建议可能已过时。请重新分析后再保存或复制草稿。
            </p>
          ) : null}
          <p className="ai-summary">{analysis.summary}</p>
          {analysis.risk_flags.length ? (
            <div className="ai-risks">
              <strong>需要你亲自核对</strong>
              <ul>
                {analysis.risk_flags.map((risk) => (
                  <li key={risk}>{RISKS[risk] || '存在需要人工核对的内容'}</li>
                ))}
              </ul>
            </div>
          ) : (
            <p className="ai-muted">
              模型未标出风险，也请核对事实、语气与是否涉及承诺。
            </p>
          )}
          <Evidence ids={analysis.evidence_ids} sources={sources} />
          <label
            className="ai-draft-label"
            htmlFor={`ai-draft-${conversationId}`}
          >
            回复草稿 · 尚未发送
          </label>
          <textarea
            id={`ai-draft-${conversationId}`}
            ref={draftField}
            className="ai-draft"
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              dirtyRef.current = true;
              setDirty(true);
              setPreview(null);
              setConsent(false);
              draftCache.set(conversationId, {
                analysis,
                body: event.target.value,
              });
            }}
            disabled={locked || stale}
            rows={5}
            maxLength={6000}
          />
          <div className="ai-draft-actions">
            <button
              className="button button-secondary"
              onClick={saveDraft}
              disabled={
                locked ||
                loading ||
                stale ||
                !dirty ||
                !draft.trim() ||
                new TextEncoder().encode(draft).length > 6000
              }
            >
              {busy === 'save' ? '保存中…' : '保存草稿'}
            </button>
            <button
              className="ai-text-button"
              onClick={copyDraft}
              disabled={locked || (stale && !dirty) || !draft.trim()}
            >
              {stale && dirty ? '复制未保存修改' : '复制草稿'}
            </button>
            {dirty ? (
              <button
                className="ai-text-button"
                disabled={locked}
                onClick={() => {
                  setDraft(analysis.draft);
                  dirtyRef.current = false;
                  setDirty(false);
                  draftCache.delete(conversationId);
                  refresh();
                }}
              >
                还原修改
              </button>
            ) : null}
            <span>仅保存在本地，不提供发送</span>
          </div>
          <p className="ai-result-meta">
            {analysis.model} · 输入 {analysis.usage.prompt_tokens} / 输出{' '}
            {analysis.usage.completion_tokens} token · AI 建议，需核对
          </p>
        </div>
      ) : !loading ? (
        <p className="ai-empty">
          还没有这段会话的 AI 分析。先预览内容，再确认开始。
        </p>
      ) : null}
      {data.memories.length ? (
        <section
          className="ai-memories"
          aria-labelledby={`ai-memory-${conversationId}`}
        >
          <h4 id={`ai-memory-${conversationId}`}>这段会话的记忆</h4>
          <p>模型提取的信息先保留为待核实，只有你确认后才用于后续分析。</p>
          <ul>
            {data.memories.map((item) => (
              <li key={item.id}>
                <div className="ai-memory-heading">
                  <span>
                    {item.verification_state === 'confirmed'
                      ? '你已确认'
                      : '待核实候选'}
                  </span>
                  <time dateTime={item.created_at}>
                    {dateText(item.created_at)}
                  </time>
                </div>
                <p>{item.content}</p>
                <Evidence ids={item.evidence_ids} sources={sources} />
                <div className="ai-inline-actions">
                  {item.verification_state === 'unverified' ? (
                    <button
                      className="ai-text-button"
                      onClick={() => changeMemory(item, false)}
                      disabled={locked || loading || dirty}
                    >
                      核对后确认
                    </button>
                  ) : null}
                  <button
                    className="ai-text-button ai-delete"
                    onClick={() => changeMemory(item, true)}
                    disabled={locked || loading || dirty}
                  >
                    删除记忆
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </section>
  );
}
