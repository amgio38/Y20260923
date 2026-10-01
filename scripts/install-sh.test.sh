#!/usr/bin/env bash
# install.sh 的端到端測試，網路用假的取代。
#
# `curl` 和 `uname` 在 PATH 上被換成假的，所以跑的是真正的 install.sh——參數解析、平台判斷、
# 壓縮檔成員白名單、checksum 驗證、pb 能不能跑的檢查、安裝目錄的處理、符號連結——對著
# 這裡用 scripts/package-release.sh 真的打出來的壓縮檔（裡面是真的 pb）。這是唯一測得到
# 重點的方法：這支腳本最重要的是「失敗時的決定」——沒驗證過、被動過手腳、跑不起來的檔案，
# 絕對不能進安裝目錄。
#
# 用法：scripts/install-sh.test.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
installer="${PB_INSTALLER:-$(cd "$here/.." && pwd)/install.sh}"
root="$(cd "$here/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/pb-install-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT INT TERM

fail=0
ok() { echo "ok: $1"; }
bad() {
	echo "FAIL: $1" >&2
	[ $# -lt 2 ] || echo "  $2" >&2
	fail=1
}

command -v python3 >/dev/null 2>&1 || { echo "install-sh.test: 需要 python3（組壞掉的壓縮檔用）" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "install-sh.test: 需要 go（打包真的 pb 用）" >&2; exit 2; }

# --- 一份真的 release 壓縮檔 --------------------------------------------------
# 跟 .github/workflows/release.yml 用同一支打包腳本，所以測試吃的跟真正發布的是同一種形狀。
dist="$work/dist"
bash "$here/package-release.sh" linux amd64 "$dist" >/dev/null
base_asset="projectboard-linux-amd64.tar.gz"
[ -f "$dist/$base_asset" ] || { echo "install-sh.test: 打包失敗" >&2; exit 2; }

# 同一個壓縮檔換個名字，當作別的平台的 asset（測的是「安裝腳本有沒有要對的檔名」，
# 不是在別的 CPU 上跑；裡面的 pb 在這台機器上能跑就夠了）。checksum 檔跟著改名。
serve="$work/serve"
mkdir -p "$serve"
publish_as() { # publish_as <asset 名稱> <來源 tar.gz>
	cp "$2" "$serve/$1"
	(cd "$serve" && sha256sum "$1" >"$1.sha256")
}
for plat in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
	publish_as "projectboard-$plat.tar.gz" "$dist/$base_asset"
done

# --- 假的 curl 與 uname ------------------------------------------------------
stub_dir="$work/bin"
mkdir -p "$stub_dir"
cat >"$stub_dir/curl" <<'EOF'
#!/bin/sh
# 代替 `curl -fsSL --proto '=https' ... <url> -o <out>`。URL 靠長相找（唯一 https:// 開頭的參數），
# 每次呼叫都記下來，案例才能斷言「它到底抓了哪個網址」，而不只是檔案有沒有到。
[ -z "${STUB_LOG:-}" ] || printf '%s\n' "$*" >>"$STUB_LOG"
out="" url="" prev=""
for arg in "$@"; do
	[ "$prev" = "-o" ] && out="$arg"
	case "$arg" in https://*) url="$arg" ;; esac
	prev="$arg"
done
[ -n "$url" ] && [ -n "$out" ] || exit 2
[ -z "${STUB_OFFLINE:-}" ] || exit 6
name="${url##*/}"
case "$name" in
*.sha256)
	[ -z "${STUB_NO_CHECKSUM:-}" ] || exit 22
	if [ -n "${STUB_EMPTY_CHECKSUM:-}" ]; then : >"$out"; exit 0; fi
	[ -f "$SERVE_DIR/$name" ] || exit 22
	cp "$SERVE_DIR/$name" "$out" ;;
