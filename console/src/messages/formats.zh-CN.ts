export const formatsZhCN = {
  "formats.ociDiscovery": "OCI 目录与关联制品",
  "formats.ociCatalogCoverage":
    "原生 Catalog 按权限列出镜像名称与来源，并注明上游目录是否完整。下方缓存列表不代表完整库存。",
  "formats.ociReferrerAuthority":
    "关联制品跟随镜像摘要的来源。Group 成员调整后仍保持原来源；来源配置变化时需重新核查。",
  "formats.ociDiscoveryTrust":
    "发现 SBOM 或签名只确认内容关联，尚未验证签名者身份与信任策略。",
  "formats.ociReferrersApiRequired":
    "上游须支持 Referrers API；不支持时会明确报错，不使用标签回退。",
  "formats.name": "名称",
  "formats.filterByImageNamePrefix": "按镜像名前缀过滤…",
  "formats.artifact": "制品",
  "formats.searchGAVCoordinates": "搜索 GAV 坐标…",
  "formats.theProxyCacheAppearsAfterAMavenClient":
    "通过 Maven 客户端拉取依赖后会显示代理缓存",
  "formats.package": "包",
  "formats.filterByPackageNamePrefix": "按包名前缀过滤…",
  "formats.upstreamMetadataAndCacheStatusAppearAfterNpm":
    "通过 npm install 拉取包后会显示上游元数据与缓存状态",
  "formats.project": "项目",
  "formats.filterByProjectNamePrefix": "按项目名前缀过滤…",
  "formats.upstreamFilesAndCacheStatusAppearAfterPip":
    "通过 pip install 拉取项目后会显示上游文件与缓存状态",
  "formats.module": "模块",
  "formats.filterByModulePathPrefix": "按模块路径前缀过滤…",
  "formats.upstreamVersionsAndCachedAssetsAppearAfterGo":
    "通过 go mod download 拉取模块后会显示上游版本与缓存资产",
  "formats.searchCrateNames": "搜索 crate 名称…",
  "formats.filterByReference": "按引用名过滤…",
  "formats.filePath": "文件路径",
  "formats.searchPaths": "搜索路径…",
  "formats.aPTPath": "APT 路径",
  "formats.filterByDistsOrPoolPath": "按 dists/ 或 pool/ 路径过滤…",
  "formats.theProxyCacheAppearsAfterAptUpdateApt":
    "通过 apt update、apt install 或直接请求 APT 路径后会显示代理缓存",
  "formats.uploadDebFilesThroughPublicationSessionsAndPublish":
    "通过发布会话上传 .deb，并提交签名快照后会显示当前可见版本",
  "formats.crate": "Crate",
  "formats.filterByImageNameKeyword": "按镜像名关键词过滤…",
  "formats.filterByPackageNameKeyword": "按包名关键词过滤…",
  "formats.filterByProjectNameKeyword": "按项目名关键词过滤…",
  "formats.filterByModulePathKeyword": "按模块路径关键词过滤…",
};

export type FormatsMessageKey = keyof typeof formatsZhCN;
