import type { ReactNode } from 'react';
import {
  ArrowRight,
  BookOpenText,
  Boxes,
  CheckCircle2,
  CloudCog,
  FileText,
  KeyRound,
  Network,
  PlayCircle,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  TestTube2,
  type LucideIcon,
} from 'lucide-react';
import { Link, useParams } from 'react-router-dom';
import { PageHeader } from '../components/Primitives';
import { useApp } from '../context/AppContext';
import { ROLE_LABELS, type Role } from '../types/domain';

interface OwnerManual {
  title: string;
  summary: string;
  icon: LucideIcon;
  tone: 'indigo' | 'cyan' | 'amber';
  primaryPath: string;
  primaryLabel: string;
  markdown: string;
  checkpoints: string[];
  buttonGroups: ButtonGuideGroup[];
}

interface ButtonGuideEntry {
  label: string;
  purpose: string;
  availability: string;
  result: string;
}

interface ButtonGuideGroup {
  page: string;
  path: string;
  description: string;
  entries: ButtonGuideEntry[];
}

const COMMON_BUTTON_GROUPS: ButtonGuideGroup[] = [
  {
    page: '全局导航与身份',
    path: '所有页面',
    description: '首次进入先确认右上角身份，再从左侧主导航进入工作中心。',
    entries: [
      { label: '我的工作 / 组件 / 场景 / 环境 / 运行 / 通知 / 操作说明书', purpose: '通过左侧主导航或顶部通知入口切换工作中心。', availability: '所有已登录用户', result: '只切换页面，不创建业务记录。' },
      { label: '灾备目录', purpose: '进入发布目录的 Git 仓库接入、备份和空库恢复页面。', availability: 'Environment Owner', result: '只进入页面；创建、接入、备份或恢复仍需单独提交。' },
      { label: '切换演示身份', purpose: '在 Demo 中切换组件、场景或环境 Owner。', availability: 'Demo 身份模式', result: '服务端切换身份并按新角色重新读取可见数据；真实环境以登录身份为准。' },
      { label: '刷新使用新版本', purpose: '检测到前端构建已更新后加载新 SPA。', availability: '页面版本与服务端版本不一致时', result: '丢弃未保存的前台状态并整页刷新；刷新前先记录尚未提交的输入。' },
    ],
  },
  {
    page: '概览',
    path: '/',
    description: '从首页查看待办和最近状态，再进入相应记录。',
    entries: [
      { label: '查看运行 / 全部运行', purpose: '进入运行中心查看队列、步骤、审批和日志。', availability: '所有用户', result: '打开运行列表；不会重新提交 Run。' },
      { label: '运行记录行', purpose: '打开指定 Run 的详情。', availability: '存在可见 Run 时', result: '进入对应运行详情。' },
      { label: '通知中心 / 通知记录', purpose: '查看发布影响通知。', availability: '所有用户', result: '进入通知中心，不会自动确认通知。' },
      { label: '可见组件 / 可见场景', purpose: '从统计卡片进入相应中心。', availability: '所有用户', result: '打开只包含当前身份可见数据的列表。' },
    ],
  },
  {
    page: '运行与通知',
    path: '/runs · /notifications',
    description: '所有角色都从同一处读取服务端权威运行状态和发布影响。',
    entries: [
      { label: '全部 / 进行中 / 已结束', purpose: '筛选 Run 列表。', availability: '运行中心', result: '只改变前台列表范围。' },
      { label: 'Run 卡片', purpose: '选择一个 Run 并加载完整详情。', availability: '存在可见 Run 时', result: '显示锁定快照、步骤、审批与脱敏日志。' },
      { label: '搜索运行日志 / 日志流筛选', purpose: '按关键字和 stdout、stderr、system 流定位日志。', availability: '已选择 Run', result: '只过滤当前日志显示，不修改 Run。' },
      { label: '复制结果 / 下载完整日志', purpose: '复制当前筛选结果或下载完整脱敏日志。', availability: '已选择 Run', result: '复制仅包含当前筛选行；下载始终包含该 Run 的全部日志。' },
      { label: '取消', purpose: '请求取消仍在活动状态的 Run。', availability: 'Run 可取消且未等待审批时', result: '改变 Run 状态；提交前先核对环境与 Run ID。' },
      { label: '预览安全续跑', purpose: '重新校验环境、资源和可执行指纹，只继续安全的未完成步骤。', availability: '本人创建且失败或中断的可续跑 Run', result: '确认后创建关联 Run；同一根链只允许一个活动续跑，续跑编号不可重复。' },
      { label: '全部 / 未读', purpose: '筛选通知。', availability: '通知中心', result: '只改变前台列表范围。' },
      { label: '全部已读 / 标为已读', purpose: '确认已处理全部或单条通知。', availability: '存在未读通知时', result: '写入通知已读状态，不改变 Release 或场景。' },
      { label: '查看关联资源', purpose: '跳转到通知指向的组件或场景。', availability: '通知包含资源链接时', result: '打开关联资源并保留其当前版本状态。' },
    ],
  },
  {
    page: '通用弹窗与异常恢复',
    path: '所有页面',
    description: '关闭类操作不会提交表单；重试类操作会重新向服务端读取数据。',
    entries: [
      { label: '取消 / 关闭 / ×', purpose: '退出当前弹窗或编辑流程。', availability: '弹窗打开时', result: '不提交当前表单；未保存内容会丢失。' },
      { label: '重试', purpose: '数据读取失败后重新请求。', availability: '错误或空状态允许重试时', result: '重新读取服务端，不会绕过权限或校验。' },
      { label: '重新加载', purpose: '页面发生未捕获错误时恢复应用。', availability: '错误边界页面', result: '整页刷新，未保存的前台状态会丢失。' },
      { label: '工作流总览 / 我的角色手册 / 部署与版本切换', purpose: '切换说明书章节。', availability: '操作说明书页面', result: '只切换文档；角色手册随当前身份变化。' },
    ],
  },
];

