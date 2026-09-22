# DATA_MODEL.md — ProjectBoard 資料模型

> 真相源：`var/board.db`（SQLite 3，WAL）。本檔為 schema 權威版，目前 `schema_version=6`（見
> `internal/store/migrations/`）；改到 schema 時這份文件要跟著更新，不要只改 migration。

---

## 1. 表總覽

| 表 | 用途 |
|---|---|
| `nodes` | 樹上的節點（project／req／issue／report／item／bug／plan…，見 §2a） |
| `node_types` | node 型別的資料驅動定義（schema v6 起，見 §2a） |
| `links` | 節點對外關聯與依賴（commit／file／PR／url…） |
| `history` | 事件流（append-only，稽核用） |
| `meta` | 版本與系統鍵值 |

---

## 2. `nodes`

```sql
CREATE TABLE nodes (
  id         TEXT PRIMARY KEY,                 -- 人類可讀 ID（路徑式）
  type       TEXT NOT NULL REFERENCES node_types(key),   -- schema v6 起：FK 取代 CHECK enum，見 §2a
  parent_id  TEXT REFERENCES nodes(id) ON DELETE RESTRICT,   -- 根為 NULL
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'todo'
             CHECK (status IN ('todo','in_progress','review','blocked','hold','done','cancel')),
  owner      TEXT NOT NULL DEFAULT 'unassigned',
  priority   TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('high','medium','low')),
  tags       TEXT NOT NULL DEFAULT '',          -- 逗號分隔
  body       TEXT NOT NULL DEFAULT '',          -- markdown 正文
  sort       INTEGER NOT NULL DEFAULT 0,        -- 同層排序
  created_at TEXT NOT NULL,                     -- ISO8601（台北）
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_nodes_parent      ON nodes(parent_id);
CREATE INDEX idx_nodes_type_status ON nodes(type, status);
CREATE INDEX idx_nodes_owner       ON nodes(owner);
```

- **`status` 是欄位，不是樹的一層** —— 同一棵樹才能 filter 出「未完成」。
- `parent_id` 用 `ON DELETE RESTRICT`：**有子節點不准刪**（防止樹斷）。
- `id` 發出後**不可改**（見 §5）。

## 2a. `node_types`（schema v6 起，type 的權威來源）

```sql
CREATE TABLE node_types (
  key         TEXT PRIMARY KEY,                 -- type 代號（bug/plan 皆是這裡的一列，不是特例）
  id_prefix   TEXT NOT NULL,                    -- ID 慣用前綴（project 為空字串）
  id_shape    TEXT NOT NULL,                    -- project_date／slug／item_key／report_owner_date（見 §7）
  label       TEXT NOT NULL,                    -- 顯示名稱
  parent_type TEXT NOT NULL DEFAULT '',         -- 非空＝parent 必須是該 type；空＝可掛 project 下
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL
);
```

**設計動機（Y20260920/REQ-V05-NODE-TYPE-REGISTRY）**：schema v1~v5 的 `nodes.type` 是 `CHECK (type IN (...))`，SQLite 不能 `ALTER` 掉既有 CHECK，每加一種 type 都要重演一次 `PRAGMA writable_schema` 改 `sqlite_master` 的高風險 migration（v4 加 `item`、v6 這次是最後一次）。v6 把「type 有哪些」從結構搬成資料：`nodes.type` 改成 `REFERENCES node_types(key)`，**之後新增或修改 type 只要 INSERT／UPDATE 一筆 `node_types`，不必再動 schema、不必改 Go code**。

- `id_shape` 決定 `domain.TypeRegistry.ValidateID`／`GenerateID` 走哪個形狀函式（形狀邏輯是結構化規則，留在 Go 當固定形狀庫；「哪個 type 用哪個形狀」才是資料）。
- `parent_type` 決定建立時的父節點型別檢查（泛化自舊版只認 `item` 必須掛 `req` 的特例）。
- v6 seed 7 筆：`project／req／issue／report／item`（既有 5 種，行為零變更）＋ `bug`（掛 `req` 下，比照 `issue`）、`plan`（可掛 `project` 下，比照 `req`）。
- `internal/domain/domain.go` 的 `BuiltinTypeDefs()` 是這 7 筆的出廠副本（供純函式路徑／測試使用）；**runtime 權威一律是 DB 的 `node_types` 表**（`store.typeRegistry()` 每次查表建 registry）。

