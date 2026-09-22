# ProjectBoard

> 內用、本機優先、單一真相源的專案／需求／單管理系統。**AI 走 MCP／CLI 讀寫，人看 dashboard。**
> License：MIT（見 `LICENSE`）。單一 Go binary，無 cgo，無外部服務依賴（無 DB server、無 Redis、無 Node build）。

**這份 README 是寫給 AI／coding agent 讀的**：照著做就能把整個系統裝起來、接上你的 harness、
開始讀寫單。人類想看操作介面，直接開 dashboard（見下方「啟動」）。

---

## 現況（2026-09-22）

- Schema：`internal/store/migrations/` 共 6 版（node_types 已資料驅動，新增 type 不必再動 schema）。
- CLI（`pb`）：init／seed／serve／mcp／tree／get／create／update／move／assign／link／verify／
  comment／search／deps／history／stats／report／hook／unhook／hooks／checklist／import／
  commit attach／repo／export，共 20+ 子命令。
- MCP：20 個 `pb_*` tools（stdio ＋ HTTP 兩種傳輸，同一份邏輯）。
- Dashboard：單頁（`internal/web/dashboard.html`），唯讀，暗色主題對齊 claude.ai 視覺；狀態／
  型別統計可點篩選、樹自動展開、詳情欄預設顯示目前專案、記得上次離開的畫面、焦點／進度／母表／
  週報四個分頁。
- Git 整合：commit 訊息帶單號會自動掛連結（不需要人工操作）；ISSUE 狀態變動可以真的把訂閱的
  harness 叫醒（不是文件寫假的，見下方「hook 喚醒」一節，機制在 `pb serve` 裡常駐運作）。
- 建置：`make build`／`make linux`／`make windows` 皆已驗證過真的能編出可執行檔；`install.sh`／
  `install.ps1` 一鍵安裝已測過至少一種路徑。

---

## 這是什麼

把散落的 `ISSUE-*.md`、`REQ`、母表 checklist 收進**一顆 SQLite**（`var/board.db`，WAL 模式），
用**一棵樹**管理：

```
Project
 └─ REQ（需求）
      ├─ ISSUE（單／PR，也可以直接掛在 Project 下）
      ├─ ITEM（母表項目）
      ├─ BUG（缺陷）
      └─ REPORT（報告）
 └─ PLAN（計畫，可以是頂層）
```

節點的「型別」（type）記錄它本質上是什麼（req/issue/bug/plan/…），「狀態」（status）記錄它
目前流程走到哪（todo/in_progress/review/blocked/hold/done/cancel）——**兩個維度分開，型別做完
不會變成別的型別**，只有狀態會變。所有寫入都記進 `history`（誰／何時／從什麼改成什麼／證據
說明），不採信口頭宣稱。

---

## 安裝

### 一鍵（新機器，還沒 clone 過）

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/amgio38/Y20260923/main/install.sh | bash
```

```powershell
# Windows（PowerShell）
Invoke-WebRequest -Uri https://raw.githubusercontent.com/amgio38/Y20260923/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

腳本會檢查 `git`／`go`（>=1.25）、clone（或用現有 checkout）、build、把 `pb` 裝進
`~/.local/bin`，並印出下一步。

### 手動（已經 clone 好）

```bash
cd project_board
make build          # 編給目前這台機器 → bin/pb
make linux          # 交叉編 Linux amd64 → bin/pb-linux-amd64
make windows        # 交叉編 Windows amd64 → bin/pb-windows-amd64.exe
make test           # go test ./...（改完程式碼務必先跑這個再 commit）
```

純 Go（`modernc.org/sqlite`，無 cgo），跨平台編譯不需要額外的 C 工具鏈。

### 初始化資料庫

```bash
./bin/pb init                    # 建 var/board.db（含 schema，可重複跑）
./bin/pb seed                    # 建立第一個專案節點（可重複跑）
```

### 設定你自己團隊的 owner 名冊

`actor`（誰在寫入）跟 dashboard 上的「每人手上張數」認的名字清單，**原始碼裡沒有寫死任何
團隊或任何人的名字**——沒設定的話只認 `unassigned` 一個值（新裝好、還沒設定就是這樣，乾淨、
不會看到不相干的名字）。設定方式擇一（先讀到的贏）：

```bash
# 方式一：環境變數（逗號分隔），適合container/CI這種本來就會注入env的場合
export PB_OWNERS="alice,bob,carol,human,unassigned"

# 方式二：設定檔，適合一般本機/常駐部署
cp owners.example.txt owners.txt   # 跟 pb 執行檔同一個工作目錄；改成你自己團隊的名字
# 或用 PB_OWNERS_FILE 指到別的路徑：export PB_OWNERS_FILE=/path/to/owners.txt
```