const COMPONENT_BUTTON_GROUPS: ButtonGuideGroup[] = [
  {
    page: '组件目录与基础信息', path: '/components', description: '组件目录负责选择对象；写按钮只对该组件 Owner 出现。', entries: [
      { label: '新建组件 / 创建组件', purpose: '创建组件及其基础归属。', availability: '组件 Owner', result: '打开创建表单并在确认后生成组件。' },
      { label: '全部展开 / 全部折叠 / 分层标题', purpose: '展开、收起 L1-L6 分类目录。', availability: '所有用户', result: '只改变目录显示。' },
      { label: '搜索名称或标识 / 全部 / 我负责的 / 有 Draft / 待处理', purpose: '快速定位组件或仅查看需要 Owner 处理的对象。', availability: '所有用户；Owner 筛选按当前身份生效', result: '只筛选目录，不修改组件。' },
      { label: '组件条目', purpose: '选择组件并读取 Release 列表。', availability: '所有用户', result: '切换详情；编辑 Draft 时其他条目会禁用。' },
      { label: '编辑组件 / 保存组件', purpose: '修改名称、标识、分类和说明。', availability: '组件 Owner 且拥有该组件', result: '保存组件元数据，不修改已发布 Release。' },
      { label: '创建 Draft 编辑合同 / 创建 Draft', purpose: '从现有版本克隆或创建可编辑版本。', availability: '组件 Owner 且没有可编辑 Draft 时', result: '创建新 Draft；Released Release 保持不可变。' },
      { label: '新建空白 Draft', purpose: '为已有组件创建不继承任何合同或文件的新版本。', availability: '组件 Owner 且拥有该组件', result: '创建独立 Draft，不复制依赖、参数、Action、Playbook、介质或镜像。' },
      { label: '批量导入 / 预检并导入', purpose: '从 JSON 模板批量录入细粒度组件、Draft、依赖和独立 Playbook；Action 按文件名严格绑定模板内文件。', availability: '组件 Owner；全部条目、依赖 DAG 和文件引用必须先通过校验', result: '组件、Draft、托管文件和审计记录整批生效或整批失败；不会自动验证、加入候选集或发布。' },
    ],
  },
  {
    page: 'Release 合同与生命周期', path: '/components', description: '先选 Release，再根据其状态进行查看、编辑、环境验证、发布或废弃。', entries: [
      { label: 'Release 版本行', purpose: '选择该版本的依赖和参数合同。', availability: '所有用户', result: '只切换当前查看版本。' },
      { label: 'Draft 发布就绪度 / 编辑合同 / 配置动作 / 构建镜像 / 管理介质', purpose: '汇总发布阻断项并进入对应处理入口。', availability: '组件 Owner 自有 Draft', result: '卡片本身只汇总状态；具体写操作仍需在后续表单确认。' },
      { label: '查看详情 / 关闭', purpose: '只读查看版本信息、环境约束、参数、依赖、生命周期动作和 Playbook。', availability: '所有可见 Release', result: '不修改 Release 或 Playbook。' },
      { label: '配置合同 / 编辑依赖和参数 / 编辑', purpose: '编辑 Draft 的参数与精确依赖映射。', availability: '组件 Owner 自有 Draft', result: '打开合同编辑器；需“保存依赖和参数”才写入。' },
      { label: '编辑可见性与映射', purpose: '从合同查看弹窗转入 Draft 编辑。', availability: '当前 Release 可编辑时', result: '关闭只读视图并打开编辑器。' },
      { label: '保存依赖和参数', purpose: '保存 Draft 参数、依赖及公开参数映射。', availability: '合同编辑器校验通过', result: '更新 Draft，并使相关旧测试证据失效。' },
      { label: '加入候选集 / 撤回候选', purpose: '把已完成交付证据的 Draft 交给场景 Owner 编排，或停止新的候选引用。', availability: '组件 Owner 自有 Draft；加入时必须满足当前合同的生命周期、安装验证和回退证据', result: '候选 Draft 可随场景 Revision 原子发布；Draft 再次编辑会自动撤回候选状态。' },
      { label: '发布 / 确认发布并通知', purpose: '预览下游影响并发布不可变 Release。', availability: '组件 Owner 自有 Draft 且满足发布校验', result: 'Release 进入 Released，并为受影响 Owner 创建通知。' },
      { label: '废弃草稿 / 废弃 / 确认废弃', purpose: '预览影响后撤销候选 Draft 或停止把旧 Release 作为推荐版本。', availability: '组件 Owner 自有 Draft 或已发布 Release；活动测试中的 Draft，以及已被场景 Run 锁定的版本不可废弃', result: '确认后状态变为已废弃并保留历史引用、Playbook 与介质。' },
    ],
  },
  {
    page: 'Draft 与 Playbook', path: '/components', description: '动作配置与文件内容分别保存，离开前必须处理未保存提示。', entries: [
      { label: '编辑版本与 Playbook / Playbook', purpose: '打开完整 Draft 编辑器。', availability: '组件 Owner 自有 Draft', result: '可维护版本、约束、动作、参数与依赖。' },
      { label: '新增动作 / 新增第一个动作', purpose: '增加 install、verify、rollback 等生命周期动作。', availability: 'Draft 编辑器', result: '新增未保存动作配置。' },
      { label: '动作标签', purpose: '切换当前编辑的生命周期动作。', availability: '已配置动作时', result: '只切换编辑对象。' },
      { label: '幂等安装，同时作为升级作业', purpose: '声明 install Playbook 可安全重复执行并复用于 upgrade。', availability: '当前动作类型为 install', result: '保存后场景可直接选择 upgrade，无需重复录入升级动作。' },
      { label: '载入编辑器', purpose: '读取已填写路径的 Playbook 内容。', availability: '路径非空', result: '把服务器文件载入在线编辑器，不自动保存。' },
      { label: '上传文件', purpose: '从本机载入 Playbook 文本。', availability: 'Draft 编辑器', result: '只填充编辑器，仍需保存。' },
      { label: '移除动作', purpose: '从 Draft 删除当前生命周期动作。', availability: '已选择动作', result: '标记动作删除；最终以保存 Draft 为准。' },
      { label: '保存 Playbook', purpose: '保存当前动作的 Playbook 文件内容。', availability: '内容非空', result: '写入 Draft Playbook；随后仍需保存 Draft 动作配置。' },
      { label: '添加 / 清空全部 / 删除 CredentialRef', purpose: '声明动作所需的凭据引用名称。', availability: 'Draft 编辑器', result: '只保存引用名，不读取或展示实际 Secret。' },
      { label: '新增参数 / 删除参数 / 新增依赖 / 删除依赖 / 增加映射 / 删除映射', purpose: '维护参数合同和精确依赖传值。', availability: 'Draft 编辑器', result: '改变尚未提交的 Draft 表单。' },
      { label: '保存 Draft', purpose: '提交版本、约束、动作和合同。', availability: '校验通过且 Playbook 无未保存内容', result: '更新 Draft；不会改写 Released Release。' },
    ],
  },
  {
    page: '组件测试与镜像构建', path: '/components', description: '预览成功前不能提交测试；回滚合同与 verify 目标相互独立。', entries: [
      { label: '环境验证', purpose: '打开安装验证或回退验证，并明确提示是否会修改环境。', availability: '具备可执行动作的 Release', result: '只打开表单，不创建 Run。' },
      { label: '验证模式 / 回退验证策略 / verify 目标 Release / 目标环境', purpose: '明确运行类型、可选 verify 基线和环境。', availability: '组件环境验证弹窗', result: '任一选择变化都会使旧执行计划失效。' },
      { label: '预览执行计划 / 刷新执行计划', purpose: '由服务端校验并返回完整步骤和 planDigest。', availability: '必填项完整', result: '不创建 Run、Approval 或审计执行记录。' },
      { label: '确认提交安装验证 / 确认提交回退验证', purpose: '按已确认计划创建 Run。', availability: '预览成功且计划仍有效', result: '创建 Run；破坏性动作进入环境 Owner 审批。' },
      { label: '构建镜像', purpose: '为 Draft 上传 Dockerfile 并查看构建。', availability: '组件 Owner 自有 Draft', result: '打开镜像构建弹窗。' },
      { label: '上传并构建', purpose: '在所选环境的 IMAGE_REGISTRY 中创建镜像构建。', availability: 'Dockerfile、环境和 Registry 均有效', result: '创建构建记录并异步执行。' },
      { label: '组件介质', purpose: '上传介质或登记 file-station 已有路径及 SHA-256。', availability: '组件 Owner 自有 Draft', result: '把介质元数据锁定到 Release。' },
      { label: '构建记录', purpose: '选择构建并查看状态和日志。', availability: '存在构建记录', result: '只切换详情。' },
    ],
  },
];

