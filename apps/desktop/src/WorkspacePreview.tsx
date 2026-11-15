import { useCallback, useState } from 'react';
import WechatWorkspace from './WechatWorkspace';
import AwayDesk from './AwayDesk';
import { createWechatDemo } from './wechat-demo';
import type { WechatScenario } from './wechat-demo';
import type { AIDraftBuffer } from './ai-api';

function WechatDemo({
  scenario,
  controls,
}: {
  scenario: WechatScenario;
  controls: React.ReactNode;
}) {
  const [clients] = useState(() => createWechatDemo(scenario));
  const [drafts] = useState(() => new Map<string, AIDraftBuffer>());
  const [help, setHelp] = useState(false);
  const onAuthFailure = useCallback(() => {}, []);
  return (
    <WechatWorkspace
      token="synthetic-preview"
      aiDrafts={drafts}
      client={clients.inbox}
      wechatClient={clients.wechat}
      aiClient={clients.ai}
      preview
      onAuthFailure={onAuthFailure}
      onEmail={() => {
        location.search = '?demo=away';
      }}
      onSettings={() => setHelp((value) => !value)}
      previewControls={
        <>
          {controls}
          {help && (
            <section className="wechat-preview-help" aria-label="接入说明">
              <button
                className="email-text-button"
                onClick={() => setHelp(false)}
              >
                收起说明 ×
              </button>
              <h2>从本机微信，接上真实工作流。</h2>
              <p>
                在 Windows 微信 4.x
                上完成本机授权，选择要关注的私聊，开启后台读取。关闭工作台后，电脑与后台服务仍需运行。
              </p>
              <p>
                AI
                整理需单独配置模型，并逐次确认内容范围。这个网页使用合成数据，不读取你的微信，也不调用模型。
              </p>
              <a
                href="https://github.com/qilinbro/msg-manager/blob/main/docs/wechat-db-setup.md"
                target="_blank"
                rel="noreferrer"
              >
                查看微信接入说明 ↗
              </a>
            </section>
          )}
        </>
      }
    />
  );
}

export default function WorkspacePreview({
  initialPage,
}: {
  initialPage: 'wechat' | 'away';
}) {
  const [scenario, setScenario] = useState<WechatScenario>('ready');
  if (initialPage === 'away')
    return (
      <AwayDesk
        demo
        onBack={() => {
          location.search = '?demo=wechat';
        }}
      />
    );
  const controls = (
    <div className="wechat-demo-bar">
      <span>
        <i /> 演示数据{' '}
        <span className="wechat-demo-description">
          · 消息与分析均为合成，不连接微信或模型
        </span>
      </span>
      <label>
        体验状态
        <select
          value={scenario}
          onChange={(event) =>
            setScenario(event.target.value as WechatScenario)
          }
        >
          <option value="ready">已保存 · 已有手动分析</option>
          <option value="unconfigured">尚未接入微信</option>
          <option value="read-error">后台读取失败</option>
          <option value="no-model">模型尚未配置</option>
        </select>
      </label>
    </div>
  );
  return <WechatDemo key={scenario} scenario={scenario} controls={controls} />;
}
