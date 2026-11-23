<#
.SYNOPSIS
  建筑能耗监测分析（桌面版 V1.0）Windows 一键本地 / 局域网预览启动脚本。
.DESCRIPTION
  1. 自动检查本地 PostgreSQL (127.0.0.1:55432)、Go 后端 (127.0.0.1:18090) 与 Vite 前端 (127.0.0.1:5176)。
  2. 传入 -EnableLan 时，自动识别本机有效 Private 局域网 IPv4 地址并启动专用局域网网关 (默认端口 5177)，
     提供独立口令认证与同源反向代理，不暴露 PostgreSQL、Go 管理接口或本地 dev-auth。
  3. 已运行的服务直接复用，避免重复启动造成端口冲突；绝不把密码写入 Git 或日志。
#>
[CmdletBinding()]
param(
  [int]$PgPort = 55432,
  [int]$BackendPort = 18090,
  [int]$FrontendPort = 5176,
  [switch]$EnableLan,
  [int]$LanPort = 5177,
  [string]$PasswordFile = ""
)

$ErrorActionPreference = "Stop"
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$WebDir = Join-Path $RepoRoot "web"
$LogDir = Join-Path $env:TEMP "iot-energy-preview-logs"
$LocalCredDir = Join-Path $env:LOCALAPPDATA "iot-energy-preview"
New-Item -ItemType Directory -Force -Path $LogDir, $LocalCredDir | Out-Null

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

function Get-PrivateLanInfo {
  # Detect real RFC1918 IPv4 address on active non-virtual adapter (prefer Private network profile).
  $profiles = @{}
  foreach ($p in (Get-NetConnectionProfile -ErrorAction SilentlyContinue)) {
    $profiles[$p.InterfaceAlias] = $p.NetworkCategory
  }
  $candidates = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object {
    $_.AddressState -eq "Preferred" -and
    $_.InterfaceAlias -notmatch "Loopback|vEthernet|WSL|Docker|Clash|FlClash|Tailscale|ZeroTier|TUN|TAP|WireGuard|VMware|VirtualBox" -and
    ($_.IPAddress -match "^(192\.168\.|10\.|172\.(1[6-9]|2[0-9]|3[0-1])\.)")
  }
  $preferred = $candidates | Sort-Object @{ Expression = { if ($profiles[$_.InterfaceAlias] -eq "Private") { 0 } else { 1 } } }, InterfaceMetric | Select-Object -First 1
  if (-not $preferred) { return $null }

  # Compute IPv4 subnet CIDR (e.g. 192.168.100.69/24 -> 192.168.100.0/24)
  $ipBytes = ([System.Net.IPAddress]::Parse($preferred.IPAddress)).GetAddressBytes()
  $prefix = [int]$preferred.PrefixLength
  $maskUInt = if ($prefix -eq 0) { [uint32]0 } else { [uint32](([uint64]4294967295 -shl (32 - $prefix)) -band [uint64]4294967295) }
  $maskBytes = [System.BitConverter]::GetBytes($maskUInt)
  [Array]::Reverse($maskBytes)
  $netBytes = New-Object byte[] 4
  for ($i = 0; $i -lt 4; $i++) { $netBytes[$i] = $ipBytes[$i] -band $maskBytes[$i] }
  $subnet = "$([System.Net.IPAddress]::new($netBytes).IPAddressToString)/$prefix"

  return [PSCustomObject]@{
    InterfaceAlias  = $preferred.InterfaceAlias
    IPAddress       = $preferred.IPAddress
    PrefixLength    = $prefix
    Subnet          = $subnet
    NetworkCategory = $profiles[$preferred.InterfaceAlias]
  }
}

function Ensure-LanPasscodeFile {
  $file = Join-Path $LocalCredDir "lan-passcode.txt"
  if ($env:IOT_LAN_PASSCODE -and $env:IOT_LAN_PASSCODE.Trim().Length -ge 8) {
    Set-Content -Path $file -Value $env:IOT_LAN_PASSCODE.Trim() -NoNewline -Encoding ASCII
    return $file
  }
  if (-not (Test-Path $file) -or ((Get-Content -Raw $file).Trim().Length -lt 8)) {
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $bytes = New-Object byte[] 6
    $rng.GetBytes($bytes)
    $code = "LAN-" + ([System.BitConverter]::ToString($bytes).Replace("-", "").Substring(0, 8))
    Set-Content -Path $file -Value $code -NoNewline -Encoding ASCII
  }
  return $file
}

Write-Host "=== 建筑能耗监测分析 · 本地桌面预览启动检查 ===" -ForegroundColor Cyan

# 1. Check local PostgreSQL
if (-not (Test-TcpPort -HostName "127.0.0.1" -Port $PgPort)) {
  Write-Error "本地 PostgreSQL (127.0.0.1:$PgPort) 未运行，请先启动本地预览数据库（iot_preview）后再重试。"
  exit 1
}
Write-Host "[OK] 本地 PostgreSQL (127.0.0.1:$PgPort) 已就绪（仅回环访问）" -ForegroundColor Green

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