const SCENARIO_BUTTON_GROUPS: ButtonGuideGroup[] = [
  {
    page: '场景与 Revision', path: '/scenarios', description: '场景 Owner 管理 Draft；Released Revision 永远只读。', entries: [
      { label: '新建场景 / 创建场景', purpose: '创建场景和初始 Draft Revision。', availability: '场景 Owner', result: '生成可编辑 Revision。' },
      { label: '删除场景', purpose: '清理误建且尚未进入交付生命周期的场景。', availability: '场景 Owner 自有场景，所有 Revision 均未发布且从未产生任何 Run', result: '确认后永久删除场景、未发布 Revision 与相关个人运行参数预设；已发布或有 Run 的场景会被后端拒绝。' },
      { label: '导入模板 / 载入草稿', purpose: '一次载入节点、边和执行策略。', availability: '场景 Owner 自有 Draft', result: '只更新前台草稿，仍需人工检查并保存。' },
      { label: '导出 JSON', purpose: '下载当前 Revision 的可移植 JSON 模板。', availability: '已选择 Revision', result: '生成带场景标识和 Revision 编号的 .json 文件，不修改场景。' },
      { label: '场景条目 / Revision 选择', purpose: '切换场景或查看某个 Revision。', availability: '所有用户', result: '只切换详情。' },
      { label: '校验', purpose: '检查 DAG、依赖、动作和参数解析。', availability: '已选择 Revision', result: '返回校验结果，不创建 Run。' },
      { label: '保存草稿', purpose: '保存节点、连线、策略和参数。', availability: '自有 Draft', result: '更新 Draft，并使旧完整测试证据失效。' },
      { label: '新 Revision', purpose: '确认后从当前 Released/Deprecated Revision 克隆新草稿。', availability: '场景 Owner 自有不可变 Revision且没有活动 Draft', result: '创建新 Draft并设为当前，不修改原 Revision。' },
      { label: '放弃草稿', purpose: '放弃误建或不再需要的当前 Draft。', availability: '自有当前 Draft且存在可恢复的不可变 Revision', result: '草稿保留为已放弃历史，并恢复最近的不可变 Revision。' },
      { label: '预览候选集并发布 / 确认原子发布', purpose: '预览本 Revision 引用的候选 Draft，并提交场景发布。', availability: '自有 Test Passed Revision 且候选集整体就绪', result: '场景 Revision 与全部候选 Release 在同一事务中进入 Released；任一项变化或失败都不会部分发布。' },
      { label: '废弃', purpose: '废弃已发布 Revision。', availability: '场景 Owner 自有 Released Revision', result: '状态变为 Deprecated；历史 Run 不变。' },
    ],
  },
  {
    page: 'DAG 画布与运行', path: '/scenarios', description: '组件节点代表逻辑执行单元，不代表环境主机数量。', entries: [
      { label: '组件库条目', purpose: '把组件最新 Released 或已共享候选 Release 加入画布。', availability: '自有 Draft', result: '新增锁定精确 Release 的节点。' },
      { label: 'DAG / 节点表', purpose: '在拓扑画布与结构化节点清单之间切换。', availability: '已选择 Revision', result: '只改变查看方式；节点表可快速核对版本、动作、主机组和前后置数量。' },
      { label: '节点 / 连线端点', purpose: '选择节点或拖拽建立执行依赖。', availability: '画布', result: '选择会打开检查器；新连线需保存草稿。' },
      { label: '放大 / 缩小 / 适配视图', purpose: '调整 DAG 画布视口。', availability: '画布', result: '只改变前台视图。' },
      { label: '删除节点', purpose: '删除节点及其关联连线。', availability: '自有 Draft 且已选节点', result: '改变未保存画布，需保存草稿。' },
      { label: '环境测试 / 开始完整测试', purpose: '在共享环境验证当前 Draft 的完整 DAG。', availability: '自有可测试 Draft', result: '创建 Run；成功后 Revision 进入 Test Passed。' },
      { label: '环境运行 / 开始运行', purpose: '执行已发布场景。', availability: 'Released Revision', result: '锁定场景、组件、环境和输入并创建 Run。' },
    ],
  },
];

