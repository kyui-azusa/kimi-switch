export interface ChangelogEntry {
  version: string;
  date: string;
  title: string;
  changes: string[];
}

export const changelog: ChangelogEntry[] = [
  {
    version: "0.2.8",
    date: "2026-08-26",
    title: "403 自动换号重试",
    changes: [
      "自动轮换开启时，当前账号返回 403 后会切换到另一个健康账号，并原样重发当前请求一次。",
      "重发请求无论成功或失败都不会继续尝试第三个账号，避免形成重复请求链。",
      "没有其他健康账号时直接返回原始 403，不会再次使用刚失败的账号。",
    ],
  },
  {
    version: "0.2.7",
    date: "2026-08-24",
    title: "后台运行与状态判断更可靠",
    changes: [
      "新增应用内更新日志，可随时回看各版本的主要变化。",
      "关闭主窗口后继续在系统托盘运行，可从托盘重新打开或完全退出。",
      "主动按 ESC、中止请求或由主 Agent 停止 Subagent 时，不再误判为账号额度用尽。",
      "支持手工记录 5 小时与周额度刷新参考时间，并明确标注为非官方参考信息。",
    ],
  },
  {
    version: "0.2.6",
    date: "2026-08-21",
    title: "发布基线修正",
    changes: [
      "将账号拖拽排序修复带入可自动更新的发布基线。",
      "整合 0.2.4 的接管自动恢复修复到最新可更新版本。",
    ],
  },
  {
    version: "0.2.5",
    date: "2026-08-21",
    title: "账号排序体验优化",
    changes: [
      "修复账号拖拽排序并完善键盘操作。",
      "调整后的账号顺序会持久保存，重启后保持不变。",
    ],
  },
  {
    version: "0.2.4",
    date: "2026-08-21",
    title: "接管状态自动恢复",
    changes: [
      "持久保存用户选择的接管状态，外部程序重写 config.toml 后会自动恢复接管。",
      "兼容旧版配置迁移，并保留用户明确关闭接管的选择。",
    ],
  },
  {
    version: "0.2.3",
    date: "2026-08-19",
    title: "Windows 启动修复",
    changes: [
      "修复 Windows 安装版无法稳定启动本地代理的问题。",
      "完善系统凭据读取与安装包构建。",
    ],
  },
  {
    version: "0.2.1",
    date: "2026-08-18",
    title: "应用内自动更新",
    changes: [
      "接入签名更新包和独立更新服务，支持在应用内检查、下载、安装并重启。",
      "提供 macOS Apple Silicon、macOS Intel 和 Windows x86_64 安装包。",
    ],
  },
  {
    version: "0.2.0",
    date: "2026-08-18",
    title: "首个可自动更新版本",
    changes: [
      "建立 Tauri 更新签名和多平台发布链路。",
      "后续版本可通过 KimiSwitch 内置更新功能直接安装。",
    ],
  },
];
