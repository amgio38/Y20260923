# PROJECTBOARD_REQ20260920 — ProjectBoard 專案／需求／單 管理系統 需求書

- 版本：v0.1（**草案，2026-09-20 待裁示**）
- 日期：2026-09-20
- 提案人：JOBY（學長／影者）
- 規劃／CR／QA：**待指派**（建議克勞德）
- 開發：小蝦（螯蝦Pi）為主
- 狀態：**規格已定，尚未開工**（本文件為 v0.1 開發依據）
- **工作目錄鐵律**：本案所有產出（Go 程式碼、文件、skill）一律只放在
  `/usr/account/project_board/` 之下，不落在其他目錄。
- 交付定位：**內用工具、非對外產品**；本機單機跑，不對公網暴露。
- 詳規（開發時對照）：`docs/REQ.md`（FR／NFR 全文）、`docs/DATA_MODEL.md`（schema／狀態機）、
  `docs/INTERFACE.md`（CLI／MCP／REST／dashboard／skill）、`docs/INTEGRATION.md`（外部 harness 接法）、
  `docs/ROADMAP.md`、`docs/OPERATIONS.md`。

---

## 1. 背景

目前專案管理全靠**散落的 markdown**：`Campaign/Y20260916/dev_docs/ISSUE-*.md`（~120 份）、
`MEMBER_REQ20260916.md`、母表 `TOTAL_CHECKLIST_TODO_REQ20260919_2027.md`、各 `*_status_*.md`。痛點：

| 痛點 | 說明 |
|---|---|
| 單與單沒連結 | 哪條 REQ 生出哪些單？哪些單卡哪些單？誰派給誰？全靠人工記 |
| 狀態靠人腦／檔名 | `🟡 待命`／`🔲`／`✅` 沒有單一版本，母表靠手動維護 |
| 沒有事件流 | 「完成」只能讀檔案，**無法稽核**（誰、何時、X→Y、附什麼證據） |
| AI 無共用介面 | 小蝦／一龍／開碼客／開碼弟／克勞德沒有一個共用的讀寫入口 |

## 2. 目標與範圍

### 2.1 目標

1. 把上述內容收進**一顆 SQLite**，成為**唯一真相**。
2. 提供**一棵樹**：`Project → REQ → ISSUE →（狀態）`；`REPORT` 為 ISSUE 的產出物。
   （`DONE` 是**狀態欄位**，不是樹的一層。）
3. 提供 **skill（小蝦）／MCP（外部 harness：claude code／cursor／opencode）** 讀寫；
   **唯讀 dashboard** 給學長看。
4. 每次變更留 **history**（append-only），杜絕「口頭宣稱完成」。
5. 能**承接既有 markdown 單**（import），也能**匯出**回 markdown（export）。

### 2.2 明確排除（Out of Scope）

- 登入／權限／多租戶、任何對外公開。
- 通知（Email／Slack／webhook）、雲端同步、多機、行動版。
- 富文本編輯、拖拉 Kanban、甘特圖；與 git 自動同步／自動 commit。
- v1 **不做**母表條目建模、不多專案（見 §9 開放問題 D4／D5）。

## 3. 架構總覽

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

- **單一 binary `pb`**：`pb <cmd>` 一次性 CLI；`pb serve` 常駐（REST＋dashboard＋MCP HTTP）；`pb mcp` 走 stdio。
- **核心邏輯只在 core**（`internal/domain`＋`internal/store`）；任何入口不得自行寫 SQL 或自判狀態機。
- 四個入口**共用同一份 core 與同一顆 DB**。

| 使用者 | 首選入口 |
|---|---|
| 小蝦（cray/pi harness） | `skill_invoke("project_board", …)` |
| 一龍／開碼客／開碼弟／克勞德 | MCP（見 §5、`docs/INTEGRATION.md`） |
| 學長 | dashboard |
| 腳本／維運 | `pb` CLI |

## 4. 資料模型與流程（摘要，詳見 `docs/DATA_MODEL.md`）

**三張核心表**：`nodes`（樹節點）／`links`（關聯與依賴）／`history`（事件流，append-only）。

**狀態機**

```
todo ⬜ → in_progress 🔶 → review 👀 → done ✅
   ↘         ↘              ↘
    blocked 🚧 ─→ in_progress ； 任何狀態 → hold ⏸ ／ cancel ❌
```

