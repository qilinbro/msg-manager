# T08 标签页连接保持与恢复

- 日期：2026-11-14（Asia/Shanghai）；CHG-T08-004 / FEAT-T08。
- 负责人joey；指定非作者审查angjustinl；kirinbao组织验收。
- 状态：工程检查通过，子项待指定审查/真实页面验收；实际完成日期—，整项完成统计不变。

## 问题与范围

用户报告换标签页后要重新连接。原App令牌只在React内存，页面重建/刷新丢失；全局身份失败回调需要隔离旧请求，重复连接检查也不应清空当前有效令牌和AI草稿。纯应用内导航不应当重置顶层连接状态，具体浏览器操作的真人复现另行登记。

修复当前标签页连接恢复：只保存已验证、本机服务地址绑定的访问令牌至sessionStorage；恢复/返回页面仅GET系统状态重新确认，不重放业务操作。断网/超时/503保留已验证令牌；401或主动清除删除本会话记录。旧请求/旧401不能覆盖新连接；存储异常降级内存并如实提示。

仅处理本产品访问令牌，不使用localStorage、URL或日志，不保存微信数据库密钥，不读取真实浏览器存储/剪贴板/聊天。sessionStorage按来源和标签页隔离，新独立标签页不是共享会话；依据见[MDN](https://developer.mozilla.org/en-US/docs/Web/API/Window/sessionStorage)。

## 检查记录

- `scripts/test-service-session.mjs`11组检查通过，实际执行storage helper、hook、api.ts和App，使用合成sessionStorage/fetch/React hooks：只保存验证成功令牌、地址/version/格式、存储被拒、刷新恢复、导航保持、focus/visibility并发只GET系统、503/断网/超时保留、401/清除、迟到请求、同令牌重新授权、卸载回调、StrictMode和AI草稿保留。
- 测试真实发现clear后重新授权同token时旧metadata/401回调仍能误覆盖，已用认证代次修复；卸载实例回调也加入mounted守卫并复测。普通focus/同token检查不换认证代次，避免重启各页面请求。
- 既有`test-wechat-monitor.mjs`、`test-inbox-import.mjs`通过；前端格式、TypeScript、生产构建通过（JS332.00kB/CSS40.53kB）。新回归已纳入check.ps1前端分支，PowerShell语法检查通过。
- App机械检测结果[]；保持现有布局，只调整连接保存提示。无真实浏览器/原生Tauri操作、无真实令牌/存储/聊天访问，不将hooks替身检查写成真人界面验收。

## 待真实验收

kirinbao需实际连接后切换应用页、切换浏览器标签、刷新/恢复同标签页，检查自动校验和草稿；临时断网恢复、令牌换新和手动清除行为需确认。指定审查与真人浏览器/原生Tauri验收未进行，不以合成组件检查替代。
