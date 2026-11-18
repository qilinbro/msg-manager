import { useEffect, useRef, useState } from 'react';
import { ServiceError } from './api';
import {
  getReceiveMonitor,
  saveReceiveMonitor,
  receiveMonitorError,
} from './receive-monitor-api';
import type {
  ReceiveChannel,
  ReceiveMonitorStatus,
} from './receive-monitor-api';
import './receive-monitor.css';

interface Props {
  channel: ReceiveChannel;
  token: string;
  canEnable: boolean;
  onAuthFailure: () => void;
  verifiedAccountID?: string;
  dialogsAccountID?: string;
  dialogs?: { peer_id: string; name: string }[];
}
const STATES: Record<string, string> = {
  waiting: '已保存开启设置，等待下一轮检查',
  running: '接收计划已启用，查看下方最近成功检查',
  starting: '正在建立新消息起点',
  baselining: '正在建立新消息起点',
  reading: '本轮正在读取',
  catching_up: '正在处理读取窗口内的新消息',
  flood_wait: '平台要求等待',
  error: '上次接收未完成',
  needs_reenable: '请核对邮箱后重新明确开启',
  not_configured: '等待本机账号配置',
  storage_disabled: '等待启用本地加密存储',
  blocked: '接收已受阻，请核对账号和本机状态',
  stopped: '后台接收已停止，请刷新确认',
};
const messageOf = (value: unknown) =>
  value instanceof ServiceError
    ? value.message
    : '后台接收状态未确认，请刷新后核对。';
const dateText = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour12: false,
  });

