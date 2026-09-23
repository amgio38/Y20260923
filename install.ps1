# ProjectBoard 一鍵安裝腳本（Windows / PowerShell）。
#
# 用法：
#   Invoke-WebRequest -Uri https://raw.githubusercontent.com/amgio38/Y20260923/main/install.ps1 -OutFile install.ps1
#   .\install.ps1
#   （或先 clone 下來在本機跑：.\install.ps1）
#
# 做的事：檢查 git -> 確認有能用的 go（系統沒有、太舊、或壞掉就自己下載一份
# 乾淨的 go 工具鏈，不依賴這台機器剛好裝好對的環境）-> clone（或用現有
# checkout）-> go build -> 把 pb.exe 裝進一個目錄、提醒加進 PATH -> 印下一步該
# 做什麼。純 Go（modernc.org/sqlite 無 cgo），不需要額外的 C 工具鏈，Windows 上
# 不依賴 make（用 go build 直接編）。
#
# 這是公開在 GitHub 上的 repo，安裝的人在什麼機器、什麼帳號權限下跑都有可能，
# 所以走保守路線：不猜使用者想把原始碼放哪，一開始就問，預設值是「現在所在目
# 錄底下的 project_board 子目錄」，不是使用者的家目錄。目標目錄如果已經有內容、
# 又不是既有的 project_board checkout，直接中止，不覆蓋。
#
# 可用環境變數覆寫：
#   $env:PROJECT_BOARD_REPO      repo clone 網址（預設 origin）
#   $env:PROJECT_BOARD_HOME      原始碼＋資料要放哪裡（設了就不會互動詢問，預設：詢問使用者，預設值＝目前所在目錄底下的 project_board）
#   $env:PROJECT_BOARD_BINDIR    裝去哪個目錄（預設 $HOME\.local\bin，這是編譯好的執行檔，跟原始碼目錄分開）
#   $env:PROJECT_BOARD_GO_CACHE  獨立下載的 go 工具鏈放哪裡（預設 $HOME\.cache\project_board\go-toolchain）

$ErrorActionPreference = "Stop"

$Repo       = if ($env:PROJECT_BOARD_REPO)      { $env:PROJECT_BOARD_REPO }      else { "https://github.com/amgio38/Y20260923.git" }
$BinDir     = if ($env:PROJECT_BOARD_BINDIR)    { $env:PROJECT_BOARD_BINDIR }    else { Join-Path $HOME ".local\bin" }
$GoCacheDir = if ($env:PROJECT_BOARD_GO_CACHE)  { $env:PROJECT_BOARD_GO_CACHE }  else { Join-Path $HOME ".cache\project_board\go-toolchain" }
$MinGoMajor = 1
$MinGoMinor = 25

function Log($msg) { Write-Host "==> $msg" -ForegroundColor Green }
function Die($msg) { Write-Host "錯誤：$msg" -ForegroundColor Red; exit 1 }

function Need-Cmd($name, $hint) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        Die "找不到 $name，請先安裝（$hint）再重跑這支腳本。"
    }
}

function Test-GoVersionOk($verLine) {
    if ($verLine -notmatch 'go(\d+)\.(\d+)') { return $false }
    $major = [int]$Matches[1]
    $minor = [int]$Matches[2]
    return ($major -gt $MinGoMajor) -or ($major -eq $MinGoMajor -and $minor -ge $MinGoMinor)
}

# 系統上「go」這個名字能不能直接拿來用：找得到、真的能執行、版本夠新。任何一項
# 不成立就回傳 $null，不 Die——讓呼叫端 fallback 去下載一份乾淨的 go，而不是要
# 求使用者自己修好系統環境（PATH、損毀的安裝、公司政策擋執行檔…）才能繼續。
function Get-UsableSystemGo {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if (-not $cmd) { return $null }
    try {
        $verLine = & $cmd.Source version 2>&1
    } catch {
        Log "系統的 go（$($cmd.Source)）跑不起來：$_ —— 改用獨立下載的 go"
        return $null
    }
    if ($LASTEXITCODE -ne 0) {
        Log "系統的 go（$($cmd.Source)）跑不起來（exit $LASTEXITCODE）：$verLine —— 改用獨立下載的 go"
        return $null
    }
    if (-not (Test-GoVersionOk $verLine)) {
        Log "系統的 go 版本不夠新（$verLine，需要 >= $MinGoMajor.$MinGoMinor）——改用獨立下載的 go"
        return $null
    }
    return $cmd.Source
}

