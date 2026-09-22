# INTERFACE.md — ProjectBoard 介面規格

> 四個入口共用同一份 core 與同一顆 DB。本檔為介面權威版，2026-09-22 對照原始碼／實際 CLI
> `--help` 輸出核實過一次（CLI 子命令、20 個 MCP 工具、REST 端點、skill 子指令涵蓋範圍）。

---

## 0. 系統架構

```
                    ┌──────────── pb core（Go library）────────────┐
   入口             │  internal/domain   狀態機／ID／驗證／型別      │
 ├── pb CLI          │  internal/store    SQLite（WAL）            │
 ├── MCP (stdio/http)│  internal/importer markdown→DB（v0.2）     │
 ├── REST（唯讀）     │  internal/mcp／httpapi／web（薄殼）          │
 ├── skill_invoke    └──────────────────┬─────────────────────────┘
 └── dashboard（web）                    │
                                   var/board.db
```

- **單一 binary `pb`**：`pb <cmd>` 為一次性 CLI；`pb serve` 常駐提供 REST＋dashboard＋MCP(HTTP)；`pb mcp` 走 stdio。
- **核心邏輯只在 core**（`internal/domain`＋`internal/store`）；任何入口都不得自行寫 SQL 或自行判定狀態機。
- 對照：

| 使用者 | 首選入口 |
|---|---|
| 小蝦（cray/pi harness） | `skill_invoke("project_board", …)` |
| 一龍／開碼客／開碼弟／克勞德 | MCP |
| 學長 | dashboard |
| 腳本／維運 | `pb` CLI |

---

## 1. CLI（`pb`）

```
pb init   [--db var/board.db]                   # 建 var/board.db（schema）
pb seed   [--db var/board.db] --actor <a>        # 建第一個 project 節點（寫入，須 --actor）
pb serve  [--db var/board.db] [--addr 127.0.0.1:8787]  # REST + dashboard + MCP(HTTP) + GitHub webhook
pb mcp    [--db var/board.db]                    # MCP over stdio（外部 harness 用）

pb tree    [--project X] [--status s] [--owner o] [--tag t] [--type t] [--depth n]
pb get     <id>
pb create  --type <t> --title <s> --actor <a> [--parent <id>] [--id <id>] [--owner <o>] [--priority p] [--tags s] [--body s]
pb update  <id> --actor <a> [--title s] [--body s] [--owner o] [--priority p] [--tags s] [--sort n] [--if-unmodified-since <ts>]
pb move    <id> <status> --actor <a> [--note s] [--if-unmodified-since <ts>]  # 狀態流轉（= transition）
pb assign  <id> <owner> --actor <a>
pb link    <id> --kind <k> --target <s> --actor <a> [--note s]
pb verify  <id> --note <evidence> --actor <a>
pb comment <id> <text> --actor <a>
pb search  [query] [--project X] [--tag t] [--owner o]
pb deps    [--project X]                         # depends_on 依賴清單
pb history <id> [--limit n]
pb stats   [--project X]
pb report  [--week YYYY-MM-DD] [--project X]     # 週報（做了什麼／進行中／下週計畫）
pb checklist [--project X]                       # 母表項目清單

pb hook    <node_id> --target <agent> [--harness herdr] --actor <a>   # 訂閱：節點異動時喚醒 harness
pb unhook  <node_id> --target <agent> [--harness herdr] --actor <a>
pb hooks   [<node_id>]                           # 列出訂閱

pb import  <dir> [--project Y20260916] [--dry-run]
pb export  <id> [--out dir]

pb commit attach [--sha <sha>] [--message-file <path>] [--dry-run] --actor <a>  # 從 commit 訊息掛單號（git hook 用這支）
pb repo set  <project-id> --url <url> [--path <p>] --actor <a>                 # 設定 project↔repo 對應
pb repo show <project-id> [--json]
```

> **`--actor`**：寫入類命令必填（含 `seed`，它也是寫入）；未帶時退回 env `PB_ACTOR`；兩者皆無則拒絕（exit 2）。值須在 owner 名冊內（`DATA_MODEL.md` §8）。詳見 `DATA_MODEL.md` §11.1。
> **`--if-unmodified-since`**（`update`／`move` 可選）：帶了就做樂觀鎖比對，衝突回 `conflict` 不寫入。詳見 `DATA_MODEL.md` §11.6。
> **`--db`**：所有子命令統一支援（不只 `mcp`），預設退回 env `PB_DB` → `var/board.db`（小蝦 PB03 CR 補充，2026-09-20）。
> **`--json`**：讀取類子命令（`tree`／`get`／`search`／`deps`／`history`／`stats`／`report`／`hooks`／`checklist`／`export`）皆可加，取結構化輸出。
> **exit code**：缺必要參數（格式／用法錯誤）＝2；狀態機／store 層拒絕（如非法轉移、衝突、撞名）＝1；成功＝0。

