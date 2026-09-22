# ROADMAP.md — ProjectBoard 里程碑

> 原則：**先能用、再變好**。每個階段都要能實跑、有測試、覆蓋率 ≥90%。
>
> **2026-09-22 現況**：v0.1～v0.5 全部已交付並在跑（此文件原本只寫到 v0.3，漏掉已經做完的
> v0.4／v0.5 兩個里程碑，2026-09-22 補上）。v0.1～v0.3 底下的 `- [ ]` 是當初立項時的 DoD
> checklist，逐項對照原始碼／測試核實過都已達成，這裡不逐一改成 `[x]` 洗掉歷史勾選紀錄，
> 想確認哪一項有沒有做，直接看對應的原始碼／`go test`／ProjectBoard 節點史，不要只看有沒有打勾。

---

## v0.1 — MVP（先讓樹站起來）

**目標**：能用 `skill_invoke`／MCP 建出 `Y20260916 → REQ → ISSUE`，走完 `todo→in_progress→review→done`，學長看得到 dashboard。

| # | 交付 | 內容 |
|---|---|---|
| 1 | `internal/store` | SQLite schema（`nodes`／`links`／`history`／`meta`）、`init`／`seed`、CRUD、transaction 內同寫 history |
| 2 | `internal/domain` | 節點型別、**狀態機（允許轉移表）**、ID 慣例與驗證、owner 名冊 |
| 3 | `cmd/pb` CLI | `init`／`seed`／`get`／`tree`／`create`／`update`／`move`／`assign`／`link`／`verify`／`comment`／`search`／`history`／`stats` |
| 4 | `internal/mcp` | 14 個 `pb_*` tools（stdio ＋ HTTP） |
| 5 | `internal/httpapi`＋`web` | 唯讀 REST ＋ 單頁 dashboard（含**焦點面板**） |
| 6 | `skill/` | `SKILL.md`＋`skill.py`（CLI 直連為主、REST 為輔） |
| 7 | 測試 | 狀態機／驗證／ID／history 單測；改到的 package ≥90% |

**DoD**
- [ ] `skill_invoke("project_board", args="tree")` 回得出樹。
- [ ] 建 `Y20260916 → REQ → ISSUE` 三層，`move`／`assign`／`link commit`／`verify` 全通，且每筆 `history` 都有正確 `actor`。
- [ ] 非法轉移（如 `todo → done`）被拒；轉 `blocked` 沒附理由也被拒。
- [ ] `expected_updated_at` 衝突檢查實測能擋下一次併發覆蓋。
- [ ] dashboard 開 `127.0.0.1:8787`，樹／詳情／統計與 DB 一致。
- [ ] dashboard 焦點面板看得到卡點／待裁示／最近異動／每人手上張數／**自我驗收張數**。
- [ ] `internal/mcp` 有自動化 round-trip 整合測試（不只人工點）。
- [ ] `go build`／`vet`／`test` 過、`gofmt -l` 乾淨、覆蓋 ≥90%。

> 上述 `actor`／`blocked` 理由／`expected_updated_at`／自我驗收／自動化整合測試 五項為克勞德 2026-09-20 CTO review 補充，詳見 `DATA_MODEL.md` §11。

---

## v0.2 — 接上既有單（把散落的收進來）

| # | 交付 | 內容 |
|---|---|---|
| 1 | `pb import <dir>` | 掃 `dev_docs/*.md`：`ISSUE-*`→issue、`*_status_*`→report（掛對應 issue）、`*REQ*`→req；front-matter／標題推斷 owner／狀態；**同名只警告不覆蓋** |
| 2 | `pb export <id>` | 節點 → markdown，寫到 `var/export/`（**不覆蓋原件**） |
| 3 | 依賴圖 | `depends_on` 的卡點清單（哪些單等哪些單） |
| 4 | 搜尋加強 | body 全文（SQLite FTS5）＋ 標籤／owner 過濾 |
| 5 | dashboard+ | 篩選器（狀態／owner／專案）、卡點面板 |

**DoD**
- [ ] 匯入 `Campaign/Y20260916/dev_docs`（~120 檔）不失敗，數量與抽樣對得上。
- [ ] 匯入後 `tree --project Y20260916` 能看出 REQ／ISSUE 歸屬。
- [ ] 匯出檔與原單可讀性一致。

---

## v0.3 — 給學長看的報表