# 系統的 go 不能用時（沒裝、版本太舊、或跑不起來），下載官方 go.dev 的 Windows
# 工具鏈（checksum 驗證過），放在使用者自己的 cache 目錄，不動系統環境、不需要
# 系統管理員權限。這是標準做法（跟 nvm/rustup 處理工具鏈的方式一樣）：安裝腳本
# 不該假設「這台機器剛好已經裝好對的工具鏈」，而是自己想辦法生出一份能用的。
function Install-GoToolchain {
    if (-not [Environment]::Is64BitOperatingSystem) {
        Die "系統的 go 不能用，且這台機器是 32 位元，沒有自動下載對應版本，請手動安裝 go >= $MinGoMajor.$MinGoMinor（https://go.dev/dl/）後再重跑。"
    }
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

    $goBin = Join-Path $GoCacheDir "go\bin\go.exe"
    if (Test-Path $goBin) {
        try {
            $verLine = & $goBin version 2>&1
            if ($LASTEXITCODE -eq 0 -and (Test-GoVersionOk $verLine)) {
                Log "沿用先前下載好的獨立 go（$goBin）"
                return $goBin
            }
        } catch {}
    }

    Log "系統沒有可用的 go，改用官方 go.dev 下載一份乾淨的工具鏈（放在 $GoCacheDir，不影響系統）"
    $manifest = Invoke-RestMethod -Uri "https://go.dev/dl/?mode=json" -UseBasicParsing

    $selected = $null
    foreach ($release in $manifest) {
        $match = $release.files |
            Where-Object { $_.os -eq "windows" -and $_.arch -eq $arch -and $_.kind -eq "archive" -and $_.filename -like "*.zip" } |
            Select-Object -First 1
        if ($match) { $selected = $match; break }
    }
    if (-not $selected) {
        Die "在 go.dev 的版本清單裡找不到 windows/$arch 的下載檔，請手動安裝 go >= $MinGoMajor.$MinGoMinor（https://go.dev/dl/）。"
    }

    New-Item -ItemType Directory -Force -Path $GoCacheDir | Out-Null
    $tmpZip = Join-Path ([System.IO.Path]::GetTempPath()) $selected.filename
    Log "下載 https://go.dev/dl/$($selected.filename)"
    Invoke-WebRequest -Uri "https://go.dev/dl/$($selected.filename)" -OutFile $tmpZip -UseBasicParsing

    $gotSha = (Get-FileHash -Path $tmpZip -Algorithm SHA256).Hash.ToLower()
    if ($gotSha -ne $selected.sha256) {
        Remove-Item -Force $tmpZip -ErrorAction SilentlyContinue
        Die "下載的 go 工具鏈 checksum 對不上（預期 $($selected.sha256)，拿到 $gotSha），可能是下載損毀或被竄改，已中止安裝。"
    }

    $goDir = Join-Path $GoCacheDir "go"
    if (Test-Path $goDir) { Remove-Item -Recurse -Force $goDir }
    Expand-Archive -Path $tmpZip -DestinationPath $GoCacheDir -Force
    Remove-Item -Force $tmpZip -ErrorAction SilentlyContinue

    if (-not (Test-Path $goBin)) {
        Die "解壓後找不到可執行的 $goBin，go.dev 的封裝格式可能變了，請手動安裝 go（https://go.dev/dl/）。"
    }
    Log "獨立 go 工具鏈就緒：$goBin（$(& $goBin version)）"
    return $goBin
}

Log "檢查必要工具（git／go）"
Need-Cmd git "https://git-scm.com/downloads"

$GoBin = Get-UsableSystemGo
if (-not $GoBin) { $GoBin = Install-GoToolchain }
Log "使用 go：$GoBin"

# 預設安裝目錄＝使用者現在所在目錄底下的 project_board 子目錄（不是 $HOME），
# 誰在哪裡執行這支腳本，原始碼就裝在那個目錄下面。
$DefaultHomeDir = Join-Path (Get-Location) "project_board"

$cwdGoMod = Join-Path (Get-Location) "go.mod"
if ((Test-Path $cwdGoMod) -and (Select-String -Path $cwdGoMod -Pattern '^module project_board$' -Quiet)) {
    Log "偵測到已經在 project_board 的 checkout 裡，直接用這份，不重新 clone"
    $HomeDir = (Get-Location).Path
} elseif ($env:PROJECT_BOARD_HOME) {
    $HomeDir = $env:PROJECT_BOARD_HOME
    Log "用環境變數指定的安裝目錄：$HomeDir"
} else {
    if ([Console]::IsInputRedirected) {
        Log "非互動模式（stdin 被重導向，問不了），用預設安裝目錄：$DefaultHomeDir"
        Log "要指定別的路徑：設定 `$env:PROJECT_BOARD_HOME 再重跑，或先把腳本存下來本機執行再回答。"
        $HomeDir = $DefaultHomeDir
    } else {
        $inputDir = Read-Host "安裝目錄（原始碼＋資料放這裡，直接按 Enter 用預設值 $DefaultHomeDir）"
        $HomeDir = if ([string]::IsNullOrWhiteSpace($inputDir)) { $DefaultHomeDir } else { $inputDir }
    }
}

New-Item -ItemType Directory -Force -Path $HomeDir | Out-Null
$HomeDir = (Resolve-Path $HomeDir).Path

if (Test-Path (Join-Path $HomeDir ".git")) {
    Log "$HomeDir 已經是 git checkout，跑 git pull 更新"
    git -C $HomeDir pull --ff-only
} elseif ((Get-ChildItem -Path $HomeDir -Force | Measure-Object).Count -gt 0) {
    Die "$HomeDir 不是空目錄，而且不是既有的 project_board checkout。安全起見不會覆蓋既有內容——換一個空目錄，或指到既有的 checkout，再重跑。"
} else {
    Log "clone $Repo 到 $HomeDir"
    git clone $Repo $HomeDir
}

Set-Location $HomeDir

Log "go build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Force -Path (Join-Path $HomeDir "bin") | Out-Null
& $GoBin build -o (Join-Path $HomeDir "bin\pb.exe") ./cmd/pb

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