export function ReceiveMonitor({
  channel,
  token,
  canEnable,
  onAuthFailure,
  verifiedAccountID,
  dialogsAccountID,
  dialogs = [],
}: Props) {
  const [monitor, setMonitor] = useState<ReceiveMonitorStatus | null>(null);
  const [seconds, setSeconds] = useState('60');
  const [peers, setPeers] = useState<string[]>([]);
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [unsupported, setUnsupported] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const mounted = useRef(false);
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const unsupportedRef = useRef(false);
  const read = useRef<AbortController | null>(null);
  const write = useRef<AbortController | null>(null);
  const refresh = useRef<() => void>(() => {});
  const scope = useRef({
    token,
    canEnable,
    onAuthFailure,
    verifiedAccountID,
    dialogsAccountID,
    dialogs,
  });
  scope.current = {
    token,
    canEnable,
    onAuthFailure,
    verifiedAccountID,
    dialogsAccountID,
    dialogs,
  };
  const telegram = channel === 'telegram-user';

  const apply = (next: ReceiveMonitorStatus) => {
    setMonitor(next);
    if (!dirtyRef.current) {
      setSeconds(String(next.interval_seconds));
      setPeers(
        channel === 'telegram-user' &&
          next.expected_account_id &&
          next.expected_account_id === scope.current.verifiedAccountID
          ? [...(next.peer_ids ?? [])]
          : [],
      );
    }
  };

  useEffect(() => {
    mounted.current = true;
    dirtyRef.current = false;
    savingRef.current = false;
    unsupportedRef.current = false;
    setMonitor(null);
    setSeconds('60');
    setPeers([]);
    setDirty(false);
    setLoading(false);
    setSaving(false);
    setError(null);
    setNotice(null);
    setUnsupported(false);
    setUncertain(false);
    let active = true;
    const fetchStatus = async (force = false) => {
      if (
        !token ||
        !active ||
        savingRef.current ||
        read.current ||
        (!force &&
          (unsupportedRef.current || document.visibilityState !== 'visible'))
      )
        return;
      const controller = new AbortController();
      read.current = controller;
      setLoading(true);
      try {
        const next = await getReceiveMonitor(channel, token, controller.signal);
        if (!active || controller.signal.aborted || read.current !== controller)
          return;
        apply(next);
        setError(null);
        setUncertain(false);
        setUnsupported(false);
        unsupportedRef.current = false;
      } catch (failure: unknown) {
        if (!active || controller.signal.aborted || read.current !== controller)
          return;
        if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
          scope.current.onAuthFailure();
        else if (
          failure instanceof ServiceError &&
          failure.code === 'MONITOR_UNSUPPORTED'
        ) {
          unsupportedRef.current = true;
          setUnsupported(true);
          setError(null);
        } else setError(messageOf(failure));
      } finally {
        if (active && read.current === controller) {
          read.current = null;
          setLoading(false);
        }
      }
    };
    refresh.current = () => {
      unsupportedRef.current = false;
      void fetchStatus(true);
    };
    const visible = () => {
      if (document.visibilityState === 'visible') void fetchStatus();
    };
    void fetchStatus();
    const timer = window.setInterval(visible, 15_000);
    document.addEventListener('visibilitychange', visible);
    window.addEventListener('focus', visible);
    return () => {
      active = false;
      mounted.current = false;
      read.current?.abort();
      read.current = null;
      write.current?.abort();
      write.current = null;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visible);
      window.removeEventListener('focus', visible);
    };
  }, [channel, token]);

  useEffect(() => {
    if (!telegram) return;
    dirtyRef.current = false;
    setDirty(false);
    setPeers(
      monitor?.expected_account_id &&
        monitor.expected_account_id === verifiedAccountID
        ? [...(monitor.peer_ids ?? [])]
        : [],
    );
  }, [verifiedAccountID, telegram]);

  const change = () => {
    dirtyRef.current = true;
    setDirty(true);
    setNotice(null);
  };
  const validScope =
    !telegram ||
    Boolean(
      verifiedAccountID &&
      dialogsAccountID === verifiedAccountID &&
      peers.length >= 1 &&
      peers.length <= 10 &&
      peers.every((id) => dialogs.some((dialog) => dialog.peer_id === id)),
    );
  const validSeconds =
    Number.isInteger(Number(seconds)) &&
    Number(seconds) >= 30 &&
    Number(seconds) <= 900;
  const save = async (enabled: boolean) => {
    if (
      !token ||
      token !== scope.current.token ||
      savingRef.current ||
      (enabled &&
        (!monitor ||
          !scope.current.canEnable ||
          !validScope ||
          !validSeconds ||
          uncertain))
    )
      return;
    if (
      enabled &&
      telegram &&
      (!verifiedAccountID ||
        scope.current.verifiedAccountID !== verifiedAccountID ||
        scope.current.dialogsAccountID !== verifiedAccountID ||
        !peers.every((id) =>
          scope.current.dialogs.some((item) => item.peer_id === id),
        ))
    )
      return;
    const controller = new AbortController();
    read.current?.abort();
    read.current = null;
    setLoading(false);
    write.current = controller;
    savingRef.current = true;
    setSaving(true);
    setError(null);
    setNotice(null);
    const account = verifiedAccountID;
    try {
      const next = await saveReceiveMonitor(
        channel,
        token,
        controller.signal,
        enabled
          ? {
              enabled: true,
              interval_seconds: Number(seconds),
              ...(telegram
                ? { expected_account_id: account, peer_ids: [...peers] }
                : {}),
            }
          : { enabled: false },
      );
      if (
        !mounted.current ||
        controller.signal.aborted ||
        write.current !== controller
      )
        return;
      if (telegram && account !== scope.current.verifiedAccountID) {
        setUncertain(true);
        setError('账号状态已变化，请刷新后台接收设置并重新核验。');
        return;
      }
      dirtyRef.current = false;
      setDirty(false);
      apply(next);
      setUncertain(false);
      setNotice(
        enabled
          ? '后台接收设置已保存。首次开启或新增私聊仅建立新消息起点，不会追补开启前的历史。'
          : '暂停设置已保存；正在结束的本轮可能仍有结果，请查看状态。',
      );
    } catch (failure: unknown) {
      if (
        !mounted.current ||
        controller.signal.aborted ||
        write.current !== controller
      )
        return;
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
        scope.current.onAuthFailure();
      else if (
        failure instanceof ServiceError &&
        failure.code === 'MONITOR_UNSUPPORTED'
      ) {
        unsupportedRef.current = true;
        setUnsupported(true);
        setError(null);
      } else {
        setError(messageOf(failure));
        if (
          failure instanceof ServiceError &&
          ['TIMEOUT', 'NETWORK_ERROR', 'INVALID_RESPONSE'].includes(
            failure.code,
          )
        )
          setUncertain(true);
      }
    } finally {
      if (mounted.current && write.current === controller) {
        write.current = null;
        savingRef.current = false;
        setSaving(false);
      }
    }
  };
  const id = channel === 'email-imap' ? 'email-receive' : 'telegram-receive';
  const stateText = error
    ? '接收状态未确认，请刷新核对'
    : monitor
      ? !monitor.enabled
        ? [
            'needs_reenable',
            'not_configured',
            'storage_disabled',
            'blocked',
            'stopped',
            'error',
          ].includes(monitor.state)
          ? STATES[monitor.state]
          : '后续后台接收已暂停'
        : (STATES[monitor.state] ?? '开启设置已保存，执行状态待确认')
      : loading
        ? '正在读取后台配置'
        : '尚未读取后台配置';

  return (
    <section
      className="receive-monitor"
      aria-labelledby={`${id}-title`}
      aria-busy={saving}
    >
      <div className="section-heading">
        <h3 id={`${id}-title`}>后台自动接收</h3>
        <button
          type="button"
          className="button button-quiet"
          disabled={!token || loading || saving}
          onClick={() => refresh.current()}
        >
          {loading ? '检查中…' : '检查接收状态'}
        </button>
      </div>
      {unsupported ? (
        <p className="input-help">
          当前后台版本暂不支持自动接收；请更新并重启服务后再次检查。手动读取仍可使用。
        </p>
      ) : (
        <>
          <p role="status">{stateText}</p>
          <p className="input-help">
            首次开启只建立新消息起点，之后按计划接收新内容，不会追补开启前的历史。暂停保留已保存记录和接收进度。后台和电脑需保持运行，开启设置不代表持续在线。
          </p>
          {monitor?.last_success_at ? (
            <p className="input-help">
              最近成功检查{' '}
              <time dateTime={monitor.last_success_at}>
                {dateText(monitor.last_success_at)}
              </time>
            </p>
          ) : (
            <p className="input-help">尚无成功接收检查记录。</p>
          )}
          {monitor?.retry_not_before ? (
            <p className="input-help">
              最早再次检查{' '}
              <time dateTime={monitor.retry_not_before}>
                {dateText(monitor.retry_not_before)}
              </time>
              ；实际执行以下次状态为准。
            </p>
          ) : null}
          {monitor?.last_error_code ? (
            <p className="form-message error-message" role="alert">
              {receiveMonitorError(monitor.last_error_code)}
            </p>
          ) : null}
          {monitor &&
          ((monitor.skipped ?? monitor.skipped_count ?? 0) > 0 ||
            (monitor.missing_uid_count ?? 0) > 0 ||
            monitor.last_gap_reason ||
            monitor.limited) ? (
            <p className="input-help">
              接收记录：跳过 {monitor.skipped ?? monitor.skipped_count ?? 0} 条
              {!telegram
                ? `，UID 空位 ${monitor.missing_uid_count ?? 0} 个`
                : ''}
              {monitor.limited ? '，已达到读取窗口上限' : ''}
              。请查看失败原因，缺口不会标为已补齐。
            </p>
          ) : null}
          {telegram ? (
            <>
              <p className="input-help">
                已保存 {monitor?.peer_ids?.length ?? 0}{' '}
                个私聊。请先在上方核验本人账号、读取列表，再明确选择 1—10
                个私聊；单次手动同步的联系人不会自动加入。
              </p>
              {dialogs.length > 0 &&
              verifiedAccountID &&
              dialogsAccountID === verifiedAccountID ? (
                <fieldset className="receive-monitor-peers" disabled={saving}>
                  <legend>允许后台接收的私聊</legend>
                  {dialogs.map((dialog) => (
                    <label key={dialog.peer_id}>
                      <input
                        type="checkbox"
                        checked={peers.includes(dialog.peer_id)}
                        disabled={
                          !peers.includes(dialog.peer_id) && peers.length >= 10
                        }
                        onChange={(event) => {
                          change();
                          setPeers((current) =>
                            event.target.checked
                              ? [...current, dialog.peer_id]
                              : current.filter(
                                  (value) => value !== dialog.peer_id,
                                ),
                          );
                        }}
                      />
                      <span>
                        {dialog.name || dialog.peer_id} · {dialog.peer_id}
                      </span>
                    </label>
                  ))}
                </fieldset>
              ) : null}
            </>
          ) : null}
          <label
            className="receive-monitor-interval"
            htmlFor={`${id}-interval`}
          >
            接收间隔（秒）
            <input
              id={`${id}-interval`}
              type="number"
              min={30}
              max={900}
              step={1}
              value={seconds}
              disabled={saving || !token}
              onChange={(event) => {
                change();
                setSeconds(event.target.value);
              }}
            />
          </label>
          <p className="input-help">
            30—900 秒，默认 60 秒。页面检查状态不会发起平台收件。
          </p>
          {!validSeconds ? (
            <p className="form-message error-message" role="alert">
              接收间隔须为 30—900 秒的整数。
            </p>
          ) : null}
          {dirty ? <p className="input-help">修改尚未保存。</p> : null}
          {!canEnable ? (
            <p className="input-help">
              {telegram
                ? '完成本机初始化与本人账号核验后可开启。'
                : '配置本机邮箱并启用加密存储后可开启。'}
              暂停已保存的计划不受此限制。
            </p>
          ) : null}
          <div className="form-actions">
            <button
              type="button"
              className="button button-primary"
              disabled={
                !token ||
                !monitor ||
                saving ||
                !canEnable ||
                !validScope ||
                !validSeconds ||
                uncertain ||
                Boolean(error)
              }
              onClick={() => void save(true)}
            >
              {saving ? '保存中…' : '保存并开启后台接收'}
            </button>
            <button
              type="button"
              className="button button-secondary"
              disabled={!token || saving || (!monitor?.enabled && !uncertain)}
              onClick={() => void save(false)}
            >
              暂停后台接收
            </button>
          </div>
        </>
      )}
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