const ENVIRONMENT_BUTTON_GROUPS: ButtonGuideGroup[] = [
  {
    page: '环境与 Revision', path: '/environments', description: '环境保存始终创建新 Revision；其他角色只能选择环境，不能编辑。', entries: [
      { label: '新建环境 / 创建环境', purpose: '创建共享执行环境。', availability: '环境 Owner', result: '生成环境及初始 Revision。' },
      { label: '环境条目', purpose: '选择环境并读取当前 Revision。', availability: '所有用户', result: '只切换详情。' },
      { label: 'Inventory / 环境事实 / 环境变量 / 凭据引用', purpose: '切换环境配置分区。', availability: '所有用户可查看', result: '只切换分区；编辑能力由归属决定。' },
      { label: '添加主机 / 删除主机', purpose: '维护 Inventory 主机和主机组。', availability: '环境 Owner 自有环境', result: '改变未保存 Inventory。' },
      { label: '添加变量 / 删除环境变量', purpose: '维护非敏感作业环境变量、IMAGE_REGISTRY 和 FILE_STATION。', availability: '环境 Owner 自有环境', result: '改变未保存变量；Secret 不得填入此处。' },
      { label: '添加引用 / 删除凭据引用', purpose: '维护 CredentialRef 类型与引用位置。', availability: '环境 Owner 自有环境', result: '只保存引用，不把实际凭据返回前台。' },
      { label: '立即检查', purpose: '一次执行 TCP 端点探测和 Inventory 主机的 Go SSH 认证与 true 命令。', availability: '环境 Owner 自有环境', result: '分别保存带 Revision 来源的 TCP、SSH 结果和审计记录；不会创建 Run 或执行安装。' },
      { label: '一键回滚至干净状态 / 创建回滚 Run（待审批）', purpose: '从当前安装清单自动生成分层逆序 rollback 计划。', availability: '环境 Owner 自有且调度空闲的环境', result: '逐项校验来源 Run、备份和 Playbook 指纹；按来源时间倒序、来源内步骤逆序，输入环境名称后创建待审批 Run。' },
      { label: '移除环境 / 永久删除环境 / 确认归档环境', purpose: '先统计 Revision、Run、镜像构建和安装基线，再选择永久删除或保留历史的归档。', availability: '环境 Owner 自有环境；删除仅限从未使用，归档要求无活动 Run、无活动构建且无安装基线', result: '删除不可恢复但保留审计；归档保留全部历史并退出新任务选择。' },
      { label: '恢复环境', purpose: '让归档环境重新参与组件构建、验证和场景运行。', availability: '环境 Owner 自有归档环境', result: '清除归档状态，不修改 Revision、Run、构建或审计历史。' },
      { label: '放弃本页更改', purpose: '撤销当前配置分区尚未保存的修改。', availability: '环境 Owner 自有环境且当前分区有改动', result: '恢复当前 Revision 的该分区内容，不影响其他分区。' },
      { label: '保存新 Revision', purpose: '预览 Inventory、Facts、变量或凭据引用的差异。', availability: '环境 Owner 自有环境且当前分区有改动', result: '打开差异与变更原因确认框，尚未写入。' },
      { label: '确认创建 Revision', purpose: '用填写的变更原因提交预览差异。', availability: '差异确认框且变更原因非空', result: '创建不可变 Environment Revision；已提交 Run 仍用旧快照。' },
      { label: '基于此恢复', purpose: '把历史 Revision 的配置复制为一个新 Revision。', availability: '环境 Owner 自有环境的非当前 Revision', result: '要求填写恢复原因；保留原历史，不回写或删除旧 Revision。' },
      { label: '安全导出 / 含凭据引用导出', purpose: '下载指定 Environment Revision 的可移植配置。', availability: '环境 Owner 自有环境', result: '默认移除 reference；敏感导出仍不包含 Secret 实际值。' },
      { label: '导入 Revision / 预览差异', purpose: '导入新环境 r1 或既有环境的新 Revision。', availability: '环境 Owner；文件与目标通过预检', result: '按实际非空 reference 要求确认复用；规范化后的预览快照与落库内容一致。' },
    ],
  },
  {
    page: '危险运行审批', path: '/runs', description: '审批前必须重新读取 Run，并核对环境、步骤所属版本和目标主机组。', entries: [
      { label: '拒绝', purpose: '拒绝等待审批的破坏性 Run。', availability: '环境 Owner 且 Run 为 Awaiting Approval', result: 'Run 不会执行并进入拒绝状态。' },
      { label: '批准执行', purpose: '允许破坏性 Run 进入执行队列。', availability: '环境 Owner 且 Run 为 Awaiting Approval', result: '写入审批决定并继续执行；不替代线下变更授权。' },
      { label: '批量审批 / 确认批量批准', purpose: '使用必填的统一理由一次审批当前可见的多个危险 Run。', availability: '环境 Owner 且存在等待审批的 Run', result: '后端拒绝空白理由并原子消费整批审批；任何一项失效都会整批失败，成功后仍按各环境 FIFO 串行执行。' },
    ],
  },
  {
    page: '发布目录灾备', path: '/disaster-recovery', description: '只备份已发布或曾发布的目录与 Playbook；环境、Run、Secret 和大型介质不在其中。', entries: [
      { label: '创建私有仓库 / 创建并接入', purpose: '在服务端允许根目录下创建私有 bare Git 仓库。', availability: '环境 Owner 且服务端已启用发布目录灾备', result: '创建权限为 0700 的仓库并设为在线备份目标。' },
      { label: '接入已有备份仓库 / 更换备份仓库 / 验证并接入', purpose: '验证已有仓库的 catalog 分支并切换在线目标。', availability: '环境 Owner；路径必须位于允许根目录', result: '只接入仓库，不自动覆盖当前数据。' },
      { label: '从已有 Git 仓库恢复 / 仅接入，暂不恢复', purpose: '给空目录选择恢复向导，或只先完成仓库接入。', availability: '组件和场景均为空时可继续恢复', result: '接入后进入恢复点选择；非空目录只允许接入。' },
      { label: '立即备份', purpose: '同步创建当前已发布目录的不可变恢复点。', availability: '已接入私有仓库', result: '只有 SQLite、外键、Catalog/Playbook 摘要、catalog 分支和 backup/* 标签全部成功才返回成功。' },
      { label: '从恢复点恢复空库 / 预览恢复 / 确认恢复空库', purpose: '按锁定 Git 标签恢复已发布组件、场景和 Playbook。', availability: '当前组件与场景均为空且预检未漂移', result: '输入“恢复发布目录”后整批恢复；冲突时不留下部分数据。' },
    ],
  },
];

