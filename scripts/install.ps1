# CloudInit 一键安装脚本（Windows）
#
# 用法（管理员 PowerShell）：
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://eo-idc.pika.net.cn/cloudinit/install.ps1 | iex"
#
# 指定版本：
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm https://eo-idc.pika.net.cn/cloudinit/install.ps1))) -Version v0.4.6"
param(
    [string]$Version = "latest",
    [string]$InstallDir = "C:\cloudinit",
    [string]$MirrorBase = "https://eo-idc.pika.net.cn/cloudinit",
    [string]$Repo = "OpenIDCSTeam/CloudInit"
)

$ErrorActionPreference = "Stop"
$ServiceName = "cloudinit"

function Write-Log([string]$Message) {
    Write-Host "[CloudInit] $Message"
}

# ---------- 管理员权限 ----------
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Log "需要管理员权限，正在请求提权..."
    $scriptUrl = "https://github.com/$Repo/releases/latest/download/install.ps1"
    $command = "irm $scriptUrl | iex"
    if ($Version -ne "latest") {
        $command = "& ([scriptblock]::Create((irm $scriptUrl))) -Version $Version"
    }
    Start-Process powershell -Verb RunAs -ArgumentList @(
        "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", $command
    )
    return
}

# ---------- 选择安装包 ----------
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    "AMD64" { $assetArch = "x64" }
    "x86"   { $assetArch = "x86" }
    "ARM64" { $assetArch = "a64" }
    default { throw "不支持的架构: $arch" }
}

$osMajor = [Environment]::OSVersion.Version.Major
$assetOs = if ($osMajor -ge 10) { "win10" } else { "win78" }

if ($assetOs -eq "win78" -and $assetArch -eq "a64") {
    throw "Windows 7/8 不支持 ARM64，请使用 x64 版本"
}

$asset = "CloudInit-$assetOs-$assetArch.exe"
Write-Log "系统版本: ${osMajor}，架构: ${arch}，安装包: ${asset}"

# ---------- 下载 ----------
$tempFile = Join-Path $env:TEMP ("CloudInit-" + [guid]::NewGuid().ToString("N") + ".exe")

function Get-Installer([string]$Url) {
    Write-Log "下载: $Url"
    Invoke-WebRequest -Uri $Url -OutFile $tempFile -UseBasicParsing -TimeoutSec 600
}

if ($Version -eq "latest") {
    try {
        Get-Installer "$MirrorBase/$asset"
    } catch {
        Write-Log "镜像不可用，回退 GitHub"
        Get-Installer "https://github.com/$Repo/releases/latest/download/$asset"
    }
} else {
    Get-Installer "https://github.com/$Repo/releases/download/$Version/$asset"
}

if (-not (Test-Path $tempFile) -or (Get-Item $tempFile).Length -lt 1MB) {
    throw "安装包下载失败"
}

# ---------- 停止旧服务 ----------
$service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($service) {
    if ($service.Status -ne "Stopped") {
        Write-Log "停止现有服务..."
        & sc.exe stop $ServiceName | Out-Null

        for ($i = 0; $i -lt 30; $i++) {
            Start-Sleep -Seconds 1
            $service.Refresh()
            if ($service.Status -eq "Stopped") { break }
        }
        if ($service.Status -ne "Stopped") {
            throw "服务停止超时，请手动执行: sc.exe stop $ServiceName"
        }
    }
}

# ---------- 部署二进制 ----------
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "logs") | Out-Null

$target = Join-Path $InstallDir "CloudInit.exe"
Copy-Item -Force $tempFile $target
Remove-Item -Force $tempFile

# 保留已有的 IDCConfig.ini 与 .version，不做覆盖

# ---------- 注册服务 ----------
$binPath = '"' + $target + '"'
$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue

if ($existing) {
    Write-Log "更新服务配置: $ServiceName"
    & sc.exe config $ServiceName "binPath= $binPath" "start= auto" | Out-Null
} else {
    Write-Log "创建服务: $ServiceName"
    & sc.exe create $ServiceName "binPath= $binPath" "start= auto" "DisplayName= CloudInit" | Out-Null
}

& sc.exe description $ServiceName "CloudInit 虚拟机初始化与状态上报服务" | Out-Null
& sc.exe failure $ServiceName "reset= 86400" "actions= restart/5000/restart/10000/restart/30000" | Out-Null
& sc.exe start $ServiceName | Out-Null

Start-Sleep -Seconds 2
$service = Get-Service -Name $ServiceName
if ($service.Status -ne "Running") {
    throw "服务启动失败，当前状态: $($service.Status)。可用 sc.exe query $ServiceName 查看"
}

Write-Log "安装完成：$target"
Write-Log "服务名称: ${ServiceName}（开机自启）"
Write-Log "日志目录: $InstallDir\logs"