projectboard-*.tar.gz)
	[ -z "${STUB_NO_ASSET:-}" ] || exit 22
	[ -f "$SERVE_DIR/$name" ] || exit 22
	cp "$SERVE_DIR/$name" "$out" ;;
*) exit 22 ;;
esac
EOF
chmod 0755 "$stub_dir/curl"

uname_dir="$work/uname-bin"
mkdir -p "$uname_dir"
cat >"$uname_dir/uname" <<'EOF'
#!/bin/sh
case "$1" in
-s) echo "${FAKE_UNAME_S:-Linux}" ;;
-m) echo "${FAKE_UNAME_M:-x86_64}" ;;
*) echo "${FAKE_UNAME_S:-Linux}" ;;
esac
EOF
chmod 0755 "$uname_dir/uname"

# 安裝腳本需要的最小工具集合，而且刻意沒有 git／go／make：要走原始碼編譯的案例會在第一個
# 檢查就停下來，不會真的去編譯（這台開發機的 /usr/bin 裡就有 git，不能直接拿來當 PATH）。
core="$work/core"
mkdir -p "$core" "$work/home"
for tool in bash sh env awk sed head tr tar gzip mktemp sha256sum grep cat ls cp mv rm mkdir rmdir chmod ln dirname basename kill sort wc setsid readlink; do
	p=$(command -v "$tool" 2>/dev/null || true)
	[ -z "$p" ] || ln -sf "$p" "$core/$tool"
done

# 跑 install.sh。環境變數用 env -i 清乾淨；終端機用 setsid 脫離，/dev/tty 打不開，
# 才測得到「沒有終端機」的路徑。額外的參數傳給 install.sh。
run_install() {
	local extra_env=()
	[ -z "${EXTRA_ENV:-}" ] || read -r -a extra_env <<<"$EXTRA_ENV"
	local setsid_cmd=()
	command -v setsid >/dev/null 2>&1 && setsid_cmd=(setsid -w)
	mkdir -p "$work/neutral"
	( cd "${RUN_CWD:-$work/neutral}" && env -i \
		PATH="$uname_dir:$stub_dir:$core" \
		HOME="${TEST_HOME-$work/home}" \
		SERVE_DIR="$serve" \
		STUB_LOG="${STUB_LOG:-}" \
		${STUB_NO_CHECKSUM:+STUB_NO_CHECKSUM=1} \
		${STUB_EMPTY_CHECKSUM:+STUB_EMPTY_CHECKSUM=1} \
		${STUB_NO_ASSET:+STUB_NO_ASSET=1} \
		${STUB_OFFLINE:+STUB_OFFLINE=1} \
		${FAKE_UNAME_S:+FAKE_UNAME_S="$FAKE_UNAME_S"} \
		${FAKE_UNAME_M:+FAKE_UNAME_M="$FAKE_UNAME_M"} \
		${TMPDIR:+TMPDIR="$TMPDIR"} \
		${PB_REPO_OVERRIDE:+PROJECT_BOARD_REPO="$PB_REPO_OVERRIDE"} \
		"${extra_env[@]}" \
		"${setsid_cmd[@]}" bash "$installer" "$@" </dev/null >"$work/out" 2>"$work/err" ) && echo 0 || echo $?
}
# 常用：指定安裝目錄與連結目錄。
fresh_env() { echo "PROJECT_BOARD_HOME=$1 PROJECT_BOARD_BINDIR=$2"; }