const OWNER_MANUALS: Record<Role, OwnerManual> = {
  component_owner: {
    title: '组件 Owner 操作手册',
    summary: '把一个可复用能力整理成可测试、可追踪、不可变的 Component Release。',
    icon: Boxes,
    tone: 'indigo',
    primaryPath: '/components',
    primaryLabel: '进入组件中心',
    checkpoints: ['依赖锁定到精确 Release', '敏感信息只声明 CredentialRef', '发布前复核影响预览', '版本更新后重新生成测试计划'],
    buttonGroups: COMPONENT_BUTTON_GROUPS,
    markdown: `## 组件 Owner 操作路径

> 目标：维护自己名下的组件，并把经过验证的能力发布为不可变 Release。已发布版本不能原地修改。

### 1. 创建组件与 Draft

1. 在 **组件** 页面新建组件，填写名称、标识、说明和 L1-L6 分类。
2. 点击 **创建 Draft 编辑合同**；已有版本时会复制当前合同，新组件则从空 Draft 开始。
3. 只有组件 Owner 能修改自己拥有的组件和 Draft。
4. 需要批量录入时使用 **批量导入**；点击 **预检并导入** 后，组件、Draft 和 Playbook 整批生效或整批失败，成功后仍要逐个完成环境验证与候选交接。

### 2. 配置版本合同

- 使用结构化表单设置操作系统、版本、Docker 版本等环境约束，以及参数、依赖和 Ansible 生命周期动作。
- 直接发布时依赖必须锁定已发布的上游 Release；候选链应由场景 Revision 一并编排和原子发布。下游只能映射上游的公开参数。
- 密码、Token、私钥等不能写进参数或动作 JSON，只能声明所需的 CredentialRef。
- install 可显式声明为幂等并复用于 upgrade；正式发布仍必须定义 install、verify、rollback。
- 分层用于目录展示；真正的执行先后由依赖关系和场景 DAG 决定。

### 3. 在共享环境测试

1. 需要镜像时先构建镜像；需要离线介质时通过 **组件介质** 上传或登记，并校验 SHA-256。
2. 选择 Draft，点击 **环境验证**，选择安装验证或回退验证及共享环境。
3. 补齐运行输入和依赖 Fixture，提交后到 **运行** 页面查看步骤与脱敏日志。
4. 来源仓库与目标环境不一致时，Run 会要求目标环境 Owner 审批平移；Draft 再次保存后旧测试证据失效。

### 4. 发布与维护

1. 当前合同必须已完成 install + verify 的安装验证，并具备相同规格摘要下的 rollback + verify 回退证据；Draft 再次保存后旧证据失效。
2. 独立直接发布前，所有上游依赖必须已经 Released；打开影响预览并用 **确认发布并通知** 提交。
3. 一组相互依赖的 Draft 应先逐个 **加入候选集**，由场景 Owner 编排、测试，再随 Scenario Revision 原子发布；不再交接时使用 **撤回候选**。
4. 后续变更请创建新 Draft；不再使用且没有被场景 Run 锁定的 Released Release 可废弃，但不会删除历史。

### 5. 前台版本更新时

1. 计划内部署前先保存 Draft；看到 **请刷新后继续操作** 后，不要继续使用旧测试或回滚弹窗。
2. 点击 **刷新使用新版本**，重新打开同一组件和 Release，复核 Draft/Released 状态与 rollback 的 from/to 合同。
3. 旧的测试计划摘要一律作废；重新选择环境、verify 目标或 rollback-only，并重新预览完整步骤后才能提交。`,
  },
  scenario_owner: {
    title: '场景 Owner 操作手册',
    summary: '把精确组件版本编排成 DAG，通过完整环境测试后发布不可变 Scenario Revision。',
    icon: Network,
    tone: 'cyan',
    primaryPath: '/scenarios',
    primaryLabel: '进入场景中心',
    checkpoints: ['节点锁定精确 Release', 'DAG 连线表达执行顺序', 'Test Passed 后才能发布', '版本更新后重读 Revision 并校验'],
    buttonGroups: SCENARIO_BUTTON_GROUPS,
    markdown: `## 场景 Owner 操作路径

> 目标：把已发布或完成交付证据的候选组件版本编排成可复现的交付场景，并对完整链路测试结果负责。

### 1. 创建并编排 Draft

1. 在 **场景** 页面新建场景，系统会创建初始 Draft Revision。
2. 误建且从未发布、从未产生 Run 的场景可使用 **删除场景** 永久清理；若已有发布或运行历史，只能废弃并保留记录。
3. 从组件库加入已发布 Release 或组件 Owner 共享的候选 Draft；每个节点锁定加入时的精确版本。
4. 用 DAG 连线表达硬依赖和实际顺序，再配置动作、主机组、节点参数和运行输入。
5. 可用 **导入模板** 载入节点、边和策略，或用 **导出 JSON** 下载当前 Revision；通过 **DAG / 节点表** 交叉核对拓扑与节点合同。

### 2. 保存并校验

- 保存 Draft 后执行校验，处理空图、环、缺失依赖、顺序错误和必填参数未解析等问题。
- 上游映射得到的参数不能再被节点值或运行输入覆盖。
- 当前 Demo 会保存 Execution Policy，但调度仍按 DAG 串行、首个失败即停止，不要把它当成并发策略。

### 3. 完整环境测试

1. 选择共享环境，填写已声明的运行输入并提交完整测试。
2. 全部步骤成功后 Revision 进入 **Test Passed**；失败、取消或拒绝会回到 Draft。
3. 危险步骤会停在等待审批，由目标环境 Owner 决定批准或拒绝。

### 4. 发布与运行

1. 只有 Test Passed 的当前 Revision 可以点击 **预览候选集并发布**；确认后端会再次校验 DAG、候选状态与证据，并在同一事务中发布场景和全部候选 Release。
2. Released Revision 不可编辑；修改时克隆一个新 Revision 并重新测试。
3. 创建新 Revision 前需要确认；同一场景只能有一个活动 Draft。不再需要时使用 **放弃草稿** 恢复最近的不可变 Revision。
4. Revision 选择器可以查看历史版本；已发布场景可选择目标环境运行，提交后统一在 **运行** 页面跟踪。

### 5. 前台版本更新时

1. 计划内部署前保存 DAG Draft；版本提示出现后不要尝试从旧页面继续保存、测试或发布。
2. 刷新后重新打开场景，确认当前 Revision、节点锁定 Release、主机组和连线仍是预期内容。
3. 重新执行校验；部署前尚未提交的测试输入需要重新填写，已经创建的 Run 仍按其锁定快照执行。`,
  },
  environment_owner: {
    title: '环境 Owner 操作手册',
    summary: '维护可执行的环境快照，管理 Inventory、仓库变量、凭据引用和危险运行审批。',
    icon: CloudCog,
    tone: 'amber',
    primaryPath: '/environments',
    primaryLabel: '进入环境中心',
    checkpoints: ['Inventory 主机与主机组完整', 'IMAGE_REGISTRY 与 FILE_STATION 可达', '危险运行审批前核对平移目标与动作', '发布代次已有最新成功恢复点', '版本更新后重新读取 Run 状态'],
    buttonGroups: ENVIRONMENT_BUTTON_GROUPS,
    markdown: `## 环境 Owner 操作路径

> 目标：提供可信的目标环境快照，并对凭据引用、环境容量和危险操作审批负责。

### 1. 创建与维护环境

1. 在 **环境** 页面新建环境，填写架构、操作系统、网络栈和说明。
2. 在 Inventory 中维护主机地址、SSH 用户、端口和主机组。
3. 分别维护环境事实、环境变量与 CredentialRef；每次保存都会创建新的 Environment Revision。
4. 需要迁移配置时使用 Revision 导出/导入；含实际 CredentialRef 引用的文件必须在当前文件上重新确认，编辑或替换文件会清除旧确认。

### 2. 管理仓库变量与凭据

- 环境事实用于组件兼容性预检，键名和值应与 Release 的环境约束一致。
- IMAGE_REGISTRY 和 FILE_STATION 都填写 host:port，分别作为镜像仓库和组件介质站。
- 数据库只保存 CredentialRef，不保存真实密码；不要把 Secret 粘贴到 Facts、Variables 或运行输入。

### 3. 支持测试与正式运行

1. 环境 Owner 可以用可见组件发起测试，也可以运行已发布场景。
2. Run 提交时会锁定环境、组件、场景、参数和 Playbook 摘要，后续环境修改不会改变已排队 Run。
3. 同一环境按 FIFO 串行执行；到 **运行** 页面查看队列、步骤与脱敏日志。

### 4. 审批危险操作

1. destructive、recovery、clean、destroy、uninstall 等动作会进入等待审批。
2. 核对目标环境、主机组、锁定版本、动作、运行参数，以及镜像/介质平移的来源和目标后再批准或拒绝。
3. 整集群清理可从环境详情单击“一键回滚至干净状态”：平台按来源 Run 给安装清单分层，先回滚较新的来源，再在每个来源内反转原节点顺序，并校验每个 rollback、backup_ref 和 Playbook 指纹。
4. 预览不会创建 Run；完整输入环境名称后只创建 Awaiting Approval Run，仍需在运行中心批准。
5. 批准只表示允许平台执行，不替代真实环境变更评审。

### 5. 保护已发布目录

1. 进入 **灾备目录**，在服务端允许根目录下创建或接入私有 Git 仓库。
2. 发布或废弃已发布对象后，确认已备份代次追上当前发布代次；需要明确交接点时使用 **立即备份**。
3. 只有组件和场景均为空的目标库才允许恢复；恢复不包含环境、Run、审批、通知、Secret 或大型介质。
4. 先选恢复点并预览组件、Release、场景和 Playbook 计数，再输入“恢复发布目录”提交；冲突或计划漂移会整批失败。

### 6. 前台版本更新时

1. 看到版本提示后不要在旧运行详情中批准、拒绝或取消 Run；先刷新页面。
2. 刷新后重新读取 Run 状态、目标环境、Environment Revision、步骤版本和审批要求，避免依据部署前缓存状态决策。
3. 已排队 Run 使用已锁定快照，不会被前台部署改写；环境编辑表单中尚未保存的内容需要重新核对后再提交。`,
  },
};

