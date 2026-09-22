# GIT_INTEGRATION — 單 ↔ commit 對應（v0.4）

> 來源：`Y20260920/REQ-V04-GIT-INTEGRATION`（2026-09-20 學長定調）。
> 目標：**從單快速找到對應的 commit／PR，也能從 commit 回推單**；把 sha 自動接到單上。

## 模型

- **專案（project 根）↔ 一個 repo**（GitHub 或本地 git）。
- **每張 issue ↔ 它的 commit／PR**（一對多）。
- 對應資料用既有的 `link`：`kind=commit`（target=sha）、`kind=pr`（target=PR 編號／URL）。

## commit 訊息規範

在 commit 訊息（subject 或 body 皆可）**帶上節點 id**，程式用一條 regex 抓
（`Y<8 碼日期>/…`，如 `Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-COMMIT`）：

```
feat(web): dashboard SPA deep-link (#Y20260920/REQ-V03-DASH-TABS/ISSUE-DASH-URL-ROUTING)
```

- 用幾張單就帶幾個 id；**沒帶 id 的 commit 不會掛**（避免亂連）。
- `#` 只是慣例前綴，可有可無。

## 用法

手動：

```
pb commit attach                 # 讀 git HEAD 的訊息，把 sha 接到訊息中的單
pb commit attach --sha <sha> --message-file <path>
pb commit attach --dry-run       # 只列出會掛哪些，不寫入
```

自動（post-commit hook）：

```
scripts/install-git-hooks.sh /path/to/repo     # 在目標 repo 設定 core.hooksPath
```

hook 會做：`git log -1 --format=%B | pb commit attach --sha "$(git rev-parse HEAD)"`。

## 環境變數

| 變數 | 用途 |
|---|---|
| `PB_BIN` | pb 執行檔（預設 `pb`，要在 PATH） |
| `PB_DB` | 板子 DB 路徑（預設 `./var/board.db`） |
| `PB_ACTOR` | 寫入者名冊名（hook 預設讀 `git config pb.actor`，沒有才 `human`） |

## 底線

- **只建立 link，不動狀態**：不自動 `move`／`done`（避免越權）。

## 專案 ↔ repo（GIT-REPO）

`project` 節點用 `link kind=repo` 存它對應的 repo：

```
pb repo set Y20260916 --url https://github.com/org/repo [--path /local/path] --actor xiaoxia
pb repo show Y20260916 [--json]
```

`repo` 是 v5 migration 新增的 link kind。`set` 是 idempotent（先移除舊 repo link 再掛）。
MCP：`pb_set_repo`（actor／project_id／url／path）。

## GitHub webhook（GIT-WEBHOOK）

`pb serve` 掛 `POST /api/integrations/github`（X-GitHub-Event）：

- `push` → 逐 commit：訊息中的單號掛 `link commit`（target=sha）。
- `pull_request` → 標題＋內文的單號掛 `link pr`（target=PR html_url）。
- `ping` → 200（設定握手）。

簽章：設 env `GH_WEBHOOK_SECRET` 就驗 `X-Hub-Signature-256`（HMAC-SHA256）；沒設＝不驗（限本機）。
外部事件一律以 actor `human` 寫入；**只掛 link，不改狀態**。

## Dashboard 顯示（GIT-DASH）

單的詳情「關聯」會列出 commit／pr；**若該專案設了 repo，sha／PR 變成可點連結**
（commit → `<repo>/commit/<sha>`、pr → `<repo>/pull/<n>` 或原 URL）。

