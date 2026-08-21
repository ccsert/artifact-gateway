# 安全策略

[English](SECURITY.md) | [文档索引](docs/README.zh-CN.md)

Artifact Gateway 会处理认证凭证、仓库权限、不可信包元数据和可执行制品字节。在修复方案和披露计划确定之前，安全报告必须私下处理。

## 报告漏洞

不要为疑似漏洞创建公开 Issue。当代码托管平台提供私密漏洞报告通道后，请使用该通道；在此之前，通过核心团队使用的私有项目渠道联系维护者。

报告尽量包含：

- 受影响的 revision 或部署版本；
- 受影响的协议、API 路由或组件；
- 前置条件和可复现步骤；
- 安全影响和预期被突破的信任边界；
- 已移除凭证和制品内容的日志或最小 PoC。

不要测试不属于你的系统或数据。报告不得包含仍有效的 Token、密码、私有包内容或个人数据。

## 响应流程

维护者将验证报告、识别受影响版本、准备测试和修复，并在操作员拥有可行升级路径后协调披露。项目尚未启动公开支持计划，因此暂不承诺公开的确认或修复时限。

## 当前安全基线

- 生产流量必须在 Gateway 或可信反向代理终止 TLS；仓库中的明文 HTTP 示例只用于本地开发。
- 替换 `.env.example` 和 Compose 示例中的所有占位 secret。
- 将管理员/解析 Token、API Key、数据库及对象存储凭证、`GATEWAY_EGRESS_PROXY_KEY` 保存在 Secret Manager。
- CI 与第三方应用使用 Service Account；Grant 绑定稳定 principal，轮换一次性凭证时保留重叠窗口，事件响应时禁用账号以拒绝其全部凭证。参见[Service Account 运维](docs/service-account-operations.zh-CN.md)。
- 数据库迁移作为独立部署任务运行，普通 Gateway 节点不得持有 schema owner 凭证。
- 限制 Proxy 上游主机和出站网络；每仓库自定义代理密码需要 `GATEWAY_EGRESS_PROXY_KEY`。
- 上传包与元数据一律视为不可信输入。Gateway 不内建恶意软件检测；只有显式启用参考扫描器或契约兼容的外部扫描器后，才提供漏洞、许可证和 SBOM 分析。
- 运维和管理路由仅暴露给预期网络与身份。纯 Worker/调度节点只暴露存活、就绪和指标接口。
- Service Account、凭证、权限、匿名访问、保留、删除、晋级和复制变更后检查审计记录。

发布候选前运行依赖审计：

```sh
make dependency-audit
```