const DEPLOYMENT_MARKDOWN = `## 部署与版本切换交接

> 目标：部署人员完成可回退发布，各业务角色在刷新后重新取得服务端权威状态，不从旧 SPA 延续写操作。

### 平台部署人员

1. 部署前确认目标地址、当前分支和工作区，检查没有 Running、Queued 或 Awaiting Approval 的活动 Run。
2. 运行 Go、前端测试、测试环境嵌入式构建和差异检查；记录未执行的门禁，不能把“依赖缺失”写成“验证通过”。
3. 切换服务前备份二进制、数据库和环境配置；失败时恢复三者，不能只回退二进制。
4. 部署后验证服务状态、HTTP、二进制校验和、HTML 构建版本与不缓存的 version.json 一致。
5. 用已加载版本守卫的旧页面验证升级提示；确认旧应用区域已经 inert，刷新后页面版本与服务端一致。
6. 验收只做页面读取和计划预览；除非另有授权，不发布 Release、不提交破坏性 Run、不修改 Inventory。

首次上线版本守卫时，部署前已经打开的更老页面没有检测代码，需要通知所有角色手工刷新一次。从该版本开始，后续部署会自动阻断旧页面。

### 组件 Owner

- 计划内部署前保存 Draft。出现版本提示后刷新，重新打开组件和 Release。
- 复核 rollback 的不可变 from/to、verify 目标和环境；旧 planDigest 不再使用，必须重新预览。
- 后端错误应留在弹窗内处理，不用修改 Released Release 或 Environment Revision 绕过校验。

### 场景 Owner

- 计划内部署前保存 DAG Draft；未保存的画布状态不会跨刷新保留。
- 刷新后重新确认 Revision、精确 Release、连线、主机组和运行输入，再执行校验或测试。
- 已创建 Run 使用锁定快照；不要因为前台升级重复提交同一场景。

### 环境 Owner

- 版本提示出现后先刷新，再处理审批、取消或环境编辑。
- 审批前重新核对 Run 状态、环境 Revision、目标主机组、步骤所属版本与破坏性动作。
- 不通过篡改 Inventory、备份记录或 Revision 来消除计划错误；应修复真实合同或重新建立合格基线。

### 交接证据

- 记录部署前后构建版本、二进制 SHA-256、备份目录、服务状态和 HTTP 状态。
- 对比 Run、Approval、审计事件与 Environment Revision 数量，证明只读验收没有产生业务写入。
- 明确区分代码测试、构建成功、Ansible 门禁和真实环境验收，缺一项就标记为未验证。`;

const DEPLOYMENT_CHECKPOINTS = [
  '活动 Run 为零且回退备份已建立',
  'version.json 与 HTML 构建版本一致',
  '旧页面已阻断且刷新后版本一致',
  '业务记录计数未被只读验收改变',
];

