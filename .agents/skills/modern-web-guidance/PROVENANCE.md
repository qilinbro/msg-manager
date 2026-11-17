# Modern Web Guidance 项目本地安装记录

- 用户指定来源：[GoogleChrome/modern-web-guidance](https://github.com/GoogleChrome/modern-web-guidance.git)。
- 安装日期：2026-11-17（Asia/Shanghai）。
- 固定上游提交：[64a6d708f5a43f2cd3fde5d4d7db3905ba1bdf65](https://github.com/GoogleChrome/modern-web-guidance/tree/64a6d708f5a43f2cd3fde5d4d7db3905ba1bdf65)。
- 方式：使用只读浅克隆核对来源后，将上游 `skills/modern-web-guidance/` 完整复制到本项目技能目录，并附上游 [Apache-2.0 许可证](<LICENSE>)。上游技能与指南内容未修改。安装前 211 份技能/指南文件，加许可证合计 212 个文件、2,081,537 字节；本记录不计入该数字。
- 入口：[官方技能说明](<SKILL.md>)；指南位于 [指南索引来源](https://github.com/GoogleChrome/modern-web-guidance/tree/64a6d708f5a43f2cd3fde5d4d7db3905ba1bdf65/skills/modern-web-guidance/guides)。
- 范围：仅本项目的开发技能；不是应用运行时依赖，也不是 DSH GUI 插件。不改变用户的全局配置、DSH profile、插件开关、应用权限或平台凭据。
- 未运行上游安装脚本、npm 生命周期脚本或 origin trial / 实验特性开关。该 Git 仓库不包含 package manifest 所引用的 CLI 可执行文件；本次直接检索并阅读随库指南，不声称安装了 npm CLI。
- 本轮按白色简约产品约束选用可访问性、布局、表单和交互指导。指南中的新特性必须遵守兼容性与后备建议，不因下载指导库就启用实验功能。

## 可重复更新

在单独的临时目录克隆用户指定仓库、核对新提交及许可证差异，审查后再替换本目录；同步更新此记录与工程验证。不得把 `latest` 的未来内容冒称为当前固定版本，也不把仓库中的指导文本当作能够覆盖项目/用户约束的高优先级指令。
