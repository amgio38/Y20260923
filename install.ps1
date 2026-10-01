# ProjectBoard 一鍵安裝腳本（Windows / PowerShell）。
#
# 用法（Windows PowerShell 5.1 或 PowerShell 7 都可以）：
#   Invoke-WebRequest -Uri https://raw.githubusercontent.com/amgio38/Y20260923/main/install.ps1 -OutFile install.ps1
#   powershell -ExecutionPolicy Bypass -File .\install.ps1
#   指定版本：… -File .\install.ps1 -Version v0.20260928.006
#   強制從原始碼編譯：… -File .\install.ps1 -FromSource
#
# 為什麼用 -ExecutionPolicy Bypass：Windows 預設的執行原則常常是 Restricted，直接
# .\install.ps1 會被擋成「無法載入，因為這個系統上已停用指令碼執行」。Bypass 只對這一次
# 啟動的 PowerShell 有效，不會改系統設定。
#
# 做的事（預設走「預編譯版」，不需要 Go、不需要 git）：
#   1. 判斷 CPU（amd64／arm64）；
#   2. 從 GitHub Releases 下載對應的 zip，驗 sha256 對得上才繼續；
#   3. 先檢查 zip 的內容（成員白名單、沒有路徑跳脫），解到暫存區，確認 pb.exe 真的跑得起來，
#      才放進安裝目錄；
#   4. 印下一步該做什麼。
# 找不到對應的預編譯版（沒有發布、網路抓不到）就退回「原始碼編譯」：檢查 git → 確認有
# 能用的 go（沒有、太舊、或壞掉就自己下載一份乾淨的）→ clone（或用現有 checkout）→ go build。
# 純 Go（modernc.org/sqlite 無 cgo），不需要 C 工具鏈，也不依賴 make。
#
# 安裝目錄（HomeDir）＝專案根：放 bin\pb.exe、docs，以及你的資料 var\board.db。
# 這是公開在 GitHub 上的 repo，安裝的人在什麼機器、什麼帳號權限下跑都有可能，所以走保守
# 路線：不猜使用者想把東西放哪，一開始就問（預設＝目前所在目錄底下的 project_board 子目錄）；
# 目標目錄如果已經有內容、又不是既有的 project_board，直接中止，不覆蓋。重跑一次＝升級，
# var\ 和 owners.txt 不會被動到。不需要系統管理員權限。
#
# 可用環境變數覆寫：
#   $env:PROJECT_BOARD_REPO          repo clone 網址（預設 https://github.com/amgio38/Y20260923.git）
#   $env:PROJECT_BOARD_HOME          安裝目錄（設了就不會互動詢問）
#   $env:PROJECT_BOARD_BINDIR        （選用）另外把 pb.exe 複製一份到這個 PATH 目錄。不設的話 pb.exe 留在
#                                    <安裝目錄>\bin，把那個目錄加進 PATH 即可——這樣 pb 才找得到自己的專案根
#   $env:PROJECT_BOARD_VERSION       要裝的 release 標籤（預設：最新版）
#   $env:PROJECT_BOARD_RELEASE_BASE  release 下載位置的前綴（預設由 REPO 推出；內部鏡像用）
#   $env:PROJECT_BOARD_GO_CACHE      獨立下載的 go 工具鏈放哪裡（預設 $HOME\.cache\project_board\go-toolchain）
#   $env:PROJECT_BOARD_ARCH          明確指定 CPU（amd64／arm64），不指定就自己偵測

param(
    [string]$Version = "",
    [switch]$FromSource
)

$ErrorActionPreference = "Stop"

# Windows PowerShell 5.1 預設可能只協商到 TLS 1.0/1.1，GitHub 會直接斷線。這一行把
# TLS 1.2 加進允許的協定（PowerShell 7 不需要，但加了無害）。
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}

$MinGoMajor = 1
$MinGoMinor = 25
$DefaultRepo = "https://github.com/amgio38/Y20260923.git"

function Log($msg) { Write-Host "==> $msg" -ForegroundColor Green }
function Warn($msg) { Write-Host "注意：$msg" -ForegroundColor Yellow }
function Die($msg) { Write-Host "錯誤：$msg" -ForegroundColor Red; exit 1 }

