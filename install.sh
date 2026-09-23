#!/usr/bin/env bash
# ProjectBoard 一鍵安裝腳本。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/amgio38/Y20260923/main/install.sh | bash
#   （或先 clone 下來在本機跑：bash install.sh）
#
# 做的事：檢查 git → 確認有能用的 go（系統沒有、太舊、或壞掉就自己下載一份乾淨
# 的 go 工具鏈，不依賴這台機器剛好裝好對的環境）→ clone（或用現有 checkout）→
# make build → 把 pb 裝進 PATH 裡 → 印下一步該做什麼。純 Go（modernc.org/sqlite
# 無 cgo），不需要額外的 C 工具鏈；跨平台編譯見 Makefile 的 linux/windows target。
#
# 這是公開在 GitHub 上的 repo，安裝的人在什麼機器、什麼帳號權限下跑都有可能，
# 所以走保守路線：不猜使用者想把原始碼放哪，一開始就問，預設值是「現在所在目
# 錄底下的 project_board 子目錄」，不是使用者的家目錄——避免沒問過使用者同意
# 就在 $HOME 底下生東西。目標目錄如果已經有內容、又不是既有的 project_board
# checkout，直接中止，不覆蓋。
#
# 可用環境變數覆寫：
#   PROJECT_BOARD_REPO      repo clone 網址（預設 origin）
#   PROJECT_BOARD_HOME      原始碼＋資料要放哪裡（設了就不會互動詢問，預設：詢問使用者，預設值＝目前所在目錄底下的 project_board）
#   PROJECT_BOARD_BINDIR    裝去哪個 PATH 目錄（預設 ~/.local/bin，這是編譯好的執行檔，跟原始碼目錄分開）
#   PROJECT_BOARD_GO_CACHE  獨立下載的 go 工具鏈放哪裡（預設 ~/.cache/project_board/go-toolchain）

set -euo pipefail

REPO="${PROJECT_BOARD_REPO:-https://github.com/amgio38/Y20260923.git}"
BIN_DIR="${PROJECT_BOARD_BINDIR:-$HOME/.local/bin}"
GO_CACHE_DIR="${PROJECT_BOARD_GO_CACHE:-$HOME/.cache/project_board/go-toolchain}"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m錯誤：\033[0m %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "找不到 $1，請先安裝（$2）再重跑這支腳本。"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

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
	local os arch manifest line fname sha tmp_tar

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

	need curl "https://curl.se"
	log "系統沒有可用的 go，改用官方 go.dev 下載一份乾淨的工具鏈（放在 $GO_CACHE_DIR，不影響系統）"

	manifest="$(curl -fsSL 'https://go.dev/dl/?mode=json')" \
		|| die "抓不到 go.dev 的版本清單，檢查一下網路連線，或手動安裝 go >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（https://go.dev/dl/）。"

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
	tmp_tar="$(mktemp "${TMPDIR:-/tmp}/go-toolchain.XXXXXX.tar.gz")"
	trap 'rm -f "$tmp_tar"' RETURN

	log "下載 https://go.dev/dl/$fname"
	curl -fsSL "https://go.dev/dl/$fname" -o "$tmp_tar" \
		|| die "下載 go 工具鏈失敗，檢查網路連線後重跑。"

	got_sha="$(sha256_of "$tmp_tar")"
	[ "$got_sha" = "$sha" ] \
		|| die "下載的 go 工具鏈 checksum 對不上（預期 $sha，拿到 $got_sha），可能是下載損毀或被竄改，已中止安裝。"

	rm -rf "$GO_CACHE_DIR/go"
	tar -xzf "$tmp_tar" -C "$GO_CACHE_DIR"
	[ -x "$GO_BIN" ] || die "解壓後找不到可執行的 $GO_BIN，go.dev 的封裝格式可能變了，請手動安裝 go（https://go.dev/dl/）。"
	log "獨立 go 工具鏈就緒：$GO_BIN（$("$GO_BIN" version)）"
}

log "檢查必要工具（git／go）"
need git "https://git-scm.com/downloads"

GO_BIN=""
system_go_usable || bootstrap_go
log "使用 go：$GO_BIN"

# 預設安裝目錄＝使用者現在所在目錄底下的 project_board 子目錄（不是 $HOME），
# 誰在哪裡執行這支腳本，原始碼就裝在那個目錄下面。
DEFAULT_HOME_DIR="$(pwd)/project_board"

