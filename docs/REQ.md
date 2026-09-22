# REQ.md — ProjectBoard 需求規格

> **根目錄需求書版**：`../PROJECTBOARD_REQ20260920.md`（給團隊／克勞德看的正式需求書）。
> 本檔為**開發對照用詳規**（FR／NFR 全文），兩者內容一致、本檔較細。

| 欄位 | 內容 |
|---|---|
| 編號 | **REQ-PROJECTBOARD-20260920** |
| 立項 | JOBY（學長）2026-09-20 交辦 |
| 負責 | 小蝦（螯蝦Pi） |
| 定位 | 小蝦團隊**內用**的專案＋代碼開發流程管理系統（專案／需求／單 一棵樹） |
| 狀態 | **v0.1～v0.5 已實作並在跑**（本文件持續更新，跟不上實作時以原始碼／`README.md`／`INTERFACE.md` 為準） |

---

## 1. 背景與痛點

現況：需求、母表、單、回報**全是散落的 markdown**（`dev_docs/ISSUE-*.md`、`REQ`、`TOTAL_CHECKLIST_*`、`*_status_*.md`）。

- 單與單之間**沒有連結**（哪條 REQ 生出哪些單？哪些單被哪些單卡住？誰派給誰？）。
- 狀態靠**人腦＋檔名**記（`🟡 待命`／`🔲`／`✅`），沒有單一版本。
- 「回報完成」只能靠讀檔案，**沒有可稽核的事件流**（誰、何時、把狀態從 X 推到 Y、附什麼證據）。
- AI 成員（小蝦／一龍／開碼客／開碼弟／克勞德）**沒有一個共用的讀寫介面**去開單、查單、收單。

## 2. 目標

1. 把上述內容收進**一顆 SQLite**，成為**唯一真相**。
2. 提供**一棵樹**：`Project → REQ → ISSUE →（狀態）`；`REPORT` 為 ISSUE 的產出物。
3. 提供 **MCP server** 給 AI 讀寫；提供**唯讀 dashboard** 給學長看。
4. 每次變更留 **history**（append-only），杜絕「口頭宣稱完成」。
5. 能**承接既有 markdown 單**（匯入），也能**匯出**回 markdown。

## 3. 使用者與情境

| 角色 | 代表 | 主要動作 |
|---|---|---|
| 真人（唯一） | 學長（JOBY） | 看 dashboard（進度、卡點、誰在做什麼）、裁示 |
| AI 工程師 | 小蝦、開碼客、開碼弟 | 開單、領單、回報、貼 commit／證據、自我驗收 |
| AI 管理 | 一龍馬斯客（CTO）、克勞德 | 拆單、派單、驗收、看全樹 |

**典型情境**
- 學長：「開一個 REQ，從 PHP 跟進 X」→ 一龍經 MCP 建 `REQ`，再拆數張 `ISSUE` 派給三人。
- 小蝦領單 → `transition in_progress` → 做完 `link commit`＋`verify`（附覆蓋率）→ `review`。
- 學長在 dashboard 一眼看出「哪些 ISSUE 卡在 blocked、誰手上還有幾張」。

## 4. 名詞

| 詞 | 定義 |
|---|---|
| **Project** | 一個開發案（例 `Y20260916`）。樹根。 |
| **REQ** | 需求（一組相關 ISSUE 的集合）。 |
| **ISSUE** | 單／任務（= 一次開發、修 bug、驗收單位）。可細分多軌。 |
| **REPORT** | 回報／產出物（status 檔、報告），掛在 ISSUE 下。 |
| **狀態** | 節點在流程中的位置（`todo`…`done`）；**不是樹的一層**。 |
| **跡象（history）** | 對節點做的每一次動作事件。 |

## 5. 功能需求（FR）

