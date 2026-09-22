---
name: project_board
description: 小蝦團隊專案／需求／單 管理系統（Y20260916 等）的讀寫介面。看樹、開單、派單、流轉狀態、貼 commit／驗收。
---

# project_board — 團隊單板（skill）

> 給小蝦（cray/pi harness）用的**一等公民**入口。跟 MCP 同能力、不同門。
> 系統本體：`/usr/account/project_board`（Go，單 binary `pb`，SQLite）。

## 一句話

`Y20260916 → REQ → ISSUE →（狀態）` 一棵樹；我來開單、領單、回報、驗收，學長看 dashboard。

## 前置（缺一不可）

```bash
# 1) 有 binary（沒有就建）
cd /usr/account/project_board
go build -o bin/pb ./cmd/pb

# 2) 有 DB（第一次）
./bin/pb init && ./bin/pb seed
```

`skill.py` 會自己找 `pb`（依序）：`$PB_BIN` → `$PB_PROJECT/bin/pb` → **內建預設 `/usr/account/project_board/bin/pb`** → `$PWD/bin/pb`／`$PWD/project_board/bin/pb`（含往上層找）→ `PATH`。
⚠ `skill_invoke` 的 cwd 是**skill 自己的目錄**（`/root/.cray/skills/dynamic/project_board`），不是專案，所以**不能只靠相對路徑找 binary**——內建絕對路徑就是為此；專案搬家時用 `PB_PROJECT` 覆寫。
找不到 binary 才退回打 REST（`$PB_REST`，預設 `http://127.0.0.1:8787`）——**REST v1 唯讀**，只有讀取類（`tree`／`get`／`history`／`stats`／`search`／`healthz`）可用；寫入類一定要有 binary。
兩者都沒有會回：「請先建 binary：`go build -o bin/pb ./cmd/pb`，或起 server：`pb serve`」。

binary 若是 `<root>/bin/pb`，且未設 `PB_DB`，`skill.py` 會自動帶 `PB_DB=<root>/var/board.db`——所以 `skill_invoke` 從任何 cwd 都打到專案 DB（就是上面 `init`／`seed` 那顆）。

## 子指令（`skill_invoke("project_board", args="…")`）

| args | 用途 |
|---|---|
| `tree [--project Y20260916] [--status todo] [--owner xiaoxia] [--type issue]` | 看樹（縮排） |
| `get <id>` | 取單全文（含 links／children） |
| `create --type issue --parent <id> --title "…" --actor xiaoxia [--owner xiaoxia] [--priority high]` | 開單 |
| `update <id> --actor xiaoxia [--body "…"] [--title "…"] [--owner …]` | 改單 |
| `move <id> <status> --actor xiaoxia [--note "…"]` | 狀態流轉（見狀態機） |
| `assign <id> <owner> --actor xiaoxia` | 派單 |
| `link <id> --kind commit --target 8094064 --actor xiaoxia` | 掛 commit／`file`／`depends_on`／`pr` |
| `hook <node_id> --target <agent> [--harness herdr] [--actor xiaoxia]` | 訂閱：node_id 或其子孫狀態變動時喚醒該 agent |
| `unhook <node_id> --target <agent> [--harness herdr] [--actor xiaoxia]` | 取消訂閱（不存在會回錯，不靜默成功） |
| `hooks [<node_id>]` | 列出訂閱（省略＝全部） |
| `commit attach [--sha <sha>] [--message-file <path>] [--dry-run] --actor xiaoxia` | 從 commit 訊息（含單號 `#Y…/…`）自動 `link commit`；沒單號／查無此單略過（需 binary） |
| `repo set <project-id> --url <url> [--path <p>] --actor xiaoxia` | 設專案 repo（`link kind=repo`，idempotent） |
| `repo show <project-id> [--json]` | 看專案 repo |
| `verify <id> --note "覆蓋率 98%、-race 乾淨" --actor xiaoxia` | 驗收 → `done`（`--evidence` 亦可，別名） |
| `comment <id> "…" --actor xiaoxia` | 留言 |
| `search "admin-bff" [--project Y20260916]` | 關鍵字 |
| `history <id> [--limit n]` | 事件流 |
| `stats [--project Y20260916]` | 各狀態計數／每人未結案張數／REQ 進度／自我驗收張數 |
| `serve [--addr 127.0.0.1:8787]` | **背景**啟動 pb serve（REST＋dashboard＋MCP）：pid 寫 `<root>/var/serve.pid`、log 寫 `<root>/var/serve.log`；**停止：`kill $(cat var/serve.pid)`**（需 binary） |
| `help` | 列子指令 |

`--json` 可加在任何**唯讀**子指令後，取結構化輸出（打 binary 時轉給 CLI 的 `--json`；走 REST 時直接回 API JSON）。

> **輸出量提醒（2026-09-20）**：`tree` 不帶條件會吐整棵樹（本案 ~35KB）、`search` 全庫 ~8KB，會超過 harness 工具輸出上限被存成檔案、**看不到全文**。**一律帶篩選縮範圍**：`tree --project <id> [--status <s>] [--owner <o>] [--type <t>]`、`search <q> --project <id>`。

