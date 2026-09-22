#!/usr/bin/env python3
"""project_board skill shim（thin）：把 `skill_invoke` 的 args 轉給 pb CLI／REST。

執行策略（docs/INTERFACE.md §5）：
  1. 找 `pb` binary：$PB_BIN → $PB_PROJECT/bin/pb → $PWD/bin/pb、$PWD/project_board/bin/pb
     （再往上層找）→ PATH。不寫死特定機器的安裝路徑；跨機器／跨安裝位置時請設 $PB_PROJECT
     （skill_invoke 的 cwd 是 skill 自己的目錄，不是專案根，所以找不到就都找不到，不會亂猜）。
  2. 找不到 binary → 打 REST（純 stdlib urllib；v1 **唯讀**，只有讀取類指令可用）。
  3. 兩者皆無 → 清楚錯誤 ＋ 提示 `go build -o bin/pb ./cmd/pb`。

寫入類指令（create／update／move／assign／link／verify／comment）
一定要有 binary（REST v1 不提供寫入）。

環境變數：
  PB_BIN       指定 binary 路徑（最優先）
  PB_PROJECT   專案根目錄（找 <root>/bin/pb）
  PB_DB        SQLite 路徑；未設且 binary 為 <root>/bin/pb 時，自動帶 <root>/var/board.db
  PB_REST      REST base（預設 http://127.0.0.1:8787）
  PB_ACTOR     寫入操作 actor（預設 xiaoxia，CLI --actor 優先）
  PB_SERVE_LOG／PB_SERVE_PID  serve 的 log／pid 路徑（預設 <root>/var/serve.log／serve.pid）
"""

import json
import os
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

REST_BASE = os.environ.get("PB_REST", "http://127.0.0.1:8787").rstrip("/")
REST_TIMEOUT = 10.0
DEFAULT_ACTOR = os.environ.get("PB_ACTOR", "xiaoxia")
# skill_invoke 的 cwd 是「skill 自己的目錄」（cray skill.rs：cwd=skill dir），不是專案根，
# 所以找 binary 一定要靠 PB_PROJECT／PATH／往上層找 cwd 這幾條路（見 candidate_paths()），
# 不寫死特定機器的安裝路徑——寫死的話搬到別台機器／別的安裝路徑就直接找不到。

READ_CMDS = ("tree", "get", "history", "stats", "search", "healthz")
WRITE_CMDS = ("create", "update", "move", "assign", "link", "verify", "comment", "hook", "unhook", "commit", "repo")
OTHER_CMDS = ("serve", "help")
# hooks：讀取類，但 REST v1 沒有對應端點，只能用 binary（v0.3 接單 hook）。
BINARY_ONLY_CMDS = ("hooks",)
ALL_CMDS = READ_CMDS + WRITE_CMDS + OTHER_CMDS + BINARY_ONLY_CMDS

STATUS_ORDER = ("todo", "in_progress", "review", "blocked", "hold", "done", "cancel")


class Usage(Exception):
    """參數用法錯誤。"""


def fail(msg):
    print("錯誤：%s" % msg)
    return 1


# --------------------------------------------------------------------------
# 找 pb binary
# --------------------------------------------------------------------------

def candidate_paths():
    env = os.environ.get("PB_BIN")
    if env:
        yield env
    # 專案根：只認 $PB_PROJECT（不寫死特定機器的安裝路徑，見上面常數區的說明）。
    proj = os.environ.get("PB_PROJECT")
    if proj:
        yield os.path.join(proj, "bin", "pb")
    cwd = os.getcwd()
    yield os.path.join(cwd, "bin", "pb")
    yield os.path.join(cwd, "project_board", "bin", "pb")
    d = cwd
    for _ in range(6):
        yield os.path.join(d, "bin", "pb")
        yield os.path.join(d, "project_board", "bin", "pb")
        parent = os.path.dirname(d)
        if parent == d:
            break
        d = parent
    found = shutil.which("pb")
    if found:
        yield found


def find_pb():
    for path in candidate_paths():
        if path and os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    return None