| # | 需求 | 說明 |
|---|---|---|
| PB-FR-01 | **節點樹** | 建立 `project`／`req`／`issue`／`report`；指定 `parent`；同層可排序 |
| PB-FR-02 | **狀態與流轉** | 狀態集固定；只能走狀態機允許的邊；非法轉移回錯誤 |
| PB-FR-03 | **指派** | 節點有 `owner`（AI 名冊或 `human`／`unassigned`） |
| PB-FR-04 | **關聯與依賴** | `links`：`depends_on`／`file`／`commit`／`pr`／`url`／`doc` |
| PB-FR-05 | **歷史稽核** | 任何寫入都留 `history`（actor／action／field／from→to／note／ts） |
| PB-FR-06 | **驗收** | `verify(id, evidence)`：附證據 → 狀態 `done`，證據落 history |
| PB-FR-07 | **查詢／搜尋** | 樹（可 filter 狀態／owner／type）、單節點全文、關鍵字搜尋 |
| PB-FR-08 | **MCP 介面** | 讀寫工具（見 `INTERFACE.md`），供各 AI harness 掛載 |
| PB-FR-09 | **REST 介面** | 唯讀 JSON，供 dashboard 用 |
| PB-FR-10 | **Dashboard（學長視角）** | 單頁：頂端狀態／型別統計列（合一行，可點篩選，數字跟現有篩選連動、跟點下去的樹結果永遠一致）、左樹右詳情（樹自動展開到符合篩選的節點；選定專案時詳情欄預設顯示專案本身；記住上次離開的畫面）、**焦點／進度／母表／週報**四個分頁（緊貼樹狀圖下方）、焦點面板含卡點／待裁示／最近異動／每人手上張數／**自我驗收警示**（窮舉查詢，數字跟明細清單一致） |
| PB-FR-11 | **匯入既有單** | 掃 `dev_docs/*.md`（`ISSUE-*`／`*_status_*`／`REQ`）建節點——**已實作**（`pb import`） |
| PB-FR-12 | **匯出 markdown** | 節點 → markdown（寫到 `var/export/`，**不覆蓋原件**）——**已實作**（`pb export`） |
| PB-FR-13 | **統計／進度** | 各狀態計數、每 REQ 完成度、卡點清單 |
| PB-FR-14 | **技能層（skill_invoke）** | 提供 `project_board` dynamic skill（`SKILL.md`＋`skill.py` shim），**與 cray 技能系統同構**；小蝦在 harness 內以 `skill_invoke` 操作，無需先掛 MCP |
| PB-FR-15 | **外部 harness 標準整合** | 提供**標準 MCP server**（stdio ＋ Streamable HTTP）供 claude code／cursor／opencode 等 harness 調用；各 harness 設定範例見 `INTEGRATION.md` |
| PB-FR-16 | **顯式身份（actor）** | 所有寫入操作（CLI／MCP／skill）必帶 `actor`（或退回 `PB_ACTOR`），寫入名冊外的值一律拒絕；克勞德補充，見 `DATA_MODEL.md` §11.1 |
| PB-FR-17 | **自我驗收標記** | `verify` 時 `actor==owner` 自動標記 `self-verified`，`pb_stats`／dashboard 顯示張數（不阻擋）；克勞德補充，見 §11.2 |
| PB-FR-18 | **型別可擴充化** | node type 存在 `node_types` 表（資料驅動），不再是 schema 寫死的 CHECK enum；新增型別（如 `bug`／`plan`）只需 INSERT 一筆，不必動 schema／Go switch。見 `REQ-V05-NODE-TYPE-REGISTRY` |
| PB-FR-19 | **git 整合** | commit 訊息帶單號自動掛連結（`post-commit` hook → `pb_commit_attach`／`pb commit attach`，只掛 link 不動狀態）；`commit-msg` hook 提醒沒帶單號；`pb_set_repo` 設定 project↔repo 對應；GitHub webhook 端點（`/api/integrations/github`，`GH_WEBHOOK_SECRET` 驗簽，push/PR 事件自動 link）。見 `REQ-V04-GIT-INTEGRATION`、`docs/GIT_INTEGRATION.md` |
| PB-FR-20 | **Hook 喚醒（真的喚醒，不是只訂閱）** | `pb_hook` 訂閱節點狀態變動；長駐 `pb serve` 背景每 2 秒輪詢，偵測到訂閱節點異動時**真的** `exec herdr agent prompt <target> <訊息>` 叫醒對應 harness（5 秒逾時，失敗只記 log 不擋服務）。喚醒的責任固定在 `pb serve`，改狀態的行程不直接呼叫 herdr。見 `REQ-V03-HOOK`、`cmd/pb/hookloop.go` |

