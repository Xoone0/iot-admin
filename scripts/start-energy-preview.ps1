<#
.SYNOPSIS
  建筑能耗监测分析（桌面版 V1.0）Windows 一键本地预览启动脚本。
.DESCRIPTION
  自动检查本地 PostgreSQL (127.0.0.1:55432)、Go 后端 (127.0.0.1:18090) 与 Vite 前端 (127.0.0.1:5176)。
  已运行的服务直接复用，避免重复启动造成端口冲突；绝不重置模拟数据，不连接远端/生产环境。
#>
[CmdletBinding()]
param(
  [int]$PgPort = 55432,
  [int]$BackendPort = 18090,
  [int]$FrontendPort = 5176,
  [string]$PasswordFile = ""
)

$ErrorActionPreference = "Stop"
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$WebDir = Join-Path $RepoRoot "web"
$LogDir = Join-Path $env:TEMP "iot-energy-preview-logs"
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

function Test-TcpPort {
  param([string]$HostName, [int]$Port)
  try {
    $client = New-Object System.Net.Sockets.TcpClient
    $iar = $client.BeginConnect($HostName, $Port, $null, $null)
    $ok = $iar.AsyncWaitHandle.WaitOne(800, $false)
    if ($ok -and $client.Connected) {
      $client.EndConnect($iar)
      $client.Close()
      return $true
    }
    $client.Close()
    return $false
  } catch {
    return $false
  }
}

function Test-HttpOk {
  param([string]$Url)
  try {
    $resp = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 2
    return ($resp.StatusCode -ge 200 -and $resp.StatusCode -lt 400)
  } catch {
    return $false
  }
}

function Find-NodeDir {
  if (Get-Command node -ErrorAction SilentlyContinue) {
    return (Split-Path (Get-Command node).Source -Parent)
  }
  foreach ($cand in @("D:\DevTools\NodeJS", "C:\Program Files\nodejs")) {
    if (Test-Path (Join-Path $cand "node.exe")) { return $cand }
  }
  return $null
}

function Find-GoExe {
  if (Get-Command go -ErrorAction SilentlyContinue) {
    return (Get-Command go).Source
  }
  foreach ($cand in @("D:\DevTools\Go\bin\go.exe", "C:\Program Files\Go\bin\go.exe")) {
    if (Test-Path $cand) { return $cand }
  }
  $tempGo = Get-ChildItem $env:TEMP -Directory -Filter "iot-admin-check-*" -ErrorAction SilentlyContinue |
    ForEach-Object { Join-Path $_.FullName "go\bin\go.exe" } |
    Where-Object { Test-Path $_ } |
    Select-Object -First 1
  if ($tempGo) { return $tempGo }
  return $null
}

Write-Host "=== 建筑能耗监测分析 · 本地桌面预览启动检查 ===" -ForegroundColor Cyan

# 1. Check local PostgreSQL
if (-not (Test-TcpPort -HostName "127.0.0.1" -Port $PgPort)) {
  Write-Error "本地 PostgreSQL (127.0.0.1:$PgPort) 未运行，请先启动本地预览数据库（iot_preview）后再重试。"
  exit 1
}
Write-Host "[OK] 本地 PostgreSQL (127.0.0.1:$PgPort) 已就绪" -ForegroundColor Green

