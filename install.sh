#!/usr/bin/env bash
# ProjectBoard 一鍵安裝腳本（Linux / macOS）。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/amgio38/Y20260923/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/amgio38/Y20260923/main/install.sh | bash -s -- --version v0.20260928.006
#   （或先存下來看過再跑：curl -fsSLO …/install.sh && less install.sh && bash install.sh）
#
# 做的事（預設走「預編譯版」，不需要 Go、不需要 git）：
#   1. 判斷這台機器是哪個平台（linux／darwin × amd64／arm64）；
#   2. 從 GitHub Releases 下載對應的壓縮檔，驗 sha256 對得上才繼續；
#   3. 先解到暫存區檢查（成員白名單、不含符號連結、pb 真的跑得起來），才放進安裝目錄；
#   4. 在 PATH 目錄放一個指向 <安裝目錄>/bin/pb 的符號連結，印下一步該做什麼。
# 找不到對應的預編譯版（沒有發布、平台不支援、網路抓不到）就退回「原始碼編譯」：
# 檢查 git → 確認有能用的 go（沒有、太舊、或壞掉就自己下載一份乾淨的）→ clone →
# make build。純 Go（modernc.org/sqlite 無 cgo），不需要 C 工具鏈。
# 想直接走原始碼編譯：加 --from-source。
#
# 安裝目錄（HOME_DIR）＝專案根：放 bin/pb、service.sh、docs，以及你的資料 var/board.db。
# 這是公開在 GitHub 上的 repo，安裝的人在什麼機器、什麼帳號權限下跑都有可能，所以
# 走保守路線：不猜使用者想把東西放哪，一開始就問（預設＝目前所在目錄底下的
# project_board 子目錄，不是使用者的家目錄）；目標目錄如果已經有內容、又不是既有的
# project_board，直接中止，不覆蓋。重跑一次＝升級，var/ 和 owners.txt 不會被動到。
#
# 整支腳本包在 main() 裡，最後一行才執行：curl | bash 的下載如果在半途被切斷，bash 只會
# 讀到一個不完整的函式定義、什麼都不做，而不是執行到一半的腳本。腳本本身不讀標準輸入
# （pipe 時那就是腳本本體）；要問使用者時改讀 /dev/tty。不使用 sudo。
#
# 可用環境變數覆寫（旗標見 --help）：
#   PROJECT_BOARD_REPO          repo clone 網址（預設 https://github.com/amgio38/Y20260923.git）
#   PROJECT_BOARD_HOME          安裝目錄（設了就不會互動詢問）
#   PROJECT_BOARD_BINDIR        符號連結放哪個 PATH 目錄（預設 ~/.local/bin）
#   PROJECT_BOARD_VERSION       要裝的 release 標籤（預設：最新版）
#   PROJECT_BOARD_RELEASE_BASE  release 下載位置的前綴（預設由 REPO 推出；內部鏡像用）
#   PROJECT_BOARD_GO_CACHE      獨立下載的 go 工具鏈放哪裡（預設 ~/.cache/project_board/go-toolchain）

set -euo pipefail

MIN_GO_MAJOR=1
MIN_GO_MINOR=25
DEFAULT_REPO="https://github.com/amgio38/Y20260923.git"

# 這些在 main() 裡才有值；先宣告，cleanup 才能在任何時間點安全執行。
tmp=""
GO_BIN=""

log() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m注意：\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m錯誤：\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
用法：install.sh [選項]

  --version TAG   要裝的 release 標籤，例如 v0.20260928.006（預設：最新版）
  --from-source   不下載預編譯版，直接用 git＋go 從原始碼編譯
  -h, --help      顯示這段說明

預設會下載預編譯版並驗 sha256；沒有 checksum、或對不上，就停下來，不會裝沒驗過的檔案。
可用的環境變數見這支腳本最上面的註解。
EOF
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "找不到 $1，請先安裝（$2）再重跑這支腳本。"
}

