import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Icon } from './Icon';
import {
  clearFeishuConfig,
  FeishuRequestError,
  generateFeishuPreview,
  getFeishuDelivery,
  getFeishuModelStatus,
  getFeishuReport,
  getFeishuStatus,
  getLatestFeishuReport,
  isFeishuBusinessAuthFailure,
  saveFeishuConfig,
  saveFeishuSchedule,
  sendFeishuReport,
} from './feishu-api';
import type {
  FeishuBinding,
  FeishuDelivery,
  FeishuModelStatus,
  FeishuReport,
  FeishuState,
  FeishuStatus,
} from './feishu-api';
import './feishu.css';

type Mutation = 'save' | 'clear' | 'enable' | 'disable' | 'preview' | 'send';
export interface FeishuRecovery {
  key: string;
  kind: Mutation;
  configRevision: number;
  scheduleRevision: number;
  windowEnd: string;
  reportId: string;
  deliveryId: string;
  // Known terminal report identity only; not a pending operation or consent.
  settled?: true;
}
export type FeishuRecoveryCache = Map<string, FeishuRecovery[]>;
const HOUR = 60 * 60 * 1000;
const STATES: Record<FeishuState, string> = {
  running: '后台处理中',
  succeeded: '已完成',
  failed: '失败（不重试）',
  unknown: '结果未知（须人工核实）',
};
const OPERATIONS: Record<Mutation, string> = {
  save: '保存本机配置',
  clear: '清除本机配置',
  enable: '启用计划',
  disable: '关闭计划',
  preview: '生成预览',
  send: '发送至本人飞书',
};
// Fixed Shanghai slots, not a rolling duration or the browser's time zone.
export function latestFeishuWindow(time: number) {
  const shanghai = new Date(time + 8 * HOUR);
  const day =
    Date.UTC(
      shanghai.getUTCFullYear(),
      shanghai.getUTCMonth(),
      shanghai.getUTCDate(),
    ) -
    8 * HOUR;
  const hour = shanghai.getUTCHours();
  const end = day + (hour >= 20 ? 20 : hour >= 8 ? 8 : -4) * HOUR;
  return {
    start: new Date(end - 12 * HOUR).toISOString(),
    end: new Date(end).toISOString(),
  };
}
const formatTime = (value: string | null): string =>
  value === null
    ? '—'
    : new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(new Date(value));
const sameTime = (first: string, second: string): boolean =>
  Date.parse(first) === Date.parse(second);
const terminal = (state: FeishuState): boolean =>
  state === 'succeeded' || state === 'failed';
const failureMessage = (failure: unknown): string =>
  failure instanceof FeishuRequestError
    ? failure.message
    : '无法完成本次操作，请只读核实状态。';

function Receipt({ delivery }: { delivery: FeishuDelivery }) {
  return (
    <section className="feishu-receipt" aria-labelledby="feishu-receipt-title">
      <h3 id="feishu-receipt-title">投递回执</h3>
      <p
        className={
          delivery.state === 'unknown' || delivery.state === 'failed'
            ? 'feishu-warning'
            : undefined
        }
      >
        {delivery.state === 'succeeded'
          ? '飞书平台已接受'
          : STATES[delivery.state]}
        {delivery.state === 'unknown'
          ? '。请在本人飞书中人工核实，不要再次发送。'
          : delivery.state === 'running'
            ? '。请只读获取后续回执，不要再次发送。'
            : delivery.state === 'failed'
              ? '。本窗口不提供再次发送。'
              : '，不代表手机系统已通知或已读。'}
      </p>
      <dl className="feishu-facts">
        <div>
          <dt>投递编号</dt>
          <dd>
            <code>{delivery.id}</code>
          </dd>
        </div>
        <div>
          <dt>报告编号</dt>
          <dd>
            <code>{delivery.report_id}</code>
          </dd>
        </div>
        <div>
          <dt>内容窗口（北京时间）</dt>
          <dd>
            [{formatTime(delivery.window_start)},{' '}
            {formatTime(delivery.window_end)})
          </dd>
        </div>
        <div>
          <dt>平台消息编号</dt>
          <dd>
            <code>{delivery.message_id || '尚无平台接受凭据'}</code>
          </dd>
        </div>
        <div>
          <dt>开始 / 完成</dt>
          <dd>
            {formatTime(delivery.created_at)} /{' '}
            {formatTime(delivery.completed_at)}
          </dd>
        </div>
        <div>
          <dt>错误码</dt>
          <dd>
            <code>{delivery.error_code || '—'}</code>
          </dd>
        </div>
        <div>
          <dt>目标绑定 / 配置版本</dt>
          <dd>
            <code>{delivery.target_binding_id}</code> /{' '}
            {delivery.config_revision}
          </dd>
        </div>
        <div>
          <dt>模型绑定 / 版本</dt>
          <dd>
            <code>{delivery.model_config_id}</code> /{' '}
            {delivery.model_config_revision}
          </dd>
        </div>
        <div className="feishu-wide">
          <dt>冻结报告 SHA-256（含窗口与绑定）</dt>
          <dd>
            <code>{delivery.content_hash}</code>
          </dd>
        </div>
      </dl>
    </section>
  );
}