const OVERVIEW_MARKDOWN = `> 当前是项目首个版本（V1），当前环境仅用于测试，不是生产环境。除非出现明确的 V2 文档，所有页面合同都按首版解释。

## 从可复用能力到可追踪运行

ClusterForge 把交付过程拆成组件 Release、场景 Revision、环境 Revision 和 Run 四类记录。前三类负责定义“执行什么、按什么顺序、在哪里执行”，Run 负责锁定快照并留下状态、审批和日志。

### 首次使用三步

1. 先在右上角确认当前身份和角色；本页的整体流程与公共按钮对所有角色可见。
2. 打开 **我的角色手册**，按页面顺序阅读本角色的职责、按钮出现条件和提交结果。
3. 第一次写操作从 Draft 或新 Revision 开始；提交 Run 前核对环境、版本、主机组、审批提示和完整执行计划。

### 协作边界

- **组件 Owner** 发布能力合同和动作，不决定某次集群交付的完整顺序。
- **场景 Owner** 锁定精确 Release，并通过 DAG 连线编排完整交付流程。
- **环境 Owner** 维护目标主机、参数和凭据引用，并审批危险操作。
- 已提交 Run 使用提交时快照，不会跟随后续 Release、Scenario 或 Environment 修改。

### 发布与执行规则

1. Released Release 与 Released Scenario Revision 都不可原地修改，演进必须创建新版本。
2. 场景必须通过当前 Revision 的完整测试后才能发布；组件 Release 必须具备当前合同的安装验证与回退证据，不能未验证发布。
3. 同一环境的 Run 按 FIFO 串行；危险动作先等待环境 Owner 审批。
4. 所有角色都从 **运行** 页面查看最终步骤、解析参数来源和脱敏日志。

### 场景节点数不等于环境主机数

- “六节点集群”描述目标环境 Inventory 中的主机规模，不要求场景画布也恰好有六个节点。
- 场景节点是逻辑执行单元；一个 bundle 或交付阶段可以在 Playbook 内处理多个软件组件和多台主机。
- 首版同时支持聚合节点和细粒度节点；实际数量以当前 Revision 为准。
- 判断真实执行内容要查看节点锁定的 Release、Action、Host Group 和最终 Run Steps，不能只看场景节点总数。`;

const WORKFLOW_STEPS = [
  { icon: Boxes, title: '1. 组件 Release', owner: '组件 Owner', text: '定义参数、依赖、环境约束与 Ansible 动作。', tone: 'indigo' },
  { icon: Network, title: '2. 场景 Revision', owner: '场景 Owner', text: '锁定精确版本，用 DAG 编排并完成环境测试。', tone: 'cyan' },
  { icon: CloudCog, title: '3. 环境 Revision', owner: '环境 Owner', text: '提供 Inventory、Facts、Variables 与 CredentialRef。', tone: 'amber' },
  { icon: PlayCircle, title: '4. Run', owner: '协作交付', text: '锁定快照，经过审批后串行执行并沉淀日志。', tone: 'rose' },
] as const;

const CENTER_CARDS = [
  { to: '/components', icon: Boxes, title: '组件发布', text: '创建 Draft、配置合同、共享环境测试、影响预览与发布。', tone: 'indigo' },
  { to: '/scenarios', icon: Network, title: '场景发布', text: '选择精确 Release、编排 DAG、完整测试并发布 Revision。', tone: 'cyan' },
  { to: '/environments', icon: CloudCog, title: '环境管理', text: '维护主机、事实、环境变量、CredentialRef 与 Revision。', tone: 'amber' },
] as const;

function InlineMarkdown({ text }: { text: string }) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g).filter(Boolean);
  return <>{parts.map((part, index) => {
    if (part.startsWith('**') && part.endsWith('**')) return <strong key={index}>{part.slice(2, -2)}</strong>;
    if (part.startsWith('`') && part.endsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
    return <span key={index}>{part}</span>;
  })}</>;
}

function isBlockStart(line: string) {
  return /^(#{2,3})\s|^>\s|^-\s|^\d+\.\s/.test(line);
}

function MarkdownPreview({ source }: { source: string }) {
  const lines = source.trim().split('\n');
  const blocks: ReactNode[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index].trim();
    if (!line) { index += 1; continue; }
    const heading = /^(#{2,3})\s+(.+)$/.exec(line);
    if (heading) {
      const content = <InlineMarkdown text={heading[2]} />;
      blocks.push(heading[1].length === 2 ? <h2 key={index}>{content}</h2> : <h3 key={index}>{content}</h3>);
      index += 1;
      continue;
    }
    if (line.startsWith('> ')) {
      blocks.push(<blockquote key={index}><InlineMarkdown text={line.slice(2)} /></blockquote>);
      index += 1;
      continue;
    }
    if (/^-\s/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^-\s/.test(lines[index].trim())) {
        items.push(lines[index].trim().replace(/^-\s+/, ''));
        index += 1;
      }
      blocks.push(<ul key={`ul-${index}`}>{items.map((item) => <li key={item}><InlineMarkdown text={item} /></li>)}</ul>);
      continue;
    }
    if (/^\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\d+\.\s/.test(lines[index].trim())) {
        items.push(lines[index].trim().replace(/^\d+\.\s+/, ''));
        index += 1;
      }
      blocks.push(<ol key={`ol-${index}`}>{items.map((item) => <li key={item}><InlineMarkdown text={item} /></li>)}</ol>);
      continue;
    }

    const paragraph = [line];
    index += 1;
    while (index < lines.length && lines[index].trim() && !isBlockStart(lines[index].trim())) {
      paragraph.push(lines[index].trim());
      index += 1;
    }
    blocks.push(<p key={`p-${index}`}><InlineMarkdown text={paragraph.join(' ')} /></p>);
  }

  return <div className="markdown-preview">{blocks}</div>;
}

function WorkflowOverview() {
  return (
    <div className="manual-flow" aria-label="平台交付工作流">
      {WORKFLOW_STEPS.map(({ icon: Icon, title, owner, text, tone }) => (
        <article key={title} className={`manual-flow__step manual-flow__step--${tone}`}>
          <span><Icon size={19} /></span>
          <div><small>{owner}</small><strong>{title}</strong><p>{text}</p></div>
        </article>
      ))}
    </div>
  );
}

function ButtonGuide({ title, intro, groups }: { title: string; intro: string; groups: ButtonGuideGroup[] }) {
  return (
    <section className="manual-button-guide" aria-label={title}>
      <header className="manual-button-guide__header">
        <div>
          <span>BUTTON DIRECTORY</span>
          <h2>{title}</h2>
          <p>{intro}</p>
        </div>
        <small>{groups.reduce((total, group) => total + group.entries.length, 0)} 项功能入口</small>
      </header>
      <div className="manual-button-guide__groups">
        {groups.map((group) => (
          <article className="manual-button-group" key={`${group.path}-${group.page}`}>
            <header>
              <div><h3>{group.page}</h3><code>{group.path}</code></div>
              <p>{group.description}</p>
            </header>
            <div className="manual-button-table" role="table" aria-label={`${group.page}按钮说明`}>
              <div className="manual-button-table__head" role="row">
                <span role="columnheader">按钮 / 入口</span><span role="columnheader">用途</span><span role="columnheader">何时可用</span><span role="columnheader">点击结果</span>
              </div>
              {group.entries.map((entry) => (
                <div className="manual-button-table__row" role="row" key={`${group.page}-${entry.label}`}>
                  <strong role="cell">{entry.label}</strong><span role="cell">{entry.purpose}</span><span role="cell">{entry.availability}</span><span role="cell">{entry.result}</span>
                </div>
              ))}
            </div>
          </article>
        ))}
      </div>
      <p className="manual-button-guide__scope">覆盖口径：同一功能在列表行、卡片或弹窗中重复出现时合并说明；动态记录选择、画布控件和关闭/重试入口也计入。按钮是否出现及最终权限以服务端 RBAC 和对象状态为准。</p>
    </section>
  );
}

