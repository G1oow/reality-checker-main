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

Write-Host ''
Write-Host "[*] 部署完成: $Dir"
Write-Host "[*] 一键运行: cd `"$Dir`"; .\reality-checker-main.exe auto <你的VPS_IP> --limit 5"
