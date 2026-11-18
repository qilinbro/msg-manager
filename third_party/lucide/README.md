# Lucide 离线图标子集

用于 FEAT-T08 / CHG-T08-009 的功能图标。没有使用品牌 Logo、远程字体、CDN、照片或装饰图片。

## 固定来源

- 上游：[lucide-icons/lucide](<https://github.com/lucide-icons/lucide/tree/149eba0aea48178a5ab1efe2c701de1da94f4be8>)，版本 [1.75.0](<https://github.com/lucide-icons/lucide/releases/tag/1.75.0>)。
- 精确提交：`149eba0aea48178a5ab1efe2c701de1da94f4be8`。
- [manifest.json](<manifest.json>) 记录每个 SVG 的固定 raw GitHub URL、SHA-256、50 个语义别名与对应 49 个上游文件；[icons](<icons/>) 保留下载的原始字节。
- [LICENSE](<LICENSE>) 保留该提交的**完整 ISC 许可及 Feather 衍生部分的 MIT 许可/归属**，不是只保留 Lucide 的 ISC 标题。许可文件 SHA-256：`b495047bd93a9b06913511076f504daba17d5bbeb3e0650f3bb53a4220329c57`。
- 各别名可共享上游几何，如 `contacts` / `group`；上游改名按 manifest 精确映射，不拼凑未知文件。没有添加 npm SDK 依赖。

## 本地渲染与分发

[Icon.tsx](<../../apps/desktop/src/Icon.tsx>) 是静态共享组件，仅从本地白名单几何构造 React SVG。保留 24×24 viewBox、`strokeWidth=2`、圆角和 `currentColor`；默认渲染 18px，可显式改尺寸。没有运行时抓取、XML 注入、`dangerouslySetInnerHTML`、外部引用、脚本或事件属性。

图标只补充按钮/导航本身的可访问名称：SVG 为 `aria-hidden="true"` / `focusable="false"`；主操作保留短标签，熟悉的次操作可仅显示图标，但原生按钮仍有准确 `aria-label`、同名 `title`、可见焦点及 44px 目标。说明/风险/授权不靠图标独自表达。

离线 Web/Tauri 分发同时包含：

- [完整许可副本](<../../apps/desktop/public/licenses/lucide-LICENSE.txt>) → 构建后 `licenses/lucide-LICENSE.txt`；
- [固定来源清单副本](<../../apps/desktop/public/licenses/lucide-provenance.json>) → 构建后 `licenses/lucide-provenance.json`。

[.gitattributes](<../../.gitattributes>) 对这些源字节与分发副本禁用换行转换，避免 Windows checkout 改变上游 SHA-256。源文件不执行、不直接注入 DOM。

## 验证与更新

仓库根目录执行：

```powershell
node scripts/test-ui-icons.mjs
```

[回归脚本](<../../scripts/test-ui-icons.mjs>) 完全离线校验 49 个原文件/完整许可的精确哈希、50 个语义别名**真实共享组件的几何**、白名单元素属性、装饰 SVG 语义、分发副本一致及实际功能控件的使用；[共享测试加载器](<../../scripts/frontend-icon-test-loader.mjs>) 不伪造 Icon。该检查已纳入 [完整检查](<../../scripts/check.ps1>)。

升级时须人工核对新的官方固定提交与**完整**许可，逐项检查并同步原文件、哈希清单、组件几何和离线副本，再执行回归及桌面/窄屏交互验证；不能只改版本号或放宽白名单让测试通过。本文及工程测试不代替指定人员审查、辅助技术或原生 Tauri 验收。