## 2. MCP tools

> **外部 harness（claude code／cursor／opencode…）接法見 `INTEGRATION.md`。**

- **傳輸**：`pb mcp --db <絕對路徑>` 走 stdio（最通用、免常駐）；`pb serve --addr 127.0.0.1:8787` 提供 Streamable HTTP（多 client 共用）。預設 **stdio**。
- **參數**：`--db`／`--addr`／`--bin` 亦可用 env `PB_DB`／`PB_ADDR`／`PB_BIN`。
- **命名**：全部 `pb_` 前綴。
- **錯誤**：非法狀態轉移／ID 撞名／缺必要參數 → 回 MCP error（`isError:true`），**不靜默修正**。
- **resources（唯讀）**：`board://tree`、`board://node/{id}`、`board://stats`。

> **`actor`**：以下所有寫入類 tool 皆為**必要參數**（不在此表逐一重複列出，見各列備註），值須在 owner 名冊內；`pb_tree`／`pb_get`／`pb_search`／`pb_history`／`pb_stats` 為唯讀，不需要。詳見 `DATA_MODEL.md` §11.1。

| tool | 參數 | 回傳 |
|---|---|---|
| `pb_tree` | `project?`, `status?`, `owner?`, `type?`, `depth?` | 節點精簡陣列（id/type/title/status/owner/children_count） |
| `pb_get` | `id` | 節點全文 ＋ `links` |
| `pb_create` | `actor`, `type`, `title`, `parent_id?`, `id?`, `owner?`, `priority?`, `tags?`, `body?` | 新節點（`id` 省略時的生成規則見 `DATA_MODEL.md` §7／§11.5） |
| `pb_update` | `actor`, `id`, `title?`, `body?`, `owner?`, `priority?`, `tags?`, `sort?`, `expected_updated_at?` | 更新後節點；`expected_updated_at` 不符回 `conflict`（§11.6） |
| `pb_transition` | `actor`, `id`, `to`, `note?`, `expected_updated_at?` | 節點 ＋ 新增 history；轉 `blocked` 須附 `note` 或既有 `depends_on` link（§11.3） |
| `pb_assign` | `actor`, `id`, `owner` | 節點 |
| `pb_link` | `actor`, `from_id`, `kind`, `target`, `note?` | link；`kind=depends_on` 會驗證 `target` 節點存在（§11.4） |
| `pb_unlink` | `actor`, `link_id` | `{ok:true}` |
| `pb_verify` | `actor`, `id`, `note`（證據） | 節點（status=`done`）＋ history(`verify`)；`actor==owner` 時標記 `self-verified`（§11.2） |
| `pb_comment` | `actor`, `id`, `text` | history(`comment`) |
| `pb_search` | `query?`, `project?`, `tag?`, `owner?`（四者至少一個） | 命中節點（FTS5，唯讀，回精簡陣列不含 body） |
| `pb_history` | `id`, `limit?` | 事件陣列 |
| `pb_stats` | `project?` | 各狀態計數／每 REQ 完成度／**自我驗收張數**（§11.2） |
| `pb_delete` | `actor`, `id` | `{ok:true}`（**僅 `report` 或無子、無 link 的節點**可刪） |
| `pb_deps` | `project?` | `depends_on` 依賴清單（唯讀） |
| `pb_hook` | `actor`, `node_id`, `harness`, `target` | 訂閱：`node_id` 或其子孫異動時喚醒 `harness` 的 `target`；只寫訂閱，真正喚醒見 `DATA_MODEL.md`／`README.md`「Hook 喚醒」一節 |
| `pb_unhook` | `actor`, `node_id`, `harness`, `target` | 取消訂閱；不存在回錯 |
| `pb_hooks` | `node_id?` | 列出訂閱（唯讀） |
| `pb_commit_attach` | `sha`, `message` | 從 commit 訊息把 `sha` 掛到訊息裡提到的單（`link kind=commit`）；只掛 link，不改狀態 |
| `pb_set_repo` | `actor`, `project_id`, `url`, `path?` | 設定 project 的 repo（`link kind=repo`）；idempotent，寫入前先移除既有 repo link |

