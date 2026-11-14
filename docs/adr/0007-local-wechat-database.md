# ADR 0007：微信4.x本地数据库只读适配

- 日期：2026-11-14；关联 FEAT-T06 / CHG-T06-003。
- 状态：已实施首轮独立子项，待指定人员审查与真实账号验收。

用户希望免手工导出并开始读取个人微信数据库。WeFlow 当前主分支只提供原生组件接口；已有 Rust 分支的声明存在许可差异。此次不打包第三方原生组件或复制其读取实现，使用已有 SQLCipher 驱动与自有 Go 查询适配。

## 边界

后台显式配置 `WECHAT_DB_DIR/ACCOUNT_ID/KEY/LIMIT`，前端不接收或回传路径/密钥，也不能指定另一个账号。配置可通过隐藏输入脚本保存到本机 DPAPI 保护文件。没有自动扫描磁盘、进程内存采集或密钥获取代码。

读取器校验本机固定磁盘、无 reparse 路径、`db_storage` 及账号父目录；账号以操作员配置为信任边界，Name2Id 参与者映射仅辅助检查，不能证明数据库拥有者或平台在线。

每库读取文件 salt，使用解码后的32字节密码 PBKDF2-HMAC-SHA512 / 256000 派生密钥，再使用 SQLCipher4 的 raw key + salt 形式打开。原始配置密钥不能直接作为 SQLCipher raw encryption key。复用现有 SQLCipher4 驱动，不改驱动或关闭认证；源库独立注册只读连接，绝不调用产品 Store.Open 或迁移。

只读事务读取 `session/session.db`、`contact/contact.db` 及最多64个 `message/message_<n>.db` 分片。`Msg_<MD5(peer)>` 为内部计算的表名，用户输入只作为绑定参数。发送方通过 Name2Id 与配置账号比较，未知方向拒绝保存。文本支持受限 zstd 解压，正文不裁剪昵称前缀；媒体和过长文本统计跳过，解码失败明确失败。

各分片取最近窗口后全局排序，server_id 使用精确十进制字符串构造限域身份并跨片去重；同标识内容冲突拒绝整批，缺 server_id 时使用分片/local_id。本机重建、换源及多格式记录不按正文猜测合并。当前读取窗口不是全历史或可靠增量游标。

读前后比较所有源 DB/WAL 的文件身份、长度和时间；发生变化拒绝结果。SQLite 只读事务负责单库 WAL 一致性，SHM 协调元数据仍由 SQLite 管理；没有跨文件全库快照承诺，不修改 DB/WAL 聊天内容、不生成明文库。

## 入库和界面

新 API 为缓存状态、手动会话列表和单私聊读取；同一连接只运行一个读操作。30秒请求超时和取消传入原子入库；取消等待存储锁后不得继续落库。服务端固定 weixin、账号和 wechatdb 来源，不开放发送。

产品库v4只新增来源元数据列，不重建v3身份图、AI外键或FTS。对外来源使用兼容列合并，保留旧消息seq/资源ID/本地状态和AI数据。模型调用仍遵守已有用户预览确认，不因读取而自动外传。

## 验证与后续

必要检查包括真实SQLCipher合成库、WAL内容不变、错密钥与写入拒绝、多分片/双向/压缩/大整数、来源隔离、迁移回滚、API鉴权/CORS/取消、前端类型与构建。详情见 [验收记录](../acceptance/T06-wechat-db-validation.md)。

真实路径、密钥与账号尚未具备，密钥初始化、微信版本兼容、浏览器交互、joey 后端审查、angjustinl 前端审查及 kirinbao 验收另行开展。T06 的持续收件、水位、任务和真实100条消息等整体验收不因本子项实现而通过。

## 只读技术参考

- [SQLCipher 官方接口](https://www.zetetic.net/sqlcipher/sqlcipher-api/)：raw key+salt、加密参数、只读使用边界。
- [SQLite WAL](https://sqlite.org/wal.html)：WAL/SHM 与只读事务的前提。
- [WeFlow第三方组件契约](https://github.com/hicccc77/WeFlow/blob/91adcfdbb24e8e147d8332440b8c6ac899ad2abf/docs/third-party-components.md)：自定义组件ABI不等同通用SQLCipher库。
- [WeFlow Rust 读取层](https://github.com/334456777/WeFlow/tree/9352818c39f02006590133d293264b0f7d093665/crates/weflow-native/src)：仅核对数据格式与表结构，不引入该仓依赖或代码。
- [Flandora读取层](https://github.com/OMSociety/Flandora/blob/572447dadafbd064d622f42fa2d2013871201bb4/flandora/wx_store.py)：交叉核对4.x字段，不安装运行上游。