## 3. `links`

```sql
CREATE TABLE links (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  from_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('depends_on','file','commit','pr','url','doc','repo')),
  target     TEXT NOT NULL,                     -- depends_on→節點 id；其餘→字串
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_links_from   ON links(from_id);
CREATE INDEX idx_links_target ON links(target);
```

> `repo` 是 schema v5（`0005_repo_link.sql`）補的，只放寬 CHECK，見 `REQ-V04-GIT-INTEGRATION/ISSUE-GIT-REPO`。

| kind | 意義 | `target` 例 |
|---|---|---|
| `depends_on` | 卡單（本單等那張單） | `Y20260916/REQ-A/ISSUE-X`（節點 id，**建立時須驗證節點存在**，見 §11.4） |
| `file` | 相關檔案 | `dev_docs/ISSUE-...md` |
| `commit` | 收單的 commit | `8094064`（或 `V2.20260919.001.9`）；由 `pb_commit_attach`／`post-commit` hook 自動掛，也可手動 `pb_link` |
| `pr` | 對應 PR | `#12` 或 URL |
| `url` | 參考連結 | `https://…` |
| `doc` | 規格／文件 | `CONVENTIONS.md` |
| `repo` | 專案對應的 git repo（掛在 project 節點上） | `https://github.com/…`；由 `pb_set_repo` 寫入，寫入前先移除該專案既有的 repo link（idempotent） |

## 4. `history`（append-only）

```sql
CREATE TABLE history (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id  TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  ts       TEXT NOT NULL,                       -- ISO8601
  actor    TEXT NOT NULL,                       -- 見 §6；來源見 §11.1
  action   TEXT NOT NULL CHECK (action IN
             ('create','update','transition','assign','link','unlink','comment','verify')),
  field    TEXT NOT NULL DEFAULT '',            -- update 時改的欄位
  from_val TEXT NOT NULL DEFAULT '',            -- 舊值（transition 時為舊狀態）
  to_val   TEXT NOT NULL DEFAULT '',            -- 新值
  note     TEXT NOT NULL DEFAULT ''             -- 說明／驗收證據
);
CREATE INDEX idx_history_node ON history(node_id, id);
```

- **不提供刪除**。 `verify` 的證據（覆蓋率、測試結果、file:line）一律寫進 `note`。
- **`actor` 必填但目前介面沒有任何寫入操作帶這個參數**——這是規格缺口，見 §11.1（會擋開工，優先修）。

## 5. `meta`

```sql
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
-- schema_version = <int>    目前 6（見 internal/store/migrations/，每加一版 migration 就 +1）
-- created_at     = <ISO8601>（DB 初始化時間，pb init 寫一次不再變）
-- hook_cursor    = <int>    hookLoop 用的喚醒游標，值為已處理過的 history.id（見 §11 Hook 喚醒）
-- last_id_seq    = <int>   （若採序列式 ID 時用；目前 ID 走 slugify，未實際使用這個鍵）
```

---

## 6. 狀態機

```
                ┌───────────────────────────────┐
                ▼                               │
  todo ──► in_progress ──► review ──► done ─────┘ (reopen)
    │           │            │
    │           │            │
    └───────────┴────────────┴──► blocked ──► in_progress
                                  │
        任何狀態 ──► hold（暫緩／上線前才做）
        任何狀態 ──► cancel（不做）
```

**狀態集與顯示**

| status | 顯示 | 意義 |
|---|---|---|
| `todo` | ⬜ | 未開始 |
| `in_progress` | 🔶 | 進行中 |
| `review` | 👀 | 待驗收 |
| `blocked` | 🚧 | 卡住（等前置／等裁示） |
| `hold` | ⏸ | 暫緩（上線前才做／等外部） |
| `done` | ✅ | 完成 |
| `cancel` | ❌ | 不做 |

**允許的轉移**（其餘一律拒絕）

