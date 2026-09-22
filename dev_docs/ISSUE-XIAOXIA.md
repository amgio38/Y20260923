# ISSUE-XIAOXIA.md — 小蝦（xiaoxia）任務包

> 負責目錄：`internal/store/`、`cmd/pb/`、`go.mod`（你是唯一動 `go.mod` 的人）。
> 依賴：`../dev_docs/API_CONTRACT.md`（跟開碼客的 domain 契約）、`../docs/DATA_MODEL.md`（含 §11）、`../docs/INTERFACE.md`。
> 回報：完成一項就照 `DISPATCH_PLAN.md` §3 協議用 herdr 回報克勞德，**不要自己 commit／push**。
> 測試：每個檔案寫完當下補測試，動到的 package 覆蓋率 ≥90%，回報附實測數字——不是最後才補。

---

## PB00：Bootstrap（獨作，優先，卡住其他兩人，動作要快）

**做**：
1. `cd /usr/account/project_board`
2. `go mod init project_board`；`go` 版本對齊環境 go1.25.3。
3. 建立空目錄骨架＋每個套件一個只有 `package xxx` 宣告的佔位檔（讓 `go build ./...` 一開始就能過）：
   `internal/domain/domain.go`、`internal/store/store.go`、`internal/mcp/mcp.go`、`internal/httpapi/httpapi.go`、`internal/web/web.go`、`cmd/pb/main.go`（`main.go` 可以先印一行 `TODO` 讓它能跑）。
4. `go build ./...` 過。
5. **不要**先加 `modernc.org/sqlite` 依賴——那是你 PB01 真正要用到時再 `go get`，bootstrap 階段愈乾淨愈好，減少之後衝突面。

**DoD**：`go build ./...` 過，六個空套件檔都在，目錄結構跟 `API_CONTRACT.md` §0 一致。

**完工後**：立刻用 herdr 回報克勞德「bootstrap done」，**等克勞德回「go」** 才進 PB01；同時開碼客／開碼弟也是等這個訊號才開始寫他們自己目錄下的 Go 檔。

---

## PB01：核心儲存層 `internal/store`（等 PB00 放行後開始）

**做**（權威依 `API_CONTRACT.md` §2、`../docs/DATA_MODEL.md`）：

1. `go get modernc.org/sqlite`（純 Go driver，不准 cgo）。
2. schema：`nodes`／`links`／`history`／`meta` 四張表，照 `DATA_MODEL.md` §2~§5 的 DDL 原樣建（不要自己改欄位）。
3. **Migration 機制**（`DATA_MODEL.md` §11.7）：`internal/store/migrations/0001_init.sql` 起手，`Migrate(ctx)` 依 `meta.schema_version` 依序套用，每筆一個 transaction。這不是可以先跳過、之後再補的東西——先把機制搭好，即使目前只有一個 migration 檔。
4. 依 `API_CONTRACT.md` §2 簽名實作 `Store` 的所有方法（`New`／`Migrate`／`Seed`／`Tree`／`Get`／`Create`／`Update`／`Transition`／`Assign`／`Link`／`Unlink`／`Verify`／`Comment`／`Search`／`History`／`Stats`／`Delete`）。
5. **每個寫入函式**：`actor` 為空或不在名冊 → 立刻拒絕（用 `API_CONTRACT.md` 定的 sentinel error）；每個寫入包單一 transaction，`nodes` 與 `history` 同 transaction 寫（`DATA_MODEL.md` §10）。
6. **一定要做的細節**（不是選配，是本次 CR 重點）：
   - `Create`：`ID` 空字串時呼叫 `domain.GenerateID`；撞名回 `ErrIDExists`，不自動加尾碼。
   - `Update`／`Transition`：帶 `expectedUpdatedAt` 時做樂觀鎖比對，不符回 `ErrConflict`，**不寫入、不留 history**（§11.6）。
   - `Transition` 轉 `blocked`：呼叫 `domain.RequiresBlockReason`，`note` 空且無既有 `depends_on` link → 回 `domain.ErrMissingBlockReason`（§11.3）。
   - `Link(kind=depends_on)`：驗證 `target` 節點存在，不存在回 `ErrDependsOnTargetMissing`（§11.4）；其餘 kind 不驗證。
   - `Verify`：只能對 `status=review` 的節點做，成功後轉 `done`；`actor==owner` 時 `note` 前面加 `[self-verified] `（§11.2）。
   - `Stats`：`SelfVerifiedCount` 算近期 `done` 節點中 `verify` history 帶 `[self-verified]` 前綴的數量。
