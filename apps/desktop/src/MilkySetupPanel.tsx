import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ServiceError } from './api';
import {
  getMilkyConfig,
  saveMilkyConfig,
  testMilkyConfig,
  type MilkyConfig,
  type MilkyConfigInput,
  type MilkyVerification,
} from './milky-api';
import './milky-setup.css';

interface Props {
  token: string;
  disabled: boolean;
  monitorEnabled: boolean;
  onAuthFailure: () => void;
  onBusyChange: (busy: boolean) => void;
  onSaved: () => void;
  onHistoryLimitChange?: (limit: number) => void;
}

const canonical = (value: string) =>
  value.trim().replace('://localhost:', '://127.0.0.1:').replace(/\/$/, '');

export function MilkySetupPanel(props: Props) {
  const { token, disabled, monitorEnabled } = props;
  const [config, setConfig] = useState<MilkyConfig | null>(null);
  const [url, setURL] = useState('http://127.0.0.1:3010');
  const [accountID, setAccountID] = useState('');
  const [accessToken, setAccessToken] = useState('');
  const [historyLimit, setHistoryLimit] = useState('20');
  const [tested, setTested] = useState<MilkyVerification | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState<'load' | 'test' | 'save' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const request = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  const dirty = useRef(false);
  const gate = useRef(false);
  const callbacks = useRef(props);
  callbacks.current = props;

  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    request.current = controller;
    gate.current = true;
    setBusy('load');
    setError(null);
    setTested(null);
    setConfirmed(false);
    callbacks.current.onBusyChange(true);
    if (!token) {
      setConfig(null);
      setAccessToken('');
      setBusy(null);
      gate.current = false;
      callbacks.current.onBusyChange(false);
      return () => {
        mounted.current = false;
        controller.abort();
      };
    }
    void getMilkyConfig(token, controller.signal)
      .then((next) => {
        if (
          controller.signal.aborted ||
          !mounted.current ||
          request.current !== controller
        )
          return;
        setConfig(next);
        callbacks.current.onHistoryLimitChange?.(next.history_limit);
        if (!dirty.current) {
          setURL(next.url || 'http://127.0.0.1:3010');
          setAccountID(next.account_id);
          setHistoryLimit(String(next.history_limit));
        } else {
          setNotice(
            '已读取最新保存版本，当前输入已保留。请对照已保存信息，重新测试并确认。',
          );
        }
      })
      .catch((failure: unknown) => {
        if (
          controller.signal.aborted ||
          !mounted.current ||
          request.current !== controller
        )
          return;
        if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
          callbacks.current.onAuthFailure();
        else
          setError(
            failure instanceof ServiceError
              ? failure.message
              : '无法读取 Milky 设置，请重试。',
          );
      })
      .finally(() => {
        if (
          controller.signal.aborted ||
          !mounted.current ||
          request.current !== controller
        )
          return;
        gate.current = false;
        setBusy(null);
        callbacks.current.onBusyChange(false);
      });
    return () => {
      mounted.current = false;
      request.current?.abort();
      callbacks.current.onBusyChange(false);
    };
  }, [token, reload]);

  // A changed service identity cannot retain another session's secret input.
  useEffect(() => {
    dirty.current = false;
    setAccessToken('');
    setConfig(null);
    setNotice(null);
  }, [token]);

  const edit = (change: () => void) => {
    dirty.current = true;
    change();
    setTested(null);
    setConfirmed(false);
    setNotice(null);
    setError(null);
  };
  const targetChanged =
    config !== null &&
    (canonical(url) !== canonical(config.url) ||
      accountID.trim() !== config.account_id);
  const tokenRequired = !config?.token_configured || targetChanged;
  const blocked = !token || config === null || busy !== null || disabled;

  const input = (): MilkyConfigInput | null => {
    if (!config) return null;
    if (tokenRequired && accessToken === '') {
      setError(
        '请填写协议端的 AccessToken；更换地址或 QQ 账号时不能沿用旧令牌。',
      );
      return null;
    }
    return {
      url: url.trim(),
      account_id: accountID.trim(),
      history_limit: Number(historyLimit),
      access_token: accessToken,
      expected_revision: config.revision,
    };
  };

  const act = async (name: 'test' | 'save') => {
    if (blocked || gate.current || (name === 'save' && (!tested || !confirmed)))
      return;
    const values = input();
    if (!values) return;
    const controller = new AbortController();
    request.current = controller;
    gate.current = true;
    setBusy(name);
    callbacks.current.onBusyChange(true);
    setError(null);
    setNotice(null);
    const current = () =>
      mounted.current &&
      !controller.signal.aborted &&
      request.current === controller;
    try {
      if (name === 'test') {
        const result = await testMilkyConfig(token, controller.signal, values);
        if (!current()) return;
        setTested(result);
        setNotice(
          `连接测试通过：${result.nickname || result.account_id}，Milky ${result.milky_version}。此测试未保存设置，也未开启后台接收。`,
        );
      } else {
        const next = await saveMilkyConfig(token, controller.signal, {
          ...values,
          confirmed: true,
        });
        if (!current()) return;
        setConfig(next);
        setURL(next.url);
        setAccountID(next.account_id);
        setHistoryLimit(String(next.history_limit));
        callbacks.current.onHistoryLimitChange?.(next.history_limit);
        setAccessToken('');
        setTested(null);
        setConfirmed(false);
        dirty.current = false;
        setNotice(
          '连接设置已加密保存并立即生效。请继续核验账号与版本，再选择联系人开启接收；无需重启后台。',
        );
        callbacks.current.onSaved();
      }
    } catch (failure: unknown) {
      if (!current()) return;
      setTested(null);
      setConfirmed(false);
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
        callbacks.current.onAuthFailure();
      else
        setError(
          failure instanceof ServiceError
            ? failure.message
            : '操作未完成，输入已保留。请刷新设置核对结果后再操作。',
        );
    } finally {
      if (current()) {
        gate.current = false;
        setBusy(null);
        callbacks.current.onBusyChange(false);
      }
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void act('save');
  };

  return (
    <div className="milky-setup" aria-busy={busy !== null}>
      <ol className="milky-setup-steps" aria-label="QQ 接入步骤">
        <li>
          <h3>1. 安装并登录协议端</h3>
          <p>
            推荐 LLBot 桌面版的 Milky 模式。按照官方说明下载 Windows
            版本，在自己的电脑上运行，并按引导登录本人 QQ。
          </p>
          <p>
            <a
              href="https://luckylillia.com/guide/choice_install"
              target="_blank"
              rel="noreferrer"
            >
              查看官方安装步骤 ↗
            </a>
            {' · '}
            <a
              href="https://github.com/LuckyLilliaDev/LuckyLillia.Desktop/releases/tag/v3.1.2"
              target="_blank"
              rel="noreferrer"
            >
              下载 Windows 桌面版 ↗
            </a>
          </p>
          <p className="input-help">
            下载 lucky-lillia-desktop-win-x64.zip，解压后运行
            lucky-lillia-desktop.exe。登录和二维码在 QQ / 协议端中处理。安装普通
            QQ 客户端并不会自动提供 Milky 接口。
          </p>
        </li>
        <li>
          <h3>2. 填写本机连接信息</h3>
          <p>
            在 LLBot 的 Bot 配置中启用 Milky，HTTP 端口使用
            3010，开启仅本机访问，设置自己的 AccessToken；接口前缀留空。只需使用
            Milky 模式。
          </p>
          <p>
            <a
              href="https://luckylillia.com/guide/config"
              target="_blank"
              rel="noreferrer"
            >
              查看协议端配置说明 ↗
            </a>
          </p>
          <p className="input-help">
            将对应地址、AccessToken 和本人 QQ
            号填在下方。协议端令牌与本产品服务令牌不同，地址不需要添加 /api 或
            /event。
          </p>
        </li>
        <li>
          <h3>3. 测试并保存</h3>
          <p>
            先核对账号和 Milky 1.3
            版本，再确认保存。之后通过下方核验，选择明确授权的联系人接收消息。
          </p>
        </li>
      </ol>
      <form className="connection-form milky-setup-form" onSubmit={submit}>
        <div className="section-heading">
          <h3>连接设置</h3>
          <span className={`label ${config?.configured ? 'label-green' : ''}`}>
            {config?.configured
              ? '已配置'
              : busy === 'load'
                ? '读取中…'
                : '待配置'}
          </span>
        </div>
        {config?.configured ? (
          <p className="input-help">
            已保存：{config.account_id} · {config.url}（
            {config.source === 'environment'
              ? '来自启动环境，保存后转为加密设置'
              : '已加密保存'}
            ）
          </p>
        ) : null}
        <label htmlFor="milky-url">Milky 服务地址</label>
        <input
          id="milky-url"
          type="url"
          value={url}
          onChange={(event) => edit(() => setURL(event.target.value))}
          disabled={blocked}
          maxLength={4096}
          autoComplete="off"
          spellCheck={false}
          required
          aria-describedby="milky-url-help"
        />
        <p className="input-help" id="milky-url-help">
          LLBot Milky 默认使用
          http://127.0.0.1:3010；若修改过端口，以实际配置为准。不要填写 WebUI 的
          3080、本产品的 17890 或工作台的 1420。
        </p>
        <label htmlFor="milky-account-id">本人 QQ 号</label>
        <input
          id="milky-account-id"
          inputMode="numeric"
          value={accountID}
          onChange={(event) => edit(() => setAccountID(event.target.value))}
          disabled={blocked}
          maxLength={128}
          autoComplete="off"
          spellCheck={false}
          required
        />
        <label htmlFor="milky-access-token">
          协议端 AccessToken
          {config?.token_configured && !targetChanged
            ? '（已保存，留空保留）'
            : ''}
        </label>
        <input
          id="milky-access-token"
          type="password"
          value={accessToken}
          onChange={(event) => edit(() => setAccessToken(event.target.value))}
          disabled={blocked}
          maxLength={4096}
          autoComplete="new-password"
          spellCheck={false}
          required={tokenRequired}
          aria-describedby="milky-token-help"
        />
        <p className="input-help" id="milky-token-help">
          仅提交给本机后台，加密保存后不返回页面。不要填写 QQ 密码。
          {targetChanged && config?.token_configured
            ? '地址或账号已更改，请重新填写对应令牌。'
            : ''}
        </p>
        <details className="milky-advanced">
          <summary>高级设置</summary>
          <label htmlFor="milky-history-limit">每次历史同步条数</label>
          <input
            id="milky-history-limit"
            type="number"
            min={1}
            max={30}
            step={1}
            value={historyLimit}
            onChange={(event) =>
              edit(() => setHistoryLimit(event.target.value))
            }
            disabled={blocked}
          />
        </details>
        <div className="form-actions">
          <button
            id="milky-test-connection"
            type="button"
            className="button button-secondary"
            disabled={blocked}
            onClick={() => void act('test')}
          >
            {busy === 'test' ? '正在测试…' : '测试连接'}
          </button>
          <button
            id="milky-reload-config"
            type="button"
            className="button button-quiet"
            disabled={!token || busy !== null || disabled}
            onClick={() => setReload((value) => value + 1)}
          >
            重新读取设置
          </button>
        </div>
        {monitorEnabled ? (
          <p className="input-help">
            当前保存了后台接收授权。保存连接设置会停止接收并清除原名单授权，请核验后重新选择联系人。
          </p>
        ) : (
          <p className="input-help">
            保存后仍不会自动接收。只有核验并明确选择联系人后，才可开启后台接收。
          </p>
        )}
        <label className="milky-save-consent">
          <input
            id="milky-confirm-save"
            type="checkbox"
            checked={confirmed}
            onChange={(event) => setConfirmed(event.target.checked)}
            disabled={blocked || !tested}
          />
          <span>
            我已核对测试账号，确认将连接信息加密保存；后台接收授权将重置，发送保持关闭。
          </span>
        </label>
        <div className="form-actions">
          <button
            id="milky-save-config"
            type="submit"
            className="button button-primary"
            disabled={blocked || !tested || !confirmed}
          >
            {busy === 'save' ? '正在保存…' : '保存连接设置'}
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
      </form>
    </div>
  );
}