| from | 可到 |
|---|---|
| `todo` | `in_progress`、`blocked`、`hold`、`cancel` |
| `in_progress` | `review`、`blocked`、`hold`、`cancel` |
| `review` | `done`、`in_progress`、`blocked`、`cancel` |
| `blocked` | `in_progress`、`hold`、`cancel`（**轉入 `blocked` 須附 `note` 或已存在 `depends_on` link，見 §11.3**） |
| `hold` | `todo`、`in_progress`、`cancel` |
| `done` | `in_progress`（reopen，需 `note`） |
| `cancel` | `todo`（revive） |

> `done` 只能從 `review` 進（＝必經驗收）；`verify()` 是唯一把 `review`→`done` 的正規路徑。

---

## 7. ID 慣例

id 的形狀由該 type 在 `node_types`（§2a）的 `id_shape` 決定，**不是每種 type 各自一條規則**——`id_shape` 只有 4 種固定值，新 type 一律挑一種既有形狀用：

| id_shape | 格式 | 用到的 type（v6 seed） |
|---|---|---|
| `project_date` | `Y<YYYYMMDD>` | project |
| `slug` | `<parent>/<PREFIX>-<SLUG>` | req、issue、bug、plan |
| `item_key` | `<parent>/<PREFIX>-<KEY>`（KEY＝母表編號，如 A1） | item |
| `report_owner_date` | `<parent>/<PREFIX>-<owner>-<YYYYMMDD>` | report |

範例：`Y20260916`（project）、`Y20260916/REQ-MEMBER-CORE`（req）、`Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A`（issue）、`…/ISSUE-UT90-A/REPORT-xiaoxia-20260919`（report）、`Y20260916/REQ-X/BUG-LOGIN-CRASH`（bug，掛 req 下）、`Y20260916/PLAN-V06-ROADMAP`（plan，掛 project 下）。

規則：
- `SLUG`／`KEY`：大寫、`A–Z 0–9 -`；禁空白。**沿用既有單名**（如 `ISSUE-ADMIN-BFF-UT90-A`）以便對帳。
- ID 唯一且**不可變**（改名會斷 links／history）。
- 若撞名，`create` 回錯誤（不自動加尾碼，避免失控）。
- **省略 `--id`／`id` 參數時的自動生成規則**（見 §11.5）：由 `title` slugify（轉大寫、非 `[A-Z0-9]` 一律轉 `-`、連續 `-` 收斂成一個、去頭尾 `-`）產生 `SLUG`／`KEY`，再依 `id_shape` 接上 `parent`／`PREFIX`。撞名一樣直接回錯誤，呼叫端需換 `title` 或改帶明確 `--id`。

## 8. owner 名冊（固定清單）

| 代號 | 對象 |
|---|---|
| `xiaoxia` | 小蝦（螯蝦Pi） |
| `kaimake` | 開碼客（opencode） |
| `kaimadi` | 開碼弟（opencode） |
| `yilong` | 一龍馬斯客（cursor，CTO） |
| `claude` | 克勞德（claude） |
| `human` | 學長（JOBY） |
| `unassigned` | 未指派（預設） |

---

## 9. 範例資料

```
Y20260916                                       project  in_progress  owner=human
├─ Y20260916/REQ-MEMBER-CORE                    req      in_progress  owner=yilong
│   ├─ .../ISSUE-ADMIN-BFF-UT90-A               issue    done         owner=xiaoxia
│   │    └─ .../REPORT-xiaoxia-20260919         report   done
│   ├─ .../ISSUE-ADMIN-BFF-UT90-B               issue    done         owner=kaimake
│   └─ .../ISSUE-CONC03                          issue    todo         owner=unassigned
└─ Y20260916/REQ-ARCH                            req      in_progress  owner=yilong
```

對應 `history` 片段：

| ts | actor | action | field | from→to | note |
|---|---|---|---|---|---|
| 2026-09-19T22:10 | xiaoxia | create | | | 建 ISSUE-ADMIN-BFF-UT90-A |
| 2026-09-19T22:11 | xiaoxia | assign | owner | unassigned→xiaoxia | |
| 2026-09-19T23:40 | xiaoxia | transition | status | todo→in_progress | |
| 2026-09-20T01:02 | xiaoxia | link | commit | | 8094064 |
| 2026-09-20T01:03 | xiaoxia | verify | status | review→done | 總覆蓋 98.0%、38 pkgs -race 乾淨 |

