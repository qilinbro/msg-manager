import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ServiceError } from './api';
import {
  getModelConfig,
  saveModelConfig,
  type ModelConfig,
} from './model-config-api';
import './model-config.css';

export function AIModelPanel({
  token,
  onAuthFailure,
}: {
  token: string;
  onAuthFailure: () => void;
}) {
  const [config, setConfig] = useState<ModelConfig | null>(null);
  const [baseURL, setBaseURL] = useState('https://api.deepseek.com');
  const [model, setModel] = useState('deepseek-chat');
  const [dailyLimit, setDailyLimit] = useState('50');
  const [key, setKey] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const request = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  const authFailure = useRef(onAuthFailure);
  authFailure.current = onAuthFailure;

  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    request.current = controller;
    setConfig(null);
    setKey('');
    setConfirmed(false);
    setError(null);
    setNotice(null);
    if (!token) {
      setBusy(false);
      return () => {
        mounted.current = false;
        controller.abort();
      };
    }
    setBusy(true);
    void getModelConfig(token, controller.signal)
      .then((next) => {
        if (controller.signal.aborted || !mounted.current) return;
        setConfig(next);
        setBaseURL(next.base_url || 'https://api.deepseek.com');
        setModel(next.model || 'deepseek-chat');
        setDailyLimit(String(next.daily_limit));
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted || !mounted.current) return;
        if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
          authFailure.current();
        else
          setError(
            failure instanceof ServiceError
              ? failure.message
              : '无法读取模型设置。',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted && mounted.current) setBusy(false);
      });
    return () => {
      mounted.current = false;
      request.current?.abort();
    };
  }, [token, reload]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!config || busy || !confirmed) return;
    const limit = Number(dailyLimit);
    if (!Number.isInteger(limit) || limit < 1 || limit > 1000) {
      setError('每日调用上限须在 1 至 1000 之间。');
      return;
    }
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const next = await saveModelConfig(token, controller.signal, {
        expected_revision: config.revision,
        base_url: baseURL.trim(),
        model: model.trim(),
        daily_limit: limit,
        api_key: key,
        confirmed: true,
      });
      if (controller.signal.aborted || !mounted.current) return;
      setConfig(next);
      setKey('');
      setConfirmed(false);
      setNotice(
        '模型设置已加密保存并生效。更换模型目标后，旧的自动整理授权需要重新确认。',
      );
    } catch (failure) {
      if (controller.signal.aborted || !mounted.current) return;
      if (failure instanceof ServiceError && failure.code === 'UNAUTHORIZED')
        authFailure.current();
      else
        setError(
          failure instanceof ServiceError
            ? failure.message
            : '模型设置未保存，输入已保留。',
        );
    } finally {
      if (!controller.signal.aborted && mounted.current) setBusy(false);
    }
  };

  return (
    <section className="settings-panel" aria-labelledby="ai-model-title">
      <div className="section-heading">
        <h2 id="ai-model-title">AI 模型配置</h2>
        <span className={`label ${config?.configured ? 'label-neutral' : ''}`}>
          {config
            ? config.configured
              ? '已配置'
              : '未配置'
            : token
              ? '等待读取'
              : '请先连接服务'}
        </span>
      </div>
      <p className="input-help">
        支持 DeepSeek 和兼容接口。密钥只提交给本机服务，加密保存后不会返回页面。
      </p>
      <form className="connection-form ai-model-form" onSubmit={submit}>
        <label htmlFor="ai-model-url">服务地址</label>
        <input
          id="ai-model-url"
          type="url"
          value={baseURL}
          onChange={(e) => {
            setBaseURL(e.target.value);
            setConfirmed(false);
          }}
          maxLength={2048}
          disabled={busy || !token}
          required
          spellCheck={false}
          autoComplete="off"
        />
        <label htmlFor="ai-model-name">模型名称</label>
        <input
          id="ai-model-name"
          value={model}
          onChange={(e) => {
            setModel(e.target.value);
            setConfirmed(false);
          }}
          maxLength={128}
          disabled={busy || !token}
          required
          spellCheck={false}
          autoComplete="off"
        />
        <label htmlFor="ai-model-key">
          API 密钥{config?.key_configured ? '（已保存，留空保留）' : ''}
        </label>
        <input
          id="ai-model-key"
          type="password"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          maxLength={4096}
          disabled={busy || !token}
          autoComplete="off"
          spellCheck={false}
          placeholder={
            config?.key_configured
              ? '更换地址时须填写新密钥'
              : '输入模型服务密钥'
          }
        />
        <label htmlFor="ai-model-limit">每日调用上限</label>
        <input
          id="ai-model-limit"
          type="number"
          min={1}
          max={1000}
          value={dailyLimit}
          onChange={(e) => setDailyLimit(e.target.value)}
          disabled={busy || !token}
          required
        />
        <p className="input-help">
          按上海时区自然日统计，失败调用也计入额度。保存设置不会自动分析或发送聊天。
        </p>
        <label className="ai-model-consent">
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
            disabled={busy || !token}
          />
          我已核对模型服务地址与名称，确认保存。
        </label>
        <div className="form-actions">
          <button
            className="button button-primary"
            type="submit"
            disabled={!token || !config || busy || !confirmed}
          >
            {busy ? '正在处理…' : '保存模型配置'}
          </button>
          <button
            className="button button-quiet"
            type="button"
            disabled={!token || busy}
            onClick={() => setReload((n) => n + 1)}
          >
            重新读取设置
          </button>
        </div>
      </form>
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
