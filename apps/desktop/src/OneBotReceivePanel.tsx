import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
} from 'react';
import { getSystem, ServiceError, type SystemStatus } from './api';
import {
  configureOneBotMonitor,
  getOneBotReceiveStatus,
  syncOneBotMessages,
  type OneBotReceiveStatus,
} from './onebot-api';

interface Props {
  token: string;
  onSystemUpdate: (system: SystemStatus) => void;
  onAuthFailure: () => void;
}

function parsePeers(value: string): string[] {
  return value
    .split(/[\s,，;；]+/)
    .map((part) => part.trim())
    .filter((part) => /^[1-9][0-9]*$/.test(part));
}

export function OneBotReceivePanel({
  token,
  onSystemUpdate,
  onAuthFailure,
}: Props) {
  const [status, setStatus] = useState<OneBotReceiveStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [peerID, setPeerID] = useState('');
  const [peers, setPeers] = useState('');
  const pending = useRef<AbortController | null>(null);

  const refresh = useCallback(
    async (controller: AbortController) => {
      setStatus(await getOneBotReceiveStatus(token, controller.signal));
    },
    [token],
  );

  useEffect(() => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setStatus(null);
    setError(null);
    setNotice(null);
    if (!token) return () => controller.abort();
    void refresh(controller).catch((failure: unknown) => {
      if (!controller.signal.aborted)
        setError(
          failure instanceof Error ? failure.message : '无法读取 OneBot 状态。',
        );
    });
    return () => controller.abort();
  }, [token, refresh]);

  const run = async (
    name: string,
    work: (controller: AbortController) => Promise<void>,
  ) => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setBusy(name);
    setError(null);
    setNotice(null);
    try {
      await work(controller);
    } catch (failure: unknown) {
      if (controller.signal.aborted) return;
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED') {
        onAuthFailure();
        return;
      }
      setError(failure instanceof Error ? failure.message : '操作未完成。');
    } finally {
      if (pending.current === controller) setBusy(null);
    }
  };

  const onSync = () =>
    void run('sync', async (controller) => {
      const result = await syncOneBotMessages(
        token,
        controller.signal,
        peerID.trim(),
        20,
      );
      setNotice(
        `本次新增 ${result.inserted} 条、重复 ${result.duplicates} 条、跳过 ${result.skipped} 条（自发送或不支持内容）。`,
      );
      await refresh(controller);
    });

  const onMonitor = (enabled: boolean) =>
    void run('monitor', async (controller) => {
      const next = await configureOneBotMonitor(
        token,
        controller.signal,
        enabled,
        parsePeers(peers),
      );
      setStatus(next);
      setNotice(
        enabled
          ? '已开启后台只读轮询；断线或失败会记录缺口。'
          : '后台只读轮询已停止。',
      );
      const system = await getSystem(token, controller.signal);
      onSystemUpdate(system);
    });

  return (
    <section className="settings-panel" aria-labelledby="onebot-receive-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">QQ ONEBOT RECEIVE</p>
          <h2 id="onebot-receive-title">QQ OneBot 只读消息接收</h2>
        </div>
      </div>
      <p className="qq-intro">
        从你自己部署的 OneBot v11
        服务按联系人拉取好友私聊文本，去重后存入加密消息中心。默认关闭、只读、不发送；自发送与媒体消息会跳过。
      </p>
      <div className="qq-result" aria-live="polite">
        <dl>
          <div>
            <dt>连接状态</dt>
            <dd>
              {status
                ? status.configured
                  ? status.state
                  : '未配置'
                : '读取中…'}
            </dd>
          </div>
          <div>
            <dt>QQ 账号</dt>
            <dd>{status?.account_id || '—'}</dd>
          </div>
          <div>
            <dt>后台接收</dt>
            <dd>{status?.receiving ? '已开启' : '默认关闭'}</dd>
          </div>
          <div>
            <dt>最近轮询 / 结果</dt>
            <dd>
              {status
                ? `${status.last_poll_at || '—'} / ${status.last_result || '—'}`
                : '—'}
            </dd>
          </div>
          <div>
            <dt>跳过 / 缺口</dt>
            <dd>
              {status ? `${status.skipped} / ${status.gaps.length}` : '—'}
            </dd>
          </div>
        </dl>
      </div>
      <div className="form-field">
        <label htmlFor="onebot-sync-peer">同步历史的联系人 QQ 号</label>
        <input
          id="onebot-sync-peer"
          className="text-input"
          inputMode="numeric"
          autoComplete="off"
          value={peerID}
          onChange={(event: ChangeEvent<HTMLInputElement>) =>
            setPeerID(event.target.value)
          }
          placeholder="例如 3221565903"
        />
      </div>
      <div className="form-actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={onSync}
          disabled={
            !token ||
            !status?.configured ||
            !/^[1-9][0-9]*$/.test(peerID.trim()) ||
            busy !== null
          }
        >
          {busy === 'sync' ? '正在同步…' : '同步一页历史'}
        </button>
      </div>
      <div className="form-field">
        <label htmlFor="onebot-monitor-peers">
          后台轮询的私聊 QQ 号（逗号分隔，最多 20 个）
        </label>
        <input
          id="onebot-monitor-peers"
          className="text-input"
          autoComplete="off"
          value={peers}
          onChange={(event: ChangeEvent<HTMLInputElement>) =>
            setPeers(event.target.value)
          }
          placeholder="仅填写你明确授权的私聊"
        />
        <p className="input-help">
          默认关闭；后台设置不持久化，服务重启后须重新开启。
        </p>
      </div>
      <div className="form-actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={() => onMonitor(true)}
          disabled={
            !token ||
            !status?.configured ||
            status?.receiving ||
            parsePeers(peers).length === 0 ||
            busy !== null
          }
        >
          开启后台接收
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={() => onMonitor(false)}
          disabled={!token || !status?.receiving || busy !== null}
        >
          停止后台接收
        </button>
      </div>
      <div aria-live="polite" aria-atomic="true">
        {error ? (
          <p className="form-message error-message" role="alert">
            {error}
          </p>
        ) : null}
        {notice ? (
          <p className="form-message success-message">{notice}</p>
        ) : null}
      </div>
    </section>
  );
}