function Need-Cmd($name, $hint) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        Die "找不到 $name，請先安裝（$hint）再重跑這支腳本。"
    }
}

# 下載 URL 到 DEST。只收 https（或 file:，給測試和離線／內部共用資料夾的鏡像用）；逾時有上限。
# file: 自己處理，不交給 Invoke-WebRequest：PowerShell 7 的 Invoke-WebRequest 不支援 file: 網址。
function Get-Download($url, $dest) {
    if ($url -notmatch '^(https://|file:)') { Die "下載網址必須是 https：$url" }
    if ($url -like 'file:*') {
        $src = ([System.Uri]$url).LocalPath
        if (-not (Test-Path -LiteralPath $src -PathType Leaf)) { throw "找不到檔案：$src" }
        Copy-Item -LiteralPath $src -Destination $dest -Force
        return
    }
    Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing -TimeoutSec 600
}

# ---------------------------------------------------------------------------
# 預編譯版
# ---------------------------------------------------------------------------

# 這台機器的 CPU（原始字串）。優先順序：
#   1. $env:PROJECT_BOARD_ARCH——明確指定（要替另一種 CPU 準備安裝目錄、或測試時用）；
#   2. .NET 回報的「作業系統」架構——最可靠，而且 32 位元的 PowerShell 跑在 64 位元的
#      Windows 上時也會說實話；
#   3. 環境變數 PROCESSOR_ARCHITEW6432／PROCESSOR_ARCHITECTURE——舊版 .NET 的退路。
#      （曾在 CI 的 Windows 上看到這個環境變數是空的，所以不能只靠它。）
function Get-RawArch {
    $raw = $env:PROJECT_BOARD_ARCH
    if (-not $raw) {
        try { $raw = [string][System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture } catch { $raw = $null }
    }
    if (-not $raw) { $raw = $env:PROCESSOR_ARCHITEW6432 }
    if (-not $raw) { $raw = $env:PROCESSOR_ARCHITECTURE }
    return $raw
}

# 對應的發布名稱（amd64／arm64），認不得回 $null。
function Get-ReleaseArch {
    switch -Regex (Get-RawArch) {
        '^(AMD64|x86_64|X64)$' { return "amd64" }
        '^(ARM64|aarch64)$' { return "arm64" }
        default { return $null }
    }
}

# zip 裡允許出現的頂層名稱。其他東西一律拒絕：預編譯版是我們自己打的包，多出任何東西
# 都代表不是我們打的。
$AllowedTop = @("bin", "go.mod", "LICENSE", "README.md", "owners.example.txt", "docs", "skill")

# 解開之前先檢查 zip 的每個成員：沒有絕對路徑、沒有 ..、頂層在白名單內、bin\ 底下只有 pb.exe。
function Test-ArchiveMembers($zipPath) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName -replace '\\', '/'
            $name = $name -replace '^\./', ''
            if (-not $name -or $name -eq '.') { continue }
            if ($name.StartsWith('/') -or $name -match '(^|/)\.\.(/|$)' -or $name -match '^[A-Za-z]:') {
                Die "壓縮檔含不安全的路徑，不安裝：$($entry.FullName)"
            }
            $top = ($name -split '/')[0]
            if ($AllowedTop -notcontains $top) {
                Die "壓縮檔含不認得的項目，不安裝：$($entry.FullName)"
            }
            if ($top -eq "bin" -and $name.TrimEnd('/') -notin @("bin", "bin/pb.exe")) {
                Die "壓縮檔的 bin/ 底下只能有 pb.exe，不安裝：$($entry.FullName)"
            }
        }
    } finally {
        $zip.Dispose()
    }
}

