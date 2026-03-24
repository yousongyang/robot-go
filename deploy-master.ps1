# robot-master 一键部署脚本 (Windows PowerShell)
# 用法: irm <raw_url>/deploy-master.ps1 | iex
#   或: .\deploy-master.ps1 [-Version latest] [-Dir .\robot-master] [-Repo atframework/robot-go]

param(
    [string]$Version = "latest",
    [string]$Dir = ".\robot-master",
    [string]$Repo = "atframework/robot-go"
)

$ErrorActionPreference = "Stop"

$Arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { Write-Error "仅支持 64 位系统"; exit 1 }
$BinName = "robot-master-windows-${Arch}.exe"

if ($Version -eq "latest") {
    $BaseUrl = "https://github.com/${Repo}/releases/latest/download"
} else {
    $BaseUrl = "https://github.com/${Repo}/releases/download/${Version}"
}

Write-Host "========================================"
Write-Host "  robot-master 一键部署"
Write-Host "========================================"
Write-Host "  仓库:     $Repo"
Write-Host "  版本:     $Version"
Write-Host "  平台:     windows-$Arch"
Write-Host "  安装目录: $Dir"
Write-Host "========================================"

if (-not (Test-Path $Dir)) { New-Item -ItemType Directory -Path $Dir -Force | Out-Null }

# 下载二进制
Write-Host "[1/3] 下载 $BinName ..."
Invoke-WebRequest -Uri "${BaseUrl}/${BinName}" -OutFile (Join-Path $Dir "robot-master.exe")
Write-Host "      -> $(Join-Path $Dir 'robot-master.exe')"

# 下载默认配置
Write-Host "[2/3] 下载默认配置 master.yaml ..."
$cfgPath = Join-Path $Dir "master.yaml"
if (Test-Path $cfgPath) {
    Write-Host "      -> 已存在，跳过（不覆盖已有配置）"
} else {
    Invoke-WebRequest -Uri "${BaseUrl}/master.yaml" -OutFile $cfgPath
    Write-Host "      -> $cfgPath"
}

# 完成
Write-Host "[3/3] 部署完成！"
Write-Host ""
Write-Host "启动方式："
Write-Host "  cd $Dir"
Write-Host "  .\robot-master.exe -config master.yaml"
Write-Host ""
Write-Host "或直接指定参数："
Write-Host "  .\robot-master.exe -listen :8080 -redis-addr localhost:6379"
Write-Host ""
Write-Host "浏览器打开 http://localhost:8080 访问 Web 控制台"