# --------------------------------------------------------------------------
# binary 直連
# --------------------------------------------------------------------------

def normalize_alias(args):
    """`verify` 的 `--evidence` 是 INTERFACE §5 的寫法，CLI 實作用 `--note`；兩者都收。"""
    return ["--note" if a == "--evidence" else a for a in args]


def project_root_for(pb):
    """若 binary 是 <root>/bin/pb 且 <root> 有 go.mod，推回專案根；否則 None。"""
    bindir = os.path.dirname(os.path.abspath(pb))
    if os.path.basename(bindir) != "bin":
        return None
    root = os.path.dirname(bindir)
    if os.path.isfile(os.path.join(root, "go.mod")):
        return root
    return None


def run_binary(pb, args):
    env = binary_env(pb)
    try:
        return subprocess.call([pb] + normalize_alias(args), env=env)
    except OSError as e:
        return fail("執行 %s 失敗：%s" % (pb, e))


def binary_env(pb):
    """子行程環境：PB_ACTOR 後備、未設 PB_DB 時推回 <root>/var/board.db。"""
    env = dict(os.environ)
    env.setdefault("PB_ACTOR", DEFAULT_ACTOR)
    root = project_root_for(pb)
    if root and not env.get("PB_DB"):
        env["PB_DB"] = os.path.join(root, "var", "board.db")
    return env


def serve_paths(pb):
    """serve 的 log／pid 路徑：預設放 <root>/var（PB_SERVE_LOG／PB_SERVE_PID 可覆寫）。"""
    root = project_root_for(pb)
    base = os.path.join(root, "var") if root else os.path.join(os.getcwd(), "var")
    log = os.environ.get("PB_SERVE_LOG") or os.path.join(base, "serve.log")
    pid = os.environ.get("PB_SERVE_PID") or os.path.join(base, "serve.pid")
    return root, log, pid


def run_serve(pb, args):
    """背景啟動 pb serve（長駐）：pid→var/serve.pid、log→var/serve.log，印停用方式，立即返回。"""
    root, log_path, pid_path = serve_paths(pb)
    env = binary_env(pb)
    try:
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        log = open(log_path, "ab")
    except OSError as e:
        return fail("無法寫 log %s：%s" % (log_path, e))
    try:
        proc = subprocess.Popen(
            [pb, "serve"] + args, env=env, cwd=root or None,
            stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    except OSError as e:
        return fail("啟動 %s serve 失敗：%s" % (pb, e))
    try:
        with open(pid_path, "w") as fh:
            fh.write("%d\n" % proc.pid)
    except OSError as e:
        return fail("已啟動（pid %d）但寫 pid 檔 %s 失敗：%s" % (proc.pid, pid_path, e))
    print("已背景啟動 pb serve（pid %d）" % proc.pid)
    print("  log  ：%s" % log_path)
    print("  pid  ：%s" % pid_path)
    print("  停止 ：kill $(cat %s)" % pid_path)
    return 0


# --------------------------------------------------------------------------
# REST 退回（唯讀）
# --------------------------------------------------------------------------

def http_get(path, query=None):
    url = REST_BASE + path
    if query:
        qs = urllib.parse.urlencode({k: v for k, v in query.items() if v})
        if qs:
            url += "?" + qs
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=REST_TIMEOUT) as resp:
        return json.loads(resp.read().decode("utf-8"))


def split_opt(args, flags):
    """拆成 (options, positional)；flags 是可帶值的旗標集合，`--json` 視為布林。"""
    opts, pos, i = {}, [], 0
    while i < len(args):
        a = args[i]
        if a == "--json":
            opts["--json"] = "1"
            i += 1
        elif a in flags:
            if i + 1 >= len(args):
                raise Usage("%s 缺值" % a)
            opts[a] = args[i + 1]
            i += 2
        elif a.startswith("--"):
            raise Usage("未知旗標：%s" % a)
        else:
            pos.append(a)
            i += 1
    return opts, pos


def emit_json(data):
    print(json.dumps(data, ensure_ascii=False, indent=2))


