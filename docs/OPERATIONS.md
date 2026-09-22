# OPERATIONS.md — ProjectBoard 運維

> 內用單機系統。本檔講：怎麼跑、怎麼掛、怎麼備份、怎麼併發、怎麼匯入既有單。

---

## 1. 建置與啟動

```bash
cd /usr/account/project_board

go build -o bin/pb ./cmd/pb        # 建 binary
./bin/pb init                      # 建 var/board.db（schema）
./bin/pb seed                      # 建 Y20260916 project 節點

./bin/pb serve --addr 127.0.0.1:8787   # 常駐：REST + dashboard + MCP(HTTP)
# 瀏覽器： http://127.0.0.1:8787

# 重開機後要給別台看 dashboard（學長 2026-09-20 明講）：
./service.sh start    # 綁 0.0.0.0:8787；stop／restart 同腳本
```

- **常駐服務一律走 `process` 工具**（`start`／`view`／`kill`），**禁裸 `&`／`nohup`**、禁接在 `&&` 鏈尾同步跑。重開機後給學長開看板是例外：用 `./service.sh`，不要自己再 nohup 一份。
- 一次性 CLI（`tree`／`get`／`move`…）跑完即退，可用一般 `bash`（建議仍 `timeout`）。
- 收工：`kill` server、確認埠釋放、清 `/tmp` 暫存。

## 2. 連線與埠

| 項 | 值 |
|---|---|
| 位址 | `127.0.0.1:8787`（`pb serve` 預設，僅本機） |
| DB | `var/board.db`（WAL 產生 `-wal`／`-shm`） |
| 對外 | **預設無**。只有 `./service.sh` 綁 `0.0.0.0:8787`（學長 2026-09-20：重開機後要開 dashboard）。同一 process 的 `/mcp` 可寫，不是只把唯讀頁面露出去 |

## 2.1 身份環境變數（`PB_ACTOR`）

- 所有寫入操作（CLI／MCP／skill）都要有 `actor`；沒帶 `--actor`／tool 參數時退回 env `PB_ACTOR`，兩者皆無即拒絕。
- 各入口啟動時建議固定設好，避免每次都要打參數：

| 入口 | 建議 `PB_ACTOR` |
|---|---|
| skill（小蝦執行環境） | `xiaoxia` |
| 開碼客 MCP 掛載環境 | `kaimake` |
| 開碼弟 MCP 掛載環境 | `kaimadi` |
| 一龍（cursor）MCP 掛載環境 | `yilong` |
| 克勞德（claude code）MCP 掛載環境 | `claude` |

- 詳見 `DATA_MODEL.md` §11.1（克勞德 2026-09-20 CTO review 補充）。

## 2.2 schema 升級（migration）

- `pb init`／`pb serve`／`pb mcp` 啟動時，比對 `meta.schema_version` 與程式內建的最新 migration 序號，缺的依序套用。
- 升版前**先備份**（§5）；migration 失敗要能安全重跑（每筆 migration 一個 transaction，非全部/半套）。
- 詳見 `DATA_MODEL.md` §11.7。

## 3. 各入口怎麼接

| 入口 | 接法 |
|---|---|
| **skill（小蝦）** | `cp -r skill /root/.cray/skills/dynamic/project_board` → `skill_invoke("project_board", …)`；`pb` 沒建時先 `go build` |
| **MCP（一龍／開碼客／開碼弟／克勞德）** | 各自 harness 掛 MCP：HTTP 指向 `http://127.0.0.1:8787`（需先 `serve`），或 stdio 跑 `pb mcp`。**實際設定依各 harness 文件**，本檔不代寫 |
| **dashboard（學長）** | 瀏覽器開 `http://127.0.0.1:8787` |
| **腳本** | `pb` CLI 或唯讀 REST |

> MCP 若採 stdio，每個 harness 會各起一份 `pb mcp` → 多 process 共用同一 DB（WAL 已容許）。

## 4. 多 agent 併發

