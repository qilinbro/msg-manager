import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
} from 'react';
import { getSystem, ServiceError, type SystemStatus } from './api';
import { MilkySetupPanel } from './MilkySetupPanel';
import {
  configureMilkyMonitor,
  getMilkyStatus,
  syncMilky,
  verifyMilky,
  type MilkyStatus,
  type MilkySyncResult,
} from './milky-api';

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

export function MilkyPanel({ token, onSystemUpdate, onAuthFailure }: Props) {
  const [status, setStatus] = useState<MilkyStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [peerID, setPeerID] = useState('');
  const [peers, setPeers] = useState('');
  const [configBusy, setConfigBusy] = useState(false);
  const [historyLimit, setHistoryLimit] = useState(20);
  const peersDirty = useRef(false);
  const pending = useRef<AbortController | null>(null);

  const refresh = useCallback(
    async (controller: AbortController) => {
      const next = await getMilkyStatus(token, controller.signal);
      setStatus(next);
      if (!peersDirty.current) {
        setPeers((next.monitor.peer_ids ?? []).join(', '));
      }
      return next;
    },
    [token],
  );

  useEffect(() => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setStatus(null);
    peersDirty.current = false;
    setPeers('');
    setError(null);
    setNotice(null);
    setHistoryLimit(20);
    if (!token) return () => controller.abort();
    void refresh(controller).catch((failure: unknown) => {
      if (controller.signal.aborted) return;
      setError(
        failure instanceof Error ? failure.message : '无法读取连接状态。',
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
      setError(
        failure instanceof Error
          ? failure.message
          : '操作未完成，请刷新状态后重试。',
      );
    } finally {
      if (pending.current === controller) setBusy(null);
    }
  };

  const onVerify = () =>
    void run('verify', async (controller) => {
      await verifyMilky(token, controller.signal);
      const next = await refresh(controller);
      setNotice(
        next.monitor.state === 'running'
          ? '账号与 Milky 版本已核验，已恢复授权私聊的后台接收；发送保持关闭。'
          : '账号与 Milky 版本已核验；后台接收仍未开启，发送保持关闭。',
      );
      const system = await getSystem(token, controller.signal);
      onSystemUpdate(system);
    });

  const onSync = () =>
    void run('sync', async (controller) => {
      const result: MilkySyncResult = await syncMilky(
        token,
        controller.signal,
        peerID.trim(),
        historyLimit,
      );
      setNotice(
        `本次同步新增 ${result.inserted} 条，重复 ${result.duplicates} 条，跳过 ${result.skipped} 条非文本或不支持消息。`,
      );
      await refresh(controller);
    });

  const onMonitor = (enabled: boolean) =>
    void run('monitor', async (controller) => {
      const next = await configureMilkyMonitor(
        token,
        controller.signal,
        enabled,
        parsePeers(peers),
      );
      setStatus(next);
      peersDirty.current = false;
      setPeers((next.monitor.peer_ids ?? []).join(', '));
      setNotice(
        enabled
          ? '已开启所选私聊的后台接收；断线会记录缺口，不承诺补齐全部历史。'
          : '后台接收已停止。',
      );
    });

  const onPeerChange = (event: ChangeEvent<HTMLInputElement>) =>
    setPeerID(event.target.value);
  const onPeersChange = (event: ChangeEvent<HTMLInputElement>) => {
    peersDirty.current = true;
    setPeers(event.target.value);
  };
  const verified = status?.state === 'identity_verified';
  const monitorSaved = status?.monitor.enabled ?? false;
  const monitorRunning = status?.monitor.state === 'running';
  const monitorPending = monitorSaved && !monitorRunning;

  return (
    <section className="settings-panel" aria-labelledby="milky-title">
      <div className="section-heading">
        <div>
          <h2 id="milky-title">QQ 个人号（Milky）</h2>
        </div>
      </div>
      <p className="qq-intro">
        连接本机 Milky 1.3 协议端，核验本人 QQ
        账号，按联系人同步好友私聊历史，并可显式开启后台接收。
        协议端独立运行，先按下方步骤安装并登录，再在这里测试和保存。自动发送始终关闭。
      </p>
      <MilkySetupPanel
        token={token}
        disabled={busy !== null}
        monitorEnabled={monitorSaved}
        onAuthFailure={onAuthFailure}
        onBusyChange={setConfigBusy}
        onHistoryLimitChange={setHistoryLimit}
        onSaved={() => {
          peersDirty.current = false;
          setPeers('');
          void run('refresh', async (controller) => {
            await refresh(controller);
            onSystemUpdate(await getSystem(token, controller.signal));
          });
        }}
      />

      <div className="qq-result" aria-live="polite">
        <dl>
          <div>
            <dt>连接状态</dt>
            <dd>
              {status
                ? status.configured
                  ? status.state === 'pending_verification'
                    ? '已保存，等待重新核验'
                    : status.state
                  : '未配置'
                : '读取中…'}
            </dd>
          </div>
          <div>
            <dt>QQ 账号</dt>
            <dd>{status?.account_id || '—'}</dd>
          </div>
          <div>
            <dt>协议版本</dt>
            <dd>{status?.milky_version || '锁定 1.3'}</dd>
          </div>
          <div>
            <dt>最近核验</dt>
            <dd>{status?.checked_at || '尚未核验'}</dd>
          </div>
          <div>
            <dt>后台接收</dt>
            <dd>
              {monitorRunning
                ? '运行中'
                : monitorPending
                  ? '已保存，待重新核验'
                  : '默认关闭'}
            </dd>
          </div>
          <div>
            <dt>跳过 / 缺口</dt>
            <dd>
              {status
                ? `${status.skipped} / ${(status.gaps ?? []).length}`
                : '—'}
            </dd>
          </div>
        </dl>
        <p>
          一次核验只说明最近一次检测成功，不等于持续在线。事件流没有补发机制，断线会记录缺口并用历史接口有界补查，不能承诺取回全部历史。
        </p>
      </div>

      <div className="form-actions">
        <button
          type="button"
          className="button button-primary"
          onClick={onVerify}
          disabled={
            !token || !status?.configured || busy !== null || configBusy
          }
        >
          {busy === 'verify' ? '正在核验…' : '核验账号与版本'}
        </button>
      </div>

      <div className="form-field">
        <label htmlFor="milky-peer">同步历史的联系人 QQ 号</label>
        <input
          id="milky-peer"
          className="text-input"
          inputMode="numeric"
          autoComplete="off"
          value={peerID}
          onChange={onPeerChange}
          placeholder="例如 987654321"
        />
        <p className="input-help">
          首版仅好友私聊纯文本，单次最多 30
          条；媒体与不支持场景记录跳过原因，不上传模型。
        </p>
      </div>
      <div className="form-actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={onSync}
          disabled={
            !token ||
            !verified ||
            !/^[1-9][0-9]*$/.test(peerID.trim()) ||
            busy !== null ||
            configBusy
          }
        >
          {busy === 'sync' ? '正在同步…' : '同步一页历史'}
        </button>
      </div>

      <div className="form-field">
        <label htmlFor="milky-peers">
          后台接收的私聊 QQ 号（逗号分隔，最多 20 个）
        </label>
        <input
          id="milky-peers"
          className="text-input"
          autoComplete="off"
          value={peers}
          onChange={onPeersChange}
          placeholder="仅填写你明确授权的私聊"
        />
        <p className="input-help">
          默认关闭，需先核验。已开启的联系人名单会加密保存；服务重启后需重新核验协议端，核验成功后恢复后台接收。
        </p>
      </div>
      <div className="form-actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={() => onMonitor(true)}
          disabled={
            !token ||
            !verified ||
            monitorSaved ||
            parsePeers(peers).length === 0 ||
            busy !== null ||
            configBusy
          }
        >
          {busy === 'monitor' ? '正在处理…' : '开启后台接收'}
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={() => onMonitor(false)}
          disabled={!token || !monitorSaved || busy !== null || configBusy}
        >
          {monitorPending ? '清除已保存的后台接收' : '停止后台接收'}
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
      {!token ? (
        <p className="input-help">
          请先连接上方的本地服务，再使用 QQ Milky 功能。
        </p>
      ) : null}
    </section>
  );
}