def render_tree(nodes, depth=0):
    if depth == 0 and not nodes:
        print("（空樹）")
        return
    for n in nodes:
        print("%s%s  [%s]  %s  (%s)" % (
            "  " * depth, n.get("id", ""), n.get("status", ""),
            n.get("title", ""), n.get("owner", "")))
        render_tree(n.get("children") or [], depth + 1)


def render_node(n):
    print(n.get("id", ""))
    print("type: %s  status: %s  owner: %s  priority: %s" % (
        n.get("type", ""), n.get("status", ""), n.get("owner", ""), n.get("priority", "")))
    if n.get("tags"):
        print("tags: %s" % n["tags"])
    print("updated_at: %s" % n.get("updated_at", ""))
    if n.get("body"):
        print("body:\n%s" % n["body"])
    links = n.get("links") or []
    print("links: %s" % (", ".join("%s:%s" % (l.get("kind"), l.get("target")) for l in links) or "（無）"))
    children = n.get("children") or []
    if children:
        print("children:")
        for c in children:
            print("  %s  [%s]  %s" % (c.get("id"), c.get("status"), c.get("type")))


def render_history(events):
    if not events:
        print("（無事件）")
        return
    for h in events:
        trans = ""
        if h.get("from_val") or h.get("to_val"):
            trans = "%s→%s" % (h.get("from_val", ""), h.get("to_val", ""))
        print("%s  %-10s %-9s %-22s %s" % (
            h.get("ts", ""), h.get("action", ""), h.get("actor", ""), trans, h.get("note", "")))


def render_stats(s):
    cbs = s.get("count_by_status") or {}
    print("狀態：" + "  ".join("%s=%s" % (k, cbs.get(k, 0)) for k in STATUS_ORDER))
    cbo = s.get("count_by_owner") or {}
    if cbo:
        print("未結案（手上）：" + "  ".join("%s=%s" % (k, v) for k, v in cbo.items()))
    print("自我驗收：%s 張" % s.get("self_verified_count", 0))
    for req, p in (s.get("req_progress") or {}).items():
        print("  %s：%.0f%%" % (req, p * 100))


def render_search(hits):
    if not hits:
        print("（無命中）")
        return
    for h in hits:
        print("%s  [%s]  %s  (%s)" % (h.get("id"), h.get("status"), h.get("title"), h.get("owner")))


def run_rest(cmd, args):
    want_json = "--json" in args
    if cmd == "healthz":
        data = http_get("/healthz")
        if want_json:
            emit_json(data)
        else:
            print("ok=%s  schema_version=%s" % (data.get("ok"), data.get("schema_version")))
        return 0
    if cmd == "tree":
        opts, _ = split_opt(args, {"--project", "--status", "--owner", "--type", "--tag"})
        data = http_get("/api/tree", {
            "project": opts.get("--project"), "status": opts.get("--status"),
            "owner": opts.get("--owner"), "type": opts.get("--type"), "tag": opts.get("--tag"),
        })
        emit_json(data) if want_json else render_tree(data)
        return 0
    if cmd == "get":
        _, pos = split_opt(args, set())
        if not pos:
            raise Usage("get 需要 <id>")
        data = http_get("/api/node/" + urllib.parse.quote(pos[0], safe="/"))
        emit_json(data) if want_json else render_node(data)
        return 0
    if cmd == "history":
        opts, pos = split_opt(args, {"--limit"})
        if not pos:
            raise Usage("history 需要 <id>")
        data = http_get("/api/node/" + urllib.parse.quote(pos[0], safe="/") + "/history",
                        {"limit": opts.get("--limit")})
        emit_json(data) if want_json else render_history(data)
        return 0
    if cmd == "stats":
        opts, _ = split_opt(args, {"--project"})
        data = http_get("/api/stats", {"project": opts.get("--project")})
        emit_json(data) if want_json else render_stats(data)
        return 0
    if cmd == "search":
        opts, pos = split_opt(args, {"--project"})
        if not pos:
            raise Usage("search 需要 <query>")
        data = http_get("/api/search", {"q": pos[0], "project": opts.get("--project")})
        emit_json(data) if want_json else render_search(data)
        return 0
    raise Usage("REST 不支援 %s" % cmd)