兩者都沒有就只認 `unassigned`；`unassigned` 不管哪種設定方式都會自動併入（schema 層的
owner 預設值）。`owners.txt` 是本機檔案，已加進 `.gitignore`，不會被commit。

---

## 啟動

```bash
# 前景跑（開發／除錯）
./bin/pb serve --addr 0.0.0.0:8787

# 背景常駐（正式使用；pid/log 見 var/serve.pid、var/serve.log）
./service.sh start
./service.sh status
./service.sh restart
./service.sh stop
```

`pb serve` 同時提供三件事：**REST API（唯讀，供 dashboard 用）**、**dashboard 單頁**（瀏覽器開
`http://<addr>/`）、**MCP over Streamable HTTP**（`http://<addr>/mcp`）。同一個 process 裡還有
hook 輪詢迴圈（見下方），**不是額外要開的服務**。

---

## MCP 介面（給 AI harness 接）

標準 MCP（JSON-RPC 2.0）：`initialize` → `tools/list` → `tools/call`。`initialize` 帶
`protocolVersion` 就原樣 echo 回去，沒帶回內建預設 `2024-11-05`。

### 兩種傳輸，同一份邏輯

| 傳輸 | 啟動方式 | 何時用 |
|---|---|---|
| **stdio** | `pb mcp --db <絕對路徑>` | 每個 harness 各自起一份程序，免常駐，最通用 |
| **Streamable HTTP** | `pb serve --addr ...`，連 `http://<addr>/mcp` | 單一 server、多個 AI／harness 共用一份資料 |

**stdio 一律用絕對路徑**——harness 的工作目錄不固定，相對路徑會找不到 DB。

### 設定範例

Claude Code（`.mcp.json` 或 `claude mcp add`）：

```json
{
  "mcpServers": {
    "project_board": {
      "command": "pb",
      "args": ["mcp", "--db", "/絕對路徑/project_board/var/board.db"]
    }
  }
}
```

已經有 `pb serve` 常駐時，改連 HTTP 更省資源（多個 client 共用）：

```json
{
  "mcpServers": {
    "project_board": { "type": "http", "url": "http://127.0.0.1:8787/mcp" }
  }
}
```

Cursor（`.cursor/mcp.json`）、opencode（`opencode.jsonc` 的 `mcp` 欄位）格式類似，完整範例見
`docs/INTEGRATION.md`。

### 20 個工具（`pb_*`）

寫入類工具一律要求 `actor`（寫入者名冊：`xiaoxia`、`kaimake`、`kaimadi`、`yilong`、`claude`、
`human`、`unassigned`——名冊寫死在程式裡，不在這份名單裡的名字會被拒絕）。

| 工具 | 做什麼 |
|---|---|
| `pb_tree` | 看樹：節點精簡陣列（id/type/title/status/owner/children_count）。唯讀。 |
| `pb_get` | 取單一節點全文＋links＋children。唯讀。 |
| `pb_create` | 開單；id 省略會自動生成，撞名直接回錯。 |
| `pb_update` | 部分更新（title/body/owner/priority/tags/sort）；可選樂觀鎖。 |
| `pb_transition` | 改狀態（不是驗收）；只能走狀態機允許的邊，非法轉移回錯不會靜默改掉。 |
| `pb_assign` | 派單（改 owner）。 |
| `pb_link` / `pb_unlink` | 掛／拆關聯（depends_on、repo、commit、pr…）。 |
| `pb_verify` | **正規收單**：`review`→`done`，note 當證據寫進 history。actor＝owner 時自動標 `[self-verified]`（不擋，但會被稽核出來）。改狀態別用 `pb_transition` 的 `to=done` 代替這支。 |
| `pb_comment` | 留言（只寫 history，不動節點狀態）。 |
| `pb_search` | 全文搜尋（FTS5 trigram，中文子字串可查）。 |
| `pb_deps` | 列依賴（`depends_on` 關聯）。唯讀。 |
| `pb_history` | 取節點事件（新→舊）。唯讀。 |
| `pb_stats` | 統計：各狀態計數／每人手上未結案張數／每 REQ 完成度／自我驗收張數。唯讀。 |
| `pb_delete` | 刪節點（僅 report 或無子節點、無 link 的節點可刪）。 |
| `pb_hook` / `pb_unhook` / `pb_hooks` | 訂閱／取消訂閱／列出「節點狀態變動時叫醒哪個 harness」（見下節）。 |
| `pb_commit_attach` | 從 commit 訊息把 sha 自動掛到訊息裡提到的單號（`link kind=commit`）。 |
| `pb_set_repo` | 設定專案對應的 repo（`link kind=repo`）。 |