# 這支腳本建立的所有暫存物，只用這一個清理入口：任何路徑結束（成功、失敗、被中斷）
# 都會清掉下載的壓縮檔和解壓區，不會越裝越多垃圾。
cleanup() {
	[ -z "$tmp" ] || rm -rf "$tmp"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		return 1
	fi
}

have_sha256_tool() {
	command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1
}

# 下載 URL 到 DEST，連線與總時間都有上限，黑洞網路不會把安裝卡死。
# --proto '=https' 擋掉轉址到非 https；--tlsv1.2 設協定下限。太舊、不認得這些旗標的
# curl 會直接失敗，而不是悄悄少掉保護（跟 checksum 同一個取捨）。
fetch() {
	local url="$1" dest="$2"
	case "$url" in
	https://*) ;;
	*) die "下載網址必須是 https：$url" ;;
	esac
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 --connect-timeout 10 --max-time 600 --retry 2 "$url" -o "$dest"
	elif command -v wget >/dev/null 2>&1; then
		wget -q --https-only --connect-timeout=10 --timeout=600 --tries=3 -O "$dest" "$url"
	else
		return 1
	fi
}

have_downloader() {
	command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1
}

# ---------------------------------------------------------------------------
# 參數與輸入檢查
# ---------------------------------------------------------------------------

FROM_SOURCE=0

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--from-source) FROM_SOURCE=1; shift ;;
		--version)
			[ $# -ge 2 ] || { echo "install.sh: --version 需要一個值" >&2; usage >&2; exit 2; }
			VERSION="$2"; shift 2 ;;
		--version=*) VERSION="${1#*=}"; shift ;;
		-h | --help) usage; exit 0 ;;
		*) echo "install.sh: 不認得的參數：$1" >&2; usage >&2; exit 2 ;;
		esac
	done
}

# release 標籤和 repo 網址會被拼進下載網址和 git clone，而且可以來自呼叫端的環境變數。
# 只放行標籤／網址該有的字元；特別擋掉「-」開頭（會被 git 當成選項）和「..」（路徑跳脫）。
validate_inputs() {
	if [ -n "$VERSION" ]; then
		case "$VERSION" in
		-* | *[!A-Za-z0-9._+-]* | *..*) die "release 標籤 '$VERSION' 不合法（只能有英數字和 . _ + -）" ;;
		esac
	fi
	case "$REPO" in
	-* | *[[:space:]]*) die "PROJECT_BOARD_REPO 不合法：$REPO" ;;
	esac
	if [ -n "${RELEASE_BASE:-}" ]; then
		case "$RELEASE_BASE" in
		https://*) ;;
		*) die "PROJECT_BOARD_RELEASE_BASE 必須是 https:// 開頭：$RELEASE_BASE" ;;
		esac
		case "$RELEASE_BASE" in
		*[[:space:]]*) die "PROJECT_BOARD_RELEASE_BASE 不合法：$RELEASE_BASE" ;;
		esac
	fi
}

# 這台機器是什麼平台，因此有沒有預編譯版。
# 設 OS_NAME／ARCH_NAME；兩個都有值才代表有預編譯版。原生 Windows 的 bash（Git Bash、
# MSYS、Cygwin）擋掉，導去 PowerShell 版——ProjectBoard 在 Windows 上是原生支援的，
# 只是要用對的安裝腳本。
OS_NAME=""
ARCH_NAME=""
detect_platform() {
	local s m
	s="$(uname -s 2>/dev/null || echo unknown)"
	m="$(uname -m 2>/dev/null || echo unknown)"
	case "$s" in
	Linux) OS_NAME="linux" ;;
	Darwin) OS_NAME="darwin" ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		die "偵測到 Windows 的 bash（$s）。Windows 請改用 PowerShell 版安裝腳本 install.ps1（見 README『安裝』一節）。" ;;
	*) warn "$s 不是測試過的平台，只能走原始碼編譯" ;;
	esac
	case "$m" in
	x86_64 | amd64) ARCH_NAME="amd64" ;;
	aarch64 | arm64) ARCH_NAME="arm64" ;;
	*) warn "CPU 架構 $m 沒有預編譯版，只能走原始碼編譯" ;;
	esac
	# 其中一個沒認出來，就整個當作沒有預編譯版。
	if [ -z "$OS_NAME" ] || [ -z "$ARCH_NAME" ]; then
		OS_NAME=""
		ARCH_NAME=""
	fi
}