# 2. Check / start Go energy-live-server on 127.0.0.1:$BackendPort
$healthUrl = "http://127.0.0.1:$BackendPort/healthz"
if (Test-HttpOk -Url $healthUrl) {
  Write-Host "[OK] Go 后端服务 (127.0.0.1:$BackendPort) 已在运行，直接复用" -ForegroundColor Green
} else {
  if (Test-TcpPort -HostName "127.0.0.1" -Port $BackendPort) {
    Write-Error "端口 127.0.0.1:$BackendPort 已被其他进程占用且 /healthz 不可用，请释放端口后重试。"
    exit 1
  }

  $dsn = $env:IOT_PG_DSN
  if ([string]::IsNullOrWhiteSpace($dsn)) {
    $pwCandidates = @()
    if ($PasswordFile) { $pwCandidates += $PasswordFile }
    $pwCandidates += (Join-Path $RepoRoot ".dev\postgres-password")
    $pwCandidates += "E:\Active\iot-admin\.dev\postgres-password"
    $pwPath = $pwCandidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
    if (-not $pwPath) {
      Write-Error "未设置 IOT_PG_DSN 且未找到本地 .dev/postgres-password 文件。"
      exit 1
    }
    $pw = (Get-Content -Raw $pwPath).Trim()
    $dsn = "postgres://iot_dev:$pw@127.0.0.1:$PgPort/iot_preview?sslmode=disable"
  }

  $exePath = Join-Path $env:TEMP "energy-live-server.exe"
  if (-not (Test-Path $exePath)) {
    $goExe = Find-GoExe
    if (-not $goExe) {
      Write-Error "未找到预编译 energy-live-server.exe 且系统 PATH 中无 go.exe，无法构建后端。"
      exit 1
    }
    Write-Host "[..] 正在编译本地 Go 后端 ($exePath)..." -ForegroundColor Yellow
    Push-Location $RepoRoot
    try {
      & $goExe build -o $exePath ./cmd/energy-live-server
      if ($LASTEXITCODE -ne 0) { throw "go build 失败" }
    } finally {
      Pop-Location
    }
  }

  $env:IOT_PG_DSN = $dsn
  $env:IOT_ENERGY_LIVE_LISTEN = "127.0.0.1:$BackendPort"
  $env:IOT_ENERGY_DEV_LOGIN = "1"

  $beOut = Join-Path $LogDir "backend.out.log"
  $beErr = Join-Path $LogDir "backend.err.log"
  Start-Process -FilePath $exePath -WorkingDirectory $RepoRoot -WindowStyle Hidden `
    -RedirectStandardOutput $beOut -RedirectStandardError $beErr | Out-Null

  $ready = $false
  for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 400
    if (Test-HttpOk -Url $healthUrl) { $ready = $true; break }
  }
  if (-not $ready) {
    Write-Error "Go 后端启动超时，请查看日志：$beErr"
    exit 1
  }
  Write-Host "[OK] Go 后端服务已启动 (http://127.0.0.1:$BackendPort)" -ForegroundColor Green
}

# 3. Check / start Vite preview server on 127.0.0.1:$FrontendPort
$uiUrl = "http://127.0.0.1:$FrontendPort/ui/"
if (Test-HttpOk -Url $uiUrl) {
  Write-Host "[OK] Vite 前端预览 ($uiUrl) 已在运行，直接复用" -ForegroundColor Green
} else {
  if (Test-TcpPort -HostName "127.0.0.1" -Port $FrontendPort) {
    Write-Error "端口 127.0.0.1:$FrontendPort 已被占用且 $uiUrl 无响应，请检查占用进程。"
    exit 1
  }

  $nodeDir = Find-NodeDir
  if (-not $nodeDir) {
    Write-Error "未找到 Node.js，请安装 Node.js 或将其加入 PATH。"
    exit 1
  }
  $env:PATH = "$nodeDir;$env:PATH"
  $viteCmd = Join-Path $WebDir "node_modules\.bin\vite.cmd"
  if (-not (Test-Path $viteCmd)) {
    Write-Error "未找到 $viteCmd，请先在 web 目录执行 npm install。"
    exit 1
  }

  $feOut = Join-Path $LogDir "frontend.out.log"
  $feErr = Join-Path $LogDir "frontend.err.log"
  Start-Process -FilePath $viteCmd `
    -ArgumentList "--config", "vite.energy-live.config.mjs", "--host", "127.0.0.1", "--port", "$FrontendPort" `
    -WorkingDirectory $WebDir -WindowStyle Hidden `
    -RedirectStandardOutput $feOut -RedirectStandardError $feErr | Out-Null

  $uiReady = $false
  for ($i = 0; $i -lt 35; $i++) {
    Start-Sleep -Milliseconds 400
    if (Test-HttpOk -Url $uiUrl) { $uiReady = $true; break }
  }
  if (-not $uiReady) {
    Write-Error "Vite 前端启动超时，请查看日志：$feErr"
    exit 1
  }
  Write-Host "[OK] Vite 前端预览已启动 ($uiUrl)" -ForegroundColor Green
}

Write-Host ""
Write-Host "本地预览地址：$uiUrl" -ForegroundColor Cyan
Write-Host "运行日志目录：$LogDir" -ForegroundColor DarkGray
