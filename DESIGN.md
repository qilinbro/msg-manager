---
name: 消息管理工作台
description: 白色简约，以消息、操作和真实状态为主，不添加多余修饰。
colors:
  ink: "#202124"
  secondary: "#60656c"
  accent: "#292c31"
  accent-hover: "#141619"
  border: "#e3e5e8"
  border-strong: "#868c94"
  paper: "#ffffff"
  surface: "#f7f8fa"
  surface-hover: "#eef0f2"
  nav-active: "#eaecef"
  danger: "#a12b2b"
  danger-bg: "#fff5f4"
  warning: "#805d1b"
  warning-bg: "#fff9ed"
typography:
  page-title:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "clamp(1.375rem, 1.8vw, 1.625rem)"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "0"
  section-title:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    lineHeight: 1.65
  body:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.65
  action-label:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.5
  input:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.7
  status-label:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  control: "4px"
  label: "3px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "20px"
  xxl: "24px"
  page: "32px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.paper}"
    typography: "{typography.action-label}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-secondary:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.action-label}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.secondary}"
    typography: "{typography.action-label}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
  text-input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.input}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
  navigation:
    backgroundColor: "transparent"
    textColor: "{colors.secondary}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
  navigation-selected:
    backgroundColor: "{colors.nav-active}"
    textColor: "{colors.ink}"
  surface-panel:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "20px"
  status-label:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.status-label}"
    rounded: "{rounded.label}"
    padding: "3px 7px"
  confirmation-panel:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "18px"
  warning:
    backgroundColor: "{colors.warning-bg}"
    textColor: "{colors.warning}"
    rounded: "{rounded.control}"
    padding: "12px 14px"
  code-panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "14px"
---

# Design System: 消息管理工作台

## Overview

**Creative North Star: "白色简约，不添加多余修饰"**

这是一个管理消息与操作的工作台，不是展示品牌故事的营销页面。以白色背景、近白分区、深灰文字和细线组织已有信息；密度由内容与任务决定，不为了填满空间增加装饰、统计或口号。

Agent的状态与权限延续相同视觉语言：模型说明、工具回执、固定确认和未知结果都有直接文字，不用聊天气泡或颜色制造成功感。红/棕反馈只承担错误与风险提醒，不作为品牌点缀。字体沿用中文友好的系统无衬线栈，不下载外部字体。

**Key Characteristics:**
- 白底与细线优先，内容层级清楚，装饰克制。
- 同意、操作、回执和未知状态彼此可分辨。
- 桌面保持紧凑工作区，窄屏按内容顺序堆叠。

依据：[基础样式](<apps/desktop/src/styles.css>)、[助手样式](<apps/desktop/src/assistant.css>)、[收件箱样式](<apps/desktop/src/inbox.css>)及2026-11-17的[合成浏览器记录](<docs/acceptance/T13-native-assistant-validation.md>)。这是实现后的记录，不表示真人验收或当前运行实例已更新；更多产品边界见[产品事实](<PRODUCT.md>)。

## Colors

### Primary

- **深灰操作色**（`accent` / `accent-hover`）：用于主要按钮、复选框和明确交互，不铺成大面积品牌色块。
- **风险反馈**（`danger` / `danger-bg`、`warning` / `warning-bg`）：分别区分错误与待核实/费用等提醒，始终配合可读文字。

### Neutral

- **正文与说明**（`ink` / `secondary`）：先用文字层级区分信息，不用高饱和色替代标题和说明。
- **页面与分区**（`paper` / `surface`）：白色页面与极浅内容面；`surface-hover`、`nav-active`表达可操作与当前位置。
- **分隔与控件边缘**（`border` / `border-strong`）：细分隔线组织区域，较明确的边缘标识输入框和需要核对的确认预览。

**The Functional Color Rule.** 颜色只帮助识别交互或实际风险；成功与否必须由文字和业务回执说明，不能仅靠颜色暗示。

## Typography

**Display Font / Body Font:** 共用frontmatter中的系统无衬线栈，优先本机中文字体；没有额外展示字体。

**Character:** 字体服务于连续阅读、字段核对和操作，不使用夸张标题、全大写标签或装饰性字距。页面标题、分节标题、正文、按钮标签和输入按对应token分工；辅助说明较小，长编号与地址允许换行。代码和完整参数使用本机等宽字体栈。

### Hierarchy