# repo 網址推出 GitHub 的 owner/name（release 下載位置用）；推不出來就回空字串。
repo_slug() {
	case "$REPO" in
	https://github.com/*/*)
		local s="${REPO#https://github.com/}"
		s="${s%.git}"
		s="${s%/}"
		case "$s" in
		*/*/* | */ | /* | "") return 0 ;;
		esac
		printf '%s' "$s" ;;
	esac
}

# ---------------------------------------------------------------------------
# 決定安裝目錄
# ---------------------------------------------------------------------------

is_project_board_dir() {
	[ -f "$1/go.mod" ] && grep -q '^module project_board$' "$1/go.mod" 2>/dev/null
}

# 回傳 HOME_DIR（呼叫端用 HOME_DIR 變數）。
choose_home_dir() {
	local default_dir prompt_dir prompt_msg
	default_dir="$(pwd)/project_board"

	# 明確指定的優先：人在某個 project_board 目錄裡、卻特地設了 PROJECT_BOARD_HOME，
	# 要的是指定的那個，不是悄悄裝回目前這個。
	if [ -n "${PROJECT_BOARD_HOME:-}" ]; then
		HOME_DIR="$PROJECT_BOARD_HOME"
		log "用環境變數指定的安裝目錄：$HOME_DIR"
	elif is_project_board_dir "$(pwd)"; then
		log "偵測到已經在 project_board 目錄裡，直接用這份"
		HOME_DIR="$(pwd)"
		return 0
	else
		prompt_dir=""
		prompt_msg="安裝目錄（原始碼＋資料放這裡，直接按 Enter 用預設值 $default_dir）： "
		if [ -t 0 ]; then
			read -r -p "$prompt_msg" prompt_dir
		elif { exec 3</dev/tty; } 2>/dev/null; then
			# curl | bash 這種情境：腳本本體的 stdin 被 pipe 佔走，但終端機本身
			# （/dev/tty）還在。先安靜地探測 /dev/tty 開不開得起來（探測用的 fd 3
			# 才套 2>/dev/null，不能套在下面真正要顯示提示字的 read 身上——套上去
			# 的話 -p 的提示文字會被一起吃掉，畫面看起來像當機，使用者根本不知道
			# 在等他按 Enter。血淋淋教訓：曾經因為這行多餘的 2>/dev/null 搞到提示
			# 完全不見，只能對著空白畫面等。
			exec 3<&-
			read -r -p "$prompt_msg" prompt_dir < /dev/tty
		else
			log "非互動模式（沒有終端機可問），用預設安裝目錄：$default_dir"
			log "要指定別的路徑：PROJECT_BOARD_HOME=/your/path bash install.sh，或先把腳本存下來本機執行再回答。"
		fi
		HOME_DIR="${prompt_dir:-$default_dir}"
	fi

	# 展開 ~，並把路徑轉成絕對路徑（不管使用者輸入的是相對路徑還是帶不帶結尾斜線）。
	case "$HOME_DIR" in
	"~") HOME_DIR="$HOME" ;;
	"~/"*) HOME_DIR="$HOME${HOME_DIR#\~}" ;;
	esac
	[ -n "$HOME_DIR" ] || die "安裝目錄不能是空的"
	mkdir -p "$HOME_DIR" || die "建不起安裝目錄 $HOME_DIR（沒有權限？）。換一個你有權限的目錄，例如 PROJECT_BOARD_HOME=\$HOME/project_board"
	[ -w "$HOME_DIR" ] || die "沒有寫入 $HOME_DIR 的權限。換一個你有權限的目錄，不要用 sudo 跑這支腳本。"
	HOME_DIR="$(cd "$HOME_DIR" && pwd)"
}

# 把 BIN_DIR/pb 指到 HOME_DIR/bin/pb。用符號連結而不是複製：pb 找預設資料庫時，會解開符號連結、
# 從執行檔實體路徑推「專案根」（執行檔在 <root>/bin/ 且 <root>/go.mod 存在），這樣不管在哪個
# 目錄打 pb，預設資料庫都是 <安裝目錄>/var/board.db。檔案系統不支援符號連結才退回複製，
# 並明講這會讓預設資料庫變成「相對目前目錄」。
link_into_path() {
	mkdir -p "$BIN_DIR" || die "建不起 $BIN_DIR（沒有權限？）。換一個目錄，例如 PROJECT_BOARD_BINDIR=\$HOME/bin"
	if ln -sfn "$HOME_DIR/bin/pb" "$BIN_DIR/pb" 2>/dev/null; then
		log "已建立 $BIN_DIR/pb → $HOME_DIR/bin/pb"
	else
		cp -f "$HOME_DIR/bin/pb" "$BIN_DIR/pb"
		chmod +x "$BIN_DIR/pb"
		warn "這個檔案系統不能建符號連結，改複製到 $BIN_DIR/pb。這樣 pb 推不出專案根，預設資料庫會變成『目前目錄』下的 var/board.db；請用 --db 或環境變數 PB_DB 明確指定。"
	fi
}

# ---------------------------------------------------------------------------
# 預編譯版
# ---------------------------------------------------------------------------

# 壓縮檔裡允許出現的頂層名稱。其他東西一律拒絕：預編譯版的內容是我們自己打的包，
# 多出任何東西都代表不是我們打的。
allowed_top() {
	case "$1" in
	bin | go.mod | LICENSE | README.md | owners.example.txt | service.sh | docs | skill) return 0 ;;
	*) return 1 ;;
	esac
}

# 判斷壓縮檔每個成員都是安全的普通路徑，才有資格解開。
# 成員清單先寫進檔案再用 while 讀：用 pipe 餵給 while 的話，迴圈會跑在子 shell 裡，
# 裡面的 die 只會結束子 shell，腳本會照樣往下走——一個擋不住東西的檢查比沒有更糟。
check_archive_members() {
	local archive="$1" m top listing="$tmp/members.txt"
	# 符號連結／硬連結成員一律拒絕：解開後它可以指到安裝目錄之外的任何地方。
	if tar -tvzf "$archive" | grep -qE '^[lh]'; then
		die "壓縮檔含有符號連結或硬連結，不安裝：$archive"
	fi
	tar -tzf "$archive" | sed 's|^\./||' >"$listing" || die "讀不出壓縮檔的內容：$archive"
	while IFS= read -r m; do
		[ -n "$m" ] && [ "$m" != "." ] || continue
		case "$m" in
		/* | *..*) die "壓縮檔含不安全的路徑，不安裝：$m" ;;
		esac
		top="${m%%/*}"
		allowed_top "$top" || die "壓縮檔含不認得的項目，不安裝：$m"
		if [ "$top" = bin ]; then
			case "${m%/}" in
			bin | bin/pb) ;;
			*) die "壓縮檔的 bin/ 底下只能有 pb，不安裝：$m" ;;
			esac
		fi
	done <"$listing"
}

# 為什麼分成「下載」和「安裝」兩個函式：bash 在 `if 函式` 這種條件環境裡會整段關掉 set -e，
# 函式裡任何一個沒寫 || die 的指令失敗了都會被默默放過。所以只有「要不要退回原始碼編譯」
# 這個決定放在條件裡（prebuilt_download，裡面每個失敗都有明確處理）；真正動到安裝目錄的
# prebuilt_install 一定是直接呼叫，set -e 全程有效。
PREBUILT_ASSET=""

# 下載預編譯版和它的 checksum 到暫存區。回傳 0＝抓到了；非 0＝沒有可用的預編譯版，
# 呼叫端退回原始碼編譯（只有「沒有這個東西可抓」才走這條；抓到了卻驗不過是 die）。
prebuilt_download() {
	local slug base url
	[ "$FROM_SOURCE" -eq 0 ] || return 1
	if [ -z "$OS_NAME" ]; then
		warn "這個平台沒有預編譯版，改用原始碼編譯"
		return 1
	fi

	if [ -n "${RELEASE_BASE:-}" ]; then
		base="${RELEASE_BASE%/}"
	else
		slug="$(repo_slug)"
		if [ -z "$slug" ]; then
			warn "PROJECT_BOARD_REPO 不是 github.com 的網址，沒辦法推出 release 位置，改用原始碼編譯（要用預編譯版：設 PROJECT_BOARD_RELEASE_BASE）"
			return 1
		fi
		base="https://github.com/$slug/releases"
	fi
	if [ -n "$VERSION" ]; then
		base="$base/download/$VERSION"
	else
		base="$base/latest/download"
	fi
	PREBUILT_ASSET="projectboard-$OS_NAME-$ARCH_NAME.tar.gz"
	url="$base/$PREBUILT_ASSET"

	have_downloader || { warn "沒有 curl 也沒有 wget，改用原始碼編譯"; return 1; }
	have_sha256_tool || die "找不到 sha256sum 或 shasum，沒辦法驗證下載的檔案，所以不安裝。請安裝其中之一，或加 --from-source 改用原始碼編譯。"
	command -v tar >/dev/null 2>&1 || die "找不到 tar，請用系統套件管理員安裝。"

	log "下載 $url"
	if ! fetch "$url" "$tmp/$PREBUILT_ASSET"; then
		warn "抓不到預編譯版（還沒發布？網路不通？），改用原始碼編譯"
		return 1
	fi
	# 抓到壓縮檔卻沒有 checksum，是發布出了問題，不是「沒有預編譯版」：不退回、不裝。
	if ! fetch "$url.sha256" "$tmp/$PREBUILT_ASSET.sha256" 2>/dev/null || [ ! -s "$tmp/$PREBUILT_ASSET.sha256" ]; then
		die "找不到（或是空的）$PREBUILT_ASSET 的 checksum，不安裝沒驗證過的檔案。可加 --from-source 改用原始碼編譯。"
	fi
	return 0
}

# 驗證、解開、檢查、放進 HOME_DIR、建連結。一定要直接呼叫（不要放進 if 條件）。
prebuilt_install() {
	local asset="$PREBUILT_ASSET" got want want_name extract ver
	got="$(sha256_of "$tmp/$asset")"
	want="$(awk 'NR==1 {print $1}' "$tmp/$asset.sha256")"
	want_name="$(awk 'NR==1 {print $2}' "$tmp/$asset.sha256" | sed 's/^\*//')"
	[ -n "$want" ] && [ "$got" = "$want" ] || die "$asset 的 sha256 對不上（預期 ${want:-空}，實際 $got），下載可能損毀，不安裝。"
	[ "$want_name" = "$asset" ] || die "checksum 檔指的是 ${want_name:-空白}，不是 $asset，不安裝。"

	check_archive_members "$tmp/$asset"
	extract="$tmp/extract"
	mkdir -p "$extract"
	tar --no-same-owner --no-same-permissions -xzf "$tmp/$asset" -C "$extract" || die "解壓縮失敗：$asset"
	[ -f "$extract/bin/pb" ] && [ ! -L "$extract/bin/pb" ] || die "壓縮檔裡沒有 bin/pb，不安裝。"
	chmod 0755 "$extract/bin/pb"
	# 放進安裝目錄之前先確認它真的跑得起來，而且報的是版號。壞掉的檔案不該蓋掉能用的舊版。
	ver="$("$extract/bin/pb" version 2>/dev/null | head -n 1)" || true
	case "$ver" in
	V[0-9]*.[0-9]*.[0-9]*) ;;
	*) die "下載的 pb 跑不起來，或沒有回報版號（pb version 輸出：'${ver:-空}'）。不安裝。" ;;
	esac

	# 升級時只覆蓋我們發布的檔案；var/（你的資料庫）和 owners.txt 不在壓縮檔裡，不會被動到。
	mkdir -p "$HOME_DIR/bin"
	mv -f "$extract/bin/pb" "$HOME_DIR/bin/pb"
	rmdir "$extract/bin"
	cp -R "$extract"/. "$HOME_DIR"/
	log "預編譯版 $ver 已放進 $HOME_DIR"
	link_into_path
}