## 6. 非功能需求（NFR）

| # | 需求 | 說明 |
|---|---|---|
| PB-NFR-01 | **單機、零外部服務** | 單一 Go binary ＋ 一顆 SQLite 檔；無 DB server／Redis／Node |
| PB-NFR-02 | **純 Go SQLite** | `modernc.org/sqlite`（無 cgo）；WAL ＋ busy_timeout，容多 process 共用 |
| PB-NFR-03 | **效能** | 數千節點下取整棵樹 < 200ms |
| PB-NFR-04 | **可備份** | `cp var/board.db*` 即完成備份（備份存放 `/root/.cray/BAK`） |
| PB-NFR-05 | **可測** | 狀態機／驗證／ID／history 有單測；改到的 package 覆蓋率 ≥90% |
| PB-NFR-06 | **安全** | `pb serve --addr` 可自由指定綁定位址；`service.sh` 常駐用途固定綁 `0.0.0.0:8787`——**2026-09-20 導演拍板**：跑的是宿主機（host），實際環境大多在 container 裡，dashboard／MCP(HTTP) 要能被宿主機／container 外的人與 AI 連到，綁 `127.0.0.1` 反而連不到。無需登入（內用小團隊，非公開服務；身份為自報見 NFR-10） |
| PB-NFR-07 | **可稽核** | `history` append-only，不提供刪除 |
| PB-NFR-08 | **與技能系統同構** | skill 的安裝／讀取／調度慣例，與 `/root/.cray/skills/dynamic/*` 現行慣例一致（`SKILL.md` 知識＋`skill.py` shim） |
| PB-NFR-09 | **遵從 MCP 官方規格** | JSON-RPC 2.0；`initialize`／`tools/list`／`tools/call`／`resources`；兩種標準傳輸（stdio、Streamable HTTP） |
| PB-NFR-10 | **身份為自報，非驗證** | `history.actor` 由呼叫端顯式帶入（或退回 env `PB_ACTOR`），系統**不做身份驗證**；此為內用小團隊下的合理取捨，須明文告知使用者，不得誤導為防偽造。詳見 `DATA_MODEL.md` §11.1 |
| PB-NFR-11 | **樂觀鎖** | `pb_update`／`pb_transition` 支援可選 `expected_updated_at` 前置條件，衝突回 `conflict`，防止多 agent 併發下的靜默覆蓋。詳見 `DATA_MODEL.md` §11.6 |
| PB-NFR-12 | **schema 可演進** | `internal/store` 內建循序 migration，`meta.schema_version` 驅動；v0.2／v0.3 加欄位不需砍庫重建。詳見 `DATA_MODEL.md` §11.7 |

## 7. 範圍外（v1 明確不做）

> 以下兩條原本也列在這裡、後來實際做了，2026-09-22 拿掉：「通知（webhook）」→ 見 PB-FR-19
> 的 GitHub webhook 端點；「git hook／自動 commit」→ 見 PB-FR-19 的 commit 掛單號機制。
> 拿掉不是重新開放範圍，是文件補上已經發生的事實。

- 登入／權限／多租戶、任何形式的對外公開（供內部宿主機／container 網路存取不算「對外公開」，
  見 NFR-06；沒有帳號密碼登入機制、無使用者權限分級）。
- Email／Slack 通知（webhook 只有 GitHub 事件進來這個方向，沒有主動對外發通知）。
- 雲端同步、行動版。
- 富文本編輯器、拖拉式 Kanban、甘特圖。
- 排程與提醒（hook 喚醒是「狀態變了才叫」，不是定時提醒）。

