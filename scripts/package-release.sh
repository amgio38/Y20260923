#!/usr/bin/env bash
# 把 pb 打包成一份「裝好就能跑的專案根」release 壓縮檔。
#
#   scripts/package-release.sh <goos> <goarch> <輸出目錄>
#   scripts/package-release.sh linux amd64 dist
#
# 產出（<輸出目錄> 底下）：
#   projectboard-<goos>-<goarch>.tar.gz   （windows 是 .zip）
#   projectboard-<goos>-<goarch>.<副檔名>.sha256   （sha256sum 格式：雜湊值＋兩個空白＋檔名）
#
# 壓縮檔裡沒有外層目錄，內容直接就是專案根的樣子：
#   bin/pb（windows 是 bin/pb.exe）  go.mod  LICENSE  README.md  owners.example.txt
#   service.sh（windows 沒有）  docs/  skill/
#
# 為什麼要帶 go.mod：pb 找預設資料庫（var/board.db）和 owners.txt 時，是從自己
# 執行檔的實體路徑往上推「專案根」，判定條件是「執行檔在 <root>/bin/ 且 <root>/go.mod
# 存在」（見 internal/domain 的 ProjectRootFor，解開符號連結後才判定）。這份 go.mod 只是那個判定用的標記，
# 預編譯版裝的人不需要 Go。
#
# 這支腳本同時被 .github/workflows/release.yml 和 scripts/install-sh.test.sh 呼叫：
# 兩邊用同一支，測試打的包才會跟真正發布的是同一種形狀。
set -euo pipefail

usage() {
	echo "用法：$(basename "$0") <goos> <goarch> <輸出目錄>" >&2
	exit 2
}
[ $# -eq 3 ] || usage
goos=$1 goarch=$2 out=$3

case "$goos" in
linux | darwin | windows) ;;
*) echo "package-release.sh: 不支援的 goos：$goos" >&2; exit 2 ;;
esac
case "$goarch" in
amd64 | arm64) ;;
*) echo "package-release.sh: 不支援的 goarch：$goarch" >&2; exit 2 ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

if [ "$goos" = windows ]; then
	exe=pb.exe
	ext=zip
else
	exe=pb
	ext=tar.gz
fi
name="projectboard-$goos-$goarch"
asset="$name.$ext"

mkdir -p "$out"
out="$(cd "$out" && pwd)"
stage="$(mktemp -d "${TMPDIR:-/tmp}/projectboard-package.XXXXXX")"
trap 'rm -rf "$stage"' EXIT

mkdir -p "$stage/bin"
# 純 Go（modernc.org/sqlite 無 cgo）：固定關掉 cgo，編出來的是不吃系統函式庫的靜態執行檔。
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w" -o "$stage/bin/$exe" ./cmd/pb
chmod 0755 "$stage/bin/$exe"

cp go.mod LICENSE README.md owners.example.txt "$stage/"
cp -R docs skill "$stage/"
if [ "$goos" != windows ]; then
	cp service.sh "$stage/"
	chmod 0755 "$stage/service.sh"
fi
# docs／skill 裡不應該有編譯殘留。
find "$stage" -name '__pycache__' -type d -prune -exec rm -rf {} +

rm -f "$out/$asset" "$out/$asset.sha256"
if [ "$ext" = zip ]; then
	if command -v zip >/dev/null 2>&1; then
		(cd "$stage" && zip -qr -X "$out/$asset" .)
	else
		(cd "$stage" && python3 -m zipfile -c "$out/$asset" .)
	fi
else
	# --sort／--owner 等是 GNU tar 的選項（release 在 ubuntu runner 上打包）。
	(cd "$stage" && tar --sort=name --owner=0 --group=0 --numeric-owner -czf "$out/$asset" .)
fi

if command -v sha256sum >/dev/null 2>&1; then
	(cd "$out" && sha256sum "$asset" >"$asset.sha256")
else
	(cd "$out" && shasum -a 256 "$asset" >"$asset.sha256")
fi

echo "package-release.sh: $out/$asset"
cat "$out/$asset.sha256"
