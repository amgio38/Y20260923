#!/usr/bin/env bash
# ProjectBoard 一鍵安裝腳本。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/amgio38/Y20260923/main/install.sh | bash
#   （或先 clone 下來在本機跑：bash install.sh）
#
# 做的事：檢查 git/go → clone（或用現有 checkout）→ make build → 把 pb
# 裝進 PATH 裡 → 印下一步該做什麼。純 Go（modernc.org/sqlite 無 cgo），
# 不需要額外的 C 工具鏈；跨平台編譯見 Makefile 的 linux/windows target。
#
# 可用環境變數覆寫：
#   PROJECT_BOARD_REPO   repo clone 網址（預設 origin）
#   PROJECT_BOARD_HOME   clone 到哪裡（預設 ~/project_board）
#   PROJECT_BOARD_BINDIR 裝去哪個 PATH 目錄（預設 ~/.local/bin）

set -euo pipefail

REPO="${PROJECT_BOARD_REPO:-https://github.com/amgio38/Y20260923.git}"
HOME_DIR="${PROJECT_BOARD_HOME:-$HOME/project_board}"
BIN_DIR="${PROJECT_BOARD_BINDIR:-$HOME/.local/bin}"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m錯誤：\033[0m %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "找不到 $1，請先安裝（$2）再重跑這支腳本。"
}

check_go_version() {
	local ver major minor
	ver="$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go')"
	major="${ver%%.*}"
	minor="${ver##*.}"
	if [ "$major" -lt "$MIN_GO_MAJOR" ] || { [ "$major" -eq "$MIN_GO_MAJOR" ] && [ "$minor" -lt "$MIN_GO_MINOR" ]; }; then
		die "go 版本 $ver 太舊，需要 >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR}（go.mod 要求）。"
	fi
}

log "檢查必要工具（git／go）"
need git "https://git-scm.com/downloads"
need go "https://go.dev/dl/"
check_go_version

if [ -f "./go.mod" ] && grep -q '^module project_board$' "./go.mod" 2>/dev/null; then
	log "偵測到已經在 project_board 的 checkout 裡，直接用這份，不重新 clone"
	HOME_DIR="$(pwd)"
elif [ -d "$HOME_DIR/.git" ]; then
	log "$HOME_DIR 已經是 git checkout，跑 git pull 更新"
	git -C "$HOME_DIR" pull --ff-only
else
	log "clone $REPO 到 $HOME_DIR"
	git clone "$REPO" "$HOME_DIR"
fi

cd "$HOME_DIR"

log "make build（純 Go，第一次會下載 go.mod 的依賴，需要網路）"
make build

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
