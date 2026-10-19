# Raw HTTP Range

[English](raw-http-ranges.md) · [协议矩阵](protocol-compatibility.zh-CN.md)

`GET /raw/<repository>/<path>` 为 Hosted、Proxy 和 Group 提供单段 Range 与有界
`multipart/byteranges`。返回内容前仍检查仓库及成员权限、隔离读取策略和缓存完整性。
这是原生 HTTP 读取契约的扩展，没有新增管理操作、Console 动作、schema 或认证类型。

```sh
curl --header 'Range: bytes=0-1023,1048576-1049599,-1024' \
  --header 'If-Range: "sha256-<当前 SHA-256>"' \
  --user '<身份>:<凭据>' \
  https://gateway.example/raw/downloads/releases/app.bin
```

请求只允许一个 `Range` 字段，最多 8192 字节，合并前至多 16 个区间。支持后缀和开放结尾
范围；重复、重叠与相邻区间按偏移升序合并。如果仍有可满足的范围，则忽略合法但越界的
区间。语法错误、整数溢出、零后缀、多行字段、全部越界或超过限制返回 `416`，附
`Content-Range: bytes */<size>`。

合并后有两个以上区间时返回 `206 multipart/byteranges`，有效载荷总和至多 64 MiB，
封装开销小于 32 KiB。响应包含精确的总 `Content-Length`，每段各有 `Content-Type` 和
`Content-Range`，顶层没有 `Content-Range`。合并后只剩一个区间时沿用单段响应，不受
多段有效载荷限制。

`ETag` 和 `Digest` 始终描述完整的不可变对象。`If-Range` 只有精确匹配当前强 ETag 才
返回分段；弱标签、日期或其他 validator 得到完整 `200`。`HEAD` 忽略 Range，仅返回
完整对象元数据，不打开对象字节流。匹配的 `If-None-Match` 仍在处理 Range 前返回 `304`。

各区间依次调用已有的有界对象读取接口。开始时固定 object key、size 与 digest，读取中
修改路径不会替换后续分段。流中断或区间不可用时立即停止，不补结束边界，不伪造达到声明
长度的完整响应。客户端须将其视为不完整结果，并取得新 validator 后重试。开始返回字节
后不会换成新的路径引用或其他 Group 成员。

冷 Proxy GET 先获取、校验完整的规范对象，再在本地应用 Range，不向上游转发客户端
Range 或 validator，也不拼接上游部分对象。原有临时落盘大小、配额、admission 和取消
限制继续生效。已验证的热缓存可以在上游停止时提供相同分段。冷 Proxy 的 HEAD 仍只向
选定的上游查询元数据。

验证包含 `internal/protocol/raw/multipart_test.go`、Gateway 公开 Raw 边界测试、
PostgreSQL/RustFS 跨实例集成，以及 `make native-raw-e2e`。原生门禁记录 curl 可执行
文件的精确版本和摘要，校验 32 MiB fixture 的完整内容与各分段，同时覆盖条件写和恢复
上传完成；请求仅指向本轮自有的本机目标。这些是隔离客户端和存储证据，部署验收单独跟踪。