- `done` **只能從 `review` 進**，正規路徑是 `verify()`（須附證據）。
- 非法轉移回錯誤；**不硬改 DB**。

**ID 慣例**：`project`＝`Y20260916`；`req`＝`<parent>/REQ-<SLUG>`；
`issue`＝`<parent>/ISSUE-<KEY>`（**沿用既有單名**，如 `ISSUE-ADMIN-BFF-UT90-A`）；`report`＝`<parent>/REPORT-<owner>-<YYYYMMDD>`。

**owner 名冊**：`xiaoxia`（小蝦）／`kaimake`／`kaimadi`／`yilong`（一龍，CTO）／`claude`（克勞德）／`human`（學長）／`unassigned`。

## 5. 入口與外部整合

| 入口 | 給誰 | 傳輸 | 讀寫 |
|---|---|---|---|
| skill | 小蝦 | `skill_invoke("project_board", …)` → `pb` CLI（或 REST） | 讀寫 |
| MCP | claude code／cursor／opencode／（cray 選配） | **stdio**（`pb mcp --db <絕對路徑>`）或 **Streamable HTTP**（`pb serve`） | 讀寫 |
| REST | dashboard／腳本 | HTTP/JSON | 唯讀 |
| dashboard | 學長 | 瀏覽器 `127.0.0.1:8787` | 唯讀（v1） |

- MCP **遵循官方規格**（JSON-RPC 2.0：`initialize`／`tools/list`／`tools/call`／`resources`）。
- 能力：**14 個 `pb_*` tools** ＋ resources（`board://tree`、`board://node/{id}`、`board://stats`）。
- 各 harness 設定範例（`.mcp.json`／`.cursor/mcp.json`／`opencode.json` 的 `mcp` 欄位）見 `docs/INTEGRATION.md`。

## 6. 非功能需求（NFR）

| # | 需求 |
|---|---|
| 1 | **單機、零外部服務**：單一 Go binary ＋ 一顆 SQLite；無 DB server／Redis／Node |
| 2 | **純 Go SQLite**：`modernc.org/sqlite`（無 cgo）；WAL ＋ `busy_timeout` ＋ `foreign_keys=ON` |
| 3 | **一致性**：每個寫入包單一 transaction，`nodes` 與 `history` **同 transaction** |
| 4 | **效能**：數千節點取整棵樹 < 200ms |
| 5 | **可測**：改到的 package 覆蓋率 ≥90% |
| 6 | **安全**：預設只綁 `127.0.0.1`，無登入（本機內用），不對外 |
| 7 | **可稽核**：`history` append-only，不提供刪除 |
| 8 | **可備份**：`cp var/board.db*` 即完成（備份存 `/root/.cray/BAK`，禁 `/tmp`） |

## 7. 技術選型（2026-09-20 定案）

| 項 | 選型 |
|---|---|
| 語言 | Go（環境 go1.25.3；go directive 與環境一致） |
| DB | SQLite，driver `modernc.org/sqlite`（純 Go） |
| HTTP／web | 標準庫 `net/http` ＋ 單頁 HTML/JS（**無前端框架、無 build**） |
| MCP | JSON-RPC 2.0 薄實作（若改用官方 go-sdk 需先評估依賴量） |
| 外部服務 | **無** |

## 8. ISSUE／TASK 拆分與分工

> v0.1 一輪做完；每項都要能獨立驗收。依賴：PB01／PB02 為根，其餘疊上去。

### ISSUE-PB01：核心儲存層（`internal/store`）★可先開工
- **做**：SQLite schema（`nodes`／`links`／`history`／`meta`）、`init`／`seed`、CRUD、transaction 內同寫 history；WAL／busy_timeout／foreign_keys。
- **依賴**：無。
- **DoD**：schema 建得起；CRUD 通行；**任何狀態變更必留 history**。

### ISSUE-PB02：領域層（`internal/domain`）★可先開工
- **做**：節點型別、**狀態機（允許轉移表）**、ID 慣例與驗證、owner 名冊。
- **依賴**：無。
- **DoD**：非法轉移被拒；ID 撞名被拒（不自動加尾碼）。

