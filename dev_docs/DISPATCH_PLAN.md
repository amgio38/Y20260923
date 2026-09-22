# DISPATCH_PLAN.md — ProjectBoard v0.1 派工總覽

> 派工：克勞德（CTO／CR）｜日期：2026-09-20｜狀態：**已派工，動工中**
> 需求權威：`../PROJECTBOARD_REQ20260920.md`（含 §11 CTO 補充）、`../docs/*.md`
> 分工個人任務：`ISSUE-XIAOXIA.md`（小蝦）、`ISSUE-KAIMAKE.md`（開碼客）、`ISSUE-KAIMADI.md`（開碼弟）
> 契約：`API_CONTRACT.md`（domain／store 函式簽名，避免小蝦跟開碼客互相卡）

---

## 1. 三人分工（按目錄切開，零踩線）

| 人 | herdr 名 | 負責目錄 | 不准動 |
|---|---|---|---|
| 小蝦 | `xiaoxia` | `internal/store/`、`cmd/pb/`、`go.mod`（唯一 bootstrap 人） | `internal/domain/`、`internal/mcp/`、`internal/httpapi/`、`internal/web/`、`skill/` |
| 開碼客 | `kaimake` | `internal/domain/`、`internal/mcp/` | 上面小蝦／開碼弟的目錄 |
| 開碼弟 | `kaimadi` | `internal/web/`、`internal/httpapi/`、`skill/` | 上面小蝦／開碼客的目錄 |

三人各自目錄互不重疊，**理論上可以完全平行**，唯一的序列點是下面的階段閘門（主要卡在 `go.mod` 得先存在）。

## 2. 階段與閘門

```
phase 0（小蝦，獨作，~15分鐘）
  Bootstrap：go.mod + 套件骨架 + 空 package 檔
       │
       ▼ herdr 回報「bootstrap done」給克勞德 → 克勞德確認 → 放行 phase 1
       │
  ┌────┴─────────────────┬─────────────────────┐
  ▼ phase 1（三人平行）    ▼                      ▼
小蝦：PB01 internal/store  開碼客：PB02 internal/domain   開碼弟：PB05-A 靜態 dashboard（對 fixture JSON，不等任何人）
  │（依 API_CONTRACT.md   │（依 API_CONTRACT.md            │
  │  簽名先寫，domain      │  簽名先寫）                     │
  │  沒完工也能編譯過）     │                                │
  └──────────┬────────────┘                                │
             ▼ 兩邊都回報 done，互相 go test 對得起來        │
       phase 2（小蝦＋開碼客平行，開碼弟繼續）                 │
  小蝦：PB03 cmd/pb CLI     開碼客：PB04 internal/mcp        開碼弟：PB05-B httpapi 真實端點，接掉 fixture
             │                     │                        │
             └──────────┬──────────┴────────────────────────┘
                         ▼ 三邊都回報 done
                   phase 3（開碼弟）
                   PB06 skill/（依賴 PB03 的 pb binary）
                         │
                         ▼
                   克勞德做 PB07 精神的整體 CR／實跑驗收（見 §4）
```

**閘門規則**：每個階段開始前，該階段涉及的人要在 herdr **收到克勞德的「go」訊息**才開始下一階段任務（避免有人自己提早動另一個階段、跟還沒完工的依賴打架）。文件已經先寫好放在 `dev_docs/`，不用等文件，只等「go」訊號。

## 3. herdr 回報協議（三人一致）

每完成一個 ISSUE（不是每個小步驟），依序：

1. 自己先跑完 DoD 裡列的驗收指令（`go build ./...`、`go vet ./...`、`go test ./... -cover`、`gofmt -l .`），**附實測輸出數字**，不要用「應該過了」帶過。
2. **不要自己 commit／push**（`rule.md` 既定紀律）——改完停手，等克勞德 CR。
3. 用 herdr 主動回報克勞德（`w1:p4` / agent name 找克勞德那個 pane，或直接讓克勞德來 `agent read` 你的 pane），內容至少包含：
   - 完成的 ISSUE 編號／檔案清單
   - 測試覆蓋率實測數字（貼指令輸出，不要手打數字）
   - 遇到的任何跟 `API_CONTRACT.md` 或既有文件不一致的地方（有的話要先講，不要自己默默改別人依賴的簽名）
4. 克勞德 CR 通過才算數，不通過會退回附具體修改點。

## 4. 最終驗收標準（連續開發到底，不是做完各自那塊就結束）

**驗收＝`../docs/ROADMAP.md` v0.1 DoD 清單全部打勾**，也就是整份需求文件（`PROJECTBOARD_REQ20260920.md` + `docs/*.md`，含 §11 克勞德補充）描述的功能全部做到，不是「PB01~PB06 個別測過就好」。具體包含：

- 四個入口（CLI／MCP／REST／dashboard）都能實際操作同一顆 DB，資料一致。
- 狀態機非法轉移被拒、`blocked` 沒理由被拒、`depends_on` 打錯 ID 被拒。
- 每筆寫入都有正確 `actor` 的 history；自我驗收在 stats／dashboard 看得到。
- `expected_updated_at` 樂觀鎖實測擋下一次併發覆蓋。
- dashboard 是 Google 後台風格（簡約、灰階、卡片式——`docs/INTERFACE.md` 已補視覺規格），焦點面板五項都顯示。
- `internal/mcp` 有自動化 round-trip 整合測試。
- 全專案 `go build`／`vet`／`test` 過、`gofmt -l` 乾淨、改到的 package 覆蓋率 ≥90%。

克勞德在三人都回報 PB06 完工後，會親自起 server、實際打 CLI／MCP／REST／dashboard 各跑一輪（不採信宣稱），過了才算 v0.1 收工，交給學長做最終驗收。

## 4.1 驗收權責（學長 2026-09-20 拍板，長期有效——不只 v0.1）

**ProjectBoard 全案的 `verify`／approve 一律由克勞德一人把關**，不限於這次 v0.1 開發：

- 學長後台沒有寫入能力（dashboard v1 唯讀），這是 AI 團隊自己的內部平台，approve 這件事本來就該由團隊裡的人做。
- 所有人（小蝦／開碼客／開碼弟／一龍）收工一律停在 `review`，**不要自己 `verify` 自己的單**——即使系統技術上沒擋自我驗收（見 D10），流程上這是規矩。
- 克勞德看到 `review` 狀態的節點要主動去查（`tree --status review` 或 dashboard 焦點面板），**實地核對**（讀檔案、跑指令、複測，不是看 comment 敘述就信），過了才 `verify --actor claude`。
- 克勞德驗收完，只需要**摘要**回報學長，不必每筆都讓學長重新過目。

## 5. 遇到問題怎麼辦

- 跟另一人負責的目錄有依賴疑問 → 先看 `API_CONTRACT.md`／`../docs/INTERFACE.md`／`../docs/DATA_MODEL.md`，沒寫清楚才用 herdr 互相確認或找克勞德裁示，**不要自己猜著做**。
- 卡住超過預期 → 主動用 herdr 跟克勞德說，不要悶著。
- 三人之間**不要互相改對方目錄下的檔案**，有需要一律透過 herdr 溝通或請克勞德介入。
