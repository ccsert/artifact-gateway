---
name: Artifact Gateway Console
description: 面向可信制品操作的精确、协议原生控制面。
---

# 设计系统：Artifact Gateway Console

[English](DESIGN.md) | [文档索引](docs/README.zh-CN.md)

## 概览

**创意北极星：“可验证的控制面”**

Console 应像一个所有声明都可被证明的系统控制台。视觉语言深色、精确、安静且面向运维：真实状态、不可变身份、策略边界和可恢复操作优先于装饰。默认深色主题是照明克制的工程控制台；浅色主题保持相同层级，不成为另一个产品。

这是一个 **Operate** 界面。信息密度、可扫描性、可预测的 Ant Design 行为、键盘访问和快速状态反馈高于视觉噱头。产品特征来自协议对象、等宽身份、源到分发的生命周期和克制的信号青色，而非渐变、大号营销字体或装饰动画。

关键特征：

- 证据优先：健康、风险、阻塞和必要操作先于装饰指标。
- 协议原生：坐标、摘要、Repository 类型和生命周期阶段必须可见、可复制。
- 信号色克制：青色只表示主操作、当前位置、焦点和可信链接；语义色保留状态含义。
- 表面有层次但不漂浮：边框和色调形成结构，阴影只表达真实 elevation。
- 动效清脆：短且可中断的反馈支持状态变化，不延迟高频导航。

## 颜色

调色板以中性色为主，只包含一组冷色信号色和明确的语义状态色。权威 token 值保存在英文页 frontmatter 与 Console 主题变量中。

运行时主题遵循 [ADR 0005](docs/adr/0005-console-semantic-theme-system.zh-CN.md)：受约束的 Theme Package 只解析一次，形成带类型的 Surface、Content、Border、Action、Link、Focus、Selection、Navigation、Status、Visualization、Identity 与 Effect 角色。Ant Design 组件 Token 与自定义 CSS 变量消费同一份投影；页面 CSS 必须命名语义角色，不能直接命名调色板颜色。

### 主色

- **Signal Cyan**（`primary-dark`、`primary-light`）：主操作、链接、焦点、选中导航、可信生命周期操作和当前状态。
- **Signal Wash**（`primary-soft-dark`、`primary-soft-light`）：选中操作或当前位置背景，必须从属于正文；信息表面使用 `info` 状态家族。

### 中性色

- **Night Ledger**（`dark-bg`）：深色页面画布及最深凹层。
- **Verified Surface**（`dark-surface`）：卡片、表格、控件和主要工作区。
- **Raised Instrument**（`dark-elevated`）：Modal、Drawer、Popover 等真实浮层。
- **Operational/Decisive/Supporting Text**：依次用于正文、标题与关键值、仍有行动含义的解释。
- **Muted Metadata**：时间戳和次要元数据，不能单独承载状态。
- **Daylight Canvas**：浅色页面与表面，层级与深色主题一致。

**信号稀缺规则：** 青色只用于动作、位置、焦点和可信系统链接，不作为普通装饰填充。

**中性文本选择规则：** 浏览器原生文本选择属于内容状态，背景从前景文字与容器中性色推导，不能使用主操作色。

**强调色预算规则：** 通用图标、中性 Badge、空状态、禁用控件、普通 Hover、卡片边框和大面积装饰光晕都不消耗主强调色；真正的产品/站点标志是唯一装饰性例外。

**状态必须有文字规则：** 成功、警告和错误颜色必须搭配文字、图标或两者，不能只靠颜色表达。

## 字体

标题与正文使用 Inter 及系统 UI fallback；协议坐标、摘要、principal、request ID 和命令使用 JetBrains Mono 及平台等宽 fallback。

### 层级

- **Headline**：页面身份，每个界面一个。
- **Title**：卡片、Drawer、Modal 和聚焦工作区。
- **Body**：控件、表格、说明和任务指引。
- **Label**：字段标签、表头、指标和短元数据；大写只用于紧凑分类。
- **Technical**：不可变标识、命令、摘要、principal、协议路径和 request ID。

**证据使用等宽规则：** 只有精确字符身份重要时才用等宽字体；名称、说明、状态和动作保持主字体。

**十二像素下限规则：** 10/11px 只用于非必要标记，运维指引和状态元数据使用 label 或 body。

## 布局

使用 4px 基础网格、1440px 最大内容宽度和由父容器控制的页面堆栈。相关表面间隔 16px；主要工作边界可通过 `ag-page-primary` 使用 24px。组件不得用相邻选择器表达页面顺序。

