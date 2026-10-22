export const nexusBatchZhCN = {
  "nexusBatch.sourceView": "连接与映射",
  "nexusBatch.batchView": "批次与验收",
  "nexusBatch.title": "Nexus 迁移批次",
  "nexusBatch.background": "批次由后台持续执行",
  "nexusBatch.backgroundHint":
    "关闭页面或停止轮询不会取消任务。暂停会停止领取新条目，已在处理的条目可能完成；取消会保留已发布的目标内容。",
  "nexusBatch.new": "创建迁移批次",
  "nexusBatch.useMappings": "使用 {count} 个 Hosted 映射创建批次",
  "nexusBatch.rescan": "重新扫描并追平",
  "nexusBatch.rescanHint":
    "创建新批次并完整枚举源；相同身份仍会重新校验，目标冲突不会覆盖，源删除仅报告。",
  "nexusBatch.start": "应用映射并创建批次",
  "nexusBatch.close": "关闭",
  "nexusBatch.empty": "尚无迁移批次",
  "nexusBatch.emptyHint":
    "保存并预检源，绑定真实 Hosted 目标后，使用已核实的存储容量证据创建批次。",
  "nexusBatch.select": "选择批次查看进度与报告",
  "nexusBatch.refresh": "刷新批次",
  "nexusBatch.more": "加载更多批次",
  "nexusBatch.readLatest": "读取最新批次",
  "nexusBatch.source": "已保存的迁移源",
  "nexusBatch.target": "已有 Hosted 目标仓库",
  "nexusBatch.targetFor": "{name} 的目标仓库",
  "nexusBatch.targetHint":
    "只绑定真实、活动且格式一致的 Hosted 仓库。目标不存在时请先在仓库管理中明确创建，不会自动继承 Nexus 策略或权限。",
  "nexusBatch.manageRepositories": "打开仓库管理",
  "nexusBatch.moreRepositories": "加载更多目标仓库",
  "nexusBatch.moreSources": "加载更多源",
  "nexusBatch.mappingSummary": "{source} → {target}",
  "nexusBatch.explicitMapping":
    "映射将作为批次的不可变输入保存。复制选中的 Maven 发布版本、Raw、OCI、Conan 2 和 npm Hosted 内容。npm 按包完整提交；已有标签或版本元数据不同会报告冲突。Proxy/Group 仍需单独配置。",
  "nexusBatch.capabilities": "执行能力与容量证据",
  "nexusBatch.disabled": "当前部署未启用迁移写入",
  "nexusBatch.disabledHint":
    "需配置真实对象存储池并启用迁移 worker。连接、库存观察和已有报告仍可读取；未知容量不能启动任务。",
  "nexusBatch.binding": "目标 {target} · 存储池 {pool}",
  "nexusBatch.capacityHint":
    "填写对这个真实存储池核实的外部容量证据，全部字节字段必须已知（明确为零可填 0）。下载峰值至少覆盖最大源资产的完整暂存大小。这不是 Gateway 对对象存储的测量或容量预留，不要用日志/临时目录空间代替。",
  "nexusBatch.observedAt": "容量观察时间（含时区）",
  "nexusBatch.validUntil": "证据有效截止时间（含时区）",
  "nexusBatch.timeHint":
    "使用带时区的 ISO 时间。观察时间不能在未来，有效窗口最多一小时；恢复/重试必须提供严格更新的观察。",
  "nexusBatch.freeBytes": "存储池可用字节",
  "nexusBatch.downloadBytes": "下载峰值字节",
  "nexusBatch.uploadBytes": "上传峰值字节",
  "nexusBatch.backupBytes": "备份峰值字节",
  "nexusBatch.restoreBytes": "恢复峰值字节",
  "nexusBatch.headroomBytes": "保留余量字节",
  "nexusBatch.required": "请填写已核实的值；未知不能启动或恢复。",
  "nexusBatch.invalidNumber":
    "填写非负整数字节数，且不能超过浏览器可精确表达的整数范围。",
  "nexusBatch.invalidTime": "填写含时区的有效 ISO 时间。",
  "nexusBatch.expired":
    "容量证据过期、来自未来或有效窗口超过一小时。请重新核实。",
  "nexusBatch.newerEvidence": "恢复/重试需要比上次证据严格更新的观察时间。",
  "nexusBatch.previousEvidence": "上次观察：{time}。本次不会预填旧证据。",
  "nexusBatch.pause": "暂停批次",
  "nexusBatch.resume": "恢复批次",
  "nexusBatch.retry": "重试可重试失败项",
  "nexusBatch.retryHint":
    "扫描未封存时会在原批次完整重扫；封存后只重新排入未达到 {attempts} 次上限的失败/源缺失条目。冲突和未支持项不会被覆盖。",
  "nexusBatch.cancel": "取消批次",
  "nexusBatch.cancelTitle": "取消这个迁移批次？",
  "nexusBatch.cancelHint":
    "停止新工作并阻止取消栅栏之后的发布。已提交的身份和源内容都会保留；这不是删除或回退操作。",
  "nexusBatch.scanning": "扫描与源校验中",
  "nexusBatch.ready": "等待后台领取",
  "nexusBatch.running": "正在迁移",
  "nexusBatch.pausing": "等待当前条目排空",
  "nexusBatch.paused": "已暂停",
  "nexusBatch.blocked": "需要处理阻塞",
  "nexusBatch.completed": "后台批次已完成",
  "nexusBatch.failed": "后台批次失败",
  "nexusBatch.cancelled": "已取消",
  "nexusBatch.version": "批次版本 {version}",
  "nexusBatch.updated": "服务端最后更新：{time}",
  "nexusBatch.scanned": "已发现源元数据条目",
  "nexusBatch.planned": "已封存计划条目",
  "nexusBatch.total": "支持内容的唯一源字节总量",
  "nexusBatch.unknown": "未知；完整清单尚未封存",
  "nexusBatch.sourceVerification": "扫描阶段源校验读取字节",
  "nexusBatch.sourceVerificationHint":
    "为补齐源清单缺失的完整摘要而额外读取；扫描未完成或失败时也记账，不是已复制字节。",
  "nexusBatch.transferred": "复制阶段实际读取源字节",
  "nexusBatch.transferredHint":
    "包含复制重试和重新验证已有身份的源读取，可能大于唯一内容总量。",
  "nexusBatch.verified": "已完整读回校验的唯一目标字节",
  "nexusBatch.visible": "目标可见身份",
  "nexusBatch.pending": "待处理",
  "nexusBatch.uploaded": "已上传，尚未完成发布",
  "nexusBatch.entryVerified": "目标已校验，尚未完成发布",
  "nexusBatch.committed": "已发布",
  "nexusBatch.skipped": "完整重验后跳过",
  "nexusBatch.conflict": "目标冲突，未覆盖",
  "nexusBatch.entryFailed": "失败",
  "nexusBatch.unsupported": "未支持",
  "nexusBatch.sourceMissing": "源读取时缺失",
  "nexusBatch.sourceDeleted": "后续扫描报告源已删除",
  "nexusBatch.partial":
    "存在冲突、失败、未支持或源变化；不能据此宣称全部追平。",
  "nexusBatch.entries": "批次条目与完整摘要",
  "nexusBatch.entry": "条目",
  "nexusBatch.state": "服务端状态",
  "nexusBatch.attempts": "尝试 {count} / {max}",
  "nexusBatch.asset": "资产",
  "nexusBatch.noEntries": "尚无可执行条目",
  "nexusBatch.noEntriesHint":
    "完整扫描和清单封存之前，条目列表为空不代表源为空。",
  "nexusBatch.loaded": "已加载 {loaded} / {total} 个计划条目",
  "nexusBatch.moreEntries": "加载下一页条目",
  "nexusBatch.allEntries": "读取完整库存",
  "nexusBatch.sourcePath": "源路径",
  "nexusBatch.targetPath": "目标路径",
  "nexusBatch.sha256": "完整 SHA-256",
  "nexusBatch.assetVerified": "目标已完整读回校验",
  "nexusBatch.assetUploaded": "目标字节已上传",
  "nexusBatch.assetUnverified": "目标字节尚未验证",
  "nexusBatch.report": "迁移验收观察报告",
  "nexusBatch.export": "导出当前观察报告",
  "nexusBatch.reportHint":
    "导出只包含服务端批次、已加载库存和当前目标端点，不包含源连接凭据。缺少的验收证据保持未验证。",
  "nexusBatch.manifest": "封存清单 SHA-256",
  "nexusBatch.inventoryIncomplete":
    "库存尚未完整加载或扫描未封存；报告不能作为完整库存验收。",
  "nexusBatch.coldClient": "本批次 target-only 冷客户端验收：未验证",
  "nexusBatch.catchup": "冻结写入后的最后追平点：未验证",
  "nexusBatch.cutover": "切换与回退数据边界",
  "nexusBatch.cutoverHint":
    "先明确冻结源写入，再运行最终全量重扫并核验冷客户端。目标产生新写入后，回退到源前必须核对并回补新增数据；只切回端点会丢失这些新写入。",
  "nexusBatch.residual": "残余 Nexus 依赖与消费端调整",
  "nexusBatch.residualHint":
    "检查 Proxy/Group 上游和成员、未支持格式/SNAPSHOT、权限和凭据、CI 的 Maven/Gradle/Raw 配置及源写入方。更新为原生目标端点；本页不修改 CI/DNS/Ingress，也不证明 Nexus 可以停用。",
  "nexusBatch.endpoints": "当前 Console 地址下的原生目标端点",
  "nexusBatch.endpointsHint":
    "基于真实目标仓库生成；修改消费端前核对实际对外地址和认证配置。端点可用不等于已取得本批次冷客户端证据。",
  "nexusBatch.targetUnavailable": "目标详情不可用；端点与配置尚未确认。",
  "nexusBatch.requestFailed":
    "批次请求未完成。读取最新服务端状态后重试；未回显任何源凭据或上游正文。",
  "nexusBatch.unknownOutcome":
    "提交结果尚未确认。先刷新批次核实；保持相同输入重试会复用同一请求，避免重复创建或控制。",
  "nexusBatch.versionConflict":
    "批次已被后台或其他管理员更新，已尝试重新读取服务端状态。请重新选择操作并核实新版本；不会自动重发写请求。",
  "nexusBatch.idempotencyConflict":
    "此请求标识已用于不同内容。请读取服务端事实后重新操作。",
  "nexusBatch.capacityUnknown":
    "容量信息未知，已阻止目标写入；请核实所有容量字段。",
  "nexusBatch.capacityInsufficient":
    "真实目标容量或仓库配额不足，已阻止写入。释放容量或核实新的有效证据后再恢复。",
  "nexusBatch.capacityBinding":
    "容量证据与当前真实目标/存储池不一致，请重新读取执行能力并核实。",
  "nexusBatch.sourceChanged":
    "源连接版本已变化。原批次不能继续，需重新预检并显式创建新批次。",
  "nexusBatch.invalidState": "当前服务端状态不允许这项操作，请读取最新批次。",
  "nexusBatch.sourceFailure":
    "源读取、摘要或成员集合发生变化；需核实源后再处理，未覆盖目标冲突。",
  "nexusBatch.scanFailure": "最近的持久扫描失败定位",
  "nexusBatch.scanStageLabel": "失败阶段",
  "nexusBatch.scanCode": "有限诊断码",
  "nexusBatch.scanScope": "扫描所选仓库范围",
  "nexusBatch.scanOccurred": "失败观察时间",
  "nexusBatch.scanMetadata": "源元数据发现",
  "nexusBatch.scanManifest": "清单规划",
  "nexusBatch.scanOpen": "打开源资产校验流",
  "nexusBatch.scanRead": "读取源资产校验流",
  "nexusBatch.scanDigest": "核对源资产摘要",
  "nexusBatch.scanVersion": "重新验证源连接版本",
  "nexusBatch.scanNPM": "核对 npm 包元数据与完整版本集合",
  "nexusBatch.npmClosure":
    "npm 元数据与版本集合不完整或不一致。请核实所选源仓库的读取权限和全部版本后重新扫描。",
  "nexusBatch.npmIntegrity":
    "npm 包字节与源 shasum 或 SRI 不一致，未发布此包。请核实源内容后重新扫描。",
  "nexusBatch.npmIdentity":
    "npm 组件、包元数据和包内身份不一致，未发布此包。请核实源包名与版本。",
  "nexusBatch.scanGraph": "验证 OCI 完整依赖图",
  "nexusBatch.scanInterrupted":
    "扫描中断且未封存；可凭新容量证据在原批次完整重扫。",
  "nexusBatch.pageFailure":
    "库存分页不完整或重复；请刷新后重新读取，不会导出为完整库存。",
  "nexusBatch.targetConflict": "目标已有不同身份或字节，保持原目标并报告冲突。",
  "nexusBatch.unsupportedReason":
    "条目不满足原生格式的完整迁移要求，保留为未支持项，需单独处理。",
} as const;
export type NexusBatchMessageKey = keyof typeof nexusBatchZhCN;