---

## 10. 併發與一致性

- SQLite 開 **WAL**、`busy_timeout=5000ms`、`foreign_keys=ON`。
- 多 AI 同時寫入：靠 WAL ＋ timeout 序列化；**每個寫入動作包在一個 transaction**，並在同一 transaction 內寫 `nodes` 與 `history`（保證「狀態變了必有事件」）。
- 不設 `updated_at` 觸發器以外的隱式行為；時間統一由程式寫入（ISO8601、台北時區）。

---

## 11. CTO 補充：一致性與可稽核強化（克勞德，2026-09-20）

> v0.1 草案（小蝦）架構完整、狀態機設計正確。以下是實作前必須補上的具體缺口，逐條附建議動作。
>
> **2026-09-22 補記：以下 §11.1～§11.8 全部已採納並實作完成**，不是還在等的缺口——保留原本
> 「現況／規則」的寫法是因為這節本質是決策紀錄（ADR），「現況」指的是 2026-09-20 寫這節當下
> 的狀態，不是今天的狀態。想確認某條有沒有真的做，直接看對應的 migration／測試／原始碼（本節
> 每條都有指），不要只看這份文件的文字。

### 11.1 `actor` 從哪來（★ 開工前必解，否則 history 寫不進去）

現況：`history.actor` 是 `NOT NULL`，但 CLI／MCP tools／skill 子指令**沒有任何一個帶 `actor` 參數**。這不是留白，是漏了——照現在的介面規格去實作，第一次寫入就會因缺 `actor` 而失敗或得瞎猜。

**規則**：
- 所有寫入類 MCP tool（`pb_create`／`pb_update`／`pb_transition`／`pb_assign`／`pb_link`／`pb_unlink`／`pb_verify`／`pb_comment`／`pb_delete`）**新增必要參數 `actor`**。
- 所有寫入類 CLI 子命令新增 `--actor <name>`；未帶時退回 env `PB_ACTOR`；兩者都沒有 → 直接拒絕（「缺必要參數即拒絕」是 rule.md 既定原則，這裡不例外）。
- `actor` 值須落在 §8 owner 名冊內（含 `human`），不在名冊內一律拒絕——避免 history 出現查無此人的 actor 污染稽核紀錄。
- 各入口預設 `PB_ACTOR` 建議值：skill（小蝦）啟動時設 `PB_ACTOR=xiaoxia`；各 harness 的 MCP 設定（`INTEGRATION.md`）比照掛對應 owner 代號。
- **信任邊界要寫明**：本系統不做身份驗證，`actor` 是呼叫端自報（自己講自己是誰）。這是內用小團隊下的合理取捨，但要在 `PROJECTBOARD_REQ20260920.md` NFR 明講，別讓人誤以為 history 有防偽造能力。

### 11.2 自我驗收要可見（否則繞過了本系統的存在意義）

現況：`verify()` 沒有限制 `actor` 是否等於節點的 `owner`。系統的立項理由是「杜絕口頭宣稱完成」，但一個 agent 完全可以自己做、自己 `verify` 自己，跟現在散落 markdown 的自報完成沒有本質差異，只是多了一層時間戳。

**規則（v0.1 採軟性標記，不擋）**：
- `pb_verify` 時若 `actor == 節點目前 owner`，`history` 該筆事件的 `note` 前面自動加註 `[self-verified]`（不改變 `action`／`to_val` 語意，純標記）。
- `pb_stats`／dashboard 焦點面板新增第五項：**⚠ 自我驗收張數**（近期 `done` 節點中 `self-verified` 佔比），讓學長／CTO 一眼看出「誰在球員兼裁判」。
- 是否要**硬性**擋自我驗收（例如 issue 類節點強制 `verify` actor 必須是 `claude` 或 `human`）留給學長／CTO 裁示，列為 `REQ.md` 新增 D10（預設：v0.1 不擋，只標記；觀察一段時間再決定要不要硬性分離)。