# ---------------------------------------------------------------------------
# 原始碼編譯（沒有預編譯版時的退路，也是開發者的路）
# ---------------------------------------------------------------------------

go_version_ge_min() {
	local ver="$1" major minor
	major="${ver%%.*}"
	minor="${ver#*.}"; minor="${minor%%.*}"
	[ "$major" -gt "$MIN_GO_MAJOR" ] && return 0
	[ "$major" -eq "$MIN_GO_MAJOR" ] && [ "$minor" -ge "$MIN_GO_MINOR" ]
}

# 系統上 `go` 這個名字指到的東西能不能直接拿來用：存在、真的能執行、版本夠新。
# 只要有一項不成立就回傳失敗，不 die——讓呼叫端 fallback 去下載一份乾淨的 go，
# 而不是要求使用者自己修好系統環境才能繼續安裝。
system_go_usable() {
	local bin ver out
	bin="$(command -v go 2>/dev/null)" || return 1

	if [ ! -x "$bin" ] && [ -w "$bin" ] 2>/dev/null; then
		# 常見情境：WSL 掛在 /mnt/c 之類 noexec 檔案系統、或用某些方式複製檔案
		# 沒帶到 exec bit，導致「找得到但 Permission denied」。是自己的檔案就
		# 先試著補權限，不行（例如檔案系統本身 noexec）就放棄，改走下載。
		chmod +x "$bin" 2>/dev/null || true
	fi

	if ! out="$("$bin" version 2>&1)"; then
		log "系統的 go（$bin）跑不起來：$out —— 改用獨立下載的 go"
		return 1
	fi

	ver="$(printf '%s' "$out" | grep -oE 'go[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 | tr -d 'go')"
	if [ -z "$ver" ] || ! go_version_ge_min "$ver"; then
		log "系統的 go 版本不夠新（${ver:-未知}，需要 >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}）——改用獨立下載的 go"
		return 1
	fi

	GO_BIN="$bin"
}