完整參數與允許的狀態轉移邊，見 `docs/INTERFACE.md` 或直接 `tools/list`（每個工具的 `description`
就是權威文件，跟這裡的一句話摘要一致）。

---

## Git 整合：commit 帶單號自動掛連結

裝一次（每個要接的 repo 都要裝）：

```bash
scripts/install-git-hooks.sh /path/to/some/repo   # 省略路徑＝目前目錄
```

這會把該 repo 的 `core.hooksPath` 指到 `scripts/git-hooks`，並記住 `pb.bin`／`pb.db`（可用
`git config pb.actor <名冊名>` 指定執行者，預設 `human`）。裝好之後：

- **`commit-msg` hook**：commit 訊息沒帶單號（格式 `Y<8碼>/…`，例：`Y20260920/ISSUE-XXX`）就提醒
  （不擋 commit）；`PB_SKIP_TICKET_CHECK=1` 可跳過提醒。
- **`post-commit` hook**：訊息裡有單號就自動呼叫 `pb commit attach`，把這次 commit sha 掛到
  那張單的 `links`（`kind=commit`）——**只掛連結，不會自動改狀態**，狀態還是要另外用
  `pb_transition`／`pb_verify` 動。掛不上（沒單號／單不存在／`pb` 不在 PATH）一律不擋 commit。

一個 project 節點可以用 `pb_set_repo` 掛上它對應的 git repo URL，dashboard 詳情欄跟母表都會把
commit sha 顯示成可點連結（回連 GitHub）。

---

## Hook 喚醒：ISSUE 狀態變動時真的能叫醒 AI harness

這**不是**文件寫假的、也**不是**只有 `pb_hook` 訂閱就結束——完整流程是：

1. 任何 agent 呼叫 `pb_hook(node_id, harness="herdr", target=<herdr 裡的 agent 名>, actor=<你自己>)`
   訂閱一個節點（通常訂自己剛開的那張單）。這只是寫進 SQLite 的一筆訂閱紀錄，**訂閱本身不會
   叫醒任何人**。
2. **真正的喚醒只發生在長駐的 `pb serve` 裡**：它有一個背景迴圈（`cmd/pb/hookloop.go`），
   **每 2 秒**輪詢一次新的 history（狀態轉移／verify 事件），對照訂閱表算出「誰該被叫醒」。
3. 算出來要叫醒誰之後，會**真的執行** `herdr agent prompt <target> <訊息>`（子行程，5 秒逾時，
   herdr 不在或失敗只記 log，不會讓 `pb serve` 掛掉，也不會擋任何寫入）。訊息內容是組好的人話，
   例如「`[ProjectBoard] Y20260920/ISSUE-XXX 進 review（xiaoxia）。請看單驗收。`」。
4. 改狀態的那個行程（呼叫 `pb_transition`／`pb_verify` 的人）**本身不會去叫 herdr**——喚醒的
   責任完全在 `pb serve` 這個常駐行程裡，設計上就是要避免「誰改的誰負責通知」這種容易漏掉的
   耦合。

**這代表**：只要 `pb serve` 有在跑，訂閱就會真的生效，不需要任何人再手動觸發。想確認能不能
動，最快的驗證方式：`pb_hook` 訂閱一張測試單，找另一個人（或自己）把它轉 `review`，等最多 2
秒，看 herdr 那邊的 target agent 有沒有收到訊息；`var/serve.log` 也會留下
「hook 已喚醒 …」或「hook 喚醒失敗…」的紀錄可以查。

團隊目前的 SOP（見 `rule.md`）是：ISSUE 轉 `review` 後，`pb_hook` 訂閱**加上**主動用 herdr 傳一
則訊息通知 CTO——`pb_hook` 訂閱只保證「以後這張單再變動會被動收到」，不等於「這次轉 review 這
件事本身已經通知到了」，兩件事一起做才算收工。

---

## Dashboard（唯讀，給人看）

`http://<addr>/`，暗色主題自動跟隨系統（也可手動切換，記在瀏覽器 localStorage）。目前功能：