## 8. 待裁示決策（Open Decisions）

> 這幾條請學長／CTO 拍板；未拍板前以「預設」欄開發。

| # | 決策 | 預設（未拍板時採用） | 備註 |
|---|---|---|---|
| D1 | MCP 傳輸 | **HTTP 單一常駐 server** 為主，另支援 stdio | 多人共用同一 DB 需單一 server 較單純 |
| D2 | dashboard 寫入 | **v1 唯讀** | 寫入一律走 MCP |
| D3 | 產品／binary 名 | 產品 `ProjectBoard`、binary `pb` | |
| D4 | 母表條目（`TOTAL_CHECKLIST` A1–K8）建模 | **已解決（v0.3）**：建成 `type=item`（掛在 `req` 下），`pb checklist`／dashboard 母表分頁皆已實作 | 原「先不建」已過期 |
| D5 | 納入其他 campaign | **已解決（v0.3 多專案管理）**：board 上現有多個 project（`Y20260916`／`Y20260920`／`Y20260921`…），「只收 Y20260916」這條限制已解除；原本規劃過的「納入批准制」（技術面擋非學長批准的 project 進板）後來 `cancel` 掉了，目前納入哪個 campaign 是社群／流程共識，不是系統技術擋 | 原「先只收 Y20260916」已過期；批准制取消見 `ISSUE-MULTI-APPROVAL`（status=cancel） |
| D6 | 服務埠 | **已解決**：`service.sh`／常駐用途固定 `0.0.0.0:8787`（見 NFR-06）；`pb serve --addr` 直接跑則預設 `127.0.0.1:8787`，可自由覆寫 | 原「固定127.0.0.1」已過期 |
| D7 | 「PR」概念 | ISSUE 以 `links(kind=pr)` 掛 PR，**不另立節點型別** | 學長原話「ISSUE(PR)」 |
| D8 | skill 名稱 | `project_board`（安裝到 `/root/.cray/skills/dynamic/project_board/`） | 子指令名另見 `INTERFACE.md` §5 |
| D9 | MCP protocol version 與預設傳輸 | 跟隨官方最新穩定；預設 **stdio**（最通用） | 實作時鎖定版本，記進 README；詳見 `INTEGRATION.md` |
| D10 | 自我驗收（`actor==owner` 的 `verify`）要不要硬性擋 | **技術上 v0.1 不擋**（仍只標記 `self-verified` ＋ dashboard 顯示張數）；**但流程上學長 2026-09-20 拍板：ProjectBoard 全案的 `verify`／approve 一律由克勞德一人把關**（學長後台無法操作，這是 AI 團隊自己的平台）。各 AI 成員收工一律停在 `review`，等克勞德 `verify`，不要自己 verify 自己的單；克勞德驗完只需摘要回報學長，不必逐筆再讓學長過目 | 克勞德補充，見 `DATA_MODEL.md` §11.2；流程拍板見 `dev_docs/DISPATCH_PLAN.md` |

## 9. 驗收（v0.1 DoD）

- 能用 MCP 建出 `Y20260916 → REQ → ISSUE` 一棵樹，並走完 `todo→in_progress→review→done`（含 `verify` 證據）。
- dashboard 開得起來，樹與狀態與 DB 一致。
- 非法狀態轉移被拒；每次變更都有 history，且每筆都帶得出 `actor`（PB-FR-16）。
- 自我驗收（`actor==owner`）在 `pb_stats`／dashboard 看得到張數（PB-FR-17）。
- `pb_update`／`pb_transition` 的 `expected_updated_at` 衝突檢查能實測擋下一次併發覆蓋（PB-NFR-11）。
- `internal/mcp` 有自動化 round-trip 整合測試（非僅人工點），算進覆蓋率（`DATA_MODEL.md` §11.8）。
- `go build`／`vet`／`test` 過、`gofmt -l` 乾淨、改到的 package 覆蓋率 ≥90%。
