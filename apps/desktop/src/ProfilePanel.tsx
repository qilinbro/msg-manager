import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { ServiceError } from './api';
import {
  CATEGORY_NAMES,
  PROFILE_CATEGORIES,
  enrichProfile,
  generateProfile,
  getProfile,
  getProfileStatus,
  profileErrorMessage,
  profileJobError,
  safeProfileSourceURL,
  saveProfile,
  setProfileFactState,
} from './profile-api';
import type {
  Profile,
  ProfileFact,
  ProfileJob,
  ProfileSettings,
  ProfileStatus,
} from './profile-api';
import './profile.css';
import ProfileLinksPanel from './ProfileLinksPanel';

const EMPTY: ProfileSettings = {
  public_name: '',
  company: '',
  role: '',
  manual_notes: '',
  category_override: '',
  auto_organize: false,
  public_search_enabled: false,
};
const JOB_STATES: Record<ProfileJob['state'], string> = {
  queued: '等待后台处理',
  running: '后台处理中',
  succeeded: '处理成功',
  failed: '处理未完成',
  cancelled: '已停止',
};
const FIELD_NAMES: Record<string, string> = {
  public_bio: '公开简介',
  name: '姓名',
  public_name: '姓名',
  company: '公司',
  role: '职位',
  interest: '兴趣',
  interests: '兴趣',
  project: '共同项目',
  location: '地点',
  preference: '偏好',
  commitment: '约定',
};
const settingsOf = (profile: Profile): ProfileSettings => ({
  public_name: profile.public_name,
  company: profile.company,
  role: profile.role,
  manual_notes: profile.manual_notes,
  category_override: profile.category_override,
  auto_organize: profile.auto_organize,
  public_search_enabled: profile.public_search_enabled,
});
const dateText = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour12: false,
  });
const uncertainFailure = (value: unknown) =>
  value instanceof ServiceError &&
  ['TIMEOUT', 'NETWORK_ERROR', 'INVALID_RESPONSE'].includes(value.code);
const publicIdentity = (name: string) =>
  Boolean(name.trim()) && !/^wxid_/i.test(name.trim());

function UnattendedReceipt({
  profile,
  status,
}: {
  profile: Profile;
  status: ProfileStatus | null;
}) {
  const jobs = [...profile.jobs].sort(
    (left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at),
  );
  const latest = jobs[0] ?? null;
  const active = jobs.find((job) => ['queued', 'running'].includes(job.state));
  const state = profile.ai_paused
    ? 'paused'
    : (active?.state ??
      latest?.state ??
      (profile.auto_organize ? 'queued' : 'idle'));
  const stateLabel: Record<string, string> = {
    idle: '等待授权',
    queued: '等待后台处理',
    running: '后台处理中',
    succeeded: '最近一批已完成',
    failed: '处理未完成',
    cancelled: '已停止',
    paused: '已暂停',
  };
  const action = latest?.kind === 'enrich' ? '公开资料检索' : '聊天档案整理';
  const processed = profile.analysis_processed ?? 0;
  const total = profile.analysis_total ?? 0;
  const hasProgress =
    profile.auto_organize && profile.analysis_total !== undefined;
  const nextStep = profile.ai_paused
    ? '恢复此联系人的 AI 后，后台任务才会继续。'
    : !status?.model_configured
      ? '先在设置中保存模型，再确认这位联系人的处理范围。'
      : state === 'failed'
        ? '先刷新状态并检查失败原因，再决定是否重新提交。'
        : state === 'queued' || state === 'running'
          ? '离开页面后会继续；后台和电脑需要保持运行。'
          : state === 'succeeded' && hasProgress && processed < total
            ? '这一批已完成，后台会继续读取剩余历史。'
            : profile.auto_organize
              ? '新消息会按授权继续进入后台整理。'
              : '当前没有持续任务；如需离场整理，请明确开启授权。';
  return (
    <section
      className="unattended-receipt"
      aria-labelledby="unattended-receipt-title"
    >
      <div className="unattended-receipt-heading">
        <div>
          <h4 id="unattended-receipt-title">不在场回执</h4>
        </div>
        <span className={`unattended-state is-${state}`}>
          {stateLabel[state]}
        </span>
      </div>
      <div className="unattended-receipt-grid">
        <div>
          <span className="unattended-label">离开期间做了什么</span>
          <strong>
            {latest
              ? action
              : profile.auto_organize
                ? '等待新消息或历史任务'
                : '尚未授权后台任务'}
          </strong>
        </div>
        <div>
          <span className="unattended-label">处理进度</span>
          <strong>
            {hasProgress ? `${processed} / ${total} 条` : '按任务状态查看'}
          </strong>
        </div>
        <div>
          <span className="unattended-label">最近更新时间</span>
          <strong>
            {latest ? dateText(latest.updated_at) : '暂无后台记录'}
          </strong>
        </div>
      </div>
      {latest?.last_error_code ? (
        <p className="unattended-error">
          {profileJobError(latest.last_error_code)}
        </p>
      ) : null}
      <p className="unattended-next">
        <strong>下一步：</strong>
        {nextStep}
      </p>
      <p className="profile-muted">
        后台连接：{status ? '已返回状态' : '状态未读取'} · 模型：
        {status?.model_configured ? (status.model ?? '已配置') : '未配置'} ·
        发送：关闭
      </p>
    </section>
  );
}