# 系統的 go 不能用時（沒裝、版本太舊、或權限/檔案系統問題跑不起來），下載一份
# 官方的、checksum 驗證過的 go 工具鏈，放在使用者自己的 cache 目錄裡，不動系統
# 環境、不需要 sudo。這是標準做法（跟 nvm/rustup 處理工具鏈的方式一樣）：安裝
# 腳本不該假設「這台機器剛好已經裝好對的工具鏈」，而是自己想辦法生出一份能用的。
bootstrap_go() {
	local os arch manifest line fname sha tmp_tar got_sha

	case "$(uname -s)" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) die "系統的 go 不能用，且這個作業系統（$(uname -s)）沒有自動下載對應版本，請手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）後再重跑。" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	*) die "系統的 go 不能用，且這台機器的架構（$(uname -m)）沒有自動下載對應版本，請手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）後再重跑。" ;;
	esac

	GO_BIN="$GO_CACHE_DIR/go/bin/go"
	if [ -x "$GO_BIN" ] && "$GO_BIN" version >/dev/null 2>&1; then
		log "沿用先前下載好的獨立 go（$GO_BIN）"
		return 0
	fi

	have_downloader || die "找不到 curl 或 wget，沒辦法下載 go 工具鏈。請安裝其中之一，或手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）。"
	have_sha256_tool || die "找不到 sha256sum 或 shasum，沒辦法驗證下載的 go 工具鏈，所以不下載。請安裝其中之一，或手動安裝 go。"
	log "系統沒有可用的 go，改用官方 go.dev 下載一份乾淨的工具鏈（放在 $GO_CACHE_DIR，不影響系統）"

	fetch 'https://go.dev/dl/?mode=json' "$tmp/go-manifest.json" \
		|| die "抓不到 go.dev 的版本清單，檢查一下網路連線，或手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）。"
	manifest="$(cat "$tmp/go-manifest.json")"

	# go.dev 回傳的是最新 stable 排最前面、每個版本一段的 pretty-print JSON；
	# 找第一個符合這台機器 os/arch 的 .tar.gz 檔名所在行，sha256 就在同一個
	# file 物件裡緊接在後面幾行（filename, os, arch, version, sha256, size, kind）。
	line="$(printf '%s\n' "$manifest" | grep -n "\"filename\": *\"go[0-9.]\+\.${os}-${arch}\.tar\.gz\"" | head -1 | cut -d: -f1)"
	if [ -z "$line" ]; then
		die "在 go.dev 的版本清單裡找不到 ${os}/${arch} 的下載檔，請手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）。"
	fi
	fname="$(printf '%s\n' "$manifest" | sed -n "${line}p" | grep -oE "go[0-9.]+\.${os}-${arch}\.tar\.gz")"
	sha="$(printf '%s\n' "$manifest" | sed -n "${line},$((line + 8))p" | grep -oE '"sha256": *"[0-9a-f]{64}"' | head -1 | grep -oE '[0-9a-f]{64}')"
	if [ -z "$fname" ] || [ -z "$sha" ]; then
		die "解析 go.dev 的版本清單失敗（格式可能變了），請手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）。"
	fi

	mkdir -p "$GO_CACHE_DIR"
	tmp_tar="$tmp/$fname"
	log "下載 https://go.dev/dl/$fname"
	fetch "https://go.dev/dl/$fname" "$tmp_tar" \
		|| die "下載 go 工具鏈失敗，檢查網路連線後重跑。"

	got_sha="$(sha256_of "$tmp_tar")"
	[ "$got_sha" = "$sha" ] \
		|| die "下載的 go 工具鏈 checksum 對不上（預期 $sha，拿到 $got_sha），可能是下載損毀或被竄改，已中止安裝。"

	rm -rf "$GO_CACHE_DIR/go"
	tar -xzf "$tmp_tar" -C "$GO_CACHE_DIR"
	[ -x "$GO_BIN" ] || die "解壓後找不到可執行的 $GO_BIN，go.dev 的封裝格式可能變了，請手動安裝 go（https://go.dev/dl/）。"
	log "獨立 go 工具鏈就緒：$GO_BIN（$("$GO_BIN" version)）"
}

