# 总览统计口径

`GET /api/v2/overview-statistics` 一次返回当前可管理仓库的请求量、拒绝数、逻辑对象数和占用字节，并提供与仓库明细相加一致的合计。平台管理员看到所有未删除仓库；仓库管理员仅看到自己可管理的仓库。无请求的仓库也返回零值行。

请求量来自 `resolver_audit_log`，只计 `repository` 非空且 `format` 不是 `management` 的记录。所有 `outcome` 都计入请求量；`access_denied`、`proxy_denied` 和旧数据中的 `denied` 另计为拒绝数，拒绝数是请求量的子集。1 天、7 天、30 天是以响应的 `generatedAt` 为上界的滚动 24 小时、7 天、30 天窗口，下界包含在内。已删除仓库和找不到对应仓库的历史审计不计入当前总览。

普通 Console 和管理 API 的 GET 请求不写入这张审计表，因此这里不是全站 HTTP 请求计数。若启用审计保留并将 `keepDays` 设为小于 30，30 天窗口会缺少已清理的记录；默认审计保留为关闭状态。对象数复用仓库容量统计的 `objectCount`，代表按仓库归属的存储对象引用，不等同于制品版本数。Proxy 仓库的占用沿用 read-through cache 容量口径。