## 3. REST（唯讀）

| method | path | 回傳 |
|---|---|---|
| GET | `/healthz` | `{ok:true, schema_version}` |
| GET | `/api/tree?project=&status=&owner=&type=` | 樹（巢狀） |
| GET | `/api/node/{id}` | 節點 ＋ links ＋ children |
| GET | `/api/node/{id}/history?limit=` | 事件 |
| GET | `/api/stats?project=` | 統計（含各狀態計數、每 REQ 完成度、**自我驗收張數**；`focus` 聚合見下方範例） |
| GET | `/api/search?q=&project=` | 命中 |
| GET | `/api/deps?project=` | `depends_on` 依賴清單 |
| GET | `/api/checklist?project=` | 母表項目清單 |
| GET | `/api/report?week=&project=` | 週報（做了什麼／進行中／下週計畫） |
| GET | `/api/meta` | 型別／負責人／狀態／分頁的顯示 metadata（dashboard 篩選器與統計 chip 的資料來源，唯一真相，前端不再自存字面量） |
| GET | `/assets/*` | 靜態資源（品牌圖示等） |
| GET | `/` | dashboard 單頁 |
| — | `/mcp` | **不是 REST**，是 MCP over Streamable HTTP（JSON-RPC 2.0 POST），見 §2 |
| POST | `/api/integrations/github` | **唯一的寫入端點**：GitHub webhook（`X-GitHub-Event`／`X-Hub-Signature-256`，`GH_WEBHOOK_SECRET` 驗簽），push/PR 事件自動 `link kind=commit/pr`。其餘 REST 一律唯讀，見下方說明 |

範例 `GET /api/node/Y20260916/REQ-MEMBER-CORE/ISSUE-ADMIN-BFF-UT90-A`：

```json
{
  "id": "Y20260916/REQ-MEMBER-CORE/ISSUE-ADMIN-BFF-UT90-A",
  "type": "issue", "title": "admin-bff UT90 軌A", "status": "done",
  "owner": "xiaoxia", "priority": "high", "tags": "admin-bff,ut90",
  "body": "…", "created_at": "2026-09-19T22:10:00+08:00",
  "updated_at": "2026-09-20T01:03:00+08:00",
  "links": [{"kind":"commit","target":"8094064","note":""}],
  "children": [{"id":"…/REPORT-xiaoxia-20260919","type":"report","status":"done"}]
}
```

範例 `GET /api/tree?project=Y20260916`（巢狀；`children` 一律為陣列，葉節點為 `[]`）：

```json
[
  {
    "id": "Y20260916", "type": "project", "title": "Y20260916",
    "status": "in_progress", "owner": "human", "priority": "medium",
    "children": [
      {
        "id": "Y20260916/REQ-MEMBER-CORE", "type": "req", "title": "會員核心",
        "status": "todo", "owner": "yilong", "priority": "high",
        "children": [
          {
            "id": "Y20260916/REQ-MEMBER-CORE/ISSUE-ADMIN-BFF-UT90-A", "type": "issue",
            "title": "admin-bff UT90 軌A", "status": "done", "owner": "xiaoxia",
            "priority": "high", "children": []
          }
        ]
      }
    ]
  }
]
```

> filter（`status`／`owner`／`tag`／`type`）只留命中者，但**一併帶上其祖先**保持樹形（`INTERFACE.md` §4 樹要能成樹）。

範例 `GET /api/stats?project=Y20260916`：

```json
{
  "count_by_status": {"todo":3,"in_progress":2,"review":0,"blocked":1,"hold":0,"done":1,"cancel":0},
  "count_by_owner": {"human":1,"kaimadi":1,"unassigned":1,"xiaoxia":1,"yilong":2},
  "req_progress": {"Y20260916/REQ-MEMBER-CORE":0.333,"Y20260916/REQ-ARCH":0},
  "self_verified_count": 1,
  "focus": {
    "blocked": [
      {"id":"…/ISSUE-CONC03","title":"併發寫入保護","owner":"unassigned","reason":"等 CONC01 併發案例"}
    ],
    "awaiting_decision": [
      {"id":"Y20260916/REQ-ARCH","title":"架構體檢","note":"D4 母表建模待裁示"}
    ],
    "recent": [
      {"ts":"2026-09-20T04:36:06+08:00","actor":"xiaoxia","node_id":"…/ISSUE-ADMIN-BFF-UT90-A","action":"verify","summary":"verify：[self-verified] 覆蓋率 98%"}
    ],
    "self_verified": [
      {"id":"…/ISSUE-ADMIN-BFF-UT90-A","title":"admin-bff UT90 軌A","owner":"xiaoxia","note":"[self-verified] 覆蓋率 98%"}
    ]
  }
}
```