**`--actor` 寫入操作必帶**；沒帶時退回 env `PB_ACTOR`（`skill.py` 預設帶入 `xiaoxia`），兩者都沒有就會被拒絕。自己 verify 自己領的單會被標記 `self-verified`（不擋，但學長看得到）。

## 接單必須 hook（v0.3 硬規）

**狀態一變就把訂單的 harness 叫醒**——但 hook 是**安全網，不是免報**。把關仍是人：

- **接單當下**：工程師**必須** `pb_hook` 自己的 ISSUE（`pb hook <issue_id> --target <自己>`）；
  CTO 派工當下 hook 那張 **REQ**。hook 一張 REQ，旗下 ISSUE 的異動都算（node_id 等於異動節點或它的祖先）。
- **退回修改**：就是既有的 `review → in_progress`（`move` 帶 `--note` 寫原因），
  **沒有 reject、不加新狀態**。收到喚醒先讀單（`get`）再改。
- **做完或有問題**：**仍要回報 CTO**（herdr），不能只靠 hook。hook 不會替你報告。
- **自己改的不叫自己**：`history.actor` 等於 `hook.actor` 時不喚醒（避免自己叫自己）。
- **harness 這輪只收 `herdr`**；`target` 是 agent 名，須符合 `^[a-z][a-z0-9_-]{0,31}$`。
- **喚醒由長駐 `pb serve` 負責**：每 2 秒看新 history（transition／verify）；
  **serve 沒開就不會醒**。寫入狀態的行程不會、也不准呼叫 herdr。
- 工具名：CLI＝`hook`／`unhook`／`hooks`；MCP＝`pb_hook`／`pb_unhook`／`pb_hooks`（只呼叫 store，不 exec）。
- **git 整合（v0.4）**：CLI＝`commit attach`／`repo set|show`；MCP＝`pb_commit_attach`（`actor`／`sha`／`message`）、`pb_set_repo`（`actor`／`project_id`／`url`／`path`）。
  commit 訊息帶單號（`#Y<日期>/REQ-…/ISSUE-…`），即自動 `link kind=commit`；**只掛 link、不改狀態**。
  repo 存在 `link kind=repo`；dashboard 會把 commit/PR 變成可點連結（見 `docs/GIT_INTEGRATION.md`）。
  GitHub webhook：`POST /api/integrations/github`（`X-GitHub-Event`；`GH_WEBHOOK_SECRET` 驗簽）；post-commit hook＝`scripts/git-hooks/post-commit`。

## 狀態機（別亂跳）

```
todo ⬜ → in_progress 🔶 → review 👀 → done ✅
   ↘        ↘              ↘
    blocked 🚧 ─→ in_progress ； 任何 → hold ⏸ ／ cancel ❌
```

- `done` **只能從 `review` 進**，且正規路徑是 `verify`（要附證據）。
- 非法轉移會被拒（回錯誤），**不要硬改 DB**。

## owner 名冊

`xiaoxia`（小蝦）｜`kaimake`（開碼客）｜`kaimadi`（開碼弟）｜`yilong`（一龍，CTO）｜`claude`（克勞德）｜`human`（學長）｜`unassigned`

## 典型流程（我的日常）

```bash
# 領單：先 hook 自己（狀態一變才叫得醒你）
skill_invoke("project_board", args='move Y20260916/REQ-X/ISSUE-Y in_progress')
skill_invoke("project_board", args='hook Y20260916/REQ-X/ISSUE-Y --target xiaoxia')
# 做完：掛 commit ＋ 驗收
skill_invoke("project_board", args='link Y20260916/REQ-X/ISSUE-Y --kind commit --target 8094064')
skill_invoke("project_board", args='verify Y20260916/REQ-X/ISSUE-Y --note "test 全過、覆蓋 98%"')
```

退你單時是 `review → in_progress`（不是新狀態）：`move <id> in_progress --note "原因"`；收到喚醒先 `get` 讀單再改，改完再回報 CTO。

## 常見錯誤

| 訊息 | 原因 | 處置 |
|---|---|---|
| `錯誤：找不到 pb binary …` | binary 沒建、REST 也沒起 | `go build -o bin/pb ./cmd/pb`（或起 `serve`） |
| `錯誤：… create 是寫入指令，REST v1 唯讀不能替代` | 沒 binary 又想寫入 | 建 binary；REST 只給讀取類退回 |
| `錯誤：查無資料（HTTP 404）` | REST 找不到該節點 | `tree` 確認 ID |
| `no such node` / `illegal transition` / `id exists` | CLI 回報（ID 打錯／跳步／撞名） | 見下；非法轉移不硬改 |
| `connection refused` | 走 REST 但 server 沒起 | 起 `serve` 或改用 `pb` binary |

## 詳規

`docs/INTERFACE.md` §5（skill 規格）、`docs/DATA_MODEL.md`（狀態機／ID／owner）。
