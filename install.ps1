# Reality 目标检测 · Windows 一键部署脚本
# 用法: .\install.ps1 [安装目录]      默认目录: $env:USERPROFILE\reality-checker
$ErrorActionPreference = 'Stop'

$Repo = 'G1oow/reality-checker-main'
$Ref = if ($env:REF) { $env:REF } else { 'main' }
$Dir = if ($args.Count -ge 1 -and $args[0]) { $args[0] } else { Join-Path $env:USERPROFILE 'reality-checker' }
$Base = "https://raw.githubusercontent.com/$Repo/$Ref"
$DataFiles = @('Country.mmdb', 'gfwlist.conf', 'cdn_keywords.txt', 'hot_websites.txt', 'exclude_rules.txt')

New-Item -ItemType Directory -Force -Path (Join-Path $Dir 'data') | Out-Null

Write-Host "[*] 下载主程序 -> $Dir\reality-checker-main.exe"
Invoke-WebRequest -Uri "$Base/reality-checker-main.exe" -OutFile (Join-Path $Dir 'reality-checker-main.exe')

Write-Host "[*] 下载配置与数据文件"
Invoke-WebRequest -Uri "$Base/config.yaml" -OutFile (Join-Path $Dir 'config.yaml')
foreach ($f in $DataFiles) {
    Invoke-WebRequest -Uri "$Base/data/$f" -OutFile (Join-Path $Dir "data\$f")
}

# 解除下载文件的"来自 Internet"标记，避免被拦截
Get-ChildItem -Path $Dir -Recurse -File | Unblock-File -ErrorAction SilentlyContinue

# ---------- 探测本机出口 IP，生成可执行的运行命令 ----------
function Get-ExitIp {
    param([ValidateSet('4', '6')][string]$Family)
    foreach ($h in @("api$Family.ipify.org", "ipv$Family.icanhazip.com")) {
        try {
            $r = Invoke-RestMethod -Uri "https://$h" -TimeoutSec 6
            if ($r) { return ("$r").Trim() }
        } catch { }
    }
    return $null
}
# 非交互环境（irm | iex、CI）下 Read-Host 会抛错，统一降级到默认值
function Read-Answer {
    param([string]$Prompt, [string]$Default = '')
    try {
        $v = Read-Host $Prompt
        if ($v) { return "$v".Trim() }
        return $Default
    } catch { return $Default }
}

Write-Host ''
Write-Host "[*] 部署完成: $Dir"
Write-Host '[*] 正在探测本机出口 IP ...'
$Ip4 = Get-ExitIp '4'
$Ip6 = Get-ExitIp '6'

$Target = $null
$FamilyFlag = $null
if ($Ip4 -and $Ip6) {
    Write-Host '    检测到本机双栈出口：'
    Write-Host "      1) IPv4  $Ip4"
    Write-Host "      2) IPv6  $Ip6"
    $choice = Read-Answer '    用哪个作为入口 IP？[1/2]（默认 1）' '1'
    if ($choice -eq '2') { $Target = $Ip6; $FamilyFlag = '-6' } else { $Target = $Ip4 }
}
elseif ($Ip4) {
    $Target = $Ip4
    Write-Host "    检测到 IPv4: $Target"
}
elseif ($Ip6) {
    $Target = $Ip6
    $FamilyFlag = '-6'
    Write-Host "    检测到 IPv6: $Target"
}
else {
    Write-Host '    [!] 未能自动探测出口 IP，请手动填写'
    $Target = Read-Answer '    入口 IP'
}

Write-Host ''
if ($Target) {
    $runArg = @('auto', $Target, '--limit', '5')
    if ($FamilyFlag) { $runArg += $FamilyFlag }
    Write-Host "[*] 运行命令: cd `"$Dir`"; .\reality-checker-main.exe $($runArg -join ' ')"
    $go = Read-Answer '    现在开始扫描？[y/N]' 'n'
    if ($go -match '^(y|yes)$') {
        Set-Location $Dir
        & .\reality-checker-main.exe @runArg
    }
}
else {
    Write-Host "[*] 用法: cd `"$Dir`"; .\reality-checker-main.exe auto <你的VPS_IP> --limit 5"
}