> - `count_by_owner`＝**目前未結案**（非 `done`／`cancel`）張數＝dashboard「👤 每人手上張數」（`API_CONTRACT.md` §5-3）。
> - `focus` 是 REST／dashboard 專屬聚合，不進 MCP `pb_stats`（`DATA_MODEL.md` §11.2、`API_CONTRACT.md` §5-4）；陣列長度依資料，此處僅節錄。
> - `awaiting_decision` 來源＝`tags` 含 `pending-decision` 的節點，`note` 取 `body` 或最新 `comment`；`blocked.reason` 取最近一筆 `transition`→`blocked` 的 `note`（`API_CONTRACT.md` §5-1／§5-4）。

> REST 的 `/api/*` 讀取端點皆為唯讀，一般寫入（開單、改狀態、驗收…）一律走 MCP／skill／CLI，不走
> REST。**唯一例外**是 `POST /api/integrations/github`（GitHub webhook）——它是外部系統主動推事件
> 進來，不是給 harness 呼叫的一般寫入介面，見上表。

## 4. Dashboard（單頁）

- 標準庫 `net/http` ＋ 單頁 HTML（內嵌少量 vanilla JS）；**無框架、無 build**。
- **學長視角**：一頁看完「全案狀態 ＋ 該注意的事」，不必點進去挖。
- 版面：

```
┌ ProjectBoard ──────────── 127.0.0.1:8787 ─── 更新 22:10 ─┐
│ [統計] ⬜3 🔶5 👀2 🚧1 ⏸4 ✅28 ❌1                        │
├────────── 樹（可折疊）─────────┬──────── 詳情 ────────────┤
│ ▼ Y20260916        🔶        │ ISSUE-ADMIN-BFF-UT90-A    │
│   ▼ REQ-MEMBER-CORE 🔶       │ 狀態 ✅  負責 小蝦  high   │
│     ├ ISSUE-…-A ✅           │ ── body ──                │
│     └ ISSUE-CONC03 ⬜        │ ── 關聯 ── commit:8094064 │
│   ▶ REQ-ARCH       🔶        │ ── 歷史 ── 22:10 create … │
├──────────────────────────────┴───────────────────────────┤
│ [焦點] 🚧 卡點 1：ISSUE-CONC03（等 CONC01）                 │
│        ❓ 待裁示 2：D4 母表建模、A7 CI Go 版本              │
│        🕒 最近異動：22:10 小蝦 收 ISSUE-…-A（98%）          │
│        👤 手上張數：小蝦 2／開碼客 1／開碼弟 0               │
│        ⚠ 自我驗收 1：ISSUE-…-B（owner=小蝦 自己 verify）    │
└──────────────────────────────────────────────────────────┘
```

- **四個區塊**：
  1. **頂列統計**：各狀態計數（一眼看全案健康度）。
  2. **左樹右詳情**：點樹節點，右側載入單（body／關聯／歷史）。
  3. **焦點面板**（學長最常看）：🚧 卡點清單、❓ 待裁示、🕒 最近異動、👤 每人手上張數、**⚠ 自我驗收清單**（`actor==owner` 的 `verify`，見 `DATA_MODEL.md` §11.2——球員兼裁判要看得到，不是要擋）。
- 狀態以**色點**標示（對映 `DATA_MODEL.md` §6）；樹可依狀態／owner／型別前端過濾，點狀態／型別
  統計 chip 篩選時樹會自動展開到符合條件的節點（不用手動點 caret）。
- 更新方式：純前端 SPA（fetch API 讀資料，`history.pushState` 做 deep-link），**不是**整頁刷新，
  也不做 websocket／推送——要看最新資料按右上角「重新整理」或直接重整頁面。
- 綁定位址跟著 `pb serve --addr`／`service.sh` 走，**不是固定只綁 `127.0.0.1`**（`service.sh` 常駐
  用途固定 `0.0.0.0:8787`），見 `DATA_MODEL.md`／`REQ.md` NFR-06。無登入（內用小團隊）。
