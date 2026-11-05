# CloudInit

OpenIDC 虚拟机初始化与状态上报服务。部署在虚拟机内部，负责定时采集硬件状态上报至宿主机，并接收宿主机下发的控制指令（关机/重启/改密/改主机名），同时支持磁盘自动扩容和服务自动更新。

服务使用 Go 编写，单一静态二进制，无需运行时依赖。

## 功能特性

- **状态上报** - 定时采集 CPU/内存/磁盘/GPU/网络 等硬件状态，上报至宿主机
- **主控探测** - 同一子网内从 `gateway_offset` 开始逐个地址尝试，直到找到主控；全部失败则本轮不上报
- **指令执行** - 接收宿主机下发的关机、重启、改密码、改主机名等控制指令
- **命令下发** - 接收宿主机下发的远程命令，异步执行并回传结果
- **VMware Tools 清理** - 非 VMware/ESXi 环境自动停止服务、结束进程并静默卸载（兼容 1063 等服务状态异常）
- **磁盘扩容** - 启动时自动检测并扩容根分区（支持 ext4/xfs/btrfs/APFS/NTFS）
- **自动更新** - 后台守护协程每24小时检查更新，优先走 EdgeOne 镜像，失败回退 GitHub
- **工具更新** - `tool` 目录下的工具按各自版本独立在线更新（含 sha256 校验）
- **多平台支持** - 通过平台抽象层适配 Linux / Windows / macOS
- **配置覆盖** - 支持 `IDCConfig.ini` 文件覆盖默认运行参数

## 执行流程

```mermaid
graph TD
    A[服务启动] --> B[加载 IDCConfig.ini 配置]
    B --> C[初始化平台适配层]
    C --> D[启动后台任务]
    D --> D1[自动更新协程]
    D --> D2[磁盘扩容协程]
    D --> E[进入主循环]
    E --> F{等待上报间隔}
    F -->|到达| G[采集硬件状态]
    G --> H[计算网络增量]
    H --> I[上报至宿主机]
    I --> J{解析返回指令}
    J -->|关机/重启| K[执行电源操作]
    J -->|配置变更| L[后台执行改主机名/改密码]
    J -->|远程命令| M[后台执行并回传结果]
    J -->|无指令| F
    K --> N[服务退出]

    D1 --> O{每24h检查}
    O -->|有新版本| P[下载并替换]
    P --> Q[重启服务]
    O -->|无更新| O
```

## 项目结构

```
CloudInit/
├── main.go                  # 服务入口（主循环、上报、指令处理、信号）
├── go.mod / go.sum          # Go 模块依赖
├── IDCConfig.ini            # 运行配置文件（可选，覆盖默认参数）
│
├── tool/                    # 工具目录（脚本与独立程序，支持在线更新）
│   ├── tools.json           # 工具版本清单（构建时生成发布资产）
│   ├── MASAIO.cmd           # Windows 激活脚本
│   └── ToDeskSunDump.exe    # ToDesk 信息提取工具
│
├── scripts/
│   └── sync_edgeone.sh      # EdgeOne 镜像站同步/部署脚本
│
├── apppath/                 # 运行目录 / 可执行文件路径
├── logger/                  # 日志（控制台 + 按天轮转文件）
├── config/                  # 配置加载器（解析 IDCConfig.ini）
│
├── core/                    # 核心管理模块
│   ├── manage.go            # 虚拟机配置管理（改主机名/改密码，非阻塞）
│   ├── disk.go              # 磁盘扩容（非阻塞）
│   ├── power.go             # 电源控制（关机/重启）
│   ├── cmdexecutor.go       # 远程命令执行与结果回传
│   └── autoupdate.go        # 自动更新（后台守护协程）
│
├── platform/                # 平台抽象层
│   ├── platform.go          # 接口定义 + hosts 处理 + 命令执行封装
│   ├── linux.go             # Linux 实现
│   ├── windows.go           # Windows 实现（含 VMTools 检测、系统激活）
│   └── darwin.go            # macOS 实现
│
├── nic/                     # 网卡管理模块
│   ├── ncmanage.go          # 网卡扫描与管理
│   ├── ncconfig.go          # 网卡配置数据模型
│   └── gateway_*.go         # 各平台默认网关探测
│
├── status/                  # 状态采集模块
│   ├── vmstatus.go          # 硬件状态采集器
│   ├── hwstatus.go          # 硬件状态数据模型
│   ├── vmpowers.go          # 电源状态枚举
│   └── gpu.go               # GPU 负载采集（nvidia-smi）
│
├── remotedesk/              # ToDesk 远程桌面管理
│   ├── todesk.go            # 启动 ToDesk、解析设备代码/临时密码
│   └── window_*.go          # 主窗口隐藏（Windows）
│
├── PasswdDump/              # 独立的 Go 工具模块（ToDesk/向日葵信息提取）
├── ServerInit/              # Linux 部署相关
│   ├── ServerInit.sh        # 一键部署脚本
│   └── ServerInit.service   # systemd 服务单元文件
│
└── .github/workflows/       # CI/CD
    ├── build.yml            # 构建 + 发布工作流
    └── build-passwddump.yml # PasswdDump 构建工作流
```