### ISSUE-PB03：CLI（`cmd/pb`）
- **做**：`init`／`seed`／`serve`／`mcp`／`get`／`tree`／`create`／`update`／`move`／`assign`／`link`／`verify`／`comment`／`search`／`history`／`stats`。
- **依賴**：PB01、PB02。
- **DoD**：每個子命令實跑通過。

### ISSUE-PB04：MCP server（`internal/mcp`）
- **做**：JSON-RPC 2.0；`initialize`／`tools/list`／`tools/call`／`resources`；14 個 `pb_*`；stdio ＋ HTTP。
- **依賴**：PB01–PB03。
- **DoD**：`tools/list` 見 14 個工具；`pb_tree` 可實際呼叫。

### ISSUE-PB05：REST ＋ dashboard（`internal/httpapi`／`internal/web`）
- **做**：唯讀 REST（`/api/tree`、`/api/node/{id}`、`/api/node/{id}/history`、`/api/stats`、`/api/search`）；單頁 dashboard。
- **dashboard（學長視角）**：頂列統計 ＋ 左樹右詳情 ＋ **焦點面板**（🚧卡點／❓待裁示／🕒最近異動／👤每人手上張數）。
- **依賴**：PB01、PB02。
- **DoD**：開 `127.0.0.1:8787` 樹／詳情／統計與 DB 一致；焦點面板四項都顯示。

### ISSUE-PB06：skill（`skill/`）
- **做**：`SKILL.md`（知識＋cheatsheet）、`skill.py`（thin shim：找 `pb` 直連為先，找不到退回 REST）。
- **依賴**：PB03。
- **DoD**：`skill_invoke("project_board", …)` 能 `tree`／`create`／`move`／`verify`；安裝＝`cp -r skill /root/.cray/skills/dynamic/project_board`。

### ISSUE-PB07：驗證基礎（貫穿全案，及早啟動）
- **做**：狀態機／驗證／ID／history 單測；覆蓋率量測；`gofmt -l`／`go build`／`go vet`／`go test`。
- **依賴**：全部。
- **DoD**：改到的 package 覆蓋率 ≥90%，附實測數字。

**分工（建議）**

| 角色 | 人 | 範圍 |
|---|---|---|
| 開發 | 小蝦 | PB01–PB06 |
| 驗證／CR | 克勞德 | PB07 精神：讀本需求書，實跑驗收，不採信宣稱 |
| 架構審／裁示 | 一龍馬斯客（CTO） | §9 開放問題 |
| 需求／驗收 | JOBY（學長） | 最終驗收 |

## 9. 開放問題（待裁示）

| # | 問題 | 預設（未裁示時採用） |
|---|---|---|
| D1 | MCP 傳輸 | **stdio** 最通用；HTTP 供多人共用（`serve`） |
| D2 | dashboard 寫入 | v1 **唯讀** |
| D3 | 產品／binary 名 | 產品 `ProjectBoard`、binary `pb` |
| D4 | 母表條目（`TOTAL_CHECKLIST` A1–K8）是否建模成節點 | **先不建**（v0.3 再議） |
| D5 | 納入範圍的專案 | **先只收 `Y20260916`** |
| D6 | 服務埠 | `127.0.0.1:8787` |
| D7 | 「PR」概念 | ISSUE 以 `links(kind=pr)` 掛 PR，不另立型別 |
| D8 | skill 名稱 | `project_board` |
| D9 | MCP protocol version | 跟隨官方最新穩定；實作時鎖定並記進 README |

## 10. 里程碑

| 階段 | 內容 |
|---|---|
| v0.1（MVP） | PB01–PB07：樹站起來、四入口可用、dashboard 可看 |
| v0.2 | `import`／`export` 既有單、依賴圖、FTS 搜尋、dashboard 篩選 |
| v0.3 | 母表 render、多專案、進度視圖、週報 |

## 11. CTO 補充意見（克勞德，2026-09-20）

> 小蝦這份 v0.1 草案架構完整、狀態機／樹狀模型／四入口共用 core 的設計都正確，值得肯定。以下是我以 CR／CTO 角色抓到、**會擋開工或削弱系統核心價值（可稽核）**的具體缺口，已直接補進 `docs/DATA_MODEL.md` §11、`docs/INTERFACE.md`、`docs/REQ.md`、`docs/ROADMAP.md`、`docs/OPERATIONS.md`、`skill/SKILL.md`，這裡只列摘要，細節見連結：