- 更完整的功能列表（統計 chip 與篩選連動、記住上次離開的畫面、自我驗收清單一致性等）見
  `README.md`「Dashboard」一節，這裡不重複維護兩份。

**視覺風格（學長 2026-09-20 指示，硬性要求）**：**Google 後台風格**——簡約、專業、留白足夠，不花俏。具體對照：
- 字體：系統字體堆疊（`-apple-system, "Segoe UI", Roboto, "Noto Sans TC", sans-serif`），不引外部字型檔。
- 配色：中性灰階為主（背景 `#fff`／`#f8f9fa`、邊框 `#dadce0`、主文字 `#202124`、次要文字 `#5f6368`），狀態色點才用彩色，且用低飽和（比照 Google Workspace 管理主控台的色階，不用亮色系）。
- 版面：卡片式區塊、細邊框或極淡陰影分隔（不用粗邊框／不用漸層／不用陰影堆疊）、資訊密度高但排版整齊（表格化數據對齊）。
- 元件：按鈕／輸入框走扁平風格（無立體感、無圓角過大），hover 用淡灰底色即可，不做花俏動畫。
- 一句話：**看起來要像內部工具，不要像行銷頁面**——這決定了 `internal/web` 的 CSS 全部要照這個基調寫，不是留給實作者自由發揮。

**黑夜模式（2026-09-20 學長指示；2026-09-22 導演改定為 claude.ai 風格）**：header 右上角放一個切換 icon（太陽／月亮），可在亮／暗兩色盤間切換；記住使用者選擇（換頁/重整不失憶），首次進站沒有選擇記錄時跟隨系統 `prefers-color-scheme`。暗色配色**對齊 claude.ai（cds token）**，Light 主題維持原 Google 後台色票：

| token | 亮色（既有） | 暗色（claude.ai cds） |
|---|---|---|
| 背景 `--bg` | `#f8f9fa` | `#151515`（gray-850／surface-1） |
| 卡片 `--card` | `#ffffff` | `#20201f`（gray-800／surface-3） |
| 邊框 `--border` | `#dadce0` | `rgba(255,255,255,.10)`（alpha-2） |
| 主文字 `--text` | `#202124` | `#f0efec`（gray-50） |
| 次文字 `--muted` | `#5f6368` | `#898781`（gray-400） |
| hover `--hover` | `#f1f3f4` | `rgba(255,255,255,.06)` |
| selected `--selected` | `#e8eaed` | `rgba(217,119,87,.16)`（clay tint） |
| 連結／accent | `#1a73e8` | `#d97757`（clay） |
| 狀態 `--st-todo` | `#5f6368` | `#898781` |
| 狀態 `--st-in_progress` | `#1a73e8` | `#d97757` |
| 狀態 `--st-review` | `#f9ab00` | `#c98500` |
| 狀態 `--st-blocked` | `#d93025` | `#e66767` |
| 狀態 `--st-hold` | `#80868b` | `#6d6b67` |
| 狀態 `--st-done` | `#188038` | `#91d68b` |
| 狀態 `--st-cancel` | `#9aa0a6` | `#52514e` |

暗色卡片質感另比照 claude.ai：卡片圓角 `12px`（按鈕／select `8px`）、topbar／tabs 底色併入 `--bg`。**Icon 走 Claude Code 風格**：品牌用 asterisk 記號（`✻`，clay 色），狀態用單色字符記號（`○ ◐ ◑ ● ◌ ✔ ✕`），顏色由 `--st-*` token 決定、跟隨主題（不再用 emoji、不再寫死色碼）。

實作方式：CSS custom properties 用 `[data-theme="dark"]` 覆寫（不重複整份 CSS）；切換用 `localStorage` 記住選擇，純前端行為不用打 API。

## 5. 技能層（skill_invoke）★

**目的**：讓小蝦在 cray/pi harness 內以**一等公民**方式操作本系統，不必先掛 MCP。

- **skill 名**：`project_board`
- **安裝**（沿用現行慣例，與 `golang-*` 相同）：
  ```bash
  cp -r /usr/account/project_board/skill \
        /root/.cray/skills/dynamic/project_board
  ```
- **結構**（與 dynamic skill 同構）：
  ```
  skill/
  ├── SKILL.md      # 知識＋cheatsheet（小蝦開工前讀）
  └── skill.py      # thin shim：解析 args → 呼叫 core
  ```
