# APT Proxy 与 Group

[English](apt-proxy.md) · [文档索引](README.zh-CN.md)

Artifact Gateway 通过以下地址提供 Debian 仓库：

```text
https://gateway.example.com/apt/<repository>/
```

APT 目前仍按“仅协议格式”对外声明，支持 Proxy 仓库和有序 Group。管理员可以显式创建
H2 Hosted 预览并发布原子可见的签名快照，但在 H3 提供生产签名密钥托管、轮换、恢复
与运维证据前，Hosted 及其生命周期能力不对外声明。里程碑和验收门禁见
[APT Hosted 路线图](apt-hosted-roadmap.zh-CN.md)。

## 配置软件源

创建 APT Proxy，将上游设置为 `https://deb.debian.org/debian` 等地址，并允许上游可能
重定向到的所有主机。然后使用 Gateway URL 添加软件源：

```text
deb https://gateway.example.com/apt/debian bookworm main
```

使用 Group 时，把 `debian` 替换为 Group 名称。Group 按配置顺序尝试成员，只有活动的
APT Proxy 成员参与解析。

## 缓存路径

Proxy 接受 `dists/` 与 `pool/` 下的规范 Debian 路径，例如：

```text
dists/bookworm/InRelease
dists/bookworm/Release
dists/bookworm/Release.gpg
dists/bookworm/main/binary-amd64/Packages.xz
pool/main/h/hello/hello_2.10-3_amd64.deb
```

元数据、签名和软件包按原字节缓存，Gateway 不改写签名元数据。包含空段、`.`、`..`、
转义父路径、反斜杠、Query 或 Fragment 的路径会在访问上游前被拒绝。

缓存资产支持 `GET`、`HEAD`、带 `ETag` 或 `Last-Modified` 的条件请求，以及每个请求
一个 HTTP Range，因此续传软件包时无需把完整对象载入内存。可变 `dists/` 元数据会
条件重验；不可变 `pool/` 和 `dists/*/by-hash/` 对象从可信缓存直接返回。暂时无法重验
可变元数据时，Gateway 返回最近缓存副本并附加 `Warning: 110`。

## 认证

APT 读取支持 Bearer、HTTP Basic 和匿名访问。匿名读取要求全局匿名策略与仓库或 Group
策略同时开启。仓库授权使用 APT 路径作为资源前缀，例如 `dists/bookworm` 或 `pool/main`。
