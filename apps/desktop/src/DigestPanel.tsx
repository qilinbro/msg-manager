import { useCallback, useEffect, useRef, useState } from 'react';
import { ServiceError } from './api';
import { getAIStatus } from './ai-api';
import type { AIStatus } from './ai-api';
import {
  DigestRequestError,
  getDigestSchedule,
  getLatestDigest,
  runDigestNow,
  saveDigestSchedule,
} from './digest-api';
import type { DigestReport, DigestSchedule } from './digest-api';
import './profile.css';

const isBusinessAuthFailure = (failure: unknown) =>
  failure instanceof ServiceError &&
  failure.status === 401 &&
  (failure.code === 'UNAUTHORIZED' || failure.code === 'DEVICE_UNAUTHORIZED');
const REPORT_LABELS: Record<string, string> = {
  succeeded: '已完成',
  failed: '失败',
  running: '运行中',
  unknown: '结果未知，需人工核实',
};
export function DigestPanel({
  token,
  unresolvedIdentities,
  onAuthFailure,
}: {
  token: string;
  unresolvedIdentities: Set<string>;
  onAuthFailure: () => void;
}) {
  const [schedule, setSchedule] = useState<DigestSchedule | null>(null);
  const [report, setReport] = useState<DigestReport | null>(null);
  const [model, setModel] = useState<AIStatus | null>(null);
  const [time, setTime] = useState('08:00');
  const [scheduleConsent, setScheduleConsent] = useState(false);
  const [runConsent, setRunConsent] = useState(false);
  const [busy, setBusy] = useState<'read' | 'save' | 'run' | 'stopping' | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [unverified, setUnverified] = useState(() =>
    unresolvedIdentities.has(token),
  );
  const [readAfterIssue, setReadAfterIssue] = useState(false);
  const [verified, setVerified] = useState(false);
  const mounted = useRef(false);
  const active = useRef<{
    controller: AbortController;
    mutation: boolean;
  } | null>(null);
  const authFailure = useRef(onAuthFailure);
  useEffect(() => {
    authFailure.current = onAuthFailure;
  }, [onAuthFailure]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      active.current?.controller.abort();
    };
  }, [token]);
  const reload = useCallback(async () => {
    if (!token) return;
    active.current?.controller.abort();
    const controller = new AbortController();
    active.current = { controller, mutation: false };
    setBusy('read');
    setModel(null);
    setScheduleConsent(false);
    setRunConsent(false);
    setVerified(false);
    setError(null);
    try {
      const [nextSchedule, nextReport, nextModel] = await Promise.all([
        getDigestSchedule(token, controller.signal),
        getLatestDigest(token, controller.signal),
        getAIStatus(token, controller.signal),
      ]);
      if (
        !mounted.current ||
        controller.signal.aborted ||
        active.current?.controller !== controller
      )
        return;
      setSchedule(nextSchedule);
      setTime(nextSchedule.local_time);
      setReport(nextReport);
      setModel(nextModel);
      setReadAfterIssue(true);
      if (nextReport?.state === 'running' || nextReport?.state === 'unknown') {
        unresolvedIdentities.add(token);
        setUnverified(true);
      }
    } catch (failure: unknown) {
      if (
        !mounted.current ||
        controller.signal.aborted ||
        active.current?.controller !== controller
      )
        return;
      if (isBusinessAuthFailure(failure)) authFailure.current();
      else
        setError(
          failure instanceof Error ? failure.message : '无法读取摘要状态。',
        );
    } finally {
      if (mounted.current && active.current?.controller === controller) {
        active.current = null;
        setBusy(null);
      }
    }
  }, [token, unresolvedIdentities]);
  useEffect(() => {
    setSchedule(null);
    setReport(null);
    setUnverified(unresolvedIdentities.has(token));
    setReadAfterIssue(false);
    setNotice(null);
    void reload();
  }, [reload, token, unresolvedIdentities]);

  const modelReady = Boolean(model?.configured && !busy);
  const quotaExhausted = Boolean(
    model?.configured && model.used_today >= model.daily_limit,
  );
  const mutate = async (kind: 'save' | 'run', enable = false) => {
    if (!token || active.current || (kind === 'save' && !schedule)) return;
    if (
      (kind === 'run' || enable) &&
      (!model?.configured ||
        unverified ||
        (kind === 'run' ? !runConsent || quotaExhausted : !scheduleConsent))
    )
      return;
    const previouslyUnverified = unverified || unresolvedIdentities.has(token);
    unresolvedIdentities.add(token);
    const controller = new AbortController();
    active.current = { controller, mutation: true };
    setBusy(kind);
    setNotice(null);
    setError(null);
    setScheduleConsent(false);
    setRunConsent(false);
    setVerified(false);
    let refreshChangedTarget = false;
    try {
      if (kind === 'save' && schedule) {
        const next = await saveDigestSchedule(
          token,
          {
            enabled: enable,
            local_time: enable ? time : schedule.local_time,
            expected_revision: schedule.revision,
            confirmed: true,
            ...(enable && model
              ? {
                  model_config_id: model.model_config_id,
                  model_config_revision: model.model_config_revision,
                }
              : {}),
          },
          controller.signal,
        );
        if (
          !mounted.current ||
          controller.signal.aborted ||
          active.current?.controller !== controller
        )
          return;
        if (!previouslyUnverified) unresolvedIdentities.delete(token);
        setSchedule(next);
        setTime(next.local_time);
        setNotice(
          next.enabled
            ? `后台已保存每日 ${next.local_time}（Asia/Shanghai）的计划。需后台与电脑保持运行，不承诺休眠或关机后执行。`
            : '每日摘要计划已暂停；不会取消已经开始的处理。',
        );
      } else if (model) {
        const next = await runDigestNow(
          token,
          {
            confirmed: true,
            model_config_id: model.model_config_id,
            model_config_revision: model.model_config_revision,
          },
          controller.signal,
        );
        if (
          !mounted.current ||
          controller.signal.aborted ||
          active.current?.controller !== controller
        )
          return;
        setReport(next);
        if (
          next.state !== 'running' &&
          next.state !== 'unknown' &&
          !previouslyUnverified
        )
          unresolvedIdentities.delete(token);
        if (next.state === 'succeeded')
          setNotice('后台记录本次摘要已完成，提醒仅显示在工作台。');
        else {
          setNotice(
            `后台记录本次状态：${Object.hasOwn(REPORT_LABELS, next.state) ? REPORT_LABELS[next.state] : next.state}。请核对最近摘要，不会自动重试。`,
          );
          if (next.state === 'unknown' || next.state === 'running') {
            setUnverified(true);
            setReadAfterIssue(false);
          }
        }
      }
    } catch (failure: unknown) {
      if (!mounted.current || active.current?.controller !== controller) return;
      if (isBusinessAuthFailure(failure)) {
        authFailure.current();
        return;
      }
      setError(
        failure instanceof Error
          ? failure.message
          : '未取得可核对结果，请先读取状态，不要重复提交。',
      );
      if (!(failure instanceof DigestRequestError) || failure.uncertain) {
        setUnverified(true);
        setReadAfterIssue(false);
      } else if (!previouslyUnverified) unresolvedIdentities.delete(token);
      if (
        failure instanceof ServiceError &&
        (failure.code === 'DIGEST_MODEL_CHANGED' ||
          failure.code === 'DIGEST_STALE')
      )
        refreshChangedTarget = true;
    } finally {
      if (mounted.current && active.current?.controller === controller) {
        active.current = null;
        setBusy(null);
        if (refreshChangedTarget) {
          setNotice(
            '目标或计划版本已变化，正在重新读取。请按新的实际状态重新核对，不会重放提交。',
          );
          void reload();
        }
      }
    }
  };
  const stopWaiting = () => {
    const pending = active.current;
    if (!pending) return;
    if (pending.mutation) {
      setUnverified(true);
      setReadAfterIssue(false);
      setNotice(
        '已停止本地等待，不代表后台取消。请先读取计划及最近摘要，并核实可能的费用或写入。',
      );
    }
    if (!pending.mutation) setNotice('已停止本地读取，未取消或更改后台处理。');
    setBusy('stopping');
    pending.controller.abort();
  };
  const consentTarget = model?.configured ? (
    <>
      <strong>{model.model}</strong>（{model.endpoint_host}，配置版本{' '}
      {model.model_config_revision}）
    </>
  ) : (
    <>尚未读取的模型目标</>
  );
  return (
    <section className="settings-panel" aria-labelledby="digest-title">
      <div className="section-heading">
        <h2 id="digest-title">每日消息整理</h2>
        <span className="label">
          {busy === 'read'
            ? '读取中…'
            : schedule
              ? schedule.enabled
                ? '计划已启用'
                : '计划已暂停'
              : token
                ? '状态未读取'
                : '请先连接服务'}
        </span>
      </div>
      <p className="profile-muted">
        整理未暂停会话过去 24
        小时内达到相关性阈值的已入库文本，生成工作台摘要与证据提醒。不保证覆盖未接入的消息，也不会自动发消息。
      </p>
      <p className="profile-muted">
        模型目标：{consentTarget}。
        {model
          ? `最近读取的共享今日用量 ${model.used_today} / ${model.daily_limit} 次。`
          : '刷新后核对配置与额度。'}
        修改模型配置会暂停旧计划，需要重新授权。
      </p>
      {schedule?.enabled ? (
        <p className="profile-muted">
          当前每日计划：{schedule.local_time}（Asia/Shanghai），绑定{' '}
          <code>{schedule.model_config_id}</code> · 版本{' '}
          {schedule.model_config_revision}。以后台实际状态为准。
        </p>
      ) : null}
      <div className="settings-row">
        <label className="digest-time">
          每日时间（Asia/Shanghai）
          <input
            type="time"
            value={time}
            required
            disabled={!schedule || Boolean(busy)}
            onChange={(event) => {
              setTime(event.target.value);
              setScheduleConsent(false);
            }}
          />
        </label>
      </div>
      <label className="assistant-check">
        <input
          type="checkbox"
          checked={scheduleConsent}
          disabled={!schedule || !modelReady || unverified || !time}
          onChange={(event) => setScheduleConsent(event.target.checked)}
        />
        <span>
          我同意启用 / 更新每日计划：今后每天 {time || '所选时间'}
          （Asia/Shanghai）将上述未暂停会话的摘要所需文本交给 {
            consentTarget
          }{' '}
          处理，可能产生费用。此持续授权仅绑定展示的目标与版本，保存后本次勾选会清除。
        </span>
      </label>
      <div className="profile-inline-actions">
        <button
          className="button button-secondary"
          type="button"
          disabled={
            !schedule || !modelReady || !scheduleConsent || !time || unverified
          }
          onClick={() => void mutate('save', true)}
        >
          {busy === 'save'
            ? '正在保存…'
            : schedule?.enabled
              ? '更新每日计划'
              : '启用每日计划'}
        </button>
        <button
          className="button button-quiet"
          type="button"
          disabled={!schedule?.enabled || Boolean(busy)}
          onClick={() => void mutate('save', false)}
        >
          暂停当前计划
        </button>
      </div>
      <div className="digest-manual">
        <h3>今日摘要</h3>
        <p className="profile-muted">
          同一日期已有报告时，后台返回该记录；失败或未知记录也不会被强制重跑。只有后台实际执行新的模型调用才可能产生新的费用。
        </p>
        <label className="assistant-check">
          <input
            type="checkbox"
            checked={runConsent}
            disabled={!modelReady || unverified || quotaExhausted}
            onChange={(event) => setRunConsent(event.target.checked)}
          />
          <span>
            我同意仅本次把上述未暂停会话的摘要所需文本交给 {consentTarget}{' '}
            处理，可能产生费用；不改变每日计划。
          </span>
        </label>
        <div className="profile-inline-actions">
          <button
            className="button button-primary"
            type="button"
            disabled={
              !modelReady || !runConsent || unverified || quotaExhausted
            }
            onClick={() => void mutate('run')}
          >
            {busy === 'run' ? '正在整理…' : '生成 / 读取今日摘要'}
          </button>
          <button
            className="button button-secondary"
            type="button"
            disabled={!token || Boolean(busy)}
            onClick={() => void reload()}
          >
            读取实际计划与最近摘要
          </button>
          {busy ? (
            <button
              className="button button-quiet"
              type="button"
              disabled={busy === 'stopping'}
              onClick={stopWaiting}
            >
              停止等待
            </button>
          ) : null}
        </div>
      </div>
      {quotaExhausted ? (
        <p className="profile-muted">
          今日共享额度已达限制；仍可读取已有摘要或暂停每日计划。
        </p>
      ) : null}
      {unverified ? (
        <div className="assistant-warning">
          <p>
            存在运行中、未知或中断结果。请先读取状态并在业务侧核实，不要直接重放；暂停计划仍可操作，但不取消已开始的处理。
          </p>
          <label className="assistant-check">
            <input
              type="checkbox"
              checked={verified}
              disabled={!readAfterIssue || Boolean(busy)}
              onChange={(event) => setVerified(event.target.checked)}
            />
            <span>
              我已重新读取并人工核实后台状态及可能费用，理解新的调用不是安全重试。
            </span>
          </label>
          <button
            type="button"
            className="button button-secondary"
            disabled={!verified || !readAfterIssue || Boolean(busy)}
            onClick={() => {
              unresolvedIdentities.delete(token);
              setUnverified(false);
              setVerified(false);
              setScheduleConsent(false);
              setRunConsent(false);
            }}
          >
            允许重新核对新的摘要操作
          </button>
        </div>
      ) : null}
      {report ? (
        <section
          className="digest-report"
          aria-labelledby="digest-latest-title"
        >
          <h3 id="digest-latest-title">最近摘要</h3>
          <strong>
            {report.day} ·{' '}
            {Object.hasOwn(REPORT_LABELS, report.state)
              ? REPORT_LABELS[report.state]
              : `未知状态（${report.state}）`}
          </strong>
          <p>{report.summary || '暂无摘要正文。'}</p>
          <p className="profile-muted">
            涉及 {report.conversation_count} 个会话
            {report.generated_at
              ? ` · 记录时间 ${new Date(report.generated_at).toLocaleString('zh-CN', { hour12: false })}`
              : ''}
            {report.error_code ? ` · ${report.error_code}` : ''}
          </p>
          {report.reminders.length ? (
            <ul>
              {report.reminders.map((item, index) => (
                <li key={`${report.id}:${index}:${item.conversation_id}`}>
                  <b>{item.title}</b> · {item.reason}
                  {item.evidence_ids.length ? (
                    <p className="profile-muted">
                      证据编号：{item.evidence_ids.join('、')}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : (
            <p className="profile-muted">
              本记录没有提醒条目；不代表所有会话均已处理。
            </p>
          )}
          {report.evidence_ids.length ? (
            <details>
              <summary>摘要证据编号</summary>
              <p>{report.evidence_ids.join('、')}</p>
            </details>
          ) : null}
        </section>
      ) : !busy && token ? (
        <p className="profile-muted">尚未读取到摘要记录。</p>
      ) : null}
      <div aria-live="polite">
        {notice ? <p className="profile-feedback">{notice}</p> : null}
        {error ? (
          <p className="profile-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
      </div>
    </section>
  );
}