if [ -f "./go.mod" ] && grep -q '^module project_board$' "./go.mod" 2>/dev/null; then
	log "偵測到已經在 project_board 的 checkout 裡，直接用這份，不重新 clone"
	HOME_DIR="$(pwd)"
elif [ -n "${PROJECT_BOARD_HOME:-}" ]; then
	HOME_DIR="$PROJECT_BOARD_HOME"
	log "用環境變數指定的安裝目錄：$HOME_DIR"
else
	prompt_dir=""
	prompt_msg="安裝目錄（原始碼＋資料放這裡，直接按 Enter 用預設值 $DEFAULT_HOME_DIR）： "
	if [ -t 0 ]; then
		read -r -p "$prompt_msg" prompt_dir
	elif ! read -r -p "$prompt_msg" prompt_dir 2>/dev/null < /dev/tty; then
		# 例如 curl | bash 且沒有終端機可問（stdin 被 pipe 佔掉、/dev/tty 也開不了）：
		# 不強行卡住等輸入，直接用預設值，讓 unattended 安裝也能跑完。
		log "非互動模式（沒有終端機可問），用預設安裝目錄：$DEFAULT_HOME_DIR"
		log "要指定別的路徑：PROJECT_BOARD_HOME=/your/path bash install.sh，或先把腳本存下來本機執行再回答。"
		prompt_dir=""
	fi
	HOME_DIR="${prompt_dir:-$DEFAULT_HOME_DIR}"
fi

# 展開 ~，並把路徑轉成絕對路徑（不管使用者輸入的是相對路徑還是帶不帶結尾斜線）。
case "$HOME_DIR" in
"~"*) HOME_DIR="$HOME${HOME_DIR#\~}" ;;
esac
mkdir -p "$HOME_DIR"
HOME_DIR="$(cd "$HOME_DIR" && pwd)"

if [ -d "$HOME_DIR/.git" ]; then
	log "$HOME_DIR 已經是 git checkout，跑 git pull 更新"
	git -C "$HOME_DIR" pull --ff-only
elif [ -n "$(ls -A "$HOME_DIR" 2>/dev/null)" ]; then
	die "$HOME_DIR 不是空目錄，而且不是既有的 project_board checkout。安全起見不會覆蓋既有內容——換一個空目錄，或指到既有的 checkout，再重跑。"
else
	log "clone $REPO 到 $HOME_DIR"
	git clone "$REPO" "$HOME_DIR"
fi

cd "$HOME_DIR"

log "make build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
PATH="$(dirname "$GO_BIN"):$PATH" make build

mkdir -p "$BIN_DIR"
cp -f bin/pb "$BIN_DIR/pb"
chmod +x "$BIN_DIR/pb"
log "已裝到 $BIN_DIR/pb"

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*)
	log "$BIN_DIR 還不在 PATH 裡，把這行加進你的 shell rc（~/.bashrc 或 ~/.zshrc）："
	echo "  export PATH=\"$BIN_DIR:\$PATH\""
	;;
esac

cat <<EOF

安裝完成，只有一件事要記：**兩支 pb 是同一個東西的兩個位置，不是兩套系統**——
$BIN_DIR/pb 是裝好的執行檔本體；$HOME_DIR 是原始碼＋資料（var/board.db）放的地方。
資料庫不用另外初始化，第一次啟動就會自動建好。

下一步，啟動 dashboard 二選一：

  1.（推薦）背景常駐，綁 0.0.0.0:8787，重開機也好管理：
       cd "$HOME_DIR" && ./service.sh start
       ./service.sh status   # 看有沒有活著
       ./service.sh stop     # 關掉

  2. 前景跑（測試/除錯用，Ctrl-C 結束就沒了）：
       $BIN_DIR/pb serve --db "$HOME_DIR/var/board.db"
     （PATH 設定生效後，上面這行也可以直接打 pb 不用寫完整路徑）

瀏覽器開 http://<這台機器的位址>:8787 看畫面。

接 MCP（給 harness/agent 用，stdio）：
  $BIN_DIR/pb mcp --db "$HOME_DIR/var/board.db"
詳細的 MCP client 設定／可用工具清單見 $HOME_DIR/README.md「MCP 介面」一節。

git commit 帶單號自動掛連結這類進階用法，不影響現在能不能跑起來，要用再查
$HOME_DIR/docs/GIT_INTEGRATION.md。

EOF