登录后桌面使用固定导航 rail，低于断点使用可访问 Drawer。多栏工作区使用 `minmax(0, 1fr)`、`min-width: 0` 和顶部对齐，防止长坐标或表格拉伸相邻面板。表格可在表面内滚动，页面不能产生横向溢出。

窄屏下过滤器和标题纵向堆叠、指标网格减少列数、粗指针触控目标至少 44px、生命周期改为可读纵向序列。移动端适配不得隐藏完成治理任务所需的控制项。

**父级拥有节奏规则：** 页面直接子项不带外部垂直 margin；`ag-page-stack` 与主要边界控制间距。

**几何也是证据规则：** 响应式验收使用 DOM rect、overflow 断言及桌面/移动截图，不能只检查 class 名称。

## 高度与深度

默认分层但平坦。背景、表面、边框和 overlay 色调承担主要层级。卡片使用轻微结构阴影；Popover 和 Modal 使用更强 raised 阴影。Blur 只用于能澄清层级的 shell chrome 或半透明表面。

- **Structural Card**：`--ag-shadow-card` 轻度分隔工作表面。
- **Raised Instrument**：`--ag-shadow-pop` 用于 Modal、Drawer、Dropdown、Popover。
- **Primary Action Signal**：紧凑的 Ant Design 主按钮阴影只强化主动作。

**未抬升则保持平坦：** Hover 可调整边框或背景；只有组件真正高于上下文时才使用大阴影。

## 形状

控件使用 8px 圆角，工作表面 10px，overlay 12px，Tooltip 6px。完整胶囊和圆形只用于紧凑状态点、Avatar 和真正的圆形控件。嵌套控件不应比容器更柔软、更装饰化。

## 组件

### 按钮

桌面默认高度 34px，粗指针至少 44px。每个决策面只有一个主操作。Hover、Focus、Active 使用明确 token 和可见焦点；破坏性操作使用 danger 语义，不形成第二主色。

### 卡片与容器

使用语义 surface token、10px 圆角和安静边界。常规内边距 16px；密集列表可使用 12px 垂直节奏，但保持可读触控目标。只有真正 elevation 才增强阴影。

### 输入控件

使用 Ant Design outlined 控件与共享背景、文字、边框、圆角 token。焦点使用 Signal Cyan 并提供 `focus-visible` fallback。错误和禁用必须附带明确说明，禁用对比度不能用于普通帮助文字。

### 导航

按运行时、治理和管理任务分组。选中项组合克制青色背景、可读青色文字和窄位置指示。桌面折叠应立即稳定；移动端 Drawer 有明确关闭标签和 44px 目标。

### 指标条与生命周期

指标条是紧凑摘要，不能替代页面主任务；仅在可行动时使用状态色。源、扫描、隔离判定、晋级和分发构成标志性产品对象，必须在无颜色时仍可理解，并链接真实证据。

### 反馈状态

初次请求的 loading、error、empty、stale warning 和 content 互斥。刷新失败保留旧数据并把错误置于其上。加载和动作结果向辅助技术播报；可重试读取提供 Retry。

### 动效

- 按压反馈约 120ms，普通状态约 180ms，使用既定强 ease-out。
- 高频路由和键盘动作不增加装饰性入场。
- Overlay 和反馈可用短 opacity/transform，禁止从 `scale(0)` 进入。
- 重复动态 UI 使用可中断 transition，只动画 `transform`、`opacity` 和明确选择的颜色或阴影。
- `prefers-reduced-motion` 移除位移，但保留有用的透明度或颜色反馈。

## 应做与不应做

### 应做

- 保留真实 API 数据、协议深链接、复制命令、不可变身份和格式专用操作。
- 优先使用 Ant Design 组件与 token 表达控件、表格、overlay、消息和语义状态。
- 明确 public、authenticated、loading、error、empty、partial 和 disabled 状态。
- 使用 `ag-page-stack`、16px 相关间距和 24px 主要边界。
- 按变更风险验证桌面、移动、明暗主题、键盘和 reduced-motion。
- 动画保持清脆、有目的且可中断。

### 不应做

- 不把 Console 变成通用卡片表格后台；必须表达协议、身份、信任和生命周期语义。
- 不伪造容量、健康、扫描、漏洞、发布或可用性数据。
- 不使用 `transition-all`、布局动画或高频管理页面的重复 stagger 入场。
- 不用低对比 muted text 承载必需指引，也不只靠颜色表达状态。
- 不通过子项 margin 或成对 sibling selector 增加页面级间距。
- Ant Design 与 CSS 已覆盖时，不引入第二套组件、Toast 或动效库。
- 不为视觉变更削弱协议、OpenAPI、深链接、浏览器、升级或恢复门禁。