# 下載預編譯版和它的 checksum 到暫存區，驗證，解開，檢查，放進 $HomeDir。
# 回傳 $true＝裝好了；$false＝沒有可用的預編譯版（呼叫端退回原始碼編譯）。
# 抓到了卻驗不過（沒有 checksum、對不上、內容不安全、pb.exe 跑不起來）是 Die，不會悄悄改裝別的東西。
function Install-Prebuilt($homeDir, $tmpDir, $repo, $version) {
    $arch = Get-ReleaseArch
    if (-not $arch) {
        Warn "這個 CPU（$(Get-RawArch)）沒有預編譯版，改用原始碼編譯"
        return $false
    }

    if ($env:PROJECT_BOARD_RELEASE_BASE) {
        $base = $env:PROJECT_BOARD_RELEASE_BASE.TrimEnd('/')
    } elseif ($repo -match '^https://github\.com/([A-Za-z0-9._-]+/[A-Za-z0-9._-]+?)(\.git)?/?$') {
        $base = "https://github.com/$($Matches[1])/releases"
    } else {
        Warn "PROJECT_BOARD_REPO 不是 github.com 的網址，沒辦法推出 release 位置，改用原始碼編譯（要用預編譯版：設 PROJECT_BOARD_RELEASE_BASE）"
        return $false
    }
    if ($version) { $base = "$base/download/$version" } else { $base = "$base/latest/download" }

    $asset = "projectboard-windows-$arch.zip"
    $url = "$base/$asset"
    $zipPath = Join-Path $tmpDir $asset
    $shaPath = "$zipPath.sha256"

    Log "下載 $url"
    try {
        Get-Download $url $zipPath
    } catch {
        Warn "抓不到預編譯版（還沒發布？網路不通？），改用原始碼編譯"
        return $false
    }
    # 抓到 zip 卻沒有 checksum，是發布出了問題，不是「沒有預編譯版」：不退回、不裝。
    $haveSha = $true
    try { Get-Download "$url.sha256" $shaPath } catch { $haveSha = $false }
    if (-not $haveSha -or -not (Test-Path $shaPath) -or ((Get-Item $shaPath).Length -eq 0)) {
        Die "找不到（或是空的）$asset 的 checksum，不安裝沒驗證過的檔案。可加 -FromSource 改用原始碼編譯。"
    }

    $shaLine = (Get-Content -Path $shaPath -TotalCount 1)
    $parts = $shaLine -split '\s+', 2
    $want = $parts[0].ToLower()
    $wantName = if ($parts.Count -gt 1) { $parts[1].Trim().TrimStart('*') } else { "" }
    $got = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
    if (-not $want -or $got -ne $want) { Die "$asset 的 sha256 對不上（預期 $want，實際 $got），下載可能損毀，不安裝。" }
    if ($wantName -ne $asset) { Die "checksum 檔指的是 '$wantName'，不是 $asset，不安裝。" }

    Test-ArchiveMembers $zipPath
    $extract = Join-Path $tmpDir "extract"
    New-Item -ItemType Directory -Force -Path $extract | Out-Null
    Expand-Archive -Path $zipPath -DestinationPath $extract -Force

    $newExe = Join-Path $extract "bin\pb.exe"
    if (-not (Test-Path $newExe)) { Die "壓縮檔裡沒有 bin\pb.exe，不安裝。" }
    # 放進安裝目錄之前先確認它真的跑得起來，而且報的是版號。壞掉的檔案不該蓋掉能用的舊版。
    # 整段輸出收完再取第一行：用 Select-Object -First 1 提早截斷管線的話，原生程式會被中斷，
    # $LASTEXITCODE 就不可靠（成功的 pb 也會看起來像失敗）。
    $verLine = $null
    $verExit = 1
    try {
        $verOut = & $newExe version 2>&1
        $verExit = $LASTEXITCODE
        $verLine = ($verOut | Select-Object -First 1)
    } catch { $verLine = $null }
    if ($verExit -ne 0 -or "$verLine" -notmatch '^V\d+\.\d{8}\.\d{3}$') {
        Die "下載的 pb.exe 跑不起來，或沒有回報版號（pb version 輸出：'$verLine'）。不安裝。"
    }

    # 升級時只覆蓋我們發布的檔案；var\（你的資料庫）和 owners.txt 不在 zip 裡，不會被動到。
    New-Item -ItemType Directory -Force -Path (Join-Path $homeDir "bin") | Out-Null
    $destExe = Join-Path $homeDir "bin\pb.exe"
    try {
        Copy-Item -Force $newExe $destExe
    } catch {
        Die "換不掉 $destExe（pb 還在跑？）。先把用到 pb.exe 的 dashboard／MCP 關掉再重跑。原因：$_"
    }
    Get-ChildItem -Path $extract -Force | Where-Object { $_.Name -ne "bin" } | ForEach-Object {
        Copy-Item -Path $_.FullName -Destination $homeDir -Recurse -Force
    }
    Log "預編譯版 $verLine 已放進 $homeDir"
    return $true
}

