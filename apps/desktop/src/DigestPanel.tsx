import { useEffect, useState } from 'react';
import { ServiceError } from './api';
import {
  getDigestSchedule,
  getLatestDigest,
  runDigestNow,
  saveDigestSchedule,
} from './digest-api';
import type { DigestReport, DigestSchedule } from './digest-api';
import './profile.css';

export function DigestPanel({
  token,
  onAuthFailure,
}: {
  token: string;
  onAuthFailure: () => void;
}) {
  const [schedule, setSchedule] = useState<DigestSchedule | null>(null);
  const [report, setReport] = useState<DigestReport | null>(null);
  const [time, setTime] = useState('08:00');
  const [enabled, setEnabled] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const reload = async () => {
    if (!token) return;
    try {
      const [nextSchedule, nextReport] = await Promise.all([
        getDigestSchedule(token),
        getLatestDigest(token),
      ]);
      setSchedule(nextSchedule);
      setTime(nextSchedule.local_time);
      setEnabled(nextSchedule.enabled);
      setReport(nextReport);
      setError(null);
    } catch (failure: unknown) {
      if (failure instanceof ServiceError && failure.status === 401) {
        onAuthFailure();
        return;
      }
      setError(
        failure instanceof Error ? failure.message : '无法读取摘要设置。',
      );
    }
  };
  useEffect(() => {
    void reload();
  }, [token]);
  const save = async () => {
    if (!schedule) return;
    setBusy('save');
    setNotice(null);
    setError(null);
    try {
      const next = await saveDigestSchedule(token, {
        enabled,
        local_time: time,
        expected_revision: schedule.revision,
        confirmed: true,
      });
      setSchedule(next);
      setNotice(enabled ? `已设置每天 ${time} 生成摘要。` : '每日摘要已暂停。');
    } catch (failure: unknown) {
      if (failure instanceof ServiceError && failure.status === 401)
        onAuthFailure();
      else
        setError(failure instanceof Error ? failure.message : '设置未保存。');
    } finally {
      setBusy(null);
    }
  };
  const run = async () => {
    setBusy('run');
    setNotice(null);
    setError(null);
    try {
      setReport(await runDigestNow(token));
      setNotice('摘要已生成，提醒仅显示在工作台。');
    } catch (failure: unknown) {
      if (failure instanceof ServiceError && failure.status === 401)
        onAuthFailure();
      else
        setError(failure instanceof Error ? failure.message : '摘要未生成。');
    } finally {
      setBusy(null);
    }
  };
  return (
    <section className="settings-panel" aria-labelledby="digest-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">DAILY DIGEST</p>
          <h2 id="digest-title">每日消息整理</h2>
        </div>
        <span className="label">工作台提醒</span>
      </div>
      <p className="profile-muted">
        按会话整理已有消息，优先提示未读、问句、时间、金额、承诺和待办。当前仅覆盖已入库私聊；群聊接入后再纳入。
      </p>
      <div className="settings-row">
        <label className="profile-check">
          <input
            type="checkbox"
            checked={enabled}
            disabled={!schedule || Boolean(busy)}
            onChange={(event) => setEnabled(event.target.checked)}
          />
          启用每日摘要
        </label>
        <label>
          时间
          <input
            type="time"
            value={time}
            disabled={!schedule || Boolean(busy)}
            onChange={(event) => setTime(event.target.value)}
          />
        </label>
      </div>
      <div className="profile-inline-actions">
        <button
          className="button button-secondary"
          type="button"
          disabled={!schedule || Boolean(busy)}
          onClick={() => void save()}
        >
          {busy === 'save' ? '正在保存…' : '保存每日计划'}
        </button>
        <button
          className="button button-quiet"
          type="button"
          disabled={Boolean(busy)}
          onClick={() => void run()}
        >
          {busy === 'run' ? '正在整理…' : '立即生成摘要'}
        </button>
      </div>
      {report ? (
        <div className="digest-report">
          <strong>
            {report.day} ·{' '}
            {report.state === 'succeeded' ? '已完成' : report.state}
          </strong>
          <p>{report.summary}</p>
          {report.reminders.length ? (
            <ul>
              {report.reminders.map((item) => (
                <li key={`${item.conversation_id}-${item.reason}`}>
                  <b>{item.title}</b> · {item.reason}
                </li>
              ))}
            </ul>
          ) : (
            <p className="profile-muted">暂无高相关提醒。</p>
          )}
        </div>
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
