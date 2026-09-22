# ISSUE-KAIMADI.md — 開碼弟（kaimadi）任務包

> 負責目錄：`internal/web/`、`internal/httpapi/`、`skill/`。
> 依賴：`../docs/INTERFACE.md` §3～§4（REST／dashboard，含已補的視覺規格）、`../docs/DATA_MODEL.md`（§11）。
> 回報：完成一項就照 `DISPATCH_PLAN.md` §3 協議用 herdr 回報克勞德，**不要自己 commit／push**。
> 測試：每個檔案寫完當下補測試，動到的 package 覆蓋率 ≥90%，回報附實測數字。

---

## PB05-A：靜態 dashboard（phase 1，**現在就可以開始，不用等任何人**）

REST JSON 的形狀已經在 `INTERFACE.md` §3 定案凍結，你可以先對著**假資料（fixture JSON）**把整個前端做完，之後 phase 2 只是把 fixture 換成真 API，不用重寫。

**做**：

1. `internal/web/`：一個 Go `net/http` 薄殼 ＋ 一支單頁 HTML（內嵌 vanilla JS，**無框架、無 build step**——`rule.md` 技術底線）。
2. 先寫 2~3 份 fixture JSON（照 `INTERFACE.md` §3 的 `/api/tree`、`/api/node/{id}`、`/api/stats` 回應格式手刻幾筆假資料），開一支暫時的本機檔案或內嵌常數服務這些 fixture，讓你能獨立把整頁串起來。
3. **視覺風格是硬性規格，不是自由發揮**（學長 2026-09-20 明確指示，已寫進 `INTERFACE.md` §4）：**Google 後台風格**——系統字體堆疊、灰階配色（背景 `#fff`/`#f8f9fa`、邊框 `#dadce0`、主文字 `#202124`、次要文字 `#5f6368`）、卡片式區塊、細邊框、扁平元件、低飽和狀態色點。**看起來要像內部工具，不要像行銷頁面。** 照 `INTERFACE.md` §4 的版面規格（頂列統計／左樹右詳情／焦點面板）刻。
4. 焦點面板**五項**都要做：🚧 卡點、❓ 待裁示、🕒 最近異動、👤 每人手上張數、**⚠ 自我驗收**（`DATA_MODEL.md` §11.2，這是新加的第五項，別漏）。
5. 樹可折疊、依狀態／owner 前端過濾；狀態用色點對映 `DATA_MODEL.md` §6 的七種狀態。
6. 更新方式：整頁刷新即可，不做 websocket。

**DoD**：
- 開 `127.0.0.1:8787`（用你自己的暫時埠或直接在 `internal/web` 裡跑一個獨立小 server 測）能看到樹／詳情／統計／焦點面板五項，資料先是 fixture 也算過。
- 視覺過關標準：灰階、卡片、無花俏動畫、無外部字型/框架依賴——截圖或描述讓克勞德 CR 時能判斷符合「Google 後台風格」。
- `internal/web` 覆蓋率 ≥90%（Go 部分的路由／render 邏輯；純前端 JS 不強制覆蓋率但要能手動操作過一輪）。

---

## PB05-B：REST 真實端點 `internal/httpapi`（phase 2，等克勞德放行，且小蝦 PB01 已回報完工）

**做**（權威依 `INTERFACE.md` §3）：

1. `GET /healthz`、`/api/tree`、`/api/node/{id}`、`/api/node/{id}/history`、`/api/stats`、`/api/search`——全部**唯讀**，直接呼叫小蝦 `internal/store` 的方法，**不准自己寫 SQL**。
2. 把 PB05-A 的 fixture 換成真的 `internal/httpapi` 呼叫，串起 `internal/web` 的頁面。
3. 錯誤處理：`store.ErrNotFound` → 404，其餘內部錯誤 → 500 帶簡短訊息，不洩漏內部堆疊。

**DoD**：
- 起真的 server，六個 REST 端點都實測打過一次，回應格式跟 `INTERFACE.md` §3 的範例一致。
- dashboard 串上真資料後，樹／詳情／統計跟 DB 內容一致（拿 `pb tree` CLI 輸出或直接查 DB 對一次帳）。
- `internal/httpapi` 覆蓋率 ≥90%。

---

## PB06：skill 層 `skill/`（phase 3，等小蝦 PB03 CLI 回報完工才有 `pb` binary 可用）

**做**（權威依 `skill/SKILL.md`——已經有初稿，含 `--actor` 更新，你要補的是 `skill.py` 實作＋讓 SKILL.md 跟實作對得上）：

1. `skill/skill.py`：thin shim，執行策略照 `INTERFACE.md` §5：
   - 找 `pb` binary（`$PB_BIN` → 專案 `./bin/pb` → `PATH`）→ `exec pb <cmd> …`。
   - 找不到 binary → 退回打 REST（純 stdlib `urllib`，**不引第三方套件**）。
   - 兩者都沒有 → 印清楚錯誤＋提示 `go build -o bin/pb ./cmd/pb`。
2. 子指令對應 `SKILL.md` 表格（`tree`／`get`／`create`／`update`／`move`／`assign`／`link`／`verify`／`comment`／`search`／`history`／`stats`／`serve`／`help`），**都要帶 `--actor`**（skill 執行環境建議固定 `PB_ACTOR=xiaoxia` 當後備，見 `INTERFACE.md` §5 註）。
3. 安裝驗證：`cp -r skill /root/.cray/skills/dynamic/project_board` 後，`skill_invoke("project_board", args="tree")` 要能真的跑通。

**DoD**：
- `skill.py` 對 binary 直連跟 REST 退回兩條路徑都要實測過（可以先把 binary 改名模擬「找不到」的情境測退回邏輯）。
- 安裝到 `/root/.cray/skills/dynamic/project_board` 後，實際 `skill_invoke` 跑一輪 `tree`／`create`／`move`／`verify` 全通。
- Python 部分沒有 Go 覆蓋率要求，但要附實測操作紀錄（不是宣稱「應該可以」）。

---

## 全程注意

- PB05-A 的 fixture 資料格式要跟 `INTERFACE.md` §3 的真實 API 回應**完全一致**（欄位名、巢狀結構），不然 phase 2 換真資料時會要重寫前端 JS。
- 視覺風格（Google 後台簡約灰階）是學長直接下的指示，不是可以自由發揮的部分，有疑問先問克勞德不要自己決定換風格。
