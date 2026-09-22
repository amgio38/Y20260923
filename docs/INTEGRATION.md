# INTEGRATION.md — 外部 harness 整合指南

> 目標：**一份 MCP server，claude code ／ cursor ／ opencode ／ cray(pi) 都能調用**。
> 遵循 MCP 官方規格（JSON-RPC 2.0）。本檔為整合權威版，2026-09-22 對照原始碼與本機實際
> checkout 核實過一次。

---

## 0. 原則

1. **標準 MCP**：`initialize` → `tools/list` → `tools/call`；唯讀資料另開 `resources`。
2. **兩種傳輸，同一個 server 邏輯**：

| 傳輸 | 命令 | 何時用 |
|---|---|---|
| **stdio** | `pb mcp --db <絕對路徑>` | 每個 harness 各起一份，**免常駐**；最通用 |
| **Streamable HTTP** | `pb serve --addr 127.0.0.1:8787` | **單一 server、多 client 共用**；多 AI 同時用時推薦 |

3. **參數可 env**：`PB_DB`（DB 路徑）、`PB_ADDR`（HTTP 位址）、`PB_BIN`（binary 路徑）。
4. **stdio 範例一律用絕對路徑** —— harness 的 cwd 不固定，別靠相對路徑。
5. **能力等同**：MCP 有的，skill 也有（反之亦然）；工具能力只在 `INTERFACE.md` 定一次。

---

## 1. Server 能力

| 類 | 內容 |
|---|---|
| `tools` | 20 個 `pb_*`（會隨新增工具增長，見 `INTERFACE.md` §2 或直接 `tools/list`） |
| `resources`（唯讀） | `board://tree`、`board://node/{id}`、`board://stats` —— 供 harness 以「引用」方式讀（已實作，見 `internal/mcp/server.go` 的 `readResource`） |
| `prompts` | 尚未實作，仍是可選項（例如「由 REQ 生成 ISSUE 草稿清單」），沒有排進任何里程碑 |

> tool 名在各 harness 可能被加前綴（如 `mcp__project_board__pb_tree`），屬正常。

---

## 2. Claude Code

```bash
# 最快：CLI 註冊（stdio）
claude mcp add project_board -- pb mcp --db /usr/account/project_board/var/board.db
```

或專案根 `.mcp.json`：

```json
{
  "mcpServers": {
    "project_board": {
      "command": "pb",
      "args": ["mcp", "--db", "/usr/account/project_board/var/board.db"]
    }
  }
}
```

HTTP 版（需先 `serve`）：

```json
{
  "mcpServers": {
    "project_board": { "type": "http", "url": "http://127.0.0.1:8787/mcp" }
  }
}
```

## 3. Cursor

`.cursor/mcp.json`（**本 repo 目前沒有這個檔案，要接的話自己在目標 repo 建一份**，格式即
`mcpServers`，跟 Claude Code 的 `.mcp.json` 同構）：

```json
{
  "mcpServers": {
    "project_board": {
      "command": "pb",
      "args": ["mcp", "--db", "/usr/account/project_board/var/board.db"]
    }
  }
}
```

## 4. opencode

`opencode.json(c)`（**本 repo 目前沒有這個檔案，要接的話自己在目標 repo 建一份**）加 `mcp` 欄位：

```json
{
  "mcp": {
    "project_board": {
      "type": "local",
      "command": ["pb", "mcp", "--db", "/usr/account/project_board/var/board.db"],
      "enabled": true
    }
  }
}
```

remote（需先 `serve`）：

```json
{
  "mcp": {
    "project_board": { "type": "remote", "url": "http://127.0.0.1:8787/mcp", "enabled": true }
  }
}
```

> ⚠ opencode 欄位名隨版本可能微調（`type`／`command`／`enabled`）；**實作後以當版官方文件核對一次**再定稿。

## 5. cray / pi（小蝦）

- **首選 skill**：`cp -r skill /root/.cray/skills/dynamic/project_board` → `skill_invoke("project_board", …)`（見 `skill/SKILL.md`）。
- 或 MCP：若 harness 有掛載機制，照 §0 的 stdio／HTTP 接。

## 6. 通用（其他 harness）

任何支援 `mcpServers` 的 harness，照 §2 的 JSON 填即可；只支援 HTTP 的，用 `serve` ＋ `/mcp`。

---

## 7. 驗收

**自動化（一次寫好、每次改動都能重跑，優先）**：`internal/mcp/mcp_test.go` 內建整合測試，起暫存 DB
跑完整 round trip（`initialize`→`tools/list`→`pb_create`→`pb_transition`→`pb_verify`→`pb_delete`），
算進 `go test` 覆蓋率。不依賴人工記得要點一次。詳見 `DATA_MODEL.md` §11.8。

**各 harness 手動複驗（相容性補充，非唯一防線）**：

- [ ] `tools/list` 看得到全部 `pb_*`（目前 20 個，寫入類工具都要求 `actor`）。
- [ ] 呼叫 `pb_tree` 回得出 `Y20260916`。
- [ ] 呼叫 `pb_create` ＋ `pb_transition` 建一張測試單並流轉（**驗完刪除**）。
- [ ] `resources/read board://tree` 回得出樹（若 harness 支援）。

> 驗收＝**實跑**；每個 harness 各自跑一次，不採信「應該可以」。

## 8. 相容與版本

- **MCP protocol version**：跟隨官方最新穩定；實作時**鎖定並記進 README**。
- **不相容變更**（tool 改名／參數增減）→ 升 minor，於 `CHANGELOG` 標明並同步更新本檔。
- server 於 `initialize` 回報 `name`／`version`，方便各 harness 顯示。