# ---------------------------------------------------------------------------
# 原始碼編譯（沒有預編譯版時的退路，也是開發者的路）
# ---------------------------------------------------------------------------

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
function Install-GoToolchain($goCacheDir) {
    if (-not [Environment]::Is64BitOperatingSystem) {
        Die "系統的 go 不能用，且這台機器是 32 位元，沒有自動下載對應版本，請手動安裝 go >= $MinGoMajor.$MinGoMinor（https://go.dev/dl/）後再重跑。"
    }
    $arch = Get-ReleaseArch
    if (-not $arch) { Die "系統的 go 不能用，且這個 CPU 沒有自動下載對應版本，請手動安裝 go >= $MinGoMajor.$MinGoMinor（https://go.dev/dl/）後再重跑。" }

    $goBin = Join-Path $goCacheDir "go\bin\go.exe"
    if (Test-Path $goBin) {
        try {
            $verLine = & $goBin version 2>&1
            if ($LASTEXITCODE -eq 0 -and (Test-GoVersionOk $verLine)) {
                Log "沿用先前下載好的獨立 go（$goBin）"
                return $goBin
            }
        } catch {}
    }

    Log "系統沒有可用的 go，改用官方 go.dev 下載一份乾淨的工具鏈（放在 $goCacheDir，不影響系統）"
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

    New-Item -ItemType Directory -Force -Path $goCacheDir | Out-Null
    $tmpZip = Join-Path ([System.IO.Path]::GetTempPath()) $selected.filename
    Log "下載 https://go.dev/dl/$($selected.filename)"
    Invoke-WebRequest -Uri "https://go.dev/dl/$($selected.filename)" -OutFile $tmpZip -UseBasicParsing

    $gotSha = (Get-FileHash -Path $tmpZip -Algorithm SHA256).Hash.ToLower()
    if ($gotSha -ne $selected.sha256) {
        Remove-Item -Force $tmpZip -ErrorAction SilentlyContinue
        Die "下載的 go 工具鏈 checksum 對不上（預期 $($selected.sha256)，拿到 $gotSha），可能是下載損毀或被竄改，已中止安裝。"
    }

    $goDir = Join-Path $goCacheDir "go"
    if (Test-Path $goDir) { Remove-Item -Recurse -Force $goDir }
    Expand-Archive -Path $tmpZip -DestinationPath $goCacheDir -Force
    Remove-Item -Force $tmpZip -ErrorAction SilentlyContinue

    if (-not (Test-Path $goBin)) {
        Die "解壓後找不到可執行的 $goBin，go.dev 的封裝格式可能變了，請手動安裝 go（https://go.dev/dl/）。"
    }
    Log "獨立 go 工具鏈就緒：$goBin（$(& $goBin version)）"
    return $goBin
}

