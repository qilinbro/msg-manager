import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ServiceError } from './api';
import {
  getModelConfig,
  getModelPresets,
  saveModelConfig,
  type ModelConfig,
  type ModelPresets,
} from './model-config-api';
import { Icon } from './Icon';
import { HelpDetails } from './HelpDetails';
import './model-config.css';

const PRESET_DESCRIPTION =
  '预设仅填地址，不测试连接或调用模型；请手动输入模型 ID。协议支持不等于服务已接通。';

export function AIModelPanel({
  token,
  onAuthFailure,
}: {
  token: string;
  onAuthFailure: () => void;
}) {
  const [config, setConfig] = useState<ModelConfig | null>(null);
  const [presets, setPresets] = useState<ModelPresets | null>(null);
  const [presetID, setPresetID] = useState('');
  const [presetsLoading, setPresetsLoading] = useState(false);
  const [presetsError, setPresetsError] = useState<string | null>(null);
  const [presetReload, setPresetReload] = useState(0);
  const [baseURL, setBaseURL] = useState('');
  const [model, setModel] = useState('');
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
    setPresetID('');
    setBaseURL('');
    setModel('');
    setDailyLimit('50');
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
        setBaseURL(next.base_url);
        setModel(next.configured ? next.model : '');
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

  // Presets are independent, read-only metadata. Failure must not lock an
  // existing configuration; arrival must never overwrite a user's inputs.
  useEffect(() => {
    const controller = new AbortController();
    setPresets(null);
    setPresetsError(null);
    setPresetsLoading(Boolean(token));
    if (token) {
      void getModelPresets(token, controller.signal)
        .then((next) => {
          if (!controller.signal.aborted) setPresets(next);
        })
        .catch((failure: unknown) => {
          if (controller.signal.aborted) return;
          if (
            failure instanceof ServiceError &&
            failure.code === 'UNAUTHORIZED'
          )
            authFailure.current();
          else setPresetsError('预设未能读取，可重试或手动填写。');
        })
        .finally(() => {
          if (!controller.signal.aborted) setPresetsLoading(false);
        });
    }
    return () => controller.abort();
  }, [token, presetReload]);

  const selectedPreset = presets?.items.find((item) => item.id === presetID);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (
      !token ||
      !config ||
      busy ||
      !confirmed ||
      !baseURL.trim() ||
      !model.trim()
    )
      return;
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
        <h2 id="ai-model-title" className="icon-label">
          <Icon name="model" /> AI 模型配置
        </h2>
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
      <HelpDetails
        label="模型配置说明"
        descriptions={[
          { id: 'ai-model-preset-help', text: PRESET_DESCRIPTION },
        ]}
      >
        <p className="input-help">
          OpenAI Chat Completions 兼容接口；Assistant 还需原生
          tools。密钥加密保存，不会返回页面。
        </p>
        <p className="input-help">{PRESET_DESCRIPTION}</p>
        <p className="input-help">
          按上海时区自然日统计，失败调用也计入额度。保存设置不会自动分析或发送聊天。
        </p>
      </HelpDetails>
      <form className="connection-form ai-model-form" onSubmit={submit}>
        <label htmlFor="ai-model-preset">配置预设</label>
        <div className="ai-preset-picker">
          <select
            id="ai-model-preset"
            value={presetID}
            disabled={!token || !config || busy || presetsLoading || !presets}
            aria-describedby="ai-model-preset-help"
            onChange={(event) => {
              if (!event.target.value) {
                setPresetID('');
                setKey('');
                setConfirmed(false);
                return;
              }
              const item = presets?.items.find(
                (entry) => entry.id === event.target.value,
              );
              if (!item || item.implementation_status === 'unsupported') return;
              setPresetID(item.id);
              setBaseURL(item.base_url);
              setModel('');
              setKey('');
              setConfirmed(false);
              setError(null);
              setNotice(null);
            }}
          >
            <option value="">
              {presetsLoading ? '正在读取预设…' : '选择预设，或手动填写'}
            </option>
            {presets?.items.map((item) => (
              <option
                key={item.id}
                value={item.id}
                disabled={item.implementation_status === 'unsupported'}
              >
                {item.display_name}
                {item.implementation_status === 'template_only'
                  ? ' · 仅模板'
                  : item.implementation_status === 'unsupported'
                    ? ' · 未实现'
                    : ''}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="button button-quiet button-icon"
            aria-label="重新读取预设"
            title="重新读取预设"
            disabled={!token || presetsLoading || busy}
            onClick={() => setPresetReload((n) => n + 1)}
          >
            <Icon name="refresh" />
          </button>
        </div>
        {presetsError ? (
          <p className="form-message error-message" role="alert">
            {presetsError}
          </p>
        ) : null}
        {selectedPreset ? (
          <div className="ai-preset-source">
            <span>
              {selectedPreset.implementation_status === 'protocol_supported'
                ? '协议已支持'
                : selectedPreset.implementation_status === 'template_only'
                  ? '仅配置模板，需自行核验兼容性'
                  : '尚未实现'}
            </span>
            <a
              className="icon-label"
              href={selectedPreset.source_url}
              target="_blank"
              rel="noopener noreferrer"
              referrerPolicy="no-referrer"
            >
              <Icon name="external" size={16} /> 官方配置来源
            </a>
          </div>
        ) : null}
        <label htmlFor="ai-model-url">服务地址</label>
        <input
          id="ai-model-url"
          type="url"
          value={baseURL}
          onChange={(e) => {
            setBaseURL(e.target.value);
            setPresetID('');
            setModel('');
            setKey('');
            setConfirmed(false);
          }}
          maxLength={2048}
          disabled={busy || !token}
          required
          spellCheck={false}
          autoComplete="off"
        />
        <label htmlFor="ai-model-name">模型 ID</label>
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
          API 密钥
          {config?.key_configured ? '（已保存，仅同一目标可留空保留）' : ''}
        </label>
        <input
          id="ai-model-key"
          type="password"
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            setConfirmed(false);
          }}
          maxLength={4096}
          disabled={busy || !token}
          autoComplete="off"
          spellCheck={false}
          placeholder={
            selectedPreset?.api_key_required === false
              ? '免密本机服务可留空'
              : config?.key_configured
                ? '更换公网地址须填写新密钥'
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
          onChange={(e) => {
            setDailyLimit(e.target.value);
            setConfirmed(false);
          }}
          disabled={busy || !token}
          required
        />
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
            disabled={
              !token ||
              !config ||
              busy ||
              !confirmed ||
              !baseURL.trim() ||
              !model.trim()
            }
          >
            <Icon name="save" />
            {busy ? '正在处理…' : '保存模型配置'}
          </button>
          <button
            className="button button-quiet button-icon"
            type="button"
            aria-label="重新读取设置"
            title="重新读取设置"
            disabled={!token || busy}
            onClick={() => setReload((n) => n + 1)}
          >
            <Icon name="refresh" />
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
          <HelpDetails label="保存记录">
            <p className="form-message success-message">{notice}</p>
          </HelpDetails>
        ) : null}
      </div>
    </section>
  );
}
