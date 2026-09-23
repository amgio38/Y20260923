#!/usr/bin/env bash
# ProjectBoard dashboard 常駐腳本。
#
# 為什麼：重開機後要一條指令把 dashboard 拉起來。2026-09-20 綁 0.0.0.0:8787。
# 改了什麼：start／stop／restart。只這支腳本覆寫位址；pb serve 預設仍是 127.0.0.1:8787。
# pid／log 對齊 skill（var/serve.pid、var/serve.log），避免兩套程序各寫各的。
# 注意：同一支 process 還有 MCP /mcp（可寫）。這不是只讀 dashboard 單獨對外。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN="$ROOT/bin/pb"
DB="$ROOT/var/board.db"
PID_FILE="$ROOT/var/serve.pid"
LOG_FILE="$ROOT/var/serve.log"
ADDR="0.0.0.0:8787"
PORT="8787"

usage() {
	echo "用法：$(basename "$0") start|stop|restart|status" >&2
	exit 2
}

pid_alive() {
	local pid="$1"
	[[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

read_pid() {
	if [[ -f "$PID_FILE" ]]; then
		tr -d '[:space:]' <"$PID_FILE"
	fi
}

port_open() {
	ss -ltn | awk '{print $4}' | grep -qE "(^|:)${PORT}$"
}

start() {
	if [[ ! -x "$BIN" ]]; then
		echo "找不到 $BIN。先在專案目錄執行：go build -o bin/pb ./cmd/pb" >&2
		exit 1
	fi
	# 不擋「$DB 還不存在」：pb serve 自己會建目錄＋檔案＋schema（第一次啟動就
	# 自動初始化，不用先手動跑 pb init）。
	mkdir -p "$ROOT/var"

	local pid
	pid="$(read_pid || true)"
	if pid_alive "$pid"; then
		echo "已在跑（pid $pid）http://${ADDR}"
		exit 0
	fi
	rm -f "$PID_FILE"

	if port_open; then
		echo "埠 ${PORT} 已被其他程序占用，沒有啟動。" >&2
		exit 1
	fi

	# setsid：脫離終端，關掉 shell 也不會把 server 帶走。對齊 skill 的 start_new_session。
	setsid "$BIN" serve --db "$DB" --addr "$ADDR" >>"$LOG_FILE" 2>&1 </dev/null &
	pid="$!"
	echo "$pid" >"$PID_FILE"

	local i
	for i in $(seq 1 25); do
		if ! pid_alive "$pid"; then
			echo "啟動失敗，最後幾行 log：" >&2
			tail -n 20 "$LOG_FILE" >&2 || true
			rm -f "$PID_FILE"
			exit 1
		fi
		if curl -sf -o /dev/null --max-time 1 "http://127.0.0.1:${PORT}/healthz"; then
			echo "已啟動 pid ${pid}  http://${ADDR}  dashboard http://127.0.0.1:${PORT}"
			echo "log：$LOG_FILE"
			return 0
		fi
		sleep 0.2
	done
	echo "程序還在，但 /healthz 沒回應。看 log：$LOG_FILE" >&2
	exit 1
}

stop() {
	local pid
	pid="$(read_pid || true)"
	if ! pid_alive "$pid"; then
		rm -f "$PID_FILE"
		echo "沒在跑"
		return 0
	fi
	kill "$pid" 2>/dev/null || true
	local i
	for i in $(seq 1 25); do
		if ! pid_alive "$pid"; then
			rm -f "$PID_FILE"
			echo "已停止"
			return 0
		fi
		sleep 0.2
	done
	kill -9 "$pid" 2>/dev/null || true
	rm -f "$PID_FILE"
	echo "已強制停止"
}

status() {
	local pid
	pid="$(read_pid || true)"
	if pid_alive "$pid"; then
		echo "running pid $pid  http://${ADDR}"
		exit 0
	fi
	echo "stopped"
	exit 1
}

cmd="${1:-}"
case "$cmd" in
	start) start ;;
	stop) stop ;;
	restart) stop; start ;;
	status) status ;;
	*) usage ;;
esac