- **`skill.py` 執行策略（優先序）**：
  1. 找 `pb` binary（`$PB_BIN` → 專案 `./bin/pb` → `PATH`）→ 直接 `exec pb <cmd> …`（**最短路徑、免 server**）。
  2. 找不到 → 打 REST `http://127.0.0.1:8787`（純 stdlib `urllib`，**不引第三方**）。
  3. 兩者皆無 → 明確錯誤並提示 `go build -o bin/pb ./cmd/pb`。
- **子指令**（`skill_invoke("project_board", args="…")`）：與 CLI 同名，讓心智模型一致。

| args | 動作 |
|---|---|
| `tree [--project X] [--status s] [--owner o]` | 看樹 |
| `get <id>` | 取單 |
| `create --type issue --parent <id> --title "…" --actor <a>` | 開單 |
| `update <id> --body "…" --actor <a>` | 改單 |
| `move <id> <status> --actor <a> [--note …]` | 狀態流轉 |
| `assign <id> <owner> --actor <a>` | 派單 |
| `link <id> --kind commit --target 8094064 --actor <a>` | 掛 commit／依賴 |
| `verify <id> --note "…" --actor <a>` | 驗收（→ done）；`--note` 為正式參數，`--evidence` 為 skill 別名 |
| `comment <id> "…" --actor <a>` | 留言 |
| `search <q>` / `history <id>` / `stats [--project X]` | 查詢 |
| `hook <node_id> --target <agent> --actor <a> [--harness herdr]` | 訂閱：節點異動時喚醒 harness（跟 CLI／MCP 同語意，見 `DATA_MODEL.md`「Hook 喚醒」） |
| `unhook <node_id> --target <agent> --actor <a>` | 取消訂閱 |
| `hooks [<node_id>]` | 列出訂閱（**只能走 binary**，REST 沒有對應端點，`skill.py` 的 `BINARY_ONLY_CMDS`） |
| `commit attach [--sha <sha>] [--message-file <path>] --actor <a>` | 從 commit 訊息掛單號 |
| `repo set <project-id> --url <url> --actor <a>` / `repo show <project-id>` | 設定／查詢 project↔repo 對應 |
| `serve [--addr …]` | 背景起 server（skill 入口：pid→`<root>/var/serve.pid`、log→`<root>/var/serve.log`；停止 `kill $(cat var/serve.pid)`） |
| `help` | 列子指令 |

> **`deps`／`checklist`／`report`／`import`／`export` 目前沒有對應的 skill 子指令**（不在 `skill.py` 的
> `READ_CMDS`／`WRITE_CMDS`／`OTHER_CMDS`／`BINARY_ONLY_CMDS` 任何一個清單裡），要用這幾個功能得
> 直接跑 `pb` CLI，不能透過 `skill_invoke`。這是目前 skill 層落後 CLI／MCP 的已知缺口，不是文件
> 沒寫到。

> `--actor` 寫入操作必帶；skill 執行環境建議固定設 env `PB_ACTOR=xiaoxia` 當後備，省得每次都打。
>
> `verify` 的正式參數是 **`--note`**（CLI／MCP 同：`pb verify <id> --note <evidence>`）；**`--evidence` 只是 skill 入口的別名**，由 `skill.py` 轉成 `--note`，方便沿用舊習慣。

- **回傳**：一律**精簡文字**（樹用縮排、單用欄位區塊），必要時可 `--json` 取結構化。
- **設計原則（目標，非現況）**：skill 與 MCP／CLI 理論上應該**同能力、不同入口**；新增 MCP tool
  時 skill 子指令要能對應。**現況並未完全做到**——`deps`／`checklist`／`report`／`import`／
  `export` 這幾個 CLI／MCP 都有的能力，skill 層目前還沒接（見上面表格下的說明），算是已知技術
  債，不是刻意設計成這樣。

## 6. 為什麼要 skill 這一層（設計理由）

- 小蝦的 harness 對 `skill_invoke` 是**原生能力**；MCP 需改 harness 設定才掛得上（沙箱內常不可寫）。
- skill 走 **CLI 直連 DB**，不依賴常駐 server，適合我這種「跑完即退」的使用模式。
- 其他 AI（一龍／開碼客／克勞德）走 MCP；**兩者共用同一 core**，不會有兩套真相。
