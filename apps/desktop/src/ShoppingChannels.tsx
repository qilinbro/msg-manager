const SHOPPING_SITES = [
  {
    name: '闲鱼',
    url: 'https://www.goofish.com/',
    description: '二手交易平台',
  },
  {
    name: '淘宝',
    url: 'https://www.taobao.com/',
    description: '购物与商家咨询',
  },
  { name: '京东', url: 'https://www.jd.com/', description: '购物与售后服务' },
  {
    name: '拼多多',
    url: 'https://www.yangkeduo.com/',
    description: '官网及官方 App 下载指引',
  },
];

export function ShoppingChannels() {
  return (
    <>
      <div className="page-heading">
        <h1>购物渠道</h1>
        <p className="page-description">
          从工作台打开平台官网，无需连接本地服务。
        </p>
      </div>
      <section aria-labelledby="shopping-sites-title">
        <div className="section-heading">
          <h2 id="shopping-sites-title">平台官网</h2>
          <span className="label label-neutral">仅网页入口</span>
        </div>
        <div className="connector-list">
          {SHOPPING_SITES.map(({ name, url, description }) => (
            <article className="connector-card" key={name}>
              <div className="connector-copy">
                <h3>{name}</h3>
                <p>{description}</p>
              </div>
              <a
                className="button button-secondary"
                href={url}
                target="_blank"
                rel="noopener noreferrer"
                aria-label={`打开${name}官网（新窗口）`}
              >
                打开官网 <span aria-hidden="true">↗</span>
              </a>
            </article>
          ))}
        </div>
        <p className="section-footnote">
          工作台尚未接入购物账号、聊天或订单。登录、查看和回复均在平台内操作。
        </p>
      </section>
    </>
  );
}