| # | 問題 | 為什麼重要 | 解法（已寫入文件） |
|---|---|---|---|
| 1 | **`history.actor` 必填，但現有 CLI／MCP／skill 沒有任何寫入操作帶這個參數** | 照現在規格去開工，第一筆寫入就會因缺 `actor` 卡住或被瞎猜——這是會擋 PB01/PB04 開工的硬缺口，不是可以晚點補的小事 | 所有寫入 tool／CLI／skill 子指令新增必要 `actor`（或退回 env `PB_ACTOR`），值須在 owner 名冊內；`DATA_MODEL.md` §11.1 |
| 2 | **自我驗收（`owner` 自己 `verify` 自己）目前完全不會被標記** | 本系統立項理由就是「杜絕口頭宣稱完成」，但球員兼裁判沒有任何痕跡，等於繞過設計初衷 | `verify` 時 `actor==owner` 自動標記 `self-verified`；`pb_stats`／dashboard 新增「⚠ 自我驗收」張數（v0.1 只標記不擋，是否要硬性分離列為新增 D10）；`DATA_MODEL.md` §11.2 |
| 3 | **轉 `blocked` 不強制附理由** | dashboard「🚧 卡點」面板的價值就在「卡在等什麼」，沒理由的卡點清單對學長沒用 | 轉 `blocked` 須附 `note` 或已存在 `depends_on` link，否則拒絕；`DATA_MODEL.md` §11.3 |
| 4 | **`depends_on` 的 `target` 沒有存在性驗證** | 打錯 ID 或指向還沒建立的節點會被靜默接受，卡點清單之後會指向空氣 | 建立 `depends_on` link 時驗證目標節點存在；`DATA_MODEL.md` §11.4 |
| 5 | **省略 `--id` 時沒有生成規則** | 規格沒講清楚，各 AI 各自猜會產生不一致的 ID 慣例 | 定義 slugify-from-title 的明確演算法，撞名一樣直接拒絕（不自動加尾碼）；`DATA_MODEL.md` §11.5 |
| 6 | **`update`／`transition` 沒有併發保護，兩個 agent 同時改同一張單會靜默覆蓋（lost update）且不留任何衝突痕跡** | 這剛好打在系統想解決的痛點正中間；成本低（借用既有 `updated_at` 當版本 token，不必加欄位），值得 v0.1 就做，不是 nice-to-have | 新增可選 `expected_updated_at` 前置條件，不符回 `conflict`；`DATA_MODEL.md` §11.6 |
| 7 | **schema 只定了 v1，沒有演進機制** | v0.2（FTS5／import）、v0.3（母表 item）都會動 schema；沒有 migration 機制屆時只能砍庫重建，等於稽核歷史全毀，跟「可稽核」的定位互相矛盾 | `internal/store` 內建循序 migration，`meta.schema_version` 驅動；併入 PB01 DoD；`DATA_MODEL.md` §11.7 |
| 8 | **MCP 協定驗收全靠人工逐一手點** | 每次改動 tool 都要重新手點一輪，容易漏，不是長久防線 | `internal/mcp` 內建自動化 round-trip 整合測試，算進 PB07 覆蓋率；手動清單降級為「跨 harness 相容性補充驗證」；`DATA_MODEL.md` §11.8 |

**沒改動原案的部分**：架構圖、四入口分工、狀態機轉移表、三張核心表的欄位設計、技術選型（純 Go SQLite／無框架 dashboard／薄 MCP 實作）——這些判斷都對，不動。

**建議**：第 1、3、4、6、7、8 項屬於「不做會漏掉正確性、之後補會動到已上線資料／介面」的類型，建議**排進 v0.1**（PB01/PB02/PB04 開工時一併做），不要等 v0.2 才補；第 2 項的「是否硬擋自我驗收」才是真正需要學長／CTO 裁示的政策問題（已列 D10，預設不擋只標記）。

---

## 12. 簽署

| 角色 | 人 | 簽 |
|---|---|---|
| 提案 | JOBY（學長／影者） | |
| 架構審核 | 一龍馬斯客（CTO） | |
| 開發 | 小蝦（螯蝦Pi） | |
| CR／QA | 克勞德 | |