- SQLite **WAL ＋ `busy_timeout=5000ms` ＋ `foreign_keys=ON`**。
- 每個寫入包在**單一 transaction**，且 `nodes` 變更與 `history` **同 transaction** 寫入。
- 高頻撞鎖 → 提高 `busy_timeout`；仍不足再議（本系統寫入量低，正常不會遇到）。
- **紀律**：一次只做一件事；改狀態走 `move`／`verify`，不要手改 DB。

## 5. 備份與還原

```bash
# 備份（存 /root/.cray/BAK，禁 /tmp）
cp var/board.db /root/.cray/BAK/board.db.$(date +%Y%m%d-%H%M).bak
# WAL 尚在時，連 -wal/-shm 一起帶走或用 sqlite3 .backup 更穩
```

- 還原＝把備份覆蓋回 `var/board.db`（先停 server）。
- 建議在**大規模 import 前**先備份。

## 6. 既有單匯入（v0.2）

來源：另一個內部專案（Y20260916）的 `dev_docs/` 目錄（~120 檔），一次性匯入，路徑依實際部署環境而定，這裡不寫死絕對路徑。

| 檔型 | 判定 | 建為 | 掛到 |
|---|---|---|---|
| `ISSUE-*OVERVIEW_*` | 單的總覽 | `issue`（父單） | 對應 `REQ` |
| `ISSUE-*-A/B/C_*` | 分軌單 | `issue` | 對應 `OVERVIEW` |
| `*_*_status_*.md` | 回報 | `report` | 對應 `issue` |
| `*REQ*`／`MEMBER_REQ*` | 需求 | `req` | `Y20260916` |
| `TOTAL_CHECKLIST_*` | 母表 | （v0.3 才處理；先略） | — |

- **`--dry-run` 先預覽**：列出將建的節點與歸屬，人工確認再落 DB。
- **不覆蓋**：ID 撞名只記 `warning`，不改既有節點。
- **owner 推斷**：由檔名尾綴（`xiaoxia`／`kaimake`／`kaimadi`／`yilong`）對映；推不出→`unassigned`。
- **狀態推斷**：讀檔內 `✅／🔶／🟡／⬜` 等記號對映狀態碼；推不出→`todo`。

## 7. 疑難排解

| 症狀 | 可能原因 | 處置 |
|---|---|---|
| `database is locked` | 併發寫入撞鎖 | 重試；調 `busy_timeout` |
| `pb not found`（skill） | 未建 binary | `go build -o bin/pb ./cmd/pb` |
| `connection refused`（skill 退回 REST） | server 沒起 | `serve` 起來或改用 binary |
| dashboard 樹空白 | DB 空／未 `seed` | `pb init && pb seed` |
| 埠被佔 | 前次 server 沒收乾淨 | `process` 工具 `kill`，或換 `--addr` |

## 8. 安全邊界（內用小團隊，非公開服務）

- `pb serve --addr` 直接跑預設只綁 `127.0.0.1`；`./service.sh` 常駐用途固定綁 `0.0.0.0:8787`（見
  §2、`DATA_MODEL.md`／`REQ.md` NFR-06）——這是刻意的，因為實際跑的多是宿主機／container 環境，
  綁 `127.0.0.1` 反而讓 container 外連不到。**這不是「對外公開服務」的意思**：無登入、無使用者
  權限分級、無防身份偽造（`actor` 是自報），只是內用小團隊之間可以互連，不是掛到公網給不特定
  人存取。真的要對公網開放前，這幾條都要重新設計，不是改個 `--addr` 就算數。
- DB 內可能含內部專案資訊（見 `README.md`「私人資料」相關查核紀錄，`var/`／`bin/` 都已
  `.gitignore`）→ 備份檔一併視為內部資料，不外流；分享整個專案資料夾給外部人前記得排除
  `var/`／`bin/`。
- 不寫入／不覆蓋既有 `dev_docs/*.md`（只讀）。
