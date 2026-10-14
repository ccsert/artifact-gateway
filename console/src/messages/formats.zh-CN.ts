export const formatsZhCN = {
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
