# ISSUE-KAIMAKE.md — 開碼客（kaimake）任務包

> 負責目錄：`internal/domain/`、`internal/mcp/`。
> 依賴：`../dev_docs/API_CONTRACT.md`（跟小蝦的 store 契約）、`../docs/DATA_MODEL.md`（含 §11）、`../docs/INTERFACE.md` §2。
> 回報：完成一項就照 `DISPATCH_PLAN.md` §3 協議用 herdr 回報克勞德，**不要自己 commit／push**。
> 測試：每個檔案寫完當下補測試，動到的 package 覆蓋率 ≥90%，回報附實測數字。

---

## 起手：等小蝦 herdr 回報「bootstrap done」＋克勞德說「go」才開始寫 Go 檔

Bootstrap 完成前 `go.mod` 還沒建好，`go build` 會整個失敗；文件可以先讀，但先別動手寫 `internal/domain/` 底下的檔案。

---

## PB02：領域層 `internal/domain`（phase 1，不依賴任何人，最先能開工）

**做**（權威依 `API_CONTRACT.md` §1、`../docs/DATA_MODEL.md` §6～§8、§11）：

1. 型別與常數：`NodeType`／`Status`／`Priority`／`LinkKind`／`HistoryAction`、`Owners` 名冊——**照 `API_CONTRACT.md` §1 給的簽名原樣定義，這是已經跟小蝦講好的契約，不要自己改名字或改型別**。
2. `IsValidOwner`：純粹查表。
3. `IsValidTransition`：把 `DATA_MODEL.md` §6 那張允許轉移表原樣寫成 map 或 switch，**逐一對照表格內容**，不要憑印象寫，這是全系統最重要的一段邏輯（狀態機錯了，整個「不採信口頭宣稱」的設計就破功）。
4. `RequiresBlockReason(to)`：目前只有 `to==StatusBlocked` 回 `true`。
5. `ValidateID`：照 `DATA_MODEL.md` §7 的格式規則（大寫、`A-Z0-9-`、對應 type 前綴、掛在 parent 下）。
6. `SlugifyTitle` ＋ `GenerateID`：照 `DATA_MODEL.md` §11.5 的演算法（轉大寫、非法字元轉 `-`、收斂連續 `-`、去頭尾 `-`），`GenerateID` 要能組出完整 ID（含 parent 前綴、`REQ-`／`ISSUE-`／`REPORT-<owner>-<日期>` 前綴）。
7. `IsSelfVerified(actor, owner)`：`actor==owner` 就是 true，就這麼簡單，不要過度設計。
8. `Node`／`Link`／`HistoryEntry` struct：照 `API_CONTRACT.md` §1 欄位，這些是小蝦 `internal/store` 直接用的型別，欄位名/型別不能跟契約不一致。
9. sentinel errors（`ErrInvalidOwner`／`ErrIllegalTransition`／`ErrMissingBlockReason`／`ErrInvalidID`）。

**DoD**：
- `go build`／`go vet` 過，`gofmt -l internal/domain` 乾淨。
- 單元測試：**狀態機允許轉移表要每一條 from→to 組合都測到**（合法的通過、不合法的都要驗證回 `ErrIllegalTransition`），這張表窮舉起來不到 50 組，直接寫 table test 全跑。
- `ValidateID`／`SlugifyTitle`／`GenerateID`／`IsValidOwner`／`IsSelfVerified` 各自的正常與邊界案例（空字串、非法字元、超長標題等）。
- 覆蓋率 ≥90%，回報附 `go test ./internal/domain/... -cover` 實測輸出。

---

## PB04：MCP server `internal/mcp`（phase 2，等克勞德放行，且小蝦 PB01 已回報完工）

**做**（權威依 `../docs/INTERFACE.md` §2、`../docs/INTEGRATION.md`、`../docs/DATA_MODEL.md` §11.8）：

1. JSON-RPC 2.0 薄實作：`initialize`／`tools/list`／`tools/call`，唯讀 `resources`（`board://tree`、`board://node/{id}`、`board://stats`）。
2. 14 個 `pb_*` tools，參數表**完整依 `INTERFACE.md` §2**（含已補的 `actor` 必要參數、`expected_updated_at` 可選參數）——直接呼叫小蝦 `internal/store` 的 `Store` 方法，**不准自己寫 SQL 或自己判斷狀態機**（架構鐵律，`INTERFACE.md` §0 講得很清楚）。
3. 錯誤處理：`store` 回什麼 sentinel error，就轉成對應的 MCP `isError:true` + 清楚訊息，不靜默修正、不吞錯誤。
4. 兩種傳輸：`stdio`（`pb mcp --db <path>`，小蝦的 CLI 會呼叫你這裡）、Streamable HTTP（掛在小蝦 `pb serve` 底下）。
5. **自動化 round-trip 整合測試**（`DATA_MODEL.md` §11.8，這是這次 CR 重點，不是可有可無）：起暫存 DB，跑完整流程 `initialize`→`tools/list`（驗證 14 個 `pb_*` 都在）→`pb_create`→`pb_transition`→`pb_verify`→`pb_delete`，寫成 `go test`，不能只交人工測過的宣稱。

**DoD**：
- `tools/list` 實測看得到 14 個 `pb_*`。
- 自動化 round-trip 測試通過（附 `go test ./internal/mcp/... -v` 輸出）。
- 覆蓋率 ≥90%，回報附實測數字。
- 錯誤情境至少測到：非法狀態轉移、缺 `actor`、ID 撞名、`depends_on` 目標不存在——每個都要回 `isError:true` 而不是靜默通過。

---

## 全程注意

- `internal/domain` 的型別是全系統共用的地基，一旦被 `internal/store`／CLI／httpapi 開始 import，**簽名要改務必先照 `API_CONTRACT.md` §4 流程講**，不要自己改了就沒事。
- `internal/mcp` 完工要等 `internal/store` 也完工才能真正跑起來（PB04 依賴 PB01），但**簽名層面**你可以在 `internal/store` 還在寫的時候就對著 `API_CONTRACT.md` §2 先寫你的呼叫邏輯，不用整個乾等。