export default function ProfilePanel({
  token,
  conversationId,
  displayName,
  standalone = false,
  onAuthFailure,
  onChanged,
}: {
  token: string;
  conversationId: string;
  displayName: string;
  standalone?: boolean;
  onAuthFailure: () => void;
  onChanged: () => void;
}) {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [status, setStatus] = useState<ProfileStatus | null>(null);
  const [form, setForm] = useState<ProfileSettings>(EMPTY);
  const [baseRevision, setBaseRevision] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [consent, setConsent] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const [reload, setReload] = useState(0);
  const mounted = useRef(false);
  const dirtyRef = useRef(false);
  const snapshot = useRef<Profile | null>(null);
  const readRequest = useRef<AbortController | null>(null);
  const operation = useRef<AbortController | null>(null);
  const readEpoch = useRef(0);
  const modelID = useRef<string | null>(null);
  const onChangedRef = useRef(onChanged);
  onChangedRef.current = onChanged;
  snapshot.current = profile;

  const accept = (value: Profile, replaceSettings = false) => {
    const previous = snapshot.current;
    snapshot.current = value;
    setProfile(value);
    if (replaceSettings || !dirtyRef.current) {
      setForm(settingsOf(value));
      setBaseRevision(value.revision);
      dirtyRef.current = false;
      setDirty(false);
    }
    if (previous && previous.category !== value.category)
      onChangedRef.current();
  };

  useEffect(() => {
    mounted.current = true;
    snapshot.current = null;
    dirtyRef.current = false;
    modelID.current = null;
    setProfile(null);
    setStatus(null);
    setForm(EMPTY);
    setBaseRevision(0);
    setDirty(false);
    setConsent(false);
    setBusy(null);
    setUncertain(false);
    setReadError(null);
    setStatusError(null);
    setError(null);
    setNotice(null);
    return () => {
      mounted.current = false;
      readEpoch.current++;
      readRequest.current?.abort();
      operation.current?.abort();
      readRequest.current = null;
      operation.current = null;
    };
  }, [token, conversationId]);

  useEffect(() => {
    let active = true;
    const read = async () => {
      if (!active || readRequest.current || operation.current) return;
      const controller = new AbortController();
      readRequest.current = controller;
      const epoch = ++readEpoch.current;
      if (!snapshot.current) setLoading(true);
      try {
        const [profileResult, statusResult] = await Promise.allSettled([
          getProfile(token, controller.signal, conversationId),
          getProfileStatus(token, controller.signal),
        ]);
        if (
          !active ||
          !mounted.current ||
          controller.signal.aborted ||
          epoch !== readEpoch.current
        )
          return;
        for (const result of [profileResult, statusResult]) {
          if (
            result.status === 'rejected' &&
            result.reason instanceof ServiceError &&
            result.reason.status === 401
          ) {
            onAuthFailure();
            return;
          }
        }
        if (profileResult.status === 'fulfilled') {
          accept(profileResult.value);
          setReadError(null);
          setUncertain(false);
        } else setReadError(profileErrorMessage(profileResult.reason));
        if (statusResult.status === 'fulfilled') {
          const value = statusResult.value;
          if (modelID.current !== value.model_config_id) setConsent(false);
          modelID.current = value.model_config_id;
          setStatus(value);
          setStatusError(null);
        } else {
          setStatus(null);
          setConsent(false);
          setStatusError(profileErrorMessage(statusResult.reason));
        }
      } finally {
        if (epoch === readEpoch.current) {
          readRequest.current = null;
          if (active && mounted.current) setLoading(false);
        }
      }
    };
    void read();
    const timer = window.setInterval(() => void read(), 5000);
    const onVisible = () => {
      if (document.visibilityState === 'visible') void read();
    };
    window.addEventListener('focus', read);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener('focus', read);
      document.removeEventListener('visibilitychange', onVisible);
      readRequest.current?.abort();
      readRequest.current = null;
      readEpoch.current++;
    };
  }, [token, conversationId, reload, onAuthFailure]);

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirtyRef.current) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);

  const edit = (changes: Partial<ProfileSettings>) => {
    dirtyRef.current = true;
    setDirty(true);
    setConsent(false);
    setError(null);
    setNotice(null);
    setForm((previous) => ({ ...previous, ...changes }));
  };
  const begin = (kind: string) => {
    if (operation.current || !snapshot.current || uncertain) return null;
    readRequest.current?.abort();
    readRequest.current = null;
    readEpoch.current++;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(kind);
    setError(null);
    setNotice(null);
    return controller;
  };
  const run = async (
    kind: string,
    action: (signal: AbortSignal) => Promise<Profile | ProfileJob>,
  ) => {
    const controller = begin(kind);
    if (!controller) return;
    try {
      const value = await action(controller.signal);
      if (
        controller.signal.aborted ||
        !mounted.current ||
        operation.current !== controller
      )
        return;
      if ('facts' in value) {
        accept(value, kind === 'save');
        setNotice(
          kind === 'save' ? '档案设置已保存。' : '已记录你的核实结果。',
        );
        onChangedRef.current();
      } else {
        setProfile((previous) =>
          previous
            ? {
                ...previous,
                jobs: [
                  value,
                  ...previous.jobs.filter((job) => job.id !== value.id),
                ],
              }
            : previous,
        );
        setNotice('任务已交给后台。离开页面后会继续，可稍后回来查看结果。');
      }
    } catch (failure: unknown) {
      if (
        controller.signal.aborted ||
        !mounted.current ||
        operation.current !== controller
      )
        return;
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(profileErrorMessage(failure));
      if (uncertainFailure(failure)) setUncertain(true);
    } finally {
      if (operation.current === controller) {
        operation.current = null;
        if (mounted.current) {
          setBusy(null);
          setReload((value) => value + 1);
        }
      }
    }
  };
  const needsAuthorization = form.auto_organize || form.public_search_enabled;
  const stale = Boolean(profile && dirty && profile.revision !== baseRevision);
  const modelReady = Boolean(
    status?.model_configured && status.model_config_id,
  );
  const paused = Boolean(profile?.ai_paused);
  const canAuthorize = modelReady && !paused;
  const hasPublicName = publicIdentity(form.public_name);
  const bytes = (value: string) => new TextEncoder().encode(value).length;
  const validFields =
    bytes(form.public_name) <= 256 &&
    bytes(form.company) <= 256 &&
    bytes(form.role) <= 256 &&
    bytes(form.manual_notes) <= 8000;
  const blocked =
    loading || Boolean(busy) || !profile || uncertain || Boolean(readError);
  const pausedNewAuthorization =
    paused &&
    Boolean(
      profile &&
      ((!profile.auto_organize && form.auto_organize) ||
        (!profile.public_search_enabled && form.public_search_enabled)),
    );
  const saveBlocked =
    blocked ||
    !dirty ||
    stale ||
    !validFields ||
    pausedNewAuthorization ||
    (needsAuthorization && !paused && (!consent || !canAuthorize)) ||
    (form.public_search_enabled &&
      ((!paused && !status?.search_configured) || !hasPublicName));
  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saveBlocked || !profile) return;
    void run('save', (signal) =>
      saveProfile(token, signal, conversationId, {
        ...form,
        expected_revision: baseRevision,
        confirmed: !paused && needsAuthorization && consent,
        model_config_id: paused
          ? profile.model_config_id
          : needsAuthorization
            ? (status?.model_config_id ?? '')
            : '',
      }),
    );
  };
  const submit = (kind: 'generate' | 'enrich') => {
    if (
      !profile ||
      blocked ||
      dirty ||
      !consent ||
      !canAuthorize ||
      !status?.model_config_id
    )
      return;
    if (
      kind === 'enrich' &&
      (!profile.public_search_enabled ||
        !publicIdentity(profile.public_name) ||
        !status.search_configured)
    )
      return;
    const input = {
      expected_revision: profile.revision,
      confirmed: true as const,
      model_config_id: status.model_config_id,
    };
    void run(kind, (signal) =>
      kind === 'generate'
        ? generateProfile(token, signal, conversationId, input)
        : enrichProfile(token, signal, conversationId, input),
    );
  };
  const setFact = (fact: ProfileFact, state: 'confirmed' | 'rejected') => {
    if (!profile || blocked || fact.state !== 'unverified') return;
    void run(fact.id, (signal) =>
      setProfileFactState(
        token,
        signal,
        conversationId,
        fact.id,
        state,
        profile.revision,
      ),
    );
  };
  const download = () => {
    if (!profile?.document_markdown) return;
    const url = URL.createObjectURL(
      new Blob([profile.document_markdown], {
        type: 'text/markdown;charset=utf-8',
      }),
    );
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `profile-${(profile.display_name || 'contact').replace(/[^\p{L}\p{N}_-]/gu, '_').slice(0, 64)}.md`;
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  };
  const busyJob = (kind: string) =>
    profile?.jobs.some(
      (job) => job.kind === kind && ['queued', 'running'].includes(job.state),
    );
  const factList = (items: ProfileFact[], candidate = false) =>
    items.length ? (
      <ul className="profile-facts">
        {items.map((fact) => (
          <li key={fact.id}>
            <div>
              <span className="profile-field-name">
                {FIELD_NAMES[fact.field] || fact.field}
              </span>
              <p>{fact.value}</p>
            </div>
            <p className="profile-source-note">
              {fact.origin === 'public'
                ? '公开资料候选 · 同名匹配需人工核对'
                : '聊天记录候选'}
              {fact.message_ids.length
                ? ` · ${fact.message_ids.length} 条消息依据`
                : ''}
            </p>
            {fact.source_ids.length ? (
              <ul className="profile-fact-links">
                {fact.source_ids.map((sourceID) => {
                  const source = profile?.sources.find(
                    (item) => item.id === sourceID,
                  );
                  const url = source ? safeProfileSourceURL(source.url) : null;
                  return (
                    <li key={sourceID}>
                      {source && url ? (
                        <a href={url} target="_blank" rel="noopener noreferrer">
                          {source.title || '查看来源'}
                        </a>
                      ) : (
                        <span>来源暂不可用</span>
                      )}
                    </li>
                  );
                })}
              </ul>
            ) : null}
            {candidate ? (
              <div className="profile-inline-actions">
                <button
                  type="button"
                  className="button button-secondary"
                  disabled={blocked}
                  onClick={() => setFact(fact, 'confirmed')}
                >
                  确认信息
                </button>
                <button
                  type="button"
                  className="ai-text-button"
                  disabled={blocked}
                  onClick={() => setFact(fact, 'rejected')}
                >
                  拒绝此项
                </button>
              </div>
            ) : null}
          </li>
        ))}
      </ul>
    ) : (
      <p className="profile-muted">
        {candidate
          ? '暂无待核实信息。模型和公开来源的新信息会先放在这里。'
          : '暂无已确认事实。确认候选后会归入此处。'}
      </p>
    );

  return (
    <section
      className="profile-panel"
      id={`personal-profile-${conversationId}`}
      aria-label={standalone ? '档案内容' : undefined}
      aria-labelledby={
        standalone ? undefined : `profile-title-${conversationId}`
      }
    >
      <div className="profile-panel-heading">
        <div>
          {!standalone ? (
            <h3 id={`profile-title-${conversationId}`}>
              {displayName}的个人档案
            </h3>
          ) : null}
          <p>专属文档保存在本地加密库，聊天整理与公开资料分开核对。</p>
        </div>
        <div className="profile-inline-actions">
          <button
            type="button"
            className="ai-text-button"
            onClick={() => setReload((value) => value + 1)}
            disabled={Boolean(busy)}
          >
            刷新档案
          </button>
          <button
            type="button"
            className="button button-secondary"
            disabled={!profile?.document_markdown}
            onClick={download}
          >
            导出已保存 Markdown
          </button>
        </div>
      </div>
      {loading && !profile ? (
        <p className="profile-muted" role="status">
          正在读取个人档案…
        </p>
      ) : null}
      {readError ? (
        <p className="profile-feedback is-error" role="alert">
          {readError}
        </p>
      ) : null}
      {profile ? (
        <>
          <UnattendedReceipt profile={profile} status={status} />
          <div className="profile-category-line">
            {profile.auto_organize && profile.analysis_total !== undefined ? (
              <p role="status">
                全历史整理：已处理 {profile.analysis_processed ?? 0} /{' '}
                {profile.analysis_total} 条。额度不足时会等待，暂停后可继续。
              </p>
            ) : null}
            <span>
              当前分类：<strong>{CATEGORY_NAMES[profile.category]}</strong>
              {profile.category_override
                ? ' · 手工指定'
                : profile.ai_category === 'unclassified'
                  ? ' · 待分类'
                  : ' · AI 建议'}
            </span>
            <span>AI 建议：{CATEGORY_NAMES[profile.ai_category]}</span>
            {profile.updated_at ? (
              <time dateTime={profile.updated_at}>
                更新于 {dateText(profile.updated_at)}
              </time>
            ) : null}
          </div>
          {profile.truncated ? (
            <p className="profile-muted">
              本档案显示的是有界记录与来源，未涵盖全部聊天历史或全部候选。
            </p>
          ) : null}
          <form className="profile-settings" onSubmit={save}>
            <fieldset disabled={Boolean(busy)}>
              <legend>你提供的资料</legend>
              <p className="profile-muted">
                公开搜索姓名需要你明确填写，不会将平台账号自动当作真实姓名。
              </p>
              <div className="profile-field-grid">
                <label>
                  公开搜索姓名
                  <input
                    value={form.public_name}
                    maxLength={256}
                    onChange={(event) =>
                      edit({ public_name: event.target.value })
                    }
                    autoComplete="off"
                  />
                </label>
                <label>
                  公司
                  <input
                    value={form.company}
                    maxLength={256}
                    onChange={(event) => edit({ company: event.target.value })}
                    autoComplete="off"
                  />
                </label>
                <label>
                  职位
                  <input
                    value={form.role}
                    maxLength={256}
                    onChange={(event) => edit({ role: event.target.value })}
                    autoComplete="off"
                  />
                </label>
                <label>
                  手工分类
                  <select
                    value={form.category_override}
                    onChange={(event) =>
                      edit({
                        category_override: event.target
                          .value as ProfileSettings['category_override'],
                      })
                    }
                  >
                    <option value="">使用 AI 分类</option>
                    {PROFILE_CATEGORIES.map((item) => (
                      <option key={item} value={item}>
                        {CATEGORY_NAMES[item]}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <label>
                人工备注
                <textarea
                  value={form.manual_notes}
                  maxLength={8000}
                  rows={4}
                  onChange={(event) =>
                    edit({ manual_notes: event.target.value })
                  }
                />
              </label>
              <p className="profile-muted">
                手工分类优先，AI
                不会覆盖。请简短填写姓名、公司和职位，较长资料可放入备注。
              </p>
            </fieldset>
            <fieldset
              className="profile-authorization"
              disabled={Boolean(busy)}
            >
              <legend>后台整理与公开检索</legend>
              <label className="profile-check">
                <input
                  type="checkbox"
                  checked={form.auto_organize}
                  disabled={!form.auto_organize && !canAuthorize}
                  onChange={(event) =>
                    edit({ auto_organize: event.target.checked })
                  }
                />
                <span>有新消息时自动整理此人的档案</span>
              </label>
              <label className="profile-check">
                <input
                  type="checkbox"
                  checked={form.public_search_enabled}
                  disabled={
                    !form.public_search_enabled &&
                    (!canAuthorize || !status?.search_configured)
                  }
                  onChange={(event) =>
                    edit({ public_search_enabled: event.target.checked })
                  }
                />
                <span>允许按已填写的姓名、公司和职位检索公开资料</span>
              </label>
              {paused ? (
                <p className="profile-muted">
                  该联系人的 AI 已暂停。请先在“AI
                  管理”恢复，再启用整理或检索；人工资料仍可编辑。
                </p>
              ) : null}
              {!status?.model_configured ? (
                <p className="profile-muted">
                  模型尚未配置，请在本机后台配置后重启服务。人工备注和分类可先保存。
                </p>
              ) : (
                <p className="profile-muted">
                  当前模型：{status.model || '已配置模型'}
                  {status.endpoint_host ? ` · ${status.endpoint_host}` : ''} ·
                  今日已用 {status.used_today}/{status.daily_limit}
                </p>
              )}
              {!status?.search_configured ? (
                <p className="profile-muted">
                  公开检索尚未配置，搜索密钥仅在本机后台设置。
                </p>
              ) : (
                <p className="profile-muted">
                  公开检索今日已用 {status.search_used_today}/
                  {status.search_daily_limit}
                </p>
              )}
              {statusError ? (
                <p className="profile-feedback is-error" role="alert">
                  {statusError}
                </p>
              ) : null}
              <label className="profile-check profile-consent">
                <input
                  type="checkbox"
                  checked={consent}
                  disabled={!canAuthorize}
                  onChange={(event) => setConsent(event.target.checked)}
                />
                <span>
                  我确认将此会话最近最多20条、20KB聊天及已保存档案信息发送到当前模型；开启公开检索时，只按姓名、公司和职位搜索。同名结果和新事实必须由我核实。
                </span>
              </label>
            </fieldset>
            {!validFields ? (
              <p className="profile-feedback is-error" role="alert">
                资料超过长度限制，请缩短姓名、公司、职位或备注。
              </p>
            ) : null}
            {dirty ? (
              <p className="profile-muted">
                设置有未保存改动，后台更新不会覆盖你的输入。
              </p>
            ) : null}
            {stale ? (
              <p className="profile-feedback" role="status">
                档案已有更新，当前输入已保留。请重新载入已保存设置后再修改。
                <button
                  type="button"
                  className="ai-text-button"
                  onClick={() => {
                    accept(profile, true);
                    setConsent(false);
                  }}
                >
                  重新载入设置
                </button>
              </p>
            ) : null}
            <button
              type="submit"
              className="button button-secondary"
              disabled={saveBlocked}
            >
              {busy === 'save' ? '正在保存…' : '保存档案设置'}
            </button>
          </form>
          <section className="profile-section">
            <h4>AI 摘要</h4>
            <p className="profile-summary">
              {profile.summary ||
                '还没有整理摘要。确认发送范围后，可手动整理本次聊天。'}
            </p>
            <p className="profile-muted">
              摘要和分类是 AI 建议，已确认事实单独列在下方。
            </p>
            {profile.tags.length ? (
              <div className="profile-tags">
                {profile.tags.map((tag) => (
                  <span key={tag}>{tag}</span>
                ))}
              </div>
            ) : null}
            <div className="profile-inline-actions">
              <button
                type="button"
                className="button button-secondary"
                disabled={
                  blocked ||
                  dirty ||
                  !consent ||
                  !canAuthorize ||
                  Boolean(busyJob('organize'))
                }
                onClick={() => submit('generate')}
              >
                整理聊天与分类
              </button>
              <button
                type="button"
                className="button button-secondary"
                disabled={
                  blocked ||
                  dirty ||
                  !consent ||
                  !canAuthorize ||
                  !profile.public_search_enabled ||
                  !publicIdentity(profile.public_name) ||
                  !status?.search_configured ||
                  Boolean(busyJob('enrich'))
                }
                onClick={() => submit('enrich')}
              >
                检索公开资料
              </button>
            </div>
            {dirty ? (
              <p className="profile-muted">先保存当前设置，再提交后台任务。</p>
            ) : null}
          </section>
          <section className="profile-section">
            <h4>你已确认的事实</h4>
            {factList(
              profile.facts.filter((fact) => fact.state === 'confirmed'),
            )}
          </section>
          <section className="profile-section">
            <h4>待核实信息</h4>
            {factList(
              profile.facts.filter((fact) => fact.state === 'unverified'),
              true,
            )}
            {profile.facts.some((fact) => fact.state === 'rejected') ? (
              <details className="profile-rejected">
                <summary>查看已拒绝候选</summary>
                {factList(
                  profile.facts.filter((fact) => fact.state === 'rejected'),
                )}
              </details>
            ) : null}
          </section>
          <section className="profile-section">
            <h4>公开来源</h4>
            <p className="profile-muted">
              来源存在不代表已确认属于此人，请核对姓名、公司和原文。
            </p>
            {profile.sources.length ? (
              <ul className="profile-sources">
                {profile.sources.map((source) => {
                  const url = safeProfileSourceURL(source.url);
                  return (
                    <li key={source.id}>
                      {url ? (
                        <a href={url} target="_blank" rel="noopener noreferrer">
                          {source.title || source.url}
                        </a>
                      ) : (
                        <span>{source.title || '来源链接暂不可用'}</span>
                      )}
                      <p>{source.snippet}</p>
                      <time dateTime={source.retrieved_at}>
                        获取于 {dateText(source.retrieved_at)}
                      </time>
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="profile-muted">暂无公开来源。</p>
            )}
          </section>
          <section className="profile-section">
            <h4>后台任务</h4>
            {profile.jobs.length ? (
              <ul className="profile-jobs">
                {profile.jobs.map((job) => (
                  <li key={job.id}>
                    <div>
                      <strong>
                        {job.kind === 'enrich'
                          ? '公开资料检索'
                          : '聊天档案整理'}
                      </strong>
                      <span>{JOB_STATES[job.state]}</span>
                    </div>
                    <p>
                      更新于 {dateText(job.updated_at)}
                      {job.attempts ? ` · 已尝试${job.attempts}次` : ''}
                    </p>
                    {job.last_error_code ? (
                      <p className="profile-feedback is-error">
                        {profileJobError(job.last_error_code)}
                      </p>
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="profile-muted">
                暂无任务。启用后台整理或手动提交后可在这里查看。
              </p>
            )}
            <p className="profile-muted">
              任务由独立后台执行，离开页面不会取消；电脑及后台需要保持运行。
            </p>
          </section>
          <ProfileLinksPanel
            key={`${token}:${conversationId}`}
            token={token}
            conversationId={conversationId}
            onAuthFailure={onAuthFailure}
          />
          <details className="profile-document">
            <summary>查看已保存的 Markdown 文档</summary>
            <pre>{profile.document_markdown || '尚未生成文档。'}</pre>
          </details>
        </>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {notice ? (
          <p className="profile-feedback" role="status">
            {notice}
          </p>
        ) : null}
        {error ? (
          <p className="profile-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
        {uncertain ? (
          <p className="profile-muted">
            上次操作结果尚未确认，正在读取后台状态。页面不会自动重复提交。
          </p>
        ) : null}
      </div>
    </section>
  );
}
