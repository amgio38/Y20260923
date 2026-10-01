# install.ps1 的端到端測試，網路換成本機檔案（file:// 的 release 目錄）。
#
# 跑的是真正的 install.ps1——參數與輸入檢查、CPU 判斷、checksum 驗證、zip 成員白名單、
# pb.exe 能不能跑的檢查、安裝目錄的處理——對著這裡用真的 pb 打出來的 zip。重點跟 install-sh.test.sh
# 一樣：失敗時的決定。沒驗證過、被動過手腳、跑不起來的檔案，絕對不能進安裝目錄。
#
# 在 Windows 與 Linux（PowerShell 7）都能跑：zip 用 .NET 的 ZipFile 打，Linux 上會保留執行權限，
# 所以「真的執行裝好的 pb」這一步兩邊都測得到。
#
# 用法：pwsh -NoProfile -File scripts/install-ps1.test.ps1
#       powershell -NoProfile -ExecutionPolicy Bypass -File scripts\install-ps1.test.ps1

$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Split-Path -Parent $here
$installer = if ($env:PB_INSTALLER) { $env:PB_INSTALLER } else { Join-Path $root "install.ps1" }
$self = (Get-Process -Id $PID).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("pb-ps1-test-" + [System.Guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Force -Path $work | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem

$script:fail = 0
function Ok($name) { Write-Host "ok: $name" }
function Bad($name, $detail) {
    Write-Host "FAIL: $name" -ForegroundColor Red
    if ($detail) { Write-Host "  $detail" -ForegroundColor Red }
    # 在 GitHub Actions 裡，同時印成 ::error:: 註解：公開的 check-runs API 讀得到註解，
    # 不用登入就能看到失敗的原因（完整的步驟記錄要登入才下載得到）。
    if ($env:GITHUB_ACTIONS) {
        $msg = "$name | $detail"
        if ($msg.Length -gt 1500) { $msg = $msg.Substring(0, 1500) + "..." }
        $msg = $msg -replace "`r?`n", "%0A"
        Write-Host "::error::$msg"
    }
    $script:fail = 1
}
function Check($cond, $name, $detail) { if ($cond) { Ok $name } else { Bad $name $detail } }

try {
    # --- 一份真的 release zip ---------------------------------------------------
    $stage = Join-Path $work "stage"
    New-Item -ItemType Directory -Force -Path (Join-Path $stage "bin") | Out-Null
    $env:CGO_ENABLED = "0"
    Push-Location $root
    try { & go build -o (Join-Path $stage "bin\pb.exe") ./cmd/pb } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "go build 失敗" }
    foreach ($f in @("go.mod", "LICENSE", "README.md", "owners.example.txt")) { Copy-Item (Join-Path $root $f) $stage }
    Copy-Item -Recurse (Join-Path $root "docs") (Join-Path $stage "docs")
    Copy-Item -Recurse (Join-Path $root "skill") (Join-Path $stage "skill")

    $emptyBin = Join-Path $work "emptybin"
    New-Item -ItemType Directory -Force -Path $emptyBin | Out-Null
    $serve = Join-Path $work "serve"
    $asset = "projectboard-windows-amd64.zip"
    function New-Zip($stageDir, $dest) {
        if (Test-Path $dest) { Remove-Item -Force $dest }
        [System.IO.Compression.ZipFile]::CreateFromDirectory($stageDir, $dest, [System.IO.Compression.CompressionLevel]::Optimal, $false)
    }
    function Write-Sha($zip, $nameInFile) {
        $h = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLower()
        Set-Content -Path "$zip.sha256" -Value "$h  $nameInFile" -Encoding ascii
    }
    # 把某個 zip（附 checksum）發布到 $serve 底下的 latest/download 與 download/<tag>。
    function Publish($zip) {
        foreach ($sub in @("latest\download", "download\v0.20260928.006")) {
            $d = Join-Path $serve $sub
            New-Item -ItemType Directory -Force -Path $d | Out-Null
            Copy-Item -Force $zip (Join-Path $d $asset)
            if (Test-Path "$zip.sha256") { Copy-Item -Force "$zip.sha256" (Join-Path $d "$asset.sha256") }
            elseif (Test-Path (Join-Path $d "$asset.sha256")) { Remove-Item -Force (Join-Path $d "$asset.sha256") }
        }
    }
    $good = Join-Path $work "good.zip"
    New-Zip $stage $good
    Write-Sha $good $asset
    function Reset-Serve {
        if (Test-Path $serve) { Remove-Item -Recurse -Force $serve }
        # Publish 用的是檔名 good.zip 的 checksum，內容寫的是 $asset，所以可以直接複製。
        Publish $good
    }
    Reset-Serve

    function Base-Url { $p = (Resolve-Path $serve).Path -replace '\\', '/'; if ($p.StartsWith('/')) { "file://$p" } else { "file:///$p" } }

    # 跑 install.ps1（子行程）。環境變數只在這次呼叫期間設定，結束就還原。
    $managed = @("PROJECT_BOARD_HOME", "PROJECT_BOARD_BINDIR", "PROJECT_BOARD_REPO", "PROJECT_BOARD_VERSION",
        "PROJECT_BOARD_RELEASE_BASE", "PROJECT_BOARD_GO_CACHE", "PROJECT_BOARD_ARCH", "PATH")
    function Run-Install($envs, $installerArgs) {
        $saved = @{}
        foreach ($k in $managed) { $saved[$k] = [Environment]::GetEnvironmentVariable($k) }
        try {
            # 預設不指定 CPU，讓 install.ps1 自己偵測（這台機器是 x64，所以拿到 amd64 的 zip）。
            [Environment]::SetEnvironmentVariable("PROJECT_BOARD_ARCH", $null)
            [Environment]::SetEnvironmentVariable("PROJECT_BOARD_RELEASE_BASE", (Base-Url))
            # 預設的 PATH 裡沒有 git／go：任何一個案例「意外」退回原始碼編譯，都會停在第一個檢查，
            # 不會真的去 clone 和編譯（那會碰網路，而且慢）。需要真工具的案例自己傳 PATH。
            [Environment]::SetEnvironmentVariable("PATH", $emptyBin)
            foreach ($k in $envs.Keys) { [Environment]::SetEnvironmentVariable($k, $envs[$k]) }
            $out = & $self -NoProfile -File $installer @installerArgs 2>&1 | Out-String
            return @{ Code = $LASTEXITCODE; Out = $out }
        } finally {
            foreach ($k in $managed) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
        }
    }
    # 沒有 git／go 的 PATH：要走原始碼編譯的案例會在第一個檢查就停下來，不會真的去 clone 或編譯。
    $tmpRoot = [System.IO.Path]::GetTempPath()
    function Install-Leftovers { @(Get-ChildItem -Path $tmpRoot -Directory -Filter "project_board-install-*" -ErrorAction SilentlyContinue) }
    $leftBefore = (Install-Leftovers).Count

    # --- 預編譯版裝得起來 -------------------------------------------------------
    $h1 = Join-Path $work "h1"
    $r = Run-Install @{ PROJECT_BOARD_HOME = $h1 } @()
    $pb = Join-Path $h1 "bin\pb.exe"
    Check ($r.Code -eq 0 -and (Test-Path $pb)) "預編譯版裝得起來：bin\pb.exe 在安裝目錄" "exit $($r.Code): $($r.Out)"
    $missing = @("go.mod", "LICENSE", "README.md", "owners.example.txt", "docs", "skill") | Where-Object { -not (Test-Path (Join-Path $h1 $_)) }
    Check (-not $missing) "專案根該有的檔案都在（go.mod、docs、skill…）" "缺：$($missing -join ' ')"
    Check (-not (Test-Path (Join-Path $h1 ".git"))) "預編譯版不帶 .git（不需要 git 就能裝）" ""
    $ver = (& $pb version 2>&1 | Select-Object -First 1)
    Check ("$ver" -match '^V\d+\.\d{8}\.\d{3}$') "裝好的 pb 真的跑得起來，回報版號" "輸出：$ver"
    Check ($r.Out -match 'PATH') "安裝目錄的 bin 不在 PATH 時，說明怎麼加" $r.Out
    Check ($r.Out -match 'owners') "安裝結束提醒設定 owner 名冊" $r.Out

    # --- 升級：資料與名冊不動 ---------------------------------------------------
    New-Item -ItemType Directory -Force -Path (Join-Path $h1 "var") | Out-Null
    Set-Content -Path (Join-Path $h1 "owners.txt") -Value "keep-me"
    Set-Content -Path (Join-Path $h1 "var\board.db") -Value "db-bytes"
    Set-Content -Path (Join-Path $h1 "docs\OPERATIONS.md") -Value "old docs"
    $r = Run-Install @{ PROJECT_BOARD_HOME = $h1 } @()
    Check ($r.Code -eq 0 -and ((Get-Content (Join-Path $h1 "owners.txt") -Raw).Trim() -eq "keep-me") -and ((Get-Content (Join-Path $h1 "var\board.db") -Raw).Trim() -eq "db-bytes")) `
        "重跑＝升級：var\board.db 和 owners.txt 原封不動" "exit $($r.Code): $($r.Out)"
    Check ((Get-Content (Join-Path $h1 "docs\OPERATIONS.md") -Raw).Trim() -ne "old docs") "升級會更新說明文件" ""

    # --- 版本旗標 / 環境變數 ----------------------------------------------------
    $r = Run-Install @{ PROJECT_BOARD_HOME = (Join-Path $work "h2") } @("-Version", "v0.20260928.006")
    Check ($r.Code -eq 0) "-Version 指定標籤，從 download\<標籤> 抓" "exit $($r.Code): $($r.Out)"
    $r = Run-Install @{ PROJECT_BOARD_HOME = (Join-Path $work "h2b"); PROJECT_BOARD_VERSION = "v0.20260928.006" } @()
    Check ($r.Code -eq 0) "PROJECT_BOARD_VERSION 環境變數也能指定標籤" "exit $($r.Code): $($r.Out)"
    $r = Run-Install @{ PROJECT_BOARD_HOME = (Join-Path $work "h2c"); PROJECT_BOARD_BINDIR = (Join-Path $work "bin2c") } @()
    Check ($r.Code -eq 0 -and (Test-Path (Join-Path $work "bin2c\pb.exe")) -and ($r.Out -match '推不出專案根')) `
        "PROJECT_BOARD_BINDIR：另外複製一份，並說明複製出去的 pb 推不出專案根" "exit $($r.Code): $($r.Out)"

    # --- 輸入檢查 ---------------------------------------------------------------
    foreach ($bad in @("../../evil", "-x", "v1/../2", "a b", "v1;rm")) {
        $hv = Join-Path $work "hv"
        if (Test-Path $hv) { Remove-Item -Recurse -Force $hv }
        $r = Run-Install @{ PROJECT_BOARD_HOME = $hv } @("-Version", $bad)
        Check ($r.Code -ne 0 -and ($r.Out -match '不合法') -and -not (Test-Path $hv)) "-Version '$bad' 在做任何事之前就被拒絕" "exit $($r.Code): $($r.Out)"
    }
    $r = Run-Install @{ PROJECT_BOARD_HOME = (Join-Path $work "hv"); PROJECT_BOARD_REPO = "-evil" } @()
    Check ($r.Code -ne 0 -and ($r.Out -match '不合法')) "PROJECT_BOARD_REPO='-evil' 被拒絕" "exit $($r.Code): $($r.Out)"
    $r = Run-Install @{ PROJECT_BOARD_HOME = (Join-Path $work "hv"); PROJECT_BOARD_RELEASE_BASE = "http://insecure.example.com" } @()
    Check ($r.Code -ne 0 -and ($r.Out -match 'https')) "RELEASE_BASE 不是 https 就拒絕" "exit $($r.Code): $($r.Out)"

    # --- 沒驗證過的檔案不能進安裝目錄 -------------------------------------------
    function Refused($name, $want, $homeDir, $r) {
        $installed = Test-Path (Join-Path $homeDir "bin\pb.exe")
        Check ($r.Code -ne 0 -and ($r.Out -match $want) -and -not $installed) $name "exit $($r.Code), 目錄裡有 pb：$installed；$($r.Out)"
    }
    $hx = Join-Path $work "hx"
    # 沒有 checksum
    Reset-Serve
    Get-ChildItem -Path $serve -Recurse -Filter "*.sha256" | Remove-Item -Force
    if (Test-Path $hx) { Remove-Item -Recurse -Force $hx }
    Refused "沒有發布 checksum：拒絕安裝（不會因此改裝沒驗證過的檔案）" 'checksum' $hx (Run-Install @{ PROJECT_BOARD_HOME = $hx } @())
    # 空的 checksum
    Reset-Serve
    Get-ChildItem -Path $serve -Recurse -Filter "*.sha256" | ForEach-Object { Set-Content -Path $_.FullName -Value "" -NoNewline }
    Refused "checksum 檔是空的：拒絕安裝" 'checksum' $hx (Run-Install @{ PROJECT_BOARD_HOME = $hx } @())
    # 內容被竄改
    Reset-Serve
    Get-ChildItem -Path $serve -Recurse -Filter $asset | ForEach-Object { [System.IO.File]::AppendAllText($_.FullName, "tampered") }
    Refused "zip 內容跟 checksum 對不上（被竄改／損毀）：拒絕安裝" '對不上' $hx (Run-Install @{ PROJECT_BOARD_HOME = $hx } @())
    # checksum 檔指的是別的檔案
    Reset-Serve
    Get-ChildItem -Path $serve -Recurse -Filter "*.sha256" | ForEach-Object {
        $h = (Get-FileHash -Path (Join-Path $_.DirectoryName $asset) -Algorithm SHA256).Hash.ToLower()
        Set-Content -Path $_.FullName -Value "$h  something-else.zip" -Encoding ascii
    }
    Refused "checksum 檔指的是別的檔案：拒絕安裝" 'checksum' $hx (Run-Install @{ PROJECT_BOARD_HOME = $hx } @())

    # --- 惡意／壞掉的 zip --------------------------------------------------------
    # 把好的 zip 複製一份，加一個壞成員，重算 checksum 再發布（checksum 是對的——這一關擋的是
    # 「簽得對但內容不該出現」的東西）。
    function Add-Entry($zip, $entryName) {
        $za = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Update)
        try {
            $e = $za.CreateEntry($entryName)
            $w = New-Object System.IO.StreamWriter($e.Open())
            $w.Write("x"); $w.Dispose()
        } finally { $za.Dispose() }
    }
    $evilCases = @(
        @{ Name = "路徑跳脫（../evil）"; Entry = "../evil"; Want = "不安全的路徑" },
        @{ Name = "不認得的頂層項目（evil.txt）"; Entry = "evil.txt"; Want = "不認得的項目" },
        @{ Name = "bin\ 底下多一個檔案"; Entry = "bin/evil.exe"; Want = "只能有 pb.exe" }
    )
    foreach ($c in $evilCases) {
        $evil = Join-Path $work "evil.zip"
        Copy-Item -Force $good $evil
        Add-Entry $evil $c.Entry
        Write-Sha $evil $asset
        if (Test-Path $serve) { Remove-Item -Recurse -Force $serve }
        Publish $evil
        if (Test-Path $hx) { Remove-Item -Recurse -Force $hx }
        Refused "惡意 zip（$($c.Name)）：整包拒絕" $c.Want $hx (Run-Install @{ PROJECT_BOARD_HOME = $hx } @())
    }
    Remove-Item -Force (Join-Path $work "evil.zip.sha256") -ErrorAction SilentlyContinue
    Check (-not (Test-Path (Join-Path $work "evil"))) "惡意 zip 沒有在安裝目錄之外寫出任何東西" ""

    # pb.exe 是個跑不起來的檔案：不能蓋掉能用的舊版。
    Reset-Serve
    $h4 = Join-Path $work "h4"
    $null = Run-Install @{ PROJECT_BOARD_HOME = $h4 } @()
    $oldSum = (Get-FileHash -Path (Join-Path $h4 "bin\pb.exe") -Algorithm SHA256).Hash
    $stageBad = Join-Path $work "stage-bad"
    if (Test-Path $stageBad) { Remove-Item -Recurse -Force $stageBad }
    Copy-Item -Recurse $stage $stageBad
    $brokenExe = Join-Path $stageBad "bin\pb.exe"
    if ($IsWindows -eq $false -or $env:OS -ne "Windows_NT") {
        Set-Content -Path $brokenExe -Value "#!/bin/sh`necho not-a-version" -NoNewline
        & chmod 755 $brokenExe
    } else {
        Set-Content -Path $brokenExe -Value "this is not an executable" -NoNewline
    }
    $badZip = Join-Path $work "broken.zip"
    New-Zip $stageBad $badZip
    Write-Sha $badZip $asset
    if (Test-Path $serve) { Remove-Item -Recurse -Force $serve }
    Publish $badZip
    $r = Run-Install @{ PROJECT_BOARD_HOME = $h4 } @()
    $newSum = (Get-FileHash -Path (Join-Path $h4 "bin\pb.exe") -Algorithm SHA256).Hash
    Check ($r.Code -ne 0 -and ($r.Out -match '跑不起來') -and $oldSum -eq $newSum) "新下載的 pb.exe 跑不起來：拒絕，而且舊版原封不動" "exit $($r.Code): $($r.Out)"
    Reset-Serve

    # --- 安裝目錄的處理 ---------------------------------------------------------
    $h5 = Join-Path $work "h5"
    New-Item -ItemType Directory -Force -Path $h5 | Out-Null
    Set-Content -Path (Join-Path $h5 "notes.txt") -Value "someone else's file"
    $r = Run-Install @{ PROJECT_BOARD_HOME = $h5 } @()
    Check ($r.Code -ne 0 -and ($r.Out -match '不是空目錄') -and -not (Test-Path (Join-Path $h5 "bin"))) "有別人東西的非空目錄：拒絕，一個檔案都不動" "exit $($r.Code): $($r.Out)"

    # --- 沒有預編譯版 → 說明原因並走原始碼編譯（這裡沒有 git，所以停在檢查） ----------
    $hs = Join-Path $work "hs"
    $r = Run-Install @{ PROJECT_BOARD_HOME = $hs; PROJECT_BOARD_ARCH = "x86"; PATH = $emptyBin } @()
    Check ($r.Code -ne 0 -and ($r.Out -match '沒有預編譯版') -and ($r.Out -match '找不到 git')) "x86 沒有預編譯版：說明原因並走原始碼編譯" "exit $($r.Code): $($r.Out)"
    $r = Run-Install @{ PROJECT_BOARD_HOME = $hs; PATH = $emptyBin } @("-FromSource")
    Check ($r.Code -ne 0 -and ($r.Out -match '找不到 git') -and -not (Test-Path (Join-Path $hs "bin"))) "-FromSource 不去拿預編譯版" "exit $($r.Code): $($r.Out)"
    Reset-Serve
    $hn = Join-Path $work "hn"
    Remove-Item -Recurse -Force (Join-Path $serve "latest") -ErrorAction SilentlyContinue
    $r = Run-Install @{ PROJECT_BOARD_HOME = $hn; PATH = $emptyBin } @()
    Check ($r.Code -ne 0 -and ($r.Out -match '抓不到預編譯版') -and ($r.Out -match '找不到 git')) "release 還沒發布（抓不到檔案）：說明原因並退回原始碼編譯" "exit $($r.Code): $($r.Out)"
    Reset-Serve

    # --- 暫存檔乾淨 -------------------------------------------------------------
    $leftAfter = (Install-Leftovers).Count
    Check ($leftAfter -eq $leftBefore) "所有案例（成功與失敗）跑完，暫存目錄都沒有殘留" "殘留 $($leftAfter - $leftBefore) 個 project_board-install-*"
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

if ($script:fail -ne 0) {
    Write-Host "install-ps1.test: FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "install-ps1.test: all cases passed"
# 明確回 0：`pwsh -command ". 腳本"` 這種呼叫方式（GitHub Actions 就是）在腳本結束時會拿最後一個
# 原生指令的結束碼當行程的結束碼，而「被拒絕的安裝」這類案例剛好留下 1。
exit 0