export function FeishuPanel({
  token,
  recoveryCache,
  onAuthFailure,
  onSettings,
}: {
  token: string;
  recoveryCache: FeishuRecoveryCache;
  onAuthFailure: () => void;
  onSettings: () => void;
}) {
  const [status, setStatus] = useState<FeishuStatus | null>(null);
  const [model, setModel] = useState<FeishuModelStatus | null>(null);
  const [modelError, setModelError] = useState<string | null>(null);
  const [report, setReport] = useState<FeishuReport | null>(null);
  const [delivery, setDelivery] = useState<FeishuDelivery | null>(null);
  const [busy, setBusy] = useState<Mutation | 'read' | 'stopping' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const [appID, setAppID] = useState('');
  const [retainSecret, setRetainSecret] = useState(false);
  const [configDirty, setConfigDirty] = useState(false);
  const [saveConsent, setSaveConsent] = useState(false);
  const [clearConsent, setClearConsent] = useState(false);
  const [disableConsent, setDisableConsent] = useState(false);
  const [scheduleModelConsent, setScheduleModelConsent] = useState(false);
  const [scheduleFeishuConsent, setScheduleFeishuConsent] = useState(false);
  const [previewConsent, setPreviewConsent] = useState(false);
  const [sendConsent, setSendConsent] = useState(false);
  const [reportID, setReportID] = useState('');
  const [deliveryID, setDeliveryID] = useState('');
  const [recoveries, setRecoveries] = useState(
    () => recoveryCache.get(token) ?? [],
  );
  const recoveryRef = useRef(recoveries);
  const mounted = useRef(false);
  const authFailure = useRef(onAuthFailure);
  const active = useRef<{
    controller: AbortController;
    mutation: boolean;
  } | null>(null);
  // Write-only credentials live only in this form's memory, never in the
  // recovery cache, React snapshots, URLs, storage, diagnostics or the Agent.
  const draft = useRef({ secret: '', openID: '' });
  const secretInput = useRef<HTMLInputElement | null>(null);
  const openIDInput = useRef<HTMLInputElement | null>(null);
  useEffect(() => {
    authFailure.current = onAuthFailure;
  }, [onAuthFailure]);
  const clearCredentials = useCallback(() => {
    draft.current.secret = '';
    draft.current.openID = '';
    if (secretInput.current) secretInput.current.value = '';
    if (openIDInput.current) openIDInput.current.value = '';
  }, []);
  const resetConsents = useCallback(() => {
    setSaveConsent(false);
    setClearConsent(false);
    setDisableConsent(false);
    setScheduleModelConsent(false);
    setScheduleFeishuConsent(false);
    setPreviewConsent(false);
    setSendConsent(false);
  }, []);
  const storeRecoveries = useCallback(
    (next: FeishuRecovery[]) => {
      recoveryRef.current = next;
      if (next.length) recoveryCache.set(token, next);
      else recoveryCache.delete(token);
      if (mounted.current) setRecoveries(next);
    },
    [recoveryCache, token],
  );
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearCredentials();
      const pendingRequest = active.current;
      active.current = null;
      pendingRequest?.controller.abort();
      // A mutation is recorded before IO. Leaving only stops waiting; no
      // credential or consent survives, but its unknown identity does.
    };
  }, [clearCredentials]);

  const reload = useCallback(
    async (requestedReport = '', requestedDelivery = '') => {
      if (!token || active.current) return;
      const controller = new AbortController();
      active.current = { controller, mutation: false };
      setBusy('read');
      setError(null);
      setNotice(null);
      setModelError(null);
      resetConsents();
      clearCredentials();
      setRetainSecret(false);
      setConfigDirty(false);
      setStatus(null);
      setModel(null);
      setReport(null);
      setDelivery(null);
      const pending = recoveryRef.current;
      const completedWindow = latestFeishuWindow(Date.now()).end;
      const currentPending = pending.filter(
        (item) => !item.settled && sameTime(item.windowEnd, completedWindow),
      );
      const knownReport =
        requestedReport ||
        currentPending.find((item) => item.reportId)?.reportId ||
        '';
      const knownDelivery =
        requestedDelivery ||
        currentPending.find((item) => item.deliveryId)?.deliveryId ||
        pending.find((item) => !item.settled && item.deliveryId)?.deliveryId ||
        '';
      try {
        const [statusResult, modelResult, reportResult, deliveryResult] =
          await Promise.allSettled([
            getFeishuStatus(token, controller.signal),
            getFeishuModelStatus(token, controller.signal),
            knownReport
              ? getFeishuReport(token, controller.signal, knownReport)
              : getLatestFeishuReport(token, controller.signal),
            knownDelivery
              ? getFeishuDelivery(token, controller.signal, knownDelivery)
              : Promise.resolve(null),
          ]);
        if (
          !mounted.current ||
          controller.signal.aborted ||
          active.current?.controller !== controller
        )
          return;
        const failures = [
          statusResult,
          modelResult,
          reportResult,
          deliveryResult,
        ].filter((result) => result.status === 'rejected');
        if (
          failures.some(
            (result) =>
              result.status === 'rejected' &&
              isFeishuBusinessAuthFailure(result.reason),
          )
        ) {
          authFailure.current();
          return;
        }
        const nextStatus =
          statusResult.status === 'fulfilled' ? statusResult.value : null;
        const nextReport =
          reportResult.status === 'fulfilled' ? reportResult.value : null;
        const nextDelivery =
          deliveryResult.status === 'fulfilled' ? deliveryResult.value : null;
        if (nextStatus) {
          setStatus(nextStatus);
          setAppID(nextStatus.app_id);
        }
        if (modelResult.status === 'fulfilled') setModel(modelResult.value);
        else setModelError(failureMessage(modelResult.reason));
        setReport(nextReport);
        setDelivery(
          nextDelivery ??
            nextReport?.delivery ??
            nextStatus?.latest_delivery ??
            null,
        );
        const unresolved = recoveryRef.current.flatMap((item) => {
          if (
            (item.kind === 'save' || item.kind === 'clear') &&
            nextStatus &&
            nextStatus.config_revision > item.configRevision
          )
            return [];
          if (
            (item.kind === 'enable' || item.kind === 'disable') &&
            nextStatus &&
            nextStatus.schedule.revision > item.scheduleRevision
          )
            return [];
          if (
            item.kind === 'preview' &&
            nextReport &&
            sameTime(nextReport.window_end, item.windowEnd)
          )
            return [
              {
                ...item,
                reportId: nextReport.id,
                settled: terminal(nextReport.state)
                  ? (true as const)
                  : undefined,
              },
            ];
          if (item.kind === 'send') {
            const receipt = [
              nextDelivery,
              nextReport?.delivery,
              nextStatus?.latest_delivery,
            ].find(
              (value) =>
                value &&
                (item.reportId
                  ? value.report_id === item.reportId
                  : sameTime(value.window_end, item.windowEnd)),
            );
            if (receipt)
              return terminal(receipt.state)
                ? []
                : [
                    {
                      ...item,
                      reportId: receipt.report_id,
                      deliveryId: receipt.id,
                    },
                  ];
          }
          return [item];
        });
        if (
          nextReport &&
          !unresolved.some(
            (item) =>
              item.kind === 'preview' &&
              sameTime(item.windowEnd, nextReport.window_end),
          )
        ) {
          unresolved.push({
            key: crypto.randomUUID(),
            kind: 'preview',
            configRevision: nextReport.config_revision,
            scheduleRevision: nextReport.schedule_revision,
            windowEnd: nextReport.window_end,
            reportId: nextReport.id,
            deliveryId: '',
            settled: terminal(nextReport.state) ? true : undefined,
          });
        }
        // A terminal delivery proves generation already finished, even if its
        // report body cannot be read. Keep only its settled window identity.
        for (const receipt of [
          nextDelivery,
          nextReport?.delivery,
          nextStatus?.latest_delivery,
        ]) {
          if (
            !receipt ||
            !terminal(receipt.state) ||
            unresolved.some(
              (item) =>
                item.kind === 'preview' &&
                sameTime(item.windowEnd, receipt.window_end),
            )
          )
            continue;
          unresolved.push({
            key: crypto.randomUUID(),
            kind: 'preview',
            configRevision: receipt.config_revision,
            scheduleRevision: nextStatus?.schedule.revision ?? 0,
            windowEnd: receipt.window_end,
            reportId: receipt.report_id,
            deliveryId: '',
            settled: true,
          });
        }
        const observedReceipt =
          nextDelivery ?? nextReport?.delivery ?? nextStatus?.latest_delivery;
        if (
          observedReceipt &&
          !terminal(observedReceipt.state) &&
          !unresolved.some(
            (item) =>
              item.kind === 'send' &&
              item.reportId === observedReceipt.report_id,
          )
        ) {
          unresolved.push({
            key: crypto.randomUUID(),
            kind: 'send',
            configRevision: observedReceipt.config_revision,
            scheduleRevision: nextStatus?.schedule.revision ?? 0,
            windowEnd: observedReceipt.window_end,
            reportId: observedReceipt.report_id,
            deliveryId: observedReceipt.id,
          });
        }
        storeRecoveries(
          unresolved.filter(
            (item) =>
              !item.settled || sameTime(item.windowEnd, completedWindow),
          ),
        );
        const businessFailure = [
          statusResult,
          reportResult,
          deliveryResult,
        ].find((result) => result.status === 'rejected');
        if (businessFailure?.status === 'rejected')
          setError(failureMessage(businessFailure.reason));
        else
          setNotice(
            '已只读获取当前状态；没有调用模型或发送消息。所有同意均需重新勾选。',
          );
        setNow(Date.now());
      } finally {
        if (mounted.current && active.current?.controller === controller) {
          active.current = null;
          setBusy(null);
        }
      }
    },
    [token, clearCredentials, resetConsents, storeRecoveries],
  );
  useEffect(() => {
    void reload();
  }, [reload]);
  const windowRange = latestFeishuWindow(now);
  useEffect(() => {
    const timer = window.setTimeout(
      () => {
        setNow(Date.now());
        resetConsents();
      },
      Math.max(0, Date.parse(windowRange.end) + 12 * HOUR - Date.now()) + 50,
    );
    return () => window.clearTimeout(timer);
  }, [windowRange.end, resetConsents]);

  const pendingRecoveries = recoveries.filter((item) => !item.settled);
  const blocked = pendingRecoveries.some(
    (item) =>
      !['preview', 'send'].includes(item.kind) ||
      sameTime(item.windowEnd, windowRange.end),
  );
  const usableModel = Boolean(model?.configured && !modelError);
  const quotaExhausted = Boolean(
    model?.configured && model.used_today >= model.daily_limit,
  );
  const ready = Boolean(token && status && !busy);
  const targetReady = Boolean(
    ready && status?.configured && usableModel && !configDirty && !blocked,
  );
  const currentReport = Boolean(
    report && sameTime(report.window_end, windowRange.end),
  );
  const hasWindowReport =
    currentReport ||
    recoveries.some(
      (item) =>
        item.kind === 'preview' && sameTime(item.windowEnd, windowRange.end),
    );
  const reportMatches = Boolean(
    report &&
    status &&
    model &&
    report.config_revision === status.config_revision &&
    report.target_binding_id === status.target_binding_id &&
    report.model_config_id === model.model_config_id &&
    report.model_config_revision === model.model_config_revision,
  );
  const hasDelivery = Boolean(
    report?.delivery ||
    (report && delivery?.report_id === report.id) ||
    (report &&
      recoveries.some(
        (item) =>
          item.kind === 'send' &&
          (item.reportId === report.id ||
            sameTime(item.windowEnd, report.window_end)),
      )),
  );
  const sendReady = Boolean(
    targetReady &&
    report?.state === 'succeeded' &&
    currentReport &&
    reportMatches &&
    !hasDelivery,
  );
  const binding = (): FeishuBinding | null =>
    status && model?.configured
      ? {
          confirmed: true,
          model_config_id: model.model_config_id,
          model_config_revision: model.model_config_revision,
          config_revision: status.config_revision,
          target_binding_id: status.target_binding_id,
        }
      : null;
  const changedConfig = () => {
    setConfigDirty(true);
    resetConsents();
  };

  const perform = async <T,>(
    kind: Mutation,
    action: (signal: AbortSignal) => Promise<T>,
    apply: (value: T, item: FeishuRecovery) => FeishuRecovery | null,
  ) => {
    if (!token || !status || active.current) return;
    const controller = new AbortController();
    const item: FeishuRecovery = {
      key: crypto.randomUUID(),
      kind,
      configRevision: status.config_revision,
      scheduleRevision: status.schedule.revision,
      windowEnd:
        kind === 'send' && report ? report.window_end : windowRange.end,
      reportId: kind === 'send' && report ? report.id : '',
      deliveryId: '',
    };
    storeRecoveries([...recoveryRef.current, item]);
    active.current = { controller, mutation: true };
    setBusy(kind);
    setError(null);
    setNotice(null);
    resetConsents();
    if (kind === 'save' || kind === 'clear') setReport(null);
    try {
      const value = await action(controller.signal);
      if (
        !mounted.current ||
        controller.signal.aborted ||
        active.current?.controller !== controller
      )
        return;
      const unresolved = apply(value, item);
      storeRecoveries(
        recoveryRef.current.flatMap((pending) =>
          pending.key === item.key
            ? unresolved
              ? [unresolved]
              : []
            : [pending],
        ),
      );
      setNow(Date.now());
    } catch (failure: unknown) {
      if (!mounted.current || active.current?.controller !== controller) return;
      if (failure instanceof FeishuRequestError && !failure.uncertain)
        storeRecoveries(
          recoveryRef.current.filter((pending) => pending.key !== item.key),
        );
      if (kind === 'send' || kind === 'preview') setReport(null);
      if (isFeishuBusinessAuthFailure(failure)) authFailure.current();
      else setError(failureMessage(failure));
    } finally {
      if (kind === 'save') clearCredentials();
      if (mounted.current && active.current?.controller === controller) {
        active.current = null;
        setBusy(null);
      }
    }
  };
  const saveConfig = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (
      !ready ||
      !status ||
      !saveConsent ||
      blocked ||
      (retainSecret && (!status.key_configured || appID !== status.app_id))
    )
      return;
    return perform(
      'save',
      (signal) =>
        saveFeishuConfig(token, signal, {
          expected_revision: status.config_revision,
          app_id: appID.trim(),
          app_secret: draft.current.secret,
          retain_secret: retainSecret,
          recipient_open_id: draft.current.openID.trim(),
          confirmed: true,
        }),
      (next) => {
        clearCredentials();
        setStatus(next);
        setAppID(next.app_id);
        setRetainSecret(false);
        setConfigDirty(false);
        setNotice(
          '已保存到本机并关闭原周期计划。尚未联网验证应用、收件人本人身份或手机通知；启用或发送需另行同意。',
        );
        return null;
      },
    );
  };
  const clearConfig = () => {
    if (!ready || !status || !clearConsent) return;
    return perform(
      'clear',
      (signal) =>
        clearFeishuConfig(token, signal, {
          expected_revision: status.config_revision,
          confirmed: true,
        }),
      (next) => {
        clearCredentials();
        setStatus(next);
        setAppID('');
        setRetainSecret(false);
        setConfigDirty(false);
        setNotice(
          '已清除本机应用凭据与目标并关闭计划；历史回执保留。后台停止/取消后续处理，但不能保证撤回在途请求或平台已接受的消息。',
        );
        return null;
      },
    );
  };
  const setSchedule = (enabled: boolean) => {
    if (
      !ready ||
      !status ||
      (enabled
        ? !targetReady || !scheduleModelConsent || !scheduleFeishuConsent
        : !disableConsent)
    )
      return;
    const consent = binding();
    if (enabled && !consent) return;
    return perform(
      enabled ? 'enable' : 'disable',
      (signal) =>
        saveFeishuSchedule(
          token,
          signal,
          enabled && consent
            ? {
                enabled: true,
                expected_revision: status.schedule.revision,
                ...consent,
              }
            : {
                enabled: false,
                expected_revision: status.schedule.revision,
                confirmed: true,
              },
        ),
      (next) => {
        setStatus({ ...status, schedule: next });
        setNotice(
          next.enabled
            ? '已在本机启用固定早晚报，从未来时点开始；不补发离线窗口。需主机与后台保持运行和联网。'
            : '已关闭早晚报计划并停止/取消后续处理；不能保证撤回在途请求或平台已接受的消息。',
        );
        return null;
      },
    );
  };
  const preview = () => {
    const consent = binding();
    if (
      !targetReady ||
      !consent ||
      !previewConsent ||
      quotaExhausted ||
      hasWindowReport
    )
      return;
    return perform(
      'preview',
      (signal) => generateFeishuPreview(token, signal, consent),
      (next, item) => {
        setReport(next);
        setDelivery(next.delivery);
        setNotice(
          next.state === 'succeeded'
            ? '已生成服务器固定预览，尚未发送。发送前请核对完整正文并另行同意。'
            : `生成记录：${STATES[next.state]}。只读取该记录，不会再次调用模型。`,
        );
        return {
          ...item,
          windowEnd: next.window_end,
          reportId: next.id,
          settled: terminal(next.state) ? true : undefined,
        };
      },
    );
  };
  const send = () => {
    const consent = binding();
    if (!sendReady || !consent || !report || !sendConsent) return;
    return perform(
      'send',
      (signal) =>
        sendFeishuReport(token, signal, report.id, {
          ...consent,
          content_hash: report.content_hash,
        }),
      (next, item) => {
        setDelivery(next);
        setReport({ ...report, delivery: next });
        setNotice(
          next.state === 'succeeded'
            ? '飞书平台已接受；这不是手机系统通知或已读证明。'
            : `投递记录：${STATES[next.state]}。请只读核实；本窗口不提供再次发送。`,
        );
        return terminal(next.state)
          ? null
          : { ...item, reportId: next.report_id, deliveryId: next.id };
      },
    );
  };
  const stopWaiting = () => {
    if (!active.current) return;
    const mutation = active.current.mutation;
    active.current.controller.abort();
    setBusy('stopping');
    resetConsents();
    setNotice(
      mutation
        ? '仅停止本地等待，未取消后台操作；结果未知。请重新读取，勿重复提交。'
        : '正在停止本地读取。',
    );
  };

  return (
    <div className="feishu-page">
      <div className="page-heading">
        <h1 className="icon-label">
          <Icon name="calendar" size={22} />
          飞书早晚报
        </h1>
        <p className="page-description">
          自建应用机器人，仅发送到你本人的飞书私聊；不是群机器人 Webhook。
        </p>
      </div>
      <div className="feishu-toolbar">
        <p>
          北京时间（Asia/Shanghai）每天 <strong>08:00 / 20:00</strong>
          ，相邻时点的 12 小时半开窗口 [开始, 结束)。
        </p>
        <div className="feishu-actions">
          <button
            type="button"
            className="button button-secondary button-icon"
            aria-label="重新读取"
            title="重新读取"
            disabled={!token || Boolean(busy)}
            onClick={() => void reload()}
          >
            <Icon name="refresh" />
          </button>
          {busy ? (
            <button
              type="button"
              className="button button-quiet"
              disabled={busy === 'stopping'}
              onClick={stopWaiting}
            >
              <Icon name="close" />
              停止等待
            </button>
          ) : null}
        </div>
      </div>
      <p className="feishu-note">
        主机与后台须持续运行和联网；休眠、关机不保证执行，也不离线补发。平台接受消息不代表手机系统通知或已读。
      </p>
      {!token ? (
        <div className="feishu-empty">
          <p>
            连接本地服务后读取真实配置。不要把应用密钥或 Open ID 发给 Agent。
          </p>
          <button
            type="button"
            className="button button-primary"
            onClick={onSettings}
          >
            <Icon name="plug" />
            连接本地服务
          </button>
        </div>
      ) : null}
      <div className="feishu-feedback" aria-live="polite" aria-atomic="true">
        {error ? (
          <p className="feishu-error">{error}</p>
        ) : notice ? (
          <p>{notice}</p>
        ) : busy ? (
          <p>
            正在
            {busy === 'read'
              ? '读取状态（旧预览已撤下）'
              : busy === 'stopping'
                ? '停止本地等待'
                : OPERATIONS[busy]}
            …
          </p>
        ) : null}
      </div>
      {pendingRecoveries.length ? (
        <section
          className="feishu-warning"
          aria-labelledby="feishu-unresolved-title"
        >
          <h2 id="feishu-unresolved-title">有待核实的处理</h2>
          <p>
            运行中、未知或响应中断不等于失败。只读获取状态或回执；未知投递请到本人飞书人工核实，不要重发。关闭计划、清除配置仍可单独操作，会停止/取消后续处理，但不能保证撤回在途请求或平台已接受的消息。
          </p>
          <ul>
            {pendingRecoveries.map((item) => (
              <li key={item.key}>
                {OPERATIONS[item.kind]} · 窗口结束 {formatTime(item.windowEnd)}
                {item.reportId ? (
                  <>
                    {' '}
                    · <code>{item.reportId}</code>
                  </>
                ) : null}
                {item.deliveryId ? (
                  <>
                    {' '}
                    · <code>{item.deliveryId}</code>
                  </>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      <section className="feishu-section" aria-labelledby="feishu-config-title">
        <div className="section-heading">
          <h2 id="feishu-config-title" className="icon-label">
            <Icon name="key" size={20} />
            本人应用与收件人
          </h2>
          <span className="label">
            {status
              ? status.configured
                ? '已保存到本机'
                : '尚未配置'
              : token
                ? '尚未读取'
                : '服务未连接'}
          </span>
        </div>
        <p className="feishu-note">
          仅支持飞书自建应用机器人；最小发信权限
          im:message:send_as_bot。请自行核对应用内 Open ID
          确属本人，本页不会验证这一身份。
        </p>
        <dl className="feishu-facts">
          <div>
            <dt>已保存 App ID</dt>
            <dd>
              <code>{status?.app_id || '—'}</code>
            </dd>
          </div>
          <div>
            <dt>固定私聊目标</dt>
            <dd>
              {status?.target_label_masked || '—'}{' '}
              <code>{status?.recipient_open_id_masked || ''}</code>
            </dd>
          </div>
          <div>
            <dt>本人归属核验</dt>
            <dd>未通过平台核验；配置仅记录本人的声明。</dd>
          </div>
          <div>
            <dt>本机凭据 / 配置版本</dt>
            <dd>
              {status?.key_configured ? '已保存密钥' : '未读取或未保存密钥'} /{' '}
              {status?.config_revision ?? '—'}
            </dd>
          </div>
          <div>
            <dt>目标绑定</dt>
            <dd>
              <code>{status?.target_binding_id || '—'}</code>
            </dd>
          </div>
        </dl>
        <details className="feishu-config-editor" open={!status?.configured}>
          <summary>人工配置自建应用与本人 Open ID</summary>
          <p id="feishu-credentials-help" className="feishu-note">
            密钥和原始 Open ID
            仅在本表单内存中保留。配置提交结束（含失败或未知）、重新读取或离开页面会清空；不会发给模型或写入浏览器存储。每次保存都撤销原计划授权。
          </p>
          <form
            method="post"
            aria-labelledby="feishu-config-form-title"
            aria-describedby="feishu-credentials-help"
            onSubmit={saveConfig}
          >
            <h3 id="feishu-config-form-title">保存到本机，不执行联网核验</h3>
            <fieldset disabled={!ready || blocked}>
              <legend>应用与本人私聊标识</legend>
              <div className="feishu-form-grid">
                <div className="feishu-field">
                  <label htmlFor="feishu-app-id">App ID（必填）</label>
                  <span id="feishu-app-id-hint" className="feishu-note">
                    飞书自建应用标识，以 cli_ 开头。
                  </span>
                  <input
                    id="feishu-app-id"
                    name="app_id"
                    required
                    pattern="cli_[A-Za-z0-9_\-]+"
                    maxLength={128}
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    aria-describedby="feishu-app-id-hint"
                    value={appID}
                    onChange={(event) => {
                      setAppID(event.target.value);
                      setRetainSecret(false);
                      changedConfig();
                    }}
                  />
                </div>
                <div className="feishu-field">
                  <label htmlFor="feishu-app-secret">
                    App Secret{retainSecret ? '（使用已保存密钥）' : '（必填）'}
                  </label>
                  <span id="feishu-app-secret-hint" className="feishu-note">
                    仅用于本机后端获取应用访问令牌，不发给模型。
                  </span>
                  <input
                    ref={secretInput}
                    id="feishu-app-secret"
                    name="app_secret"
                    type="password"
                    autoComplete="new-password"
                    autoCapitalize="none"
                    spellCheck={false}
                    maxLength={4096}
                    required={!retainSecret}
                    disabled={retainSecret}
                    aria-describedby="feishu-app-secret-hint"
                    defaultValue=""
                    onChange={(event) => {
                      draft.current.secret = event.target.value;
                      changedConfig();
                    }}
                  />
                </div>
                <div className="feishu-field feishu-wide">
                  <label htmlFor="feishu-open-id">
                    本人的应用内 Open ID（必填）
                  </label>
                  <span id="feishu-open-id-hint" className="feishu-note">
                    以 ou_ 开头；不是群
                    ID、手机号或任意收件人。每次保存都需人工填写完整值。
                  </span>
                  <input
                    ref={openIDInput}
                    id="feishu-open-id"
                    name="recipient_open_id"
                    required
                    pattern="ou_[A-Za-z0-9_\-]+"
                    maxLength={128}
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    aria-describedby="feishu-open-id-hint"
                    defaultValue=""
                    onChange={(event) => {
                      draft.current.openID = event.target.value;
                      changedConfig();
                    }}
                  />
                </div>
              </div>
              <label className="feishu-check">
                <input
                  id="feishu-retain-secret"
                  name="retain_secret"
                  type="checkbox"
                  checked={retainSecret}
                  disabled={!status?.key_configured || appID !== status.app_id}
                  onChange={(event) => {
                    setRetainSecret(event.target.checked);
                    draft.current.secret = '';
                    if (secretInput.current) secretInput.current.value = '';
                    changedConfig();
                  }}
                />
                <span>
                  仅沿用该 App ID 已保存在本机的密钥（不会取回或显示密钥）。
                </span>
              </label>
              <label className="feishu-check">
                <input
                  id="feishu-config-consent"
                  name="config_consent"
                  type="checkbox"
                  checked={saveConsent}
                  onChange={(event) => setSaveConsent(event.target.checked)}
                />
                <span>
                  我已确认 Open ID
                  属于本人，同意把以上应用凭据与目标保存在本机；知道这不是联网验证，并会关闭原计划。
                </span>
              </label>
              <button
                type="submit"
                className="button button-primary"
                disabled={!saveConsent}
              >
                <Icon name="save" />
                保存本机配置
              </button>
            </fieldset>
          </form>
        </details>
        <details className="feishu-danger-zone">
          <summary>清除本机飞书配置</summary>
          <p>
            无需模型可用。清除应用凭据与目标、关闭计划并停止/取消后续处理；历史回执保留，但不能保证撤回在途请求或平台已接受的消息。
          </p>
          <label className="feishu-check">
            <input
              id="feishu-clear-consent"
              type="checkbox"
              checked={clearConsent}
              disabled={!ready}
              onChange={(event) => setClearConsent(event.target.checked)}
            />
            <span>
              我确认清除本机飞书配置并关闭计划，停止/取消后续处理；理解不能保证撤回在途请求或平台已接受的消息。
            </span>
          </label>
          <button
            type="button"
            className="button button-secondary"
            disabled={!ready || !clearConsent}
            onClick={() => void clearConfig()}
          >
            <Icon name="trash" />
            清除配置并关闭计划
          </button>
        </details>
      </section>
      <section
        className="feishu-section"
        aria-labelledby="feishu-schedule-title"
      >
        <div className="section-heading">
          <h2 id="feishu-schedule-title" className="icon-label">
            <Icon name="calendar" size={20} />
            固定早晚报计划
          </h2>
          <span className="label">
            {status
              ? status.schedule.enabled
                ? '已启用'
                : '已关闭'
              : '尚未读取'}
          </span>
        </div>
        <p>
          08:00 汇总前一天 20:00 至当天 08:00；20:00 汇总当天 08:00 至
          20:00，均不含结束时点。只选已入库、未暂停且符合相关性规则的私聊与群聊文本，不代表覆盖所有重要消息。
        </p>
        <dl className="feishu-facts">
          <div>
            <dt>下次窗口结束</dt>
            <dd>{formatTime(status?.schedule.next_window_end ?? null)}</dd>
          </div>
          <div>
            <dt>最近处理窗口结束</dt>
            <dd>{formatTime(status?.schedule.last_window_end ?? null)}</dd>
          </div>
          <div>
            <dt>计划版本</dt>
            <dd>{status?.schedule.revision ?? '—'}</dd>
          </div>
          <div>
            <dt>当前模型</dt>
            <dd>
              {model?.model || '未就绪'} ·{' '}
              <code>{model?.endpoint_host || '—'}</code>
            </dd>
          </div>
          <div>
            <dt>模型绑定 / 版本</dt>
            <dd>
              <code>{model?.model_config_id || '—'}</code> /{' '}
              {model?.model_config_revision ?? '—'}
            </dd>
          </div>
          <div>
            <dt>今日共享模型额度</dt>
            <dd>
              {model
                ? `${model.used_today} / ${model.daily_limit}`
                : '尚未读取'}
            </dd>
          </div>
        </dl>
        {modelError ? (
          <p className="feishu-warning">
            模型状态未就绪：{modelError} 仍可关闭计划或清除配置。
          </p>
        ) : null}
        {!usableModel && !modelError ? (
          <p className="feishu-note">
            配置可用模型后才能生成预览或启用计划；关闭与清除不依赖模型。
          </p>
        ) : null}
        {quotaExhausted ? (
          <p className="feishu-warning">
            今日共享额度已用尽，不可新生成预览；仍可只读获取记录、发送已就绪正文或关闭计划。
          </p>
        ) : null}
        {configDirty ? (
          <p className="feishu-warning">
            应用表单有未保存内容。请先保存或重新读取；未保存目标不会用于生成或发送。
          </p>
        ) : null}
        <fieldset disabled={!targetReady || Boolean(status?.schedule.enabled)}>
          <legend>分别同意模型数据处理与飞书持续发送</legend>
          <label className="feishu-check">
            <input
              id="feishu-schedule-model-consent"
              type="checkbox"
              checked={scheduleModelConsent}
              onChange={(event) =>
                setScheduleModelConsent(event.target.checked)
              }
            />
            <span>
              我同意后续每个固定窗口，将相关已入库、未暂停的私聊与群聊内容持续发送给当前模型{' '}
              {model?.model || '（未就绪）'}（{model?.endpoint_host || '—'}
              ），可能产生费用；同意绑定上述模型 ID 与版本。
            </span>
          </label>
          <label className="feishu-check">
            <input
              id="feishu-schedule-send-consent"
              type="checkbox"
              checked={scheduleFeishuConsent}
              onChange={(event) =>
                setScheduleFeishuConsent(event.target.checked)
              }
            />
            <span>
              我另行同意持续向已保存的本人飞书私聊{' '}
              {status?.recipient_open_id_masked || '（未配置）'}{' '}
              自动发送摘要与待核对待办；启用后不会逐次询问，知道主机离线不补发，平台接受不等于手机通知。
            </span>
          </label>
          <button
            type="button"
            className="button button-primary"
            disabled={!scheduleModelConsent || !scheduleFeishuConsent}
            onClick={() => void setSchedule(true)}
          >
            <Icon name="play" />
            启用固定早晚报
          </button>
        </fieldset>
        {status?.schedule.enabled ? (
          <p className="feishu-note">
            已启用计划绑定模型 <code>{status.schedule.model_config_id}</code> /{' '}
            {status.schedule.model_config_revision}、配置{' '}
            {status.schedule.config_revision} 与目标{' '}
            <code>{status.schedule.target_binding_id}</code>
            。保存模型或飞书配置会撤销原授权。
          </p>
        ) : null}
        <div className="feishu-disable">
          <label className="feishu-check">
            <input
              id="feishu-disable-consent"
              type="checkbox"
              checked={disableConsent}
              disabled={!ready}
              onChange={(event) => setDisableConsent(event.target.checked)}
            />
            <span>
              我确认关闭早晚报计划，停止/取消后续处理；理解不能保证撤回在途请求或平台已接受的消息。
            </span>
          </label>
          <button
            type="button"
            className="button button-secondary"
            disabled={!ready || !disableConsent}
            onClick={() => void setSchedule(false)}
          >
            <Icon name="pause" />
            关闭后续计划
          </button>
        </div>
      </section>
      <section
        className="feishu-section"
        aria-labelledby="feishu-preview-title"
      >
        <div className="section-heading">
          <h2 id="feishu-preview-title" className="icon-label">
            <Icon name="eye" size={20} />
            生成并核对预览
          </h2>
          <span className="label">只生成，不发送</span>
        </div>
        <p>
          最近一个已完整结束的窗口：
          <strong>
            [{formatTime(windowRange.start)}, {formatTime(windowRange.end)})
          </strong>
          （北京时间），不是滚动过去 24 小时；以后台时钟为准。
        </p>
        <p className="feishu-note">
          每个窗口只允许一次生成尝试，任何运行中、成功、失败或未知记录都不会重做；没有
          force 或失败后重试。发送有效期截止下一固定时点。
        </p>
        <label className="feishu-check">
          <input
            id="feishu-preview-consent"
            type="checkbox"
            checked={previewConsent}
            disabled={!targetReady || quotaExhausted || hasWindowReport}
            onChange={(event) => setPreviewConsent(event.target.checked)}
          />
          <span>
            我同意仅本次把该完整窗口内相关已入库、未暂停的私聊与群聊内容发送给上述模型（ID{' '}
            <code>{model?.model_config_id || '—'}</code> / 版本{' '}
            {model?.model_config_revision ?? '—'}
            ），可能产生费用；这不授权发送到飞书。
          </span>
        </label>
        <button
          type="button"
          className="button button-secondary"
          disabled={
            !targetReady || !previewConsent || quotaExhausted || hasWindowReport
          }
          onClick={() => void preview()}
        >
          <Icon name="eye" />
          生成最近完整窗口预览
        </button>
        {hasWindowReport ? (
          <p className="feishu-note">
            本窗口已有生成尝试，请重新读取已有记录，不会重新调用模型。
          </p>
        ) : null}
        {report ? (
          <article
            className="feishu-report"
            aria-labelledby="feishu-report-title"
          >
            <div className="section-heading">
              <h3 id="feishu-report-title" className="icon-label">
                <Icon name="file" />
                服务器固定报告
              </h3>
              <span className="label">{STATES[report.state]}</span>
            </div>
            <dl className="feishu-facts">
              <div>
                <dt>报告编号</dt>
                <dd>
                  <code>{report.id}</code>
                </dd>
              </div>
              <div>
                <dt>窗口（北京时间）</dt>
                <dd>
                  [{formatTime(report.window_start)},{' '}
                  {formatTime(report.window_end)})
                </dd>
              </div>
              <div>
                <dt>涉及会话 / 有限截取</dt>
                <dd>
                  {report.conversation_count} /{' '}
                  {report.truncated ? '是，未覆盖全部候选内容' : '否'}
                </dd>
              </div>
              <div>
                <dt>生成时间 / 错误码</dt>
                <dd>
                  {formatTime(report.generated_at)} /{' '}
                  <code>{report.error_code || '—'}</code>
                </dd>
              </div>
            </dl>
            {report.state === 'succeeded' ? (
              <>
                <h4 id="feishu-frozen-text-title">将实际发送的完整正文</h4>
                <pre
                  className="feishu-frozen-text"
                  tabIndex={0}
                  aria-labelledby="feishu-frozen-text-title"
                >
                  {report.text}
                </pre>
                <p className="feishu-note">
                  仅服务器冻结正文可被发送，不能另选收件人或修改文本。待办为证据生成的候选，须人工核对，不是已执行事项。
                </p>
                <dl className="feishu-facts">
                  <div>
                    <dt>固定目标（当前已保存）</dt>
                    <dd>
                      {status?.target_label_masked || '尚未读取'}{' '}
                      <code>{status?.recipient_open_id_masked || ''}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>正文绑定配置 / 模型版本</dt>
                    <dd>
                      {report.config_revision} /{' '}
                      <code>{report.model_config_id}</code> /{' '}
                      {report.model_config_revision}
                    </dd>
                  </div>
                  <div className="feishu-wide">
                    <dt>正文绑定目标</dt>
                    <dd>
                      <code>{report.target_binding_id}</code>
                    </dd>
                  </div>
                  <div className="feishu-wide">
                    <dt>冻结报告 SHA-256（含窗口与绑定）</dt>
                    <dd>
                      <code>{report.content_hash}</code>
                    </dd>
                  </div>
                </dl>
                <details className="feishu-evidence">
                  <summary>核对候选待办与最少来源证据</summary>
                  {report.todos.length ? (
                    <ul>
                      {report.todos.map((todo, index) => (
                        <li key={index}>
                          待人工核对：{todo.text}
                          <br />
                          <span className="feishu-note">
                            证据：{todo.evidence_ids.join('、') || '未提供'}
                          </span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p>没有待办候选，不代表该窗口没有需要处理的事项。</p>
                  )}
                  {report.excerpts.map((excerpt) => (
                    <figure
                      key={`${excerpt.conversation_id}:${excerpt.message_id}`}
                    >
                      <blockquote>{excerpt.text}</blockquote>
                      <figcaption>
                        会话 <code>{excerpt.conversation_id}</code> · 证据{' '}
                        <code>{excerpt.message_id}</code> ·{' '}
                        {formatTime(excerpt.occurred_at)}
                      </figcaption>
                    </figure>
                  ))}
                  <ul className="feishu-note">
                    {report.source_refs.map((source) => (
                      <li key={source.conversation_id}>
                        来源 <code>{source.conversation_id}</code> / 设置版本{' '}
                        {source.settings_revision}
                      </li>
                    ))}
                  </ul>
                </details>
                {!currentReport ? (
                  <p className="feishu-warning">
                    此报告不属于当前最近完整窗口，已过发送时限，不向后续窗口补发。
                  </p>
                ) : null}
                {!reportMatches ? (
                  <p className="feishu-warning">
                    报告与当前模型、飞书配置或目标未匹配，不能沿用旧同意；请重新读取。来源暂停或版本变化也会被后台拒绝。
                  </p>
                ) : null}
                {hasDelivery ? (
                  <p className="feishu-warning">
                    本窗口已有投递或待核实记录，不提供再次发送。
                  </p>
                ) : null}
                <label className="feishu-check">
                  <input
                    id="feishu-send-consent"
                    type="checkbox"
                    checked={sendConsent}
                    disabled={!sendReady}
                    onChange={(event) => setSendConsent(event.target.checked)}
                  />
                  <span>
                    我已核对以上完整正文、12
                    小时窗口、固定本人目标、哈希与版本，另行同意真实发送这份服务器冻结正文至本人飞书私聊{' '}
                    {status?.recipient_open_id_masked || '（未配置）'}
                    ；理解平台接受不代表手机系统通知或已读。这与模型数据处理同意相互独立。
                  </span>
                </label>
                <button
                  type="button"
                  className="button button-primary"
                  disabled={!sendReady || !sendConsent}
                  onClick={() => void send()}
                >
                  <Icon name="send" />
                  发送这份预览至本人飞书
                </button>
              </>
            ) : (
              <p className="feishu-warning">
                {report.state === 'running'
                  ? '后台仍在生成，请重新读取此报告。HTTP 202 不代表成功；不会重新提交模型请求。'
                  : report.state === 'unknown'
                    ? '生成结果未知，可能已经产生模型费用。只读核实；此窗口不再生成或发送。'
                    : '本窗口生成失败，不重新生成或发送。'}{' '}
                未完成报告不作为可发送正文。
              </p>
            )}
          </article>
        ) : (
          <p className="feishu-empty">
            {busy === 'read'
              ? '正在重新读取；旧预览已撤下。'
              : '尚无可展示的报告。请只读获取已有报告，或在同意范围后生成最近完整窗口的预览。'}
          </p>
        )}
      </section>
      {delivery ? <Receipt delivery={delivery} /> : null}
      <details className="feishu-recovery">
        <summary>按记录编号只读恢复</summary>
        <p className="feishu-note">
          仅调用报告或投递
          GET，不调用模型，不发送。若初次响应丢失，可先用“重新读取”获取后台最近记录；没有记录不证明后台未处理。
        </p>
        <div className="feishu-form-grid">
          <form
            method="get"
            aria-labelledby="feishu-read-report-label"
            onSubmit={(event) => {
              event.preventDefault();
              void reload(reportID.trim());
            }}
          >
            <label id="feishu-read-report-label" htmlFor="feishu-report-id">
              报告编号
            </label>
            <input
              id="feishu-report-id"
              name="report_id"
              value={reportID}
              required
              pattern="fr_[a-f0-9]{32}"
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setReportID(event.target.value)}
            />
            <button
              type="submit"
              className="button button-secondary"
              disabled={!token || Boolean(busy)}
            >
              <Icon name="file" />
              只读获取报告
            </button>
          </form>
          <form
            method="get"
            aria-labelledby="feishu-read-delivery-label"
            onSubmit={(event) => {
              event.preventDefault();
              void reload('', deliveryID.trim());
            }}
          >
            <label id="feishu-read-delivery-label" htmlFor="feishu-delivery-id">
              投递编号
            </label>
            <input
              id="feishu-delivery-id"
              name="delivery_id"
              value={deliveryID}
              required
              pattern="fd_[a-f0-9]{32}"
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setDeliveryID(event.target.value)}
            />
            <button
              type="submit"
              className="button button-secondary"
              disabled={!token || Boolean(busy)}
            >
              <Icon name="mail" />
              只读获取回执
            </button>
          </form>
        </div>
      </details>
    </div>
  );
}