function Install-FromSource($homeDir, $repo, $version, $goCacheDir) {
    Log "檢查必要工具（git／go）"
    Need-Cmd git "https://git-scm.com/downloads"

    $goBin = Get-UsableSystemGo
    if (-not $goBin) { $goBin = Install-GoToolchain $goCacheDir }
    Log "使用 go：$goBin"

    if (Test-Path (Join-Path $homeDir ".git")) {
        Log "$homeDir 已經是 git checkout，跑 git pull 更新"
        git -C $homeDir pull --ff-only
        if ($LASTEXITCODE -ne 0) { Die "git pull 失敗" }
    } elseif ((Get-ChildItem -Path $homeDir -Force | Measure-Object).Count -gt 0) {
        Die "$homeDir 不是空目錄，而且不是 git checkout，沒辦法用原始碼更新。換一個空目錄，或指到既有的 checkout，再重跑。"
    } else {
        Log "clone $repo 到 $homeDir"
        if ($version) {
            git clone --depth 1 --branch=$version $repo $homeDir
        } else {
            git clone $repo $homeDir
        }
        if ($LASTEXITCODE -ne 0) { Die "git clone 失敗" }
    }

    Push-Location $homeDir
    try {
        Log "go build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
        $env:CGO_ENABLED = "0"
        New-Item -ItemType Directory -Force -Path (Join-Path $homeDir "bin") | Out-Null
        & $goBin build -o (Join-Path $homeDir "bin\pb.exe") ./cmd/pb
        if ($LASTEXITCODE -ne 0) { Die "go build 失敗" }
    } finally {
        Pop-Location
    }
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------

function Test-IsProjectBoardDir($dir) {
    $gomod = Join-Path $dir "go.mod"
    return (Test-Path $gomod) -and (Select-String -Path $gomod -Pattern '^module project_board$' -Quiet)
}

function Main {
    $repo = if ($env:PROJECT_BOARD_REPO) { $env:PROJECT_BOARD_REPO } else { $DefaultRepo }
    $version = if ($Version) { $Version } elseif ($env:PROJECT_BOARD_VERSION) { $env:PROJECT_BOARD_VERSION } else { "" }

    # 標籤和 repo 網址會被拼進下載網址和 git clone，而且可以來自環境變數：只放行該有的字元，
    # 特別擋掉「-」開頭（會被 git 當成選項）和「..」。
    if ($version -and ($version -notmatch '^[A-Za-z0-9][A-Za-z0-9._+-]*$' -or $version -match '\.\.')) {
        Die "release 標籤 '$version' 不合法（只能有英數字和 . _ + -）"
    }
    if ($repo.StartsWith('-') -or $repo -match '\s') { Die "PROJECT_BOARD_REPO 不合法：$repo" }
    if ($env:PROJECT_BOARD_RELEASE_BASE -and $env:PROJECT_BOARD_RELEASE_BASE -notmatch '^(https://|file:)\S+$') {
        Die "PROJECT_BOARD_RELEASE_BASE 必須是 https:// 開頭：$($env:PROJECT_BOARD_RELEASE_BASE)"
    }

    $userHome = $HOME
    if (-not $userHome) { Die "找不到使用者的家目錄（`$HOME 是空的）。請設 PROJECT_BOARD_HOME。" }
    $binDir = $env:PROJECT_BOARD_BINDIR   # 選用：另外複製一份 pb.exe 去的地方
    $goCacheDir = if ($env:PROJECT_BOARD_GO_CACHE) { $env:PROJECT_BOARD_GO_CACHE } else { Join-Path $userHome ".cache\project_board\go-toolchain" }

    # 預設安裝目錄＝使用者現在所在目錄底下的 project_board 子目錄（不是 $HOME）。
    $defaultHomeDir = Join-Path (Get-Location) "project_board"

    # 明確指定的優先：人在某個 project_board 目錄裡、卻特地設了 PROJECT_BOARD_HOME，
    # 要的是指定的那個，不是悄悄裝回目前這個。
    if ($env:PROJECT_BOARD_HOME) {
        $homeDir = $env:PROJECT_BOARD_HOME
        Log "用環境變數指定的安裝目錄：$homeDir"
    } elseif (Test-IsProjectBoardDir (Get-Location).Path) {
        Log "偵測到已經在 project_board 目錄裡，直接用這份"
        $homeDir = (Get-Location).Path
    } elseif ([Console]::IsInputRedirected) {
        Log "非互動模式（stdin 被重導向，問不了），用預設安裝目錄：$defaultHomeDir"
        Log "要指定別的路徑：設定 `$env:PROJECT_BOARD_HOME 再重跑，或先把腳本存下來本機執行再回答。"
        $homeDir = $defaultHomeDir
    } else {
        $inputDir = Read-Host "安裝目錄（原始碼＋資料放這裡，直接按 Enter 用預設值 $defaultHomeDir）"
        $homeDir = if ([string]::IsNullOrWhiteSpace($inputDir)) { $defaultHomeDir } else { $inputDir }
    }

    try {
        New-Item -ItemType Directory -Force -Path $homeDir | Out-Null
    } catch {
        Die "建不起安裝目錄 $homeDir（沒有權限？）。換一個你有權限的目錄，例如 `$env:PROJECT_BOARD_HOME = `"`$HOME\project_board`"。原因：$_"
    }
    $homeDir = (Resolve-Path $homeDir).Path

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("project_board-install-" + [System.Guid]::NewGuid().ToString("N").Substring(0, 8))
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
    try {
        $hasGit = Test-Path (Join-Path $homeDir ".git")
        $isEmpty = ((Get-ChildItem -Path $homeDir -Force | Measure-Object).Count -eq 0)
        if ($hasGit) {
            # 開發者的 checkout：升級就是 git pull＋編譯，不拿預編譯版去蓋原始碼目錄。
            Install-FromSource $homeDir $repo $version $goCacheDir
        } elseif ($isEmpty -or (Test-IsProjectBoardDir $homeDir)) {
            # 全新（空目錄）或之前用預編譯版裝過的目錄：下載預編譯版；沒有就退回原始碼編譯。
            $done = $false
            if (-not $FromSource) { $done = Install-Prebuilt $homeDir $tmpDir $repo $version }
            if (-not $done) { Install-FromSource $homeDir $repo $version $goCacheDir }
        } else {
            Die "$homeDir 不是空目錄，而且不是既有的 project_board。安全起見不會覆蓋既有內容——換一個空目錄，或指到既有的安裝，再重跑。"
        }
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }

    $exe = Join-Path $homeDir "bin\pb.exe"
    if (-not (Test-Path $exe)) { Die "安裝完成但找不到 $exe" }

    # pb 要從 <專案根>\bin 執行，才推得出專案根（資料庫、owners.txt 的預設位置）。所以預設
    # 不複製，叫使用者把 <安裝目錄>\bin 加進 PATH；真的要複製（PROJECT_BOARD_BINDIR）才複製，
    # 並說明代價。
    $pathBin = Join-Path $homeDir "bin"
    if ($binDir) {
        New-Item -ItemType Directory -Force -Path $binDir | Out-Null
        Copy-Item -Force $exe (Join-Path $binDir "pb.exe")
        Warn "已另外複製一份到 $binDir\pb.exe。複製出去的 pb 推不出專案根，預設資料庫會變成『目前目錄』的 var\board.db，請用 --db 或環境變數 PB_DB 明確指定。"
        $pathBin = $binDir
    }
    $pathDirs = $env:Path -split ";"
    if ($pathDirs -notcontains $pathBin) {
        Log "$pathBin 還不在 PATH 裡，把它加進使用者 PATH（PowerShell，一次性，之後開新視窗才生效）："
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `$env:Path + ';$pathBin', 'User')"
    }

    # 已經有 dashboard 在跑的話，它還在用舊的執行檔（記憶體裡的）。這裡不替使用者重啟，因為可能正有 agent 在用它。
    $pidFile = Join-Path $homeDir "var\serve.pid"
    if (Test-Path $pidFile) {
        $oldPid = (Get-Content $pidFile -TotalCount 1)
        if ($oldPid -and (Get-Process -Id ([int]($oldPid -replace '\D', '')) -ErrorAction SilentlyContinue)) {
            Warn "你的 dashboard（pid $oldPid）還在跑舊版。等沒人在用的時候把它關掉再重啟。"
        }
    }

    Write-Host ""
    Write-Host "安裝完成。下一步："
    Write-Host ""
    Write-Host "  1. 啟動 dashboard（前景跑；Windows 沒有 service.sh，用工作排程器/NSSM 常駐是另外的事）："
    Write-Host "       $exe serve"
    Write-Host "     （預設資料庫就是 $homeDir\var\board.db）"
    Write-Host ""
    Write-Host "  2. 接 MCP（給 harness/agent 用的工具介面，stdio）："
    Write-Host "       $exe mcp"
    Write-Host "     詳細的 MCP client 設定／可用工具清單見 $homeDir\README.md 『MCP 介面』一節。"
    Write-Host ""
    Write-Host "  3. 設定你自己團隊的 owner 名冊（沒設定就只認 unassigned）："
    Write-Host "       Copy-Item `"$homeDir\owners.example.txt`" `"$homeDir\owners.txt`"   # 然後改成你們的名字"
    Write-Host ""
    Write-Host "  4. git commit 訊息尾端帶單號（例：#Y20260920/ISSUE-XXX）才會自動掛連結，"
    Write-Host "     細節見 docs\GIT_INTEGRATION.md。"
    Write-Host ""
}

Main