export function OperationManualPage() {
  const { user } = useApp();
  const { section } = useParams<{ section: string }>();
  const roleView = section === 'role';
  const deploymentView = section === 'deployment';
  const manual = OWNER_MANUALS[user.role];
  const OwnerIcon = manual.icon;
  const pageTitle = deploymentView ? '部署与版本切换交接' : roleView ? manual.title : '平台工作流程总览';
  const pageSummary = deploymentView
    ? '把部署门禁、可回退切换、旧 SPA 刷新和分角色恢复步骤整理为一条可核查流程。'
    : roleView ? manual.summary : '用四类不可变记录串起组件发布、场景发布、环境管理与一次真实执行。';
  const pageSource = deploymentView ? DEPLOYMENT_MARKDOWN : roleView ? manual.markdown : OVERVIEW_MARKDOWN;
  const PageIcon = deploymentView ? ServerCog : roleView ? OwnerIcon : FileText;
  const showRoleManualAction = deploymentView || !roleView;

  return (
    <div className="page page--manual">
      <PageHeader
        eyebrow="Role-based playbook"
        title="操作说明书"
        description={`当前为项目首个版本（V1），环境仅用于测试，不是生产环境；除非出现明确的 V2 文档，所有操作都按首版合同执行。当前身份：${ROLE_LABELS[user.role]}。`}
        actions={<Link className="button button--primary" to={showRoleManualAction ? '/manual/role' : '/manual/deployment'}>{showRoleManualAction ? <OwnerIcon size={16} /> : <RefreshCw size={16} />}{showRoleManualAction ? '查看我的操作手册' : '查看部署交接'}</Link>}
      />

      <div className="manual-shell">
        <aside className="manual-toc panel" aria-label="说明书目录">
          <div className="manual-toc__heading"><BookOpenText size={17} /><div><strong>阅读目录</strong><small>随演示身份自动切换</small></div></div>
          <nav>
            <Link to="/manual" className={!roleView && !deploymentView ? 'active' : ''}><FileText size={16} /><span><strong>工作流总览</strong><small>组件、场景、环境与运行</small></span></Link>
            <Link to="/manual/role" className={roleView ? 'active' : ''}><OwnerIcon size={16} /><span><strong>{ROLE_LABELS[user.role]} 手册</strong><small>{user.name}</small></span></Link>
            <Link to="/manual/deployment" className={deploymentView ? 'active' : ''}><ServerCog size={16} /><span><strong>部署与版本切换</strong><small>分角色交接与验收</small></span></Link>
          </nav>
          <div className="manual-toc__note"><ShieldCheck size={16} /><p>页面按当前身份展示操作边界；实际写操作仍由后端 RBAC 校验。</p></div>
        </aside>

        <main className="manual-content">
          <article className="panel manual-preview-panel">
            <header className="manual-preview-panel__header">
              <span className={`manual-title-icon manual-title-icon--${deploymentView ? 'amber' : roleView ? manual.tone : 'indigo'}`}><PageIcon size={21} /></span>
              <div>
                <div className="eyebrow">Markdown preview</div>
                <h2>{pageTitle}</h2>
                <p>{pageSummary}</p>
              </div>
            </header>

            <div className="manual-preview-panel__body">
              {!roleView && !deploymentView && <WorkflowOverview />}
              <MarkdownPreview source={pageSource} />

              {!deploymentView && (
                <ButtonGuide
                  title={roleView ? `${ROLE_LABELS[user.role]} 按钮操作目录` : '全员公共按钮操作目录'}
                  intro={roleView
                    ? `仅列出 ${ROLE_LABELS[user.role]} 的业务操作；导航、运行筛选、通知与异常恢复等公共入口请先阅读工作流总览。`
                    : '所有角色先从这里了解平台公共入口；会改变业务状态的细节操作按角色放在“我的角色手册”。'}
                  groups={roleView ? manual.buttonGroups : COMMON_BUTTON_GROUPS}
                />
              )}

              {roleView || deploymentView ? (
                <>
                  <section className="manual-checklist" aria-label={deploymentView ? '部署交接检查清单' : '角色检查清单'}>
                    <header><CheckCircle2 size={17} /><div><h3>{deploymentView ? '部署交接完成条件' : '离开页面前检查'}</h3><p>每项都确认后，再进入下一阶段。</p></div></header>
                    <div>{(deploymentView ? DEPLOYMENT_CHECKPOINTS : manual.checkpoints).map((item) => <span key={item}><CheckCircle2 size={15} /> {item}</span>)}</div>
                  </section>
                  <div className="manual-actions">
                    <Link className="button button--primary" to={deploymentView ? '/manual/role' : manual.primaryPath}>{deploymentView ? '查看我的角色手册' : manual.primaryLabel} <ArrowRight size={15} /></Link>
                    <Link className="button button--quiet" to="/runs"><PlayCircle size={15} /> 查看运行中心</Link>
                  </div>
                </>
              ) : (
                <>
                  <section className="manual-card-grid" aria-label="核心工作中心">
                    {CENTER_CARDS.map(({ to, icon: Icon, title, text, tone }) => (
                      <Link key={to} to={to} className={`manual-center-card manual-center-card--${tone}`}>
                        <span><Icon size={19} /></span><div><h3>{title}</h3><p>{text}</p><small>进入页面 <ArrowRight size={13} /></small></div>
                      </Link>
                    ))}
                  </section>
                  <div className="manual-security-callout"><KeyRound size={19} /><div><strong>Secret 只走 CredentialRef</strong><p>参数、Facts、节点值和运行输入都不是密钥存储位置；危险动作还需要目标环境 Owner 审批。</p></div><TestTube2 size={19} /></div>
                </>
              )}
            </div>
          </article>
        </main>
      </div>
    </div>
  );
}