install_from_source() {
	log "檢查必要工具（git／go）"
	need git "https://git-scm.com/downloads"
	need make "Linux：apt install make／macOS：xcode-select --install"

	system_go_usable || bootstrap_go
	log "使用 go：$GO_BIN"

	if [ -d "$HOME_DIR/.git" ]; then
		log "$HOME_DIR 已經是 git checkout，跑 git pull 更新"
		git -C "$HOME_DIR" pull --ff-only < /dev/null
	elif [ -n "$(ls -A "$HOME_DIR" 2>/dev/null)" ]; then
		die "$HOME_DIR 不是空目錄，而且不是 git checkout，沒辦法用原始碼更新。換一個空目錄，或指到既有的 checkout，再重跑。"
	else
		log "clone $REPO 到 $HOME_DIR"
		if [ -n "$VERSION" ]; then
			# --branch=VALUE 而不是 --branch VALUE：「-」開頭的值會被 git 當成選項。
			git clone --depth 1 --branch="$VERSION" "$REPO" "$HOME_DIR" < /dev/null
		else
			git clone "$REPO" "$HOME_DIR" < /dev/null
		fi
	fi

	log "make build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
	( cd "$HOME_DIR" && PATH="$(dirname "$GO_BIN"):$PATH" make build < /dev/null )
	[ -x "$HOME_DIR/bin/pb" ] || die "編譯完成但找不到 $HOME_DIR/bin/pb"
	link_into_path
}

