#!/bin/sh
# 把 ProjectBoard 的 git hooks 裝進某個 repo（設定 core.hooksPath＋pb.bin／pb.db）。
# 來源：Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-HOOK。
#
# 用法：scripts/install-git-hooks.sh [/path/to/repo ...]   （省略＝目前目錄）
#      多個 repo 可一次傳入。
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"
hooks="$here/git-hooks"
pb="$root/bin/pb"
db="$root/var/board.db"

[ -d "$hooks" ] || { echo "找不到 hooks 目錄：$hooks" >&2; exit 1; }
chmod +x "$hooks"/* 2>/dev/null || true

if [ "$#" -eq 0 ]; then
	set -- "$(pwd)"
fi

for repo in "$@"; do
	if [ ! -e "$repo/.git" ]; then
		echo "略過（不是 git repo）：$repo" >&2
		continue
	fi
	git -C "$repo" config core.hooksPath "$hooks"
	git -C "$repo" config pb.bin "$pb"
	git -C "$repo" config pb.db "$db"
	echo "已裝 hooks → $repo（core.hooksPath=$hooks）"
done
echo "提醒：執行者名冊名可用 git -C <repo> config pb.actor <名冊名> 指定（預設 human）。"