reset_serve() {
	rm -f "$serve"/*
	for plat in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
		publish_as "projectboard-$plat.tar.gz" "$dist/$base_asset"
	done
}

# --- 預編譯版裝得起來 ---------------------------------------------------------
H="$work/h1" B="$work/b1"
: >"$work/stub.log"
code=$(STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$H" "$B")" run_install)
if [ "$code" = 0 ] && [ -x "$H/bin/pb" ] && [ -L "$B/pb" ]; then
	ok "預編譯版裝得起來：bin/pb 在安裝目錄，PATH 目錄裡是符號連結"
else
	bad "預編譯版裝得起來" "exit $code: $(cat "$work/err")"
fi
if [ "$(readlink "$B/pb")" = "$H/bin/pb" ]; then ok "符號連結指向 <安裝目錄>/bin/pb"; else bad "符號連結指向 <安裝目錄>/bin/pb" "$(readlink "$B/pb" || true)"; fi
missing=""
for f in go.mod LICENSE README.md owners.example.txt service.sh docs skill; do [ -e "$H/$f" ] || missing="$missing $f"; done
[ -z "$missing" ] && ok "專案根該有的檔案都在（go.mod、docs、skill、service.sh…）" || bad "專案根該有的檔案都在" "缺：$missing"
[ ! -e "$H/.git" ] && ok "預編譯版不帶 .git（不需要 git 就能裝）" || bad "預編譯版不帶 .git"
if grep -q 'projectboard-linux-amd64.tar.gz' "$work/stub.log" && grep -q 'github.com/amgio38/Y20260923/releases/latest/download/' "$work/stub.log"; then
	ok "預設從官方 repo 的 latest release 抓這個平台的檔案"
else
	bad "預設從官方 repo 的 latest release 抓這個平台的檔案" "$(cat "$work/stub.log")"
fi
if grep -q -- '--proto =https' "$work/stub.log" && grep -q -- '--tlsv1.2' "$work/stub.log"; then
	ok "下載限定 https 並設 TLS 下限"
else
	bad "下載限定 https 並設 TLS 下限" "$(cat "$work/stub.log")"
fi
# 不管在哪個目錄打 pb，預設資料庫都在專案根（靠符號連結被解開後推出專案根）。
mkdir -p "$work/elsewhere"
if (cd "$work/elsewhere" && "$B/pb" init >/dev/null 2>&1) && [ -f "$H/var/board.db" ] && [ ! -e "$work/elsewhere/var" ]; then
	ok "從別的目錄打 pb，資料庫仍落在 <安裝目錄>/var/board.db"
else
	bad "從別的目錄打 pb，資料庫仍落在 <安裝目錄>/var/board.db" "$(ls -R "$work/elsewhere" 2>&1 | head -5)"
fi
if "$B/pb" version | grep -qE '^V[0-9]+\.[0-9]{8}\.[0-9]{3}$'; then ok "pb version 回報 V0.YYYYMMDD.NNN"; else bad "pb version 回報版號"; fi
if grep -q 'export PATH=' "$work/out"; then ok "連結目錄不在 PATH 時，給出現成的 export 那一行"; else bad "給出現成的 export 那一行" "$(cat "$work/out")"; fi

# --- 升級：只換我們發布的檔案，資料與名冊不動 ------------------------------------
echo "keep-me" >"$H/owners.txt"
mkdir -p "$H/var"
echo "db-bytes" >"$H/var/board.db"
echo "old docs" >"$H/docs/OPERATIONS.md"
code=$(EXTRA_ENV="$(fresh_env "$H" "$B")" run_install)
if [ "$code" = 0 ] && [ "$(cat "$H/owners.txt")" = keep-me ] && [ "$(cat "$H/var/board.db")" = db-bytes ]; then
	ok "重跑＝升級：var/board.db 和 owners.txt 原封不動"
else
	bad "重跑＝升級：var/board.db 和 owners.txt 原封不動" "exit $code: $(cat "$work/err")"
fi
if [ "$(cat "$H/docs/OPERATIONS.md")" != "old docs" ]; then ok "升級會更新說明文件"; else bad "升級會更新說明文件"; fi

# --- 版本旗標與環境變數 ---------------------------------------------------------
: >"$work/stub.log"
H2="$work/h2"
code=$(STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$H2" "$work/b2")" run_install --version v0.20260928.006)
if [ "$code" = 0 ] && grep -q '/releases/download/v0.20260928.006/projectboard-linux-amd64.tar.gz' "$work/stub.log"; then
	ok "--version 指定標籤，從 /download/<標籤>/ 抓"
else
	bad "--version 指定標籤" "exit $code: $(cat "$work/stub.log")"
fi
: >"$work/stub.log"
code=$(STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$work/h3" "$work/b3") PROJECT_BOARD_RELEASE_BASE=https://mirror.example.com/pb/releases" run_install)
if [ "$code" = 0 ] && grep -q 'https://mirror.example.com/pb/releases/latest/download/' "$work/stub.log"; then
	ok "PROJECT_BOARD_RELEASE_BASE 可以指到內部鏡像"
else
	bad "PROJECT_BOARD_RELEASE_BASE 可以指到內部鏡像" "exit $code: $(cat "$work/stub.log")"
fi

# --- 平台判斷 -----------------------------------------------------------------
for spec in "Linux aarch64 linux-arm64" "Linux arm64 linux-arm64" "Darwin x86_64 darwin-amd64" "Darwin arm64 darwin-arm64"; do
	set -- $spec
	: >"$work/stub.log"
	code=$(FAKE_UNAME_S=$1 FAKE_UNAME_M=$2 STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$work/hp" "$work/bp")" run_install)
	if [ "$code" = 0 ] && grep -q "projectboard-$3.tar.gz" "$work/stub.log"; then
		ok "$1 $2 要的是 projectboard-$3.tar.gz"
	else
		bad "$1 $2 要的是 projectboard-$3.tar.gz" "exit $code: $(cat "$work/err"); log: $(cat "$work/stub.log")"
	fi
	rm -rf "$work/hp" "$work/bp"
done
for win in MINGW64_NT-10.0 MSYS_NT-10.0 CYGWIN_NT-10.0; do
	rm -rf "$work/hw"
	: >"$work/stub.log"
	code=$(FAKE_UNAME_S=$win STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$work/hw" "$work/bw")" run_install)
	if [ "$code" != 0 ] && grep -q 'install.ps1' "$work/err" && [ ! -e "$work/hw" ] && [ ! -s "$work/stub.log" ]; then
		ok "Windows 的 bash（$win）被擋下，並指向 install.ps1，沒下載也沒建任何東西"
	else
		bad "Windows 的 bash（$win）被擋下並指向 install.ps1" "exit $code: $(cat "$work/err")"
	fi
done
for spec in "Linux armv7l" "Linux i686" "FreeBSD amd64"; do
	set -- $spec
	rm -rf "$work/hs"
	: >"$work/stub.log"
	code=$(FAKE_UNAME_S=$1 FAKE_UNAME_M=$2 STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$work/hs" "$work/bs")" run_install)
	if [ "$code" != 0 ] && grep -q '原始碼編譯' "$work/err" && grep -q '找不到 git' "$work/err" && [ ! -s "$work/stub.log" ]; then
		ok "$1 $2 沒有預編譯版：說明原因、不下載、直接走原始碼編譯"
	else
		bad "$1 $2 沒有預編譯版時走原始碼編譯" "exit $code: $(cat "$work/err")"
	fi
done

# --- 沒有預編譯版可抓 → 退回原始碼編譯 -------------------------------------------
rm -rf "$work/hn"
code=$(STUB_NO_ASSET=1 EXTRA_ENV="$(fresh_env "$work/hn" "$work/bn")" run_install)
if [ "$code" != 0 ] && grep -q '抓不到預編譯版' "$work/err" && grep -q '找不到 git' "$work/err" && [ ! -e "$work/hn/bin/pb" ]; then
	ok "release 還沒發布（抓不到檔案）：說明原因並退回原始碼編譯"
else
	bad "release 還沒發布時退回原始碼編譯" "exit $code: $(cat "$work/err")"
fi
rm -rf "$work/hn"
: >"$work/stub.log"
code=$(STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$work/hn" "$work/bn")" run_install --from-source)
if [ "$code" != 0 ] && [ ! -s "$work/stub.log" ] && grep -q '找不到 git' "$work/err"; then
	ok "--from-source 完全不下載預編譯版"
else
	bad "--from-source 完全不下載預編譯版" "exit $code: $(cat "$work/err")"
fi

# --- 沒驗證過的檔案不能進安裝目錄 ----------------------------------------------
refused() { # refused <名稱> <應該出現在錯誤訊息的字串> <安裝目錄>
	if [ "$code" != 0 ] && grep -q -- "$2" "$work/err" && [ ! -e "$3/bin/pb" ]; then
		ok "$1"
	else
		bad "$1" "exit $code, 目錄裡有 pb：$([ -e "$3/bin/pb" ] && echo 有 || echo 沒有)；$(cat "$work/err")"
	fi
}
rm -rf "$work/hx"
code=$(STUB_NO_CHECKSUM=1 EXTRA_ENV="$(fresh_env "$work/hx" "$work/bx")" run_install)
refused "沒有發布 checksum：拒絕安裝（不會因此改裝沒驗證過的檔案）" "checksum" "$work/hx"
code=$(STUB_EMPTY_CHECKSUM=1 EXTRA_ENV="$(fresh_env "$work/hx" "$work/bx")" run_install)
refused "checksum 檔是空的：拒絕安裝" "checksum" "$work/hx"

# 內容被動過手腳：checksum 檔是原本的，asset 換成別的內容。
cp "$serve/projectboard-linux-amd64.tar.gz" "$work/orig.tar.gz"
printf 'tampered' >>"$serve/projectboard-linux-amd64.tar.gz"
code=$(EXTRA_ENV="$(fresh_env "$work/hx" "$work/bx")" run_install)
refused "asset 內容跟 checksum 對不上（被竄改／損毀）：拒絕安裝" "對不上" "$work/hx"
reset_serve
# checksum 檔指的是別的檔案。
(cd "$serve" && sha256sum projectboard-linux-arm64.tar.gz >projectboard-linux-amd64.tar.gz.sha256)
code=$(EXTRA_ENV="$(fresh_env "$work/hx" "$work/bx")" run_install)
refused "checksum 檔指的是別的檔案：拒絕安裝" "checksum" "$work/hx"
reset_serve

# --- 惡意／壞掉的壓縮檔 ---------------------------------------------------------
# 把好的壓縮檔解開，加入一個壞成員（或換掉 bin/pb），重新打包，再發布成 amd64 的 asset（checksum 是對的——
# 這一關擋的是「簽得對但內容不該出現」的東西）。
evil_archive() { # evil_archive <python 程式片段，變數 tf 是開啟寫入的 TarFile>
	python3 - "$dist/$base_asset" "$work/evil.tar.gz" "$1" <<'PY'
import sys, tarfile, io
src, dst, snippet = sys.argv[1], sys.argv[2], sys.argv[3]
with tarfile.open(src) as s, tarfile.open(dst, "w:gz") as tf:
    for m in s.getmembers():
        tf.addfile(m, s.extractfile(m) if m.isfile() else None)
    exec(snippet)
PY
	publish_as "projectboard-linux-amd64.tar.gz" "$work/evil.tar.gz"
}
evil_cases=(
	"路徑跳脫（../evil）|ti = tarfile.TarInfo('../evil'); ti.size = 1; tf.addfile(ti, io.BytesIO(b'x'))|不安全的路徑"
	"絕對路徑（/tmp/evil）|ti = tarfile.TarInfo('/tmp/pb-evil'); ti.size = 1; tf.addfile(ti, io.BytesIO(b'x'))|不安全的路徑"
	"不認得的頂層項目（evil.txt）|ti = tarfile.TarInfo('evil.txt'); ti.size = 1; tf.addfile(ti, io.BytesIO(b'x'))|不認得的項目"
	"bin/ 底下多一個檔案|ti = tarfile.TarInfo('bin/evil'); ti.size = 1; tf.addfile(ti, io.BytesIO(b'x'))|只能有 pb"
	"符號連結成員|ti = tarfile.TarInfo('docs/link'); ti.type = tarfile.SYMTYPE; ti.linkname = '/etc/passwd'; tf.addfile(ti)|符號連結"
)
for c in "${evil_cases[@]}"; do
	name="${c%%|*}"; rest="${c#*|}"; snippet="${rest%|*}"; want="${rest##*|}"
	rm -rf "$work/hx"
	evil_archive "$snippet"
	code=$(EXTRA_ENV="$(fresh_env "$work/hx" "$work/bx")" run_install)
	refused "惡意壓縮檔（$name）：整包拒絕" "$want" "$work/hx"
done
if [ ! -e /tmp/pb-evil ] && [ ! -e "$work/evil" ]; then ok "惡意壓縮檔沒有在安裝目錄之外寫出任何東西"; else bad "惡意壓縮檔沒有在安裝目錄之外寫出任何東西"; fi
reset_serve

# bin/pb 是個跑不起來的檔案：不能蓋掉能用的舊版。
H4="$work/h4"
code=$(EXTRA_ENV="$(fresh_env "$H4" "$work/b4")" run_install)
old_sum=$(sha256sum "$H4/bin/pb" | awk '{print $1}')
python3 - "$dist/$base_asset" "$work/broken.tar.gz" <<'PY'
import sys, tarfile, io
src, dst = sys.argv[1], sys.argv[2]
with tarfile.open(src) as s, tarfile.open(dst, "w:gz") as tf:
    for m in s.getmembers():
        if m.name.lstrip("./") == "bin/pb":
            data = b"#!/bin/sh\necho not-a-version\n"
            m.size = len(data)
            tf.addfile(m, io.BytesIO(data))
        else:
            tf.addfile(m, s.extractfile(m) if m.isfile() else None)
PY
publish_as "projectboard-linux-amd64.tar.gz" "$work/broken.tar.gz"
code=$(EXTRA_ENV="$(fresh_env "$H4" "$work/b4")" run_install)
new_sum=$(sha256sum "$H4/bin/pb" | awk '{print $1}')
if [ "$code" != 0 ] && grep -q '跑不起來' "$work/err" && [ "$old_sum" = "$new_sum" ]; then
	ok "新下載的 pb 跑不起來：拒絕，而且舊版原封不動"
else
	bad "新下載的 pb 跑不起來：拒絕且舊版不動" "exit $code: $(cat "$work/err")"
fi
reset_serve

# --- 安裝目錄的處理 -------------------------------------------------------------
H5="$work/h5"
mkdir -p "$H5"
echo "someone else's file" >"$H5/notes.txt"
code=$(EXTRA_ENV="$(fresh_env "$H5" "$work/b5")" run_install)
if [ "$code" != 0 ] && grep -q '不是空目錄' "$work/err" && [ ! -e "$H5/bin" ] && [ "$(cat "$H5/notes.txt")" = "someone else's file" ]; then
	ok "有別人東西的非空目錄：拒絕，一個檔案都不動"
else
	bad "有別人東西的非空目錄：拒絕" "exit $code: $(cat "$work/err")"
fi
# 開發者的 git checkout：不拿預編譯版去蓋，走 git pull（這裡沒有 git，所以停在檢查）。
H6="$work/h6"
mkdir -p "$H6/.git"
echo "module project_board" >"$H6/go.mod"
: >"$work/stub.log"
code=$(STUB_LOG="$work/stub.log" EXTRA_ENV="$(fresh_env "$H6" "$work/b6")" run_install)
if [ "$code" != 0 ] && [ ! -s "$work/stub.log" ] && [ ! -e "$H6/bin" ]; then
	ok "git checkout 不會被預編譯版覆蓋（走原始碼更新路徑）"
else
	bad "git checkout 不會被預編譯版覆蓋" "exit $code: $(cat "$work/err")"
fi
# 沒有指定安裝目錄、也沒有終端機：用預設值（目前目錄底下的 project_board），不卡住。
mkdir -p "$work/cwd"
code=$(RUN_CWD="$work/cwd" EXTRA_ENV="PROJECT_BOARD_BINDIR=$work/b7" run_install)
if [ "$code" = 0 ] && [ -x "$work/cwd/project_board/bin/pb" ] && grep -q '非互動模式' "$work/out"; then
	ok "沒終端機也沒指定目錄：用預設的 ./project_board，不卡住等輸入，並說明怎麼指定"
else
	bad "沒終端機也沒指定目錄時用預設目錄" "exit $code: $(cat "$work/err")"
fi

# --- 輸入檢查 -----------------------------------------------------------------
for badver in '../../evil' '-x' 'v1/../2' 'a b' 'v1;rm'; do
	rm -rf "$work/hv"
	code=$(EXTRA_ENV="$(fresh_env "$work/hv" "$work/bv")" run_install --version "$badver")
	if [ "$code" != 0 ] && grep -q '不合法' "$work/err" && [ ! -e "$work/hv" ]; then
		ok "--version '$badver' 在做任何事之前就被拒絕"
	else
		bad "--version '$badver' 被拒絕" "exit $code: $(cat "$work/err")"
	fi
done
for badrepo in '-evil' 'a b'; do
	code=$(EXTRA_ENV="$(fresh_env "$work/hv" "$work/bv")" PB_REPO_OVERRIDE="$badrepo" run_install)
	if [ "$code" != 0 ] && grep -q '不合法' "$work/err"; then ok "PROJECT_BOARD_REPO='$badrepo' 被拒絕"; else bad "PROJECT_BOARD_REPO='$badrepo' 被拒絕" "exit $code: $(cat "$work/err")"; fi
done
code=$(EXTRA_ENV="$(fresh_env "$work/hv" "$work/bv") PROJECT_BOARD_RELEASE_BASE=http://insecure.example.com" run_install)
if [ "$code" != 0 ] && grep -q 'https://' "$work/err"; then ok "RELEASE_BASE 不是 https 就拒絕"; else bad "RELEASE_BASE 不是 https 就拒絕" "exit $code: $(cat "$work/err")"; fi
code=$(TEST_HOME="" EXTRA_ENV="" run_install)
if [ "$code" != 0 ] && grep -q 'HOME 沒有設定' "$work/err"; then ok "沒有 HOME：說得出怎麼辦，不是 bash 的 unbound variable"; else bad "沒有 HOME 時的訊息" "exit $code: $(cat "$work/err")"; fi
code=$(EXTRA_ENV="" run_install --bogus)
if [ "$code" = 2 ]; then ok "不認得的參數：exit 2 並印用法"; else bad "不認得的參數：exit 2" "exit $code"; fi
code=$(EXTRA_ENV="" run_install --help)
if [ "$code" = 0 ] && grep -q -- '--from-source' "$work/out"; then ok "--help 不需要 HOME 也能用"; else bad "--help" "exit $code"; fi

# 不能寫的目錄（root 什麼都能寫，所以只在非 root 時測）。
if [ "$(id -u)" != 0 ]; then
	mkdir -p "$work/ro"
	chmod 0555 "$work/ro"
	code=$(EXTRA_ENV="$(fresh_env "$work/ro/h" "$work/b8")" run_install)
	if [ "$code" != 0 ] && grep -q '權限' "$work/err"; then ok "沒權限的目錄：說出原因與替代做法"; else bad "沒權限的目錄" "exit $code: $(cat "$work/err")"; fi
	chmod 0755 "$work/ro"
fi

# --- 暫存檔乾淨 ---------------------------------------------------------------
mkdir -p "$work/tmpdir"
rm -rf "$work/hc"
code=$(TMPDIR="$work/tmpdir" EXTRA_ENV="$(fresh_env "$work/hc" "$work/bc")" run_install)
left=$(ls -A "$work/tmpdir" | tr '\n' ' ')
if [ "$code" = 0 ] && [ -z "$left" ]; then ok "安裝成功後，下載與解壓的暫存區都清掉了"; else bad "安裝成功後暫存區清乾淨" "exit $code, 殘留：$left"; fi
rm -rf "$work/hc"
code=$(TMPDIR="$work/tmpdir" STUB_NO_CHECKSUM=1 EXTRA_ENV="$(fresh_env "$work/hc" "$work/bc")" run_install)
left=$(ls -A "$work/tmpdir" | tr '\n' ' ')
if [ "$code" != 0 ] && [ -z "$left" ]; then ok "安裝失敗時，暫存區也清掉了"; else bad "安裝失敗時暫存區清乾淨" "exit $code, 殘留：$left"; fi

# --- curl | bash：從 stdin 讀腳本、旗標用 bash -s -- 傳，下載被切斷什麼都不做 ----------
rm -rf "$work/hq"
mkdir -p "$work/neutral"
code=$(cd "$work/neutral" && env -i PATH="$uname_dir:$stub_dir:$core" HOME="$work/home" SERVE_DIR="$serve" \
	PROJECT_BOARD_HOME="$work/hq" PROJECT_BOARD_BINDIR="$work/bq" \
	bash -s -- --version v0.20260928.006 <"$installer" >"$work/out" 2>"$work/err" && echo 0 || echo $?)
if [ "$code" = 0 ] && [ -x "$work/hq/bin/pb" ]; then ok "用 pipe 把腳本餵給 bash（curl | bash）可以裝，旗標用 bash -s -- 傳"; else bad "curl | bash 可以裝" "exit $code: $(cat "$work/err")"; fi
size=$(wc -c <"$installer")
cut_ok=1
for fraction in 10 25 50 75 90 99; do
	cut=$((size * fraction / 100))
	rm -rf "$work/hcut" "$work/bcut"
	head -c "$cut" "$installer" >"$work/cut.sh"
	(cd "$work/neutral" && env -i PATH="$uname_dir:$stub_dir:$core" HOME="$work/home" SERVE_DIR="$serve" \
		PROJECT_BOARD_HOME="$work/hcut" PROJECT_BOARD_BINDIR="$work/bcut" \
		bash -s -- <"$work/cut.sh" >"$work/out" 2>"$work/err") || true
	if [ -e "$work/hcut" ] || [ -e "$work/bcut" ] || [ -s "$work/out" ]; then
		cut_ok=0
		bad "在 ${fraction}% 被切斷的腳本什麼都不做" "有建東西或有輸出：$(head -c 200 "$work/out")"
	fi
done
[ "$cut_ok" = 1 ] && ok "腳本在中途被切斷時什麼都不執行（6 個切點）"

# --- 解除安裝後再裝（目錄被別人刪過 bin）也不出錯 ---------------------------------
rm -rf "$work/hr/bin"
code=$(EXTRA_ENV="$(fresh_env "$work/hr" "$work/br")" run_install)
rm -rf "$work/hr/bin"
code=$(EXTRA_ENV="$(fresh_env "$work/hr" "$work/br")" run_install)
if [ "$code" = 0 ] && [ -x "$work/hr/bin/pb" ]; then ok "安裝目錄裡的 bin/ 被刪掉後重跑，補得回來"; else bad "bin/ 被刪掉後重跑" "exit $code: $(cat "$work/err")"; fi

if [ "$fail" -ne 0 ]; then
	echo "install-sh.test: FAILED" >&2
	exit 1
fi
echo "install-sh.test: all cases passed"