# ---------------------------------------------------------------------------
# 收尾
# ---------------------------------------------------------------------------

report_next_step() {
	case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*)
		log "$BIN_DIR 還不在 PATH 裡，把這行加進你的 shell rc（~/.bashrc 或 ~/.zshrc）："
		echo "  export PATH=\"$BIN_DIR:\$PATH\""
		;;
	esac

	# 已經有 dashboard 在跑的話，它還在用舊的執行檔（記憶體裡的）。這裡不替使用者重啟，
	# 因為可能正有 agent 在用它。
	if [ -f "$HOME_DIR/var/serve.pid" ] && kill -0 "$(tr -d '[:space:]' < "$HOME_DIR/var/serve.pid")" 2>/dev/null; then
		echo
		warn "你的 dashboard 還在跑舊版。等沒人在用的時候執行："
		echo "  cd \"$HOME_DIR\" && ./service.sh restart"
	fi

	cat <<EOF

安裝完成，只有一件事要記：**兩個 pb 路徑是同一個東西，不是兩套系統**——
$BIN_DIR/pb 只是指向 $HOME_DIR/bin/pb 的連結；$HOME_DIR 是執行檔＋說明＋你的資料
（var/board.db）放的地方。資料庫不用另外初始化，第一次啟動就會自動建好。

