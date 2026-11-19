import { useEffect, useRef, useState } from 'react';
import { ServiceError } from './api';
import {
  getProfileLinks,
  confirmProfileLink,
  removeProfileLink,
} from './profile-links-api';
import type { ProfileLinks } from './profile-links-api';
import { PLATFORM_NAMES } from './inbox-api';
import { HelpDetails } from './HelpDetails';

export default function ProfileLinksPanel({
  token,
  conversationId,
  onAuthFailure,
}: {
  token: string;
  conversationId: string;
  onAuthFailure: () => void;
}) {
  const [links, setLinks] = useState<ProfileLinks | null>(null);
  const [selected, setSelected] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const [revision, setRevision] = useState(0);
  const request = useRef<AbortController | null>(null);
  const alive = useRef(false);
  const authFailure = useRef(onAuthFailure);
  authFailure.current = onAuthFailure;

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    setBusy(true);
    setError(null);
    void getProfileLinks(token, controller.signal, conversationId)
      .then((value) => {
        if (!alive.current || controller.signal.aborted) return;
        setLinks(value);
        setUncertain(false);
      })
      .catch((failure: unknown) => {
        if (!alive.current || controller.signal.aborted) return;
        if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
          authFailure.current();
        else
          setError(
            failure instanceof ServiceError
              ? failure.message
              : '无法读取关联档案。',
          );
      })
      .finally(() => {
        if (alive.current && !controller.signal.aborted) setBusy(false);
      });
    return () => {
      alive.current = false;
      controller.abort();
      request.current?.abort();
    };
  }, [token, conversationId, revision]);

  const change = async (target: string, link: boolean) => {
    if (busy || uncertain || (link && !confirmed)) return;
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const value = await (link ? confirmProfileLink : removeProfileLink)(
        token,
        controller.signal,
        conversationId,
        target,
      );
      if (!alive.current || controller.signal.aborted) return;
      setLinks(value);
      setSelected('');
      setConfirmed(false);
      setNotice(
        link
          ? '已关联，可一起查看与导出。原聊天和各自AI权限仍独立保留。'
          : '已解除关联，原聊天和档案仍保留。',
      );
    } catch (failure: unknown) {
      if (!alive.current || controller.signal.aborted) return;
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED') {
        authFailure.current();
        return;
      }
      setError(
        failure instanceof ServiceError ? failure.message : '关联操作未完成。',
      );
      if (
        failure instanceof ServiceError &&
        ['TIMEOUT', 'NETWORK_ERROR', 'INVALID_RESPONSE'].includes(failure.code)
      )
        setUncertain(true);
    } finally {
      if (alive.current && !controller.signal.aborted) setBusy(false);
    }
  };
  const download = () => {
    if (!links) return;
    const url = URL.createObjectURL(
      new Blob([links.document_markdown], {
        type: 'text/markdown;charset=utf-8',
      }),
    );
    const a = document.createElement('a');
    a.href = url;
    a.download = `linked-profile-${conversationId}.md`;
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <section
      className="profile-section"
      aria-labelledby={`profile-links-${conversationId}`}
    >
      <h4 id={`profile-links-${conversationId}`}>合并同一人的聊天档案</h4>
      <HelpDetails label="档案关联说明">
        <p className="profile-muted">
          确认后一起查看与导出，原聊天、事实证据和各会话AI权限独立保留。相同昵称只是建议，请先核实。
        </p>
      </HelpDetails>
      <div className="profile-inline-actions">
        <button
          type="button"
          className="button button-quiet"
          disabled={busy}
          onClick={() => setRevision((value) => value + 1)}
        >
          {busy ? '正在读取…' : '刷新关联状态'}
        </button>
        <button
          type="button"
          className="button button-quiet"
          disabled={!links || busy}
          onClick={download}
        >
          导出联合档案
        </button>
      </div>
      {links?.linked.length ? (
        <ul className="profile-facts">
          {links.linked.map((item) => (
            <li key={item.conversation_id}>
              <p>
                <strong>{item.display_name}</strong> ·{' '}
                {PLATFORM_NAMES[item.platform as keyof typeof PLATFORM_NAMES] ??
                  item.platform}{' '}
                · {item.peer_id}
              </p>
              <button
                type="button"
                className="ai-text-button"
                disabled={busy || uncertain}
                onClick={() => void change(item.conversation_id, false)}
              >
                解除关联
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="profile-muted">尚未关联其他会话。</p>
      )}
      {links?.candidates.length ? (
        <form
          className="profile-settings"
          onSubmit={(event) => {
            event.preventDefault();
            if (selected) void change(selected, true);
          }}
        >
          <label>
            可能属于同一人的会话
            <select
              value={selected}
              disabled={busy}
              onChange={(event) => {
                setSelected(event.target.value);
                setConfirmed(false);
              }}
            >
              <option value="">选择并核对来源</option>
              {links.candidates.map((item) => (
                <option key={item.conversation_id} value={item.conversation_id}>
                  {item.display_name} ·{' '}
                  {PLATFORM_NAMES[
                    item.platform as keyof typeof PLATFORM_NAMES
                  ] ?? item.platform}{' '}
                  · {item.peer_id}（
                  {item.reason === 'same_platform_id'
                    ? '同平台账号相同'
                    : item.reason === 'same_public_name'
                      ? '公开姓名相同'
                      : '昵称相同'}
                  ）
                </option>
              ))}
            </select>
          </label>
          <label className="profile-check">
            <input
              type="checkbox"
              checked={confirmed}
              disabled={!selected || busy}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            我已核实，所选会话与当前档案属于同一人。
          </label>
          <button
            type="submit"
            className="button button-quiet"
            disabled={!selected || !confirmed || busy || uncertain}
          >
            确认关联聊天档案
          </button>
        </form>
      ) : links ? (
        <p className="profile-muted">暂无合并建议。</p>
      ) : null}
      {links?.truncated ? (
        <p className="profile-muted">
          联合文档已达到显示上限，请分别打开来源档案查看。
        </p>
      ) : null}
      {links?.linked.length ? (
        <details className="profile-document">
          <summary>查看联合文档与来源</summary>
          <pre>{links.document_markdown}</pre>
        </details>
      ) : null}
      <div aria-live="polite" aria-atomic="true">
        {notice ? (
          <HelpDetails label="关联操作记录">
            <p className="profile-feedback">{notice}</p>
          </HelpDetails>
        ) : null}
        {error ? (
          <p className="profile-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
        {uncertain ? (
          <p className="profile-muted">
            上次操作结果尚未确认，请先刷新关联状态。页面不会自动重试。
          </p>
        ) : null}
      </div>
    </section>
  );
}