# --------------------------------------------------------------------------
# help
# --------------------------------------------------------------------------

HELP = """project_board — 團隊單板（skill）

用法：skill_invoke("project_board", args="<子指令> [參數]")
  tree   [--project <id>] [--status <s>] [--owner <o>] [--type <t>] [--json]
  get    <id> [--json]
  create --type <t> --title <s> --actor <a> [--parent <id>] [--id <id>] [--owner <o>] [--priority p]
  update <id> --actor <a> [--title s] [--body s] [--owner o] [--priority p] [--tags s]
  move   <id> <status> --actor <a> [--note s]
  assign <id> <owner> --actor <a>
  link   <id> --kind <k> --target <s> --actor <a> [--note s]
  verify <id> --note <證據> --actor <a>          # --evidence 亦可（別名）
  comment <id> <text> --actor <a>
  hook   <id> --target <agent> --actor <a> [--harness herdr]   # 接單必 hook；異動時喚醒該 agent
  unhook <id> --target <agent> --actor <a> [--harness herdr]
  hooks  [<id>] [--json]                          # 列出訂閱（需 binary）
  commit attach [--sha <s>] [--message-file <f>] [--dry-run] --actor <a>   # 從 commit 訊息掛 link commit（需 binary）
  repo   set <project-id> --url <url> [--path <p>] --actor <a>            # 設專案 repo（需 binary）
  repo   show <project-id> [--json]                                       # 看專案 repo
  search <query> [--project <id>] [--json]
  history <id> [--limit n] [--json]
  stats  [--project <id>] [--json]
  serve  [--addr 127.0.0.1:8787]                 # 背景啟動：pid→var/serve.pid、log→var/serve.log；停止 kill $(cat var/serve.pid)
  help

寫入類指令要有 pb binary；REST（v1）唯讀只供讀取類指令退回。
"""


def cmd_help(code=0):
    sys.stdout.write(HELP)
    return code


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def main(argv):
    args = list(argv)
    if not args or args[0] in ("help", "-h", "--help"):
        return cmd_help()
    cmd = args[0]
    if cmd not in ALL_CMDS:
        print("錯誤：未知子指令 %r" % cmd)
        return cmd_help(1)

    pb = find_pb()
    if pb:
        if cmd == "serve":
            return run_serve(pb, args[1:])
        return run_binary(pb, args)

    if cmd in WRITE_CMDS:
        return fail("找不到 pb binary（$PB_BIN／$PB_PROJECT/bin/pb／./bin/pb／PATH）；"
                    "%s 是寫入指令，REST v1 唯讀不能替代。\n"
                    "請先建 binary：go build -o bin/pb ./cmd/pb" % cmd)
    if cmd in BINARY_ONLY_CMDS:
        return fail("找不到 pb binary（$PB_BIN／$PB_PROJECT/bin/pb／./bin/pb／PATH）；"
                    "%s 需要 binary（REST v1 沒有這個端點）。\n"
                    "請先建 binary：go build -o bin/pb ./cmd/pb" % cmd)
    if cmd == "serve":
        return fail("找不到 pb binary，serve 需要 binary。\n"
                    "請先建：go build -o bin/pb ./cmd/pb")

    try:
        return run_rest(cmd, args[1:])
    except Usage as e:
        return fail(str(e))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return fail("查無資料（HTTP 404）")
        return fail("REST 回應 HTTP %s %s" % (e.code, e.reason))
    except urllib.error.URLError as e:
        return fail("找不到 pb binary（$PB_BIN／$PB_PROJECT/bin/pb／./bin/pb／PATH），"
                    "REST 也連不上 %s（%s）。\n"
                    "請先建 binary：go build -o bin/pb ./cmd/pb，或起 server：pb serve" % (REST_BASE, e.reason))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