# 4. Optional Dedicated LAN Reverse-Proxy Gateway (Port 5177, Passcode-Protected)
$lanUrl = $null
$passcodeFilePath = $null
if ($EnableLan) {
  $lan = Get-PrivateLanInfo
  if (-not $lan) {
    Write-Error "未检测到有效的本地 RFC1918 局域网 IPv4 地址（已过滤回环与虚拟网卡）。"
    exit 1
  }

  $passcodeFilePath = Ensure-LanPasscodeFile
  $lanHealthUrl = "http://$($lan.IPAddress):$LanPort/lan-healthz"
  $lanUrl = "http://$($lan.IPAddress):$LanPort/ui/"

  if (Test-HttpOk -Url $lanHealthUrl) {
    Write-Host "[OK] 局域网专用网关 ($($lan.IPAddress):$LanPort) 已在运行，直接复用" -ForegroundColor Green
  } else {
    if (Test-TcpPort -HostName $lan.IPAddress -Port $LanPort) {
      Write-Error "端口 $($lan.IPAddress):$LanPort 已被其他进程占用，请指定 -LanPort 或释放端口。"
      exit 1
    }

    $gwExe = Join-Path $env:TEMP "energy-lan-gateway.exe"
    $goExe = Find-GoExe
    if ($goExe) {
      Write-Host "[..] 正在编译局域网专用代理网关 ($gwExe)..." -ForegroundColor Yellow
      Push-Location $RepoRoot
      try {
        & $goExe build -o $gwExe ./cmd/energy-lan-gateway
        if ($LASTEXITCODE -ne 0) { throw "go build energy-lan-gateway 失败" }
      } finally {
        Pop-Location
      }
    } elseif (-not (Test-Path $gwExe)) {
      Write-Error "未找到预编译 energy-lan-gateway.exe 且未找到 go.exe。"
      exit 1
    }

    $env:IOT_LAN_LISTEN = "$($lan.IPAddress):$LanPort"
    $env:IOT_LAN_PASSCODE_FILE = $passcodeFilePath
    $env:IOT_LAN_BACKEND_URL = "http://127.0.0.1:$BackendPort"
    $env:IOT_LAN_FRONTEND_URL = "http://127.0.0.1:$FrontendPort"

    $gwOut = Join-Path $LogDir "lan-gateway.out.log"
    $gwErr = Join-Path $LogDir "lan-gateway.err.log"
    Start-Process -FilePath $gwExe -WorkingDirectory $RepoRoot -WindowStyle Hidden `
      -RedirectStandardOutput $gwOut -RedirectStandardError $gwErr | Out-Null

    $gwReady = $false
    for ($i = 0; $i -lt 25; $i++) {
      Start-Sleep -Milliseconds 300
      if (Test-HttpOk -Url $lanHealthUrl) { $gwReady = $true; break }
    }
    if (-not $gwReady) {
      Write-Error "局域网网关启动超时，请查看日志：$gwErr"
      exit 1
    }
    Write-Host "[OK] 局域网专用网关已启动 ($lanUrl) · 网卡 $($lan.InterfaceAlias) [$($lan.NetworkCategory)]" -ForegroundColor Green
  }

  # Windows Firewall rule check (Private profile + local LAN subnet ONLY; never Public/Any!)
  $ruleName = "IOT-Energy-LAN-Preview-$LanPort"
  $existingRule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
  if ($existingRule) {
    Write-Host "[OK] Windows 防火墙规则 $ruleName 已存在（仅限 Private / $($lan.Subnet)）" -ForegroundColor Green
  } else {
    $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ($isAdmin) {
      New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Action Allow -Protocol TCP `
        -LocalPort $LanPort -Profile Private -RemoteAddress $lan.Subnet | Out-Null
      Write-Host "[OK] 已自动添加 Windows 防火墙入站规则：$ruleName (Private / $($lan.Subnet) / TCP $LanPort)" -ForegroundColor Green
    } else {
      Write-Host "[!] 当前非管理员终端，其他局域网设备访问前请以管理员运行一次以下命令放行 Private 网段：" -ForegroundColor Yellow
      Write-Host "    New-NetFirewallRule -DisplayName '$ruleName' -Direction Inbound -Action Allow -Protocol TCP -LocalPort $LanPort -Profile Private -RemoteAddress '$($lan.Subnet)'" -ForegroundColor Yellow
    }
  }
}

Write-Host ""
Write-Host "本机预览地址：$uiUrl" -ForegroundColor Cyan
if ($lanUrl) {
  Write-Host "局域网演示地址：$lanUrl" -ForegroundColor Cyan
  Write-Host "局域网口令文件：$passcodeFilePath（未入 Git，未写入日志；请通过 Get-Content 查看后输入）" -ForegroundColor DarkYellow
  Write-Host "安全提示：当前 HTTP 入口仅用于可信局域网临时模拟数据演示；生产/真实数据须采用 HTTPS 与正式认证。" -ForegroundColor DarkGray
}
Write-Host "运行日志目录：$LogDir" -ForegroundColor DarkGray
