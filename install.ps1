# ProjectBoard 一鍵安裝腳本（Windows / PowerShell）。
#
# 用法：
#   Invoke-WebRequest -Uri https://raw.githubusercontent.com/amgio38/Y20260923/main/install.ps1 -OutFile install.ps1
#   .\install.ps1
#   （或先 clone 下來在本機跑：.\install.ps1）
#
# 做的事：檢查 git/go -> clone（或用現有 checkout）-> go build -> 把 pb.exe 裝進
# 一個目錄、提醒加進 PATH -> 印下一步該做什麼。純 Go（modernc.org/sqlite 無 cgo），
# 不需要額外的 C 工具鏈，Windows 上不依賴 make（用 go build 直接編）。
#
# 可用環境變數覆寫：
#   $env:PROJECT_BOARD_REPO    repo clone 網址（預設 origin）
#   $env:PROJECT_BOARD_HOME    clone 到哪裡（預設 $HOME\project_board）
#   $env:PROJECT_BOARD_BINDIR  裝去哪個目錄（預設 $HOME\.local\bin）

$ErrorActionPreference = "Stop"

$Repo    = if ($env:PROJECT_BOARD_REPO)   { $env:PROJECT_BOARD_REPO }   else { "https://github.com/amgio38/Y20260923.git" }
$HomeDir = if ($env:PROJECT_BOARD_HOME)   { $env:PROJECT_BOARD_HOME }   else { Join-Path $HOME "project_board" }
$BinDir  = if ($env:PROJECT_BOARD_BINDIR) { $env:PROJECT_BOARD_BINDIR } else { Join-Path $HOME ".local\bin" }
$MinGoMajor = 1
$MinGoMinor = 25

function Log($msg)  { Write-Host "==> $msg" -ForegroundColor Green }
function Die($msg)  { Write-Host "錯誤：$msg" -ForegroundColor Red; exit 1 }

function Need-Cmd($name, $hint) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        Die "找不到 $name，請先安裝（$hint）再重跑這支腳本。"
    }
}

function Check-GoVersion {
    $verLine = (go version)
    if ($verLine -notmatch 'go(\d+)\.(\d+)') {
        Die "看不懂 go version 的輸出：$verLine"
    }
    $major = [int]$Matches[1]
    $minor = [int]$Matches[2]
    if ($major -lt $MinGoMajor -or ($major -eq $MinGoMajor -and $minor -lt $MinGoMinor)) {
        Die "go 版本 $major.$minor 太舊，需要 >= $MinGoMajor.$MinGoMinor（go.mod 要求）。"
    }
}

Log "檢查必要工具（git／go）"
Need-Cmd git "https://git-scm.com/downloads"
Need-Cmd go  "https://go.dev/dl/"
Check-GoVersion

$cwdGoMod = Join-Path (Get-Location) "go.mod"
if ((Test-Path $cwdGoMod) -and (Select-String -Path $cwdGoMod -Pattern '^module project_board$' -Quiet)) {
    Log "偵測到已經在 project_board 的 checkout 裡，直接用這份，不重新 clone"
    $HomeDir = (Get-Location).Path
} elseif (Test-Path (Join-Path $HomeDir ".git")) {
    Log "$HomeDir 已經是 git checkout，跑 git pull 更新"
    git -C $HomeDir pull --ff-only
} else {
    Log "clone $Repo 到 $HomeDir"
    git clone $Repo $HomeDir
}

Set-Location $HomeDir

Log "go build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Force -Path (Join-Path $HomeDir "bin") | Out-Null
go build -o (Join-Path $HomeDir "bin\pb.exe") ./cmd/pb

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Copy-Item -Force (Join-Path $HomeDir "bin\pb.exe") (Join-Path $BinDir "pb.exe")
Log "已裝到 $BinDir\pb.exe"

$pathDirs = $env:Path -split ";"
if ($pathDirs -notcontains $BinDir) {
    Log "$BinDir 還不在 PATH 裡，把它加進使用者 PATH（PowerShell，一次性）："
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `$env:Path + ';$BinDir', 'User')"
}

$dbPath = Join-Path $HomeDir "var\board.db"
Write-Host ""
Write-Host "安裝完成。下一步："
Write-Host ""
Write-Host "  1. 啟動 dashboard（前景跑；Windows 沒有 service.sh，用工作排程器/NSSM 常駐是另外的事）："
Write-Host "       $BinDir\pb.exe serve --db `"$dbPath`""
Write-Host ""
Write-Host "  2. 接 MCP（給 harness/agent 用的工具介面，stdio）："
Write-Host "       $BinDir\pb.exe mcp --db `"$dbPath`""
Write-Host "     詳細的 MCP client 設定／可用工具清單見 README.md 『MCP 介面』一節。"
Write-Host ""
Write-Host "  3. git commit 訊息尾端帶單號（例：#Y20260920/ISSUE-XXX）才會自動掛連結，"
Write-Host "     細節見 docs/GIT_INTEGRATION.md 與 scripts/install-git-hooks.sh。"
Write-Host ""