| # | 交付 | 內容 |
|---|---|---|
| 1 | 母表 render | 由樹生成 `TOTAL_CHECKLIST` 式母表（A–K 可作 `type=item` 子項） |
| 2 | 多專案 | 已解決（`REQ.md` D5）：board 上現有 `Y20260916`／`Y20260920`／`Y20260921` 等多個 project；原本規劃的「納入批准制」後來 `cancel`，改成口頭流程（學長跟 CTO 說一聲即可） |
| 3 | 進度視圖 | 每 REQ 完成度%、每 owner 手上張數、平均滯留天數 |
| 4 | 週報 | `pb report --week` 產出本週異動（誰／做了什麼／收了什麼） |

---

## v0.4 — git 整合（已交付，2026-09-20）

來源：`Y20260920/REQ-V04-GIT-INTEGRATION`（學長 2026-09-20 定調）。目標：從單快速找到對應
commit／PR，也能從 commit 回推單；不自動 `move`／`done`（避免越權），只掛連結。

| # | 交付 | 內容 |
|---|---|---|
| 1 | commit 訊息規範 | 訊息帶單號（`Y<8碼>/…`）才會掛；規範見 `docs/GIT_INTEGRATION.md` |
| 2 | `pb commit attach` | 從 commit 訊息（或 `--sha`／`--message-file`）自動把 sha 掛到訊息裡提到的單 |
| 3 | post-commit hook | `scripts/install-git-hooks.sh`：裝 `core.hooksPath`，commit 後自動 `pb commit attach`；`commit-msg` hook 提醒沒帶單號 |
| 4 | project↔repo 對應 | `pb_set_repo`／`pb repo set/show`，`link kind=repo`（schema v5 新增） |
| 5 | GitHub webhook | `POST /api/integrations/github`，push/PR 事件自動掛 `link commit/pr`，`GH_WEBHOOK_SECRET` 驗簽 |
| 6 | Dashboard 顯示 | 單的關聯列出 commit／PR；有設 repo 的話 sha／PR 變可點連結回 GitHub |
| 7 | MCP／skill 對外介面 | `pb_commit_attach`／`pb_set_repo`；skill 透傳同樣的 CLI 語法 |

七張子 ISSUE 全部 `done`，詳見 `docs/GIT_INTEGRATION.md`。

## v0.5 — 型別可擴充化 ＋ dashboard metadata API（已交付，2026-09-22）

來源：`Y20260920/REQ-V05-NODE-TYPE-REGISTRY`、`Y20260920/REQ-V05-DASHBOARD-META-API`
（導演 2026-09-22 定調）。

| # | 交付 | 內容 |
|---|---|---|
| 1 | `node_types` 表 | `nodes.type` 從 `CHECK (type IN (...))` 改成 `REFERENCES node_types(key)`（schema v6，**最後一次**動這個 CHECK）；新增型別只要 INSERT 一筆，不必再動 schema／Go switch |
| 2 | 新增 `bug`／`plan` 型別 | `bug` 掛 `req` 下（比照 `issue`）、`plan` 可掛 `project` 下（比照 `req`） |
| 3 | `GET /api/meta` | types／owners／statuses／tabs 的顯示 metadata 唯一來源；dashboard 不再自存字面量（`var TYPE`／`var STATUS`／`var TAB_LABEL` 這些寫死的 JS 常數全部砍掉改吃這支） |
| 4 | dashboard 型別篩選 | 型別下拉／統計 chip 含 bug／plan，可點篩選 |

兩個 REQ 底下的子 ISSUE 全部 `done`。v0.5 之後（2026-09-22 同一天）還有一批**沒有掛版號**的
dashboard 修復與文件整理（型別/狀態統計 chip 篩選連動、樹自動展開、記住上次畫面、自我驗收清單
窮舉查詢、Makefile／install.sh／LICENSE、五份 docs 對齊現況…），單號散落在 `Y20260920` 底下各自
獨立的 `ISSUE-*`，沒有收進一個 `REQ-V06-*`——要不要正式開一個 v0.6 把這批收攏，還是保持現狀當
零散維護，留給學長／CTO 決定。

## 風險與備註

- **範圍蔓延**：v1 只做「樹＋狀態＋稽核」；看板拖拉、通知、權限一律不做（見 `REQ.md` §7）。
- **匯入品質**：既有檔命名不完全一致（有無日期、有無人名的差異）→ 匯入規則要**可覆寫、可預覽**（`--dry-run`）。
- **併發**：多 AI 同時開單 → WAL ＋ short transaction；若出現 `database is locked` 調 `busy_timeout`。
