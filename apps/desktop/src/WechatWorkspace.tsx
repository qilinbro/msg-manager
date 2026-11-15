import type { ComponentProps, ReactNode } from 'react';
import Inbox from './Inbox';
import { BrandLockup, RelaySymbol } from './brand/Brand';
import './wechat-workspace.css';

type Props = ComponentProps<typeof Inbox> & {
  onEmail: () => void;
  onInbox?: () => void;
  previewControls?: ReactNode;
};

function NavIcon({ mail = false }: { mail?: boolean }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {mail ? (
        <>
          <rect x="3" y="5" width="18" height="14" rx="3" />
          <path d="m3 7 9 6 9-6" />
        </>
      ) : (
        <>
          <path d="M20.5 11a8 8 0 0 1-8 8H8l-5 3 1.3-5.5A8 8 0 1 1 20.5 11Z" />
          <path d="M8 10h8M8 14h5" />
        </>
      )}
    </svg>
  );
}

export default function WechatWorkspace({
  onEmail,
  onInbox,
  previewControls,
  ...inbox
}: Props) {
  return (
    <div className="wechat-workspace">
      <a className="skip-link" href="#wechat-main">
        跳到微信工作台
      </a>
      <aside className="wechat-sidebar">
        <div className="wechat-brand">
          <BrandLockup />
        </div>
        <span className="wechat-nav-label">WORKSPACE</span>
        <nav aria-label="工作台导航">
          <button className="is-current" aria-current="page">
            <NavIcon />
            <span>微信工作台</span>
            <i />
          </button>
          <button onClick={onEmail}>
            <NavIcon mail />
            <span>邮箱交接</span>
            <span className="wechat-nav-arrow">↗</span>
          </button>
          {onInbox && (
            <button onClick={onInbox}>
              <span aria-hidden="true" className="wechat-nav-grid">
                ▦
              </span>
              <span>全部消息</span>
            </button>
          )}
        </nav>
        <div className="wechat-sidebar-story">
          <RelaySymbol />
          <p>
            离开片刻，
            <br />
            也能接着往前。
          </p>
          <span>WHILE YOU’RE AWAY.</span>
        </div>
        <div className="wechat-sidebar-bottom">
          <button onClick={inbox.onSettings}>
            <span aria-hidden="true">⚙</span>{' '}
            {inbox.preview ? '了解接入方式' : '连接与设置'}{' '}
            <span aria-hidden="true">↗</span>
          </button>
          <div>
            <span className="wechat-user-avatar">J</span>
            <p>
              {inbox.preview ? '产品体验空间' : '我的工作空间'}
              <small>
                {inbox.preview ? '仅合成数据' : '本地优先 · 由你掌控'}
              </small>
            </p>
          </div>
        </div>
      </aside>
      <main id="wechat-main" className="wechat-main">
        <header className="wechat-topbar">
          <div>
            <span>工作空间</span>
            <span aria-hidden="true">/</span>
            <strong>微信工作台</strong>
          </div>
          <span className="wechat-topbar-tag">WECHAT · READ ONLY</span>
          <button className="wechat-topbar-settings" onClick={inbox.onSettings}>
            {inbox.preview ? '接入说明' : '连接设置'} ↗
          </button>
        </header>
        {previewControls}
        <div className="wechat-content">
          <Inbox {...inbox} scope="weixin" />
        </div>
        <footer className="wechat-shell-footer">
          <span>收好消息，把决定留给你。</span>
          <span>接班 / AWAY DESK</span>
        </footer>
      </main>
    </div>
  );
}