- **狀態／型別統計列**（合成一行，在頁面最上方）：每個 chip 可點篩選，數字永遠等於點下去樹會
  出現的筆數（跟其他生效中的篩選連動算，不會出現數字跟結果對不上的情形）；型別列預設排除
  `done` 的項目（只顯示還沒結案的），有一顆「已完成的BUG」捷徑可以直接看已結案的缺陷。
- **樹**：可依專案／負責人篩選；點狀態／型別 chip 篩選後自動展開到符合條件的節點，不用手動
  一層層點開；選定專案的根節點預設就是展開的。
- **詳情欄**：進專案或切換專案下拉，右側預設就顯示該專案本身的說明，不會空著。
- **記得上次的畫面**：關掉分頁／重開瀏覽器再進根網址，會自動還原成上次在看的專案或單
  （存在瀏覽器的 `localStorage`，不是伺服器端記憶）。
- **四個分頁**（焦點／進度／母表／週報，位置緊貼在樹狀圖下方，點開的內容就在按鈕正下方）：
  - **焦點**：卡點清單、依賴關係、待裁示事項、最近異動、每人手上未結案張數、**自我驗收警示**
    （actor＝owner 的 verify，數字跟明細清單是同一條查詢算出來的，不會有「標題寫幾張、底下卻
    列不出東西」的情形）。
  - **進度**：每個 REQ 的完成度百分比、每人手上未結案張數、平均滯留天數。
  - **母表**：checklist 項目與它們串到哪張單。
  - **週報**：本週做了什麼／進行中／下週計畫，依專案分節。

---

## 目錄結構

```
project_board/
├── README.md              ← 這份
├── LICENSE                ← MIT
├── rule.md                ← 開發規範（動手前先讀，含 herdr 通報 SOP）
├── Makefile                ← build/linux/windows/test/vet/fmt/clean
├── install.sh／install.ps1 ← 一鍵安裝
├── service.sh              ← 背景常駐 start/stop/status/restart
├── docs/                   ← 規格文件（REQ／DATA_MODEL／INTERFACE／INTEGRATION／OPERATIONS／
│                              GIT_INTEGRATION／ROADMAP）
├── scripts/
│   ├── install-git-hooks.sh
│   └── git-hooks/          ← commit-msg／post-commit
├── skill/                  ← skill_invoke 介面（SKILL.md ＋ skill.py shim，給 cray/pi harness）
├── cmd/pb/                 ← CLI 入口（含 hook 輪詢迴圈 hookloop.go／serve.go）
├── internal/
│   ├── store/               ← SQLite 存取（schema／CRUD／history／migrations）
│   ├── domain/               ← 節點、狀態機、type 登錄表、狀態／分頁顯示 metadata
│   ├── mcp/                  ← MCP server（tools）
│   ├── httpapi/               ← REST（唯讀）
│   ├── web/                   ← dashboard 單頁（dashboard.html）
│   └── importer/               ← 既有 markdown → SQLite
├── var/board.db             ← SQLite，真相源（gitignore）
└── bin/                      ← 建置產物（gitignore）
```

---

## 文件索引

| 檔 | 內容 |
|---|---|
| `rule.md` | **開發規範，動手前先讀**（含收工通報 SOP） |
| `docs/REQ.md` | 需求規格：背景、範圍、功能／非功能需求 |
| `docs/DATA_MODEL.md` | 資料模型：資料表、狀態機、ID 慣例、寫入者名冊 |
| `docs/INTERFACE.md` | 介面規格：MCP tools 完整參數、REST 端點、dashboard 版面 |
| `docs/INTEGRATION.md` | 外部 harness 整合：claude code／cursor／opencode 的 MCP 接法細節 |
| `docs/GIT_INTEGRATION.md` | git hook／commit 掛單號的完整設計 |
| `docs/OPERATIONS.md` | 跑法、多 agent 並發、備份、既有單匯入策略 |
| `docs/ROADMAP.md` | 里程碑與驗收標準 |

---

## 鐵則

1. **SQLite 是唯一真相**；markdown 只進（import）／出（export），不覆蓋原件。
2. **所有寫入都記 `history`**（誰／何時／從什麼到什麼／證據），不採信口頭宣稱。
3. **狀態流轉只走狀態機允許的邊**，非法轉移回錯誤，不硬改；收單用 `pb_verify` 不要用
   `pb_transition` 繞過去。
4. **不引外部服務**：無 DB server、無 Redis、無 Node build，單一 Go binary。
5. **commit 訊息帶單號**，讓 git 整合自動幫你掛連結；改完程式碼先 `make test` 再 commit。