下一步，啟動 dashboard 二選一：

  1.（推薦）背景常駐，綁 0.0.0.0:8787（區網內任何人都連得到，而且**沒有登入機制**，
     只在受信任的網路用，不要暴露到網際網路；詳見 SECURITY.md），重開機也好管理：
       cd "$HOME_DIR" && ./service.sh start
       ./service.sh status   # 看有沒有活著
       ./service.sh stop     # 關掉

  2. 前景跑（測試/除錯用，Ctrl-C 結束就沒了）：
       $BIN_DIR/pb serve
     （預設資料庫就是 $HOME_DIR/var/board.db）

瀏覽器開 http://<這台機器的位址>:8787 看畫面。

接 MCP（給 harness/agent 用，stdio）：
  $BIN_DIR/pb mcp
詳細的 MCP client 設定／可用工具清單見 $HOME_DIR/README.md「MCP 介面」一節。

先設定你自己團隊的 owner 名冊（沒設定就只認 unassigned）：
  cp "$HOME_DIR/owners.example.txt" "$HOME_DIR/owners.txt"   # 然後改成你們的名字

git commit 帶單號自動掛連結這類進階用法，不影響現在能不能跑起來，要用再查
$HOME_DIR/docs/GIT_INTEGRATION.md。

EOF
}

main() {
	REPO="${PROJECT_BOARD_REPO:-$DEFAULT_REPO}"
	VERSION="${PROJECT_BOARD_VERSION:-}"
	RELEASE_BASE="${PROJECT_BOARD_RELEASE_BASE:-}"
	parse_args "$@"
	validate_inputs

	[ -n "${HOME:-}" ] || die "HOME 沒有設定，找不到預設的安裝位置。請設 PROJECT_BOARD_HOME 和 PROJECT_BOARD_BINDIR。"
	BIN_DIR="${PROJECT_BOARD_BINDIR:-$HOME/.local/bin}"
	GO_CACHE_DIR="${PROJECT_BOARD_GO_CACHE:-$HOME/.cache/project_board/go-toolchain}"

	detect_platform
	for t in mktemp awk sed head tr; do
		command -v "$t" >/dev/null 2>&1 || die "缺少必要工具：$t"
	done

	tmp="$(mktemp -d "${TMPDIR:-/tmp}/project_board-install.XXXXXX")" || die "建不起暫存目錄（${TMPDIR:-/tmp}）"
	trap cleanup EXIT INT TERM

	choose_home_dir

	if [ -d "$HOME_DIR/.git" ]; then
		# 開發者的 checkout：升級就是 git pull＋編譯，不拿預編譯版去蓋原始碼目錄。
		install_from_source
	elif is_project_board_dir "$HOME_DIR" || [ -z "$(ls -A "$HOME_DIR" 2>/dev/null)" ]; then
		# 全新（空目錄）或之前用預編譯版裝過的目錄：下載預編譯版；沒有就退回原始碼編譯。
		if prebuilt_download; then
			prebuilt_install
		else
			install_from_source
		fi
	else
		die "$HOME_DIR 不是空目錄，而且不是既有的 project_board。安全起見不會覆蓋既有內容——換一個空目錄，或指到既有的安裝，再重跑。"
	fi

	report_next_step
}

main "$@"