## 使用方式

### 编译

```bash
# 当前平台
go build -o CloudInit .

# 交叉编译示例
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o CloudInit-linux-x64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o CloudInit-win10-x64.exe .
```

> Windows 7/8.1 需使用 Go 1.20 及以下版本编译，Go 1.21+ 不再支持旧版 Windows。
>
> **发布资产文件名不可变更**（`CloudInit-win78-x64.exe`、`CloudInit-win10-x64.exe`、`CloudInit-linux-x64` 等）：
> 存量客户端的自动更新按文件名中的“系统标识 + 架构”匹配资产，改名会导致老版本无法升级。

### Linux 部署

```bash
# 1. 将 ServerInit 目录上传至目标虚拟机（将编译产物命名为 ServerInit）
# 2. 执行部署脚本（自动安装为 systemd 服务）
cd ServerInit
chmod +x ServerInit.sh
./ServerInit.sh
```

部署后服务自动启动，开机自启。管理命令：

```bash
systemctl status ServerInit   # 查看状态
systemctl restart ServerInit  # 重启服务
systemctl stop ServerInit     # 停止服务
journalctl -u ServerInit -f   # 查看日志
```

### Windows 部署

将编译后的 `CloudInit.exe` 放置到目标目录，工具统一放在同级的 `tool` 目录：

```
C:\CloudInit\
├── CloudInit.exe
├── IDCConfig.ini        # 可选
├── .version             # 运行时生成，记录当前版本
└── tool\
    ├── MASAIO.cmd
    ├── ToDesk.exe
    └── ToDeskSunDump.exe
```

老版本把工具直接放在程序根目录的，启动时会自动迁移到 `tool`（`.version` 保持在程序目录不动）。注册为 Windows 服务或通过计划任务启动。

### 开发环境运行

```bash
go run .
```

## 配置说明

创建 `IDCConfig.ini` 文件（与可执行文件同目录），可覆盖以下参数：

```ini
[server]
report_interval = 60          # 上报间隔（秒）
report_port = 1880            # 上报端口
report_path = /api/client/upload
report_host =                 # 固定上报地址（留空则自动推算）
gateway_offset = 2            # 上报地址扫描起点（子网内依次尝试 .2~.254；0/1 表示从网关开始）

[update]
enabled = true                # 是否启用自动更新
interval = 86400              # 检查间隔（秒）
repo_url = https://api.github.com/repos/OpenIDCSTeam/CloudInit/releases/latest
mirror_url = https://eo-idc.pika.net.cn/cloudinit   # EdgeOne 镜像（优先，留空则只走 GitHub）

[log]
level = INFO                  # 日志级别
retention = 7 days            # 日志保留时间
```

## 工具目录与工具更新

- 工具统一放在程序目录下的 `tool/`，程序启动时会把老版本放在根目录的 `MASAIO.cmd`、`ToDesk.exe`、`ToDeskSunDump.exe` 自动迁移进去；`.version` 始终保留在程序目录。
- 工具版本记录在 `tool/tools.json`（`name` / `version` / `file`）。构建时会为每个工具生成带版本号的资产：`CloudInit-tool-<名称>-<版本><扩展名>`，并生成清单 `CloudInit-tools.json`（含 sha256）。
- 客户端每次检查更新时，按工具自身版本号与本地 `tool/.tool_versions.json` 比较，只下载更新的工具，并校验 sha256。
- **升级工具**：替换 `tool/` 下的文件并把 `tools.json` 里的 `version` 加一，提交后发版即可，存量机器会自动拉取。

## EdgeOne 镜像同步

正式版本发布会自动把构建产物同步到 `https://eo-idc.pika.net.cn/cloudinit`（`latest.json` + 全部资产 + 工具资产），客户端优先从该镜像更新，失败自动回退 GitHub。

需要配置：

| 类型 | 名称 | 说明 |
|---|---|---|
| Secret | `EDGEONE_API_TOKEN` | EdgeOne API Token（CI 部署用） |
| Variable | `EDGEONE_PROJECT_NAME` | EdgeOne Pages 项目名，默认 `cloudinit` |
| Variable | `EDGEONE_PREFIX` | 站点子路径，默认 `cloudinit`（即 `/cloudinit/latest.json`） |

本地生成镜像目录（不部署）：

```bash
./scripts/sync_edgeone.sh dist v0.4.1 eo-site
```

设置 `EDGEONE_API_TOKEN` 后脚本会调用 `edgeone pages deploy eo-site -n <项目> -e production` 完成部署。

## 日志

- 控制台：`时间 | 级别 | 消息`，级别由 `[log] level` 控制
- 文件：`logs/cloudinit_YYYY-MM-DD.log`，固定 DEBUG 级别，按天轮转，按保留期清理

## 许可证

GPL-3.0 License
