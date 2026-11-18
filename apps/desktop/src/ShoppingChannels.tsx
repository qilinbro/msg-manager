import type { ShoppingPlatform } from './inbox-api';

const SHOPPING_SITES: {
  id: ShoppingPlatform;
  name: string;
  url: string;
  description: string;
}[] = [
  {
    id: 'xianyu',
    name: '闲鱼',
    url: 'https://www.goofish.com/',
    description: '二手交易平台',
  },
  {
    id: 'taobao',
    name: '淘宝',
    url: 'https://www.taobao.com/',
    description: '购物与商家咨询',
  },
  {
    id: 'jd',
    name: '京东',
    url: 'https://www.jd.com/',
    description: '购物与售后服务',
  },
  {
    id: 'pinduoduo',
    name: '拼多多',
    url: 'https://www.yangkeduo.com/',
    description: '官网及官方 App 下载指引',
  },
];

export function ShoppingChannels({
  onImport,
}: {
  onImport: (platform: ShoppingPlatform) => void;
}) {
  return (
    <>
      <div className="page-heading">
        <h1>购物渠道</h1>
        <p className="page-description">
          将本人有权使用的私聊文本导入消息中心，或打开平台官网。
        </p>
      </div>
      <section aria-labelledby="shopping-sites-title">
        <div className="section-heading">
          <h2 id="shopping-sites-title">私聊记录与平台官网</h2>
          <span className="label label-neutral">手动导入</span>
        </div>
        <div className="connector-list">
          {SHOPPING_SITES.map(({ id, name, url, description }) => (
            <article className="connector-card" key={name}>
              <div className="connector-copy">
                <h3>{name}</h3>
                <p>{description}</p>
              </div>
              <div className="inbox-inline-actions">
                <button
                  type="button"
                  className="button button-primary"
                  onClick={() => onImport(id)}
                >
                  导入{name}私聊
                </button>
                <a
                  className="button button-secondary"
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  aria-label={`打开${name}官网（新窗口）`}
                >
                  打开官网 <span aria-hidden="true">↗</span>
                </a>
              </div>
            </article>
          ))}
        </div>
        <p className="section-footnote">
          导入需要连接本地服务并选择本人授权的统一 JSON
          文件；平台原始导出需先转换格式。支持私聊文本，不支持群聊、媒体或订单数据。导入只保存历史记录，不连接购物账号或开启自动收件、发送。官网入口无需连接本地服务，登录和回复仍在平台内完成。
        </p>
      </section>
    </>
  );
}
