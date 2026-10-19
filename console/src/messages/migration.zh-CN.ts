export const migrationZhCN = {
  "migration.tab": "迁移源",
  "migration.sources": "Nexus 迁移源",
  "migration.scope": "连接源并预览迁移范围",
  "migration.scopeHint":
    "这里只读取源配置和资产元数据。发现与映射预览不会复制字节或创建目标仓库。",
  "migration.newSource": "添加迁移源",
  "migration.editSource": "编辑迁移源",
  "migration.sourceName": "源名称",
  "migration.baseUrl": "Nexus HTTPS 地址",
  "migration.baseUrlHint":
    "填写实例地址及可选上下文路径；不要包含凭据、查询或片段。私网访问需要管理员在服务端明确放行。",
  "migration.credentials": "源凭据",
  "migration.noCredentials": "不使用凭据",
  "migration.keepCredentials": "保留当前凭据",
  "migration.replaceCredentials": "设置新凭据",
  "migration.clearCredentials": "清除当前凭据",
  "migration.credentialsConfigured": "已配置凭据",
  "migration.credentialsAbsent": "未配置凭据",
  "migration.credentialHint":
    "凭据不会回读或保存在浏览器。每次提交后都会清空输入；失败后请重新输入。更改源地址时须替换或清除已有凭据。",
  "migration.username": "源用户名",
  "migration.password": "源密码",
  "migration.save": "保存迁移源",
  "migration.create": "创建迁移源",
  "migration.cancel": "取消",
  "migration.edit": "编辑源",
  "migration.delete": "删除源",
  "migration.deleteTitle": "删除迁移源？",
  "migration.deleteHint":
    "删除 {name} 的连接配置和加密凭据。源 Nexus 的配置和内容不受影响。",
  "migration.refresh": "刷新源",
  "migration.reloadLatest": "读取最新版本",
  "migration.moreSources": "加载更多迁移源",
  "migration.noSources": "尚未配置迁移源",
  "migration.noSourcesHint":
    "添加 Nexus 3 源后，可预检凭据可见的仓库并预览原生目标映射。",
  "migration.selectSource": "选择迁移源查看预检与映射",
  "migration.selectSourceHint":
    "连接配置由服务端保存；预检结果是一次只读观察，不代表已迁移或可停用 Nexus。",
  "migration.version": "连接版本 {version}",
  "migration.preflight": "运行预检",
  "migration.preflightTitle": "仓库范围与显式映射",
  "migration.preflightEmpty": "尚未运行预检",
  "migration.preflightEmptyHint":
    "读取此凭据可见的仓库、格式和限制，再选择需要发现或预览映射的仓库。",
  "migration.visibleScope":
    "仅限当前凭据可见范围，不证明全局库存完整。快照使用连接版本 {version}。",
  "migration.noRepositories": "此凭据可见范围内没有仓库",
  "migration.sourceRepository": "源仓库",
  "migration.targetName": "原生目标名称",
  "migration.targetNameHint":
    "小写字母、数字和连字符；显式填写，不继承旧 URL。最多选择 10 个源仓库。",
  "migration.chooseRepository": "选择 {name}",
  "migration.targetFor": "{name} 的目标名称",
  "migration.members": "源成员顺序",
  "migration.groupHint": "同时选择成员并填写其目标名。预览保留此成员顺序。",
  "migration.supported": "可发现 Hosted 内容",
  "migration.previewOnly": "仅配置预览",
  "migration.unsupported": "未支持",
  "migration.discover": "发现选中 Hosted 内容（{count}）",
  "migration.preview": "预览显式映射",
  "migration.discoveryTitle": "源资产发现结果",
  "migration.discoverySummary":
    "{assets} 个源资产，元数据大小合计 {bytes}；{unsupported} 个明确未支持的路径。未下载或校验任何字节。",
  "migration.path": "源路径",
  "migration.size": "源元数据大小",
  "migration.reason": "处理说明",
  "migration.noAssets": "选定范围未返回受支持资产",
  "migration.unsupportedAssets": "明确未支持的资产",
  "migration.mappingTitle": "映射预览结果",
  "migration.mappingHint":
    "预览仅校验名称、格式、冲突和有序成员，不应用映射。权限、凭据与标为手动的配置需另行配置。",
  "migration.mappingValid": "所选映射通过当前校验；尚未创建目标或迁移内容。",
  "migration.mappingInvalid": "映射存在未支持项或冲突，请调整后重新预览。",
  "migration.ready": "通过当前校验",
  "migration.manual": "需要手动配置",
  "migration.invalid": "映射无效",
  "migration.targetMembers": "目标成员顺序：{members}",
  "migration.observation":
    "只读观察：{requests} 次请求，{pages} 页，响应 {bytes}。",
  "migration.previousResult": "保留上次成功结果；本次请求失败，结果未更新。",
  "migration.failed":
    "请求未完成。检查网络和服务状态后重试；源凭据不会显示在错误中。",
  "migration.denied": "没有读取或管理此迁移源的权限。请使用平台管理员身份。",
  "migration.notFound": "迁移源已不存在，请刷新源列表。",
  "migration.versionConflict":
    "源连接已被其他操作更新。先读取最新版本，再重新提交；凭据需要重新输入。",
  "migration.invalidSource":
    "连接配置无效。检查 HTTPS 地址、名称与凭据，并重新输入需要设置的凭据。",
  "migration.replaceRequired": "更改源地址时，请明确替换或清除已配置的凭据。",
  "migration.sourceChanged": "源连接版本已变化。刷新源连接后重新预检。",
  "migration.upstreamDenied":
    "源访问被拒绝。检查源凭据、只读权限和服务端允许访问的地址。",
  "migration.sourceUnavailable":
    "源读取失败。检查 Nexus 可达性、TLS 与只读 API 权限后重试。",
  "migration.budget":
    "发现超出安全读取预算或分页无效。缩小仓库范围后重试；没有返回部分成功结果。",
  "migration.keyUnavailable": "服务端凭据加密配置不可用，请联系平台管理员。",
  "migration.lookupUnavailable":
    "无法检查目标仓库冲突，请恢复目标元数据服务后重新预览。",
  "migration.reasonUnsupported":
    "此格式、仓库类型或源配置尚未支持；查看源配置后决定后续处理。",
  "migration.reasonSnapshot": "Maven SNAPSHOT 内容不在本次迁移范围。",
  "migration.reasonHostedInventory": "可读取源配置和资产元数据；尚未迁移内容。",
  "migration.reasonReleasePolicy":
    "仅发现 Maven RELEASE/MIXED 的发布版本；SNAPSHOT 或未知策略需单独处理。",
  "migration.reasonMetadata":
    "共享或无法归属单一发布版本的 Maven 元数据需单独处理。",
  "migration.reasonManual": "需显式配置原生策略和凭据。",
  "migration.reasonFieldManual": "此源字段需要手动配置。",
  "migration.reasonFieldExpressible": "可用原生配置表达。",
  "migration.reasonFieldUnsupported": "此源字段尚不支持。",
  "migration.reasonConflict": "目标名称已存在，且格式、类型或状态冲突。",
  "migration.reasonName": "目标名称无效或重复。",
  "migration.reasonFormat": "目标格式必须与源仓库一致。",
  "migration.reasonType": "目标类型必须与源仓库一致。",
  "migration.reasonMembers":
    "Group 必须显式映射全部成员，并保留源成员顺序及格式。",
  "migration.reasonExistingGroup": "已存在的 Group 需要显式配置。",
  "migration.reasonMembersOnly": "只有 Group 可设置成员。",
} as const;

export type MigrationMessageKey = keyof typeof migrationZhCN;