7. `Search`：先用 `LIKE` 全文比對 `title`／`body`／`tags` 即可（FTS5 是 v0.2 的事，別在這裡超做）。
8. WAL、`busy_timeout=5000`、`foreign_keys=ON` 開啟參數寫在 `New()`。

**DoD**：
- `go build`／`go vet` 過，`gofmt -l internal/store` 乾淨。
- 單元測試涵蓋：schema 建立、CRUD、狀態機非法轉移被拒、`blocked` 缺理由被拒、`depends_on` 打錯目標被拒、ID 撞名被拒、`expected_updated_at` 衝突被拒、self-verified 標記正確、每次寫入都留 history。
- `internal/store` 覆蓋率 ≥90%，回報附 `go test ./internal/store/... -cover` 實測輸出。

---

## PB03：CLI `cmd/pb`（等克勞德放行 phase 2，且 PB02 domain 已回報完工）

**做**（權威依 `../docs/INTERFACE.md` §1，已補 `--actor`／`--if-unmodified-since`）：

1. 子命令：`init`／`seed`／`serve`／`mcp`／`get`／`tree`／`create`／`update`／`move`／`assign`／`link`／`verify`／`comment`／`search`／`history`／`stats`（`import`／`export` 是 v0.2，先不做）。
2. `serve`：起 REST（呼叫開碼弟的 `internal/httpapi`）＋ dashboard（`internal/web`）＋ MCP HTTP（呼叫開碼客的 `internal/mcp`）——這裡是你唯一要 import 另外兩人套件的地方，介面已在 `API_CONTRACT.md`／`INTERFACE.md` 凍結，照著 import 就好。
3. `mcp`：呼叫開碼客的 `internal/mcp`，走 stdio。
4. **`--actor`**：所有寫入子命令必填，未帶時退回 env `PB_ACTOR`，兩者皆無 → 印清楚錯誤訊息並以非 0 退出（`INTERFACE.md` 已註明）。
5. **`--if-unmodified-since`**：`update`／`move` 可選，帶了就傳給 `store.Update`/`store.Transition` 的 `expectedUpdatedAt`。
6. 錯誤要能分辨種類：用 `errors.Is` 判斷 `store` 的 sentinel error，印對應人類看得懂的訊息（例如 `ErrConflict` → `錯誤：節點已被異動，請重新讀取後再試`），不要把 Go error 原始字串直接丟出來。
7. 常駐服務（`serve`）**啟動測試時走 `process` 工具**，禁裸 `&`／`nohup`（`rule.md` 已明定）。

**DoD**：
- 每個子命令都要能實跑一次成功案例＋至少一個錯誤案例（例如 `move todo→done` 直接被拒）。
- `cmd/pb` 覆蓋率 ≥90%（CLI 邏輯用 table test；`serve`／`mcp` 常駐部分至少測到「能正確組裝呼叫」，不必真的跑滿整個 server 生命週期）。
- 回報附：實際起一次 `pb init && pb seed && pb tree` 的終端輸出。

---

## 全程注意

- 你的 `internal/store` 一旦 API 簽名穩定，是**開碼客（MCP）跟開碼弟（httpapi）共同的地基**，簽名要改務必照 `API_CONTRACT.md` §4 流程先講。
- 遇到 `internal/domain` 還沒完工但你需要某個函式：先用 `API_CONTRACT.md` 裡凍結的簽名寫（Go 允許對方還沒實作完成、只要簽名對得上就能編譯過），不要因此卡住不動。