### 11.3 `blocked` 必須附理由

現況：轉入 `blocked` 不強制任何佐證，但 dashboard 焦點面板的賣點正是「🚧 卡點：等什麼」——沒有理由的卡點清單對學長沒用。

**規則**：`pb_transition id blocked` 時，`note` 非空**或**該節點已存在至少一筆 `depends_on` link，兩者擇一，否則回錯誤（`missing block reason`）。

### 11.4 `depends_on` 目標存在性驗證

現況：`links.target` 是純文字欄位，沒有像 `nodes.parent_id` 一樣的 FK 約束；`kind=depends_on` 時打錯 ID 或 ID 打了還沒建立的節點，會被靜默接受，之後卡點清單會指向空氣。

**規則**：domain 層在 `pb_link(kind=depends_on)` 時**查一次 `nodes` 是否存在該 `target`**，不存在則拒絕（`file`／`commit`／`pr`／`url`／`doc` 這幾種 kind 因為指向系統外的東西，維持現狀不驗證）。

### 11.5 ID 省略時的自動生成規則

見 §7 已補的 slugify 規則。**特別注意**：自動生成後一樣受「撞名即拒絕、不自動加尾碼」約束，所以標題重複時呼叫端必須自己換標題或明確帶 `--id`——這行為要讓小蝦／各 AI 在 `SKILL.md`／`INTERFACE.md` 錯誤說明裡看得到，避免以為系統會自動加序號。

### 11.6 樂觀鎖，防止併發下的靜默覆蓋（成本低，建議 v0.1 就做）

現況：`pb_update`／`pb_transition` 沒有版本檢查。兩個 AI（例如小蝦跟開碼客）幾乎同時對同一節點下 `update`，後寫的會無聲蓋掉先寫的欄位，且**不會**留下「發生過衝突」的任何 history 痕跡——這剛好打在系統想解決的痛點正中間（「無法稽核」），比多數功能都更值得優先做。

**規則**：`pb_update`／`pb_transition` 新增可選參數 `expected_updated_at`。帶了就比對節點目前 `updated_at`：不一致回 `conflict`（不寫入、不留 history），一致才放行。免費用既有的 `updated_at` 欄位當版本 token，不需要加新欄位。CLI／skill 對應 `--if-unmodified-since <ts>`；不帶則維持現行「後寫覆蓋」行為（相容舊呼叫）。

### 11.7 schema 演進機制

現況：`meta.schema_version` 有欄位但沒有機制。v0.2（FTS5 全文搜尋、`import`）、v0.3（母表 `type=item`）都會動 schema，屆時若沒有 migration 機制，唯一選項是砍掉 `var/board.db` 重來——對一個標榜「唯一真相、可稽核」的系統，資料庫重建等於歷史全毀，不能接受。

**規則**：`internal/store` 內建循序 migration（例如 `migrations/0001_init.sql`、`0002_fts5.sql`…），`pb init`／`pb serve`／`pb mcp` 啟動時比對 `meta.schema_version` 與最新 migration 序號，缺的依序套用（每個 migration 一個 transaction）。這條併入 PB01 的 DoD，不必等 v0.2 才補。

### 11.8 MCP 協定驗收要能自動跑，不能只靠人工點

`INTEGRATION.md` §7 現在的驗收清單是「每個 harness 手動點一次」。這對第一次上線可以，但之後任何一次改動（新增 tool、改參數）都要重新手點一輪，容易漏。

**規則**：`internal/mcp` 至少要有一支自動化整合測試，起一個暫存 DB，跑完整 JSON-RPC round trip（`initialize`→`tools/list`（驗證所有 `pb_*` 都在，工具數量隨新增工具增長，目前 20 個）→ `pb_create`→`pb_transition`→`pb_verify`→`pb_delete`），跑在 `go test` 裡，算進覆蓋率／DoD。各 harness 的手動清單留著當「跟外部 harness 相容性」的補充驗證，不是唯一防線。**已實作**：`internal/mcp/mcp_test.go` 有這支 round-trip 測試。