- **Page title**（`page-title`）：页面主任务，不做巨幅hero。
- **Section title**（`section-title`）：设置组、确认预览等局部入口。
- **Body**（`body`）：业务内容与说明。
- **Action label**（`action-label`）：紧凑按钮文字，不代替长篇说明。
- **Input**（`input`）：助手与模型配置字段使用可读的常规字号，避免窄屏输入缩放。

## Layout

桌面外壳为窄侧栏（188px）与弹性内容区；顶栏最小高度（56px），正文容器最大宽度（1440px），桌面外边距以page间距为基准。收件箱内部按筛选、会话、详情分栏，并保留独立滚动，不把所有内容挤到一张卡片。

在窄屏外壳切换为单列与可换行导航，顶栏更紧凑；正文水平留白先收为（20px），最窄断点收为（16px）。助手确认字段、恢复输入与操作按钮按空间换行/堆叠；代码、目标地址及编号必须保持可读。窄屏收件箱先筛选、再会话、后详情，保留业务顺序。断点的实际值记录于[设计扩展](<.impeccable/design.json>)。

## Elevation & Depth

不使用装饰阴影。依靠页面留白、细边框和近白背景表达分区；固定确认预览以较明确边缘区别于普通回复。聚焦使用深色轮廓（2px）及外偏移（3px），不是发光效果。

**The Flat Surface Rule.** 保持平面信息层级；不要用悬浮、玻璃、渐变或夸张阴影制造无业务意义的深度。

## Shapes

控件与面板使用frontmatter中的细小圆角和常规细边框，不做胶囊化的大按钮或大圆角卡片。图标只用于导航与业务识别，平台标记保持克制，不恢复植物装饰或新增装饰插画。

## Components

### Buttons

主按钮深灰底、白字；次按钮白底、明确边缘；轻按钮透明底、次级文字。hover只改变相应背景/文字；focus-visible沿用全局可见轮廓。禁用降低不透明度并显示不可操作光标，不伪装为可点。按钮最小高度（38px），粗指针提高为（44px）；没有弹跳、抬升或缩放动画。

同一区域的同一动作只保留一个可见入口。启停共用一个位置，按实际状态显示下一步。编辑已启用的计划时同时提供更新与暂停。状态提示写清结果，详细说明与低频操作按需展开。

### Inputs / Fields

白底、较明确细边框、细小圆角。助手输入最小高度（44px），多行输入最小高度（112px）；禁用时使用近白背景。每个字段有真实标签，placeholder不能代替标签；错误在文字中说明。

消息与联系人共用搜索控件，会话类型用原生下拉菜单。打开会话后使用同一个输入框切换搜索范围，各范围保留自己的关键词。微信和邮箱的来源操作放入「消息来源」，微信本次读取的正文条件按需展开。

### Navigation

桌面紧凑纵向文字导航；当前页使用浅灰底与较重正文，hover用另一层浅灰。窄屏横向换行，隐藏非必要图标但保留文字；键盘焦点始终可见。

个人档案排在一级导航首位，启动后直接进入。桌面左侧选联系人，右侧展示档案。窄屏选中后展示档案详情，返回时恢复联系人列表。平台、关系分类和会话状态可在「筛选」中调整。

### Cards / Containers

普通区域白底、细线分组，无阴影。不要把每一段文字再套卡片；连续业务内容可用行分隔。收件箱允许内部滚动，因此“无页面级横溢”不代表内部组件均无滚动条。

### Fixed Action Confirmation

先展示操作、目标和实际影响，完整参数、接口、确认编号、目标版本和有效期可展开核对。参数按字段展示，长值换行。说明后放独立且默认未勾选的确认行，提供「确认」和「拒绝」。

### Receipts and Unknown Results

助手文字与工具回执分开，回执可展开核对。unknown使用轻量风险面与“待核实”文字，不显示成功；只读恢复按钮与新任务提交分开。长内容遵守正常文档流，视口截图不是整页内容。

**The Honest State Rule.** 未确认、等待、失败和未知必须保持可辨；不要用动画、装饰徽标或模型语气掩盖真实执行状态。

## Do's and Don'ts

### Do:
- **Do** 保持白底、深灰文字与细分隔，优先把内容和操作放清楚。
- **Do** 保留可见键盘焦点、真实标签、必要风险文字与未默认勾选的同意。
- **Do** 让窄屏按业务顺序堆叠，长编号与参数可读，内部滚动范围明确。

### Don't:
- **Don't** 添加渐变、玻璃、光晕、植物装饰、装饰插画或夸张阴影。
- **Don't** 添加假统计、装饰徽标、营销hero或为填空而造的卡片。
- **Don't** 把模型回复、HTTP 202、本地停止或unknown渲染为业务成功。
