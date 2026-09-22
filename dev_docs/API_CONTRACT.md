# API_CONTRACT.md — internal/domain ＋ internal/store 凍結契約

> **目的**：小蝦（`internal/store`）跟開碼客（`internal/domain`）平行開發，靠這份「先講好的函式簽名」互相不卡。
> 這份契約**簽名優先於實作**——雙方先照這份簽名寫，實作內容各自負責。若開發中發現簽名要改，
> **改的人必須先用 herdr 告知對方＋回報克勞德**，不要單方面改了就算，否則對方那邊會編譯失敗。
>
> 語意權威版仍是 `../docs/DATA_MODEL.md`（含 §11 克勞德補充）、`../docs/INTERFACE.md`；本檔只定 Go 簽名。

---

## 0. 模組與目錄（小蝦 Bootstrap 階段建立，其餘兩人不要動這些既有檔）

- module path：`project_board`（`go.mod` 內 `module project_board`；`go` 版本對齊環境 go1.25.3）
- 套件路徑：
  - `internal/domain` — 開碼客
  - `internal/store` — 小蝦
  - `internal/mcp` — 開碼客（第二階段）
  - `internal/httpapi`、`internal/web` — 開碼弟
  - `cmd/pb` — 小蝦（第二階段）
  - `skill/` — 開碼弟（第三階段）

---

## 1. `internal/domain`（開碼客負責實作，簽名如下）

```go
package domain

import "time"

type NodeType string
const (
	TypeProject NodeType = "project"
	TypeReq     NodeType = "req"
	TypeIssue   NodeType = "issue"
	TypeReport  NodeType = "report"
)

type Status string
const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusReview     Status = "review"
	StatusBlocked    Status = "blocked"
	StatusHold       Status = "hold"
	StatusDone       Status = "done"
	StatusCancel     Status = "cancel"
)

type Priority string
const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

type LinkKind string
const (
	LinkDependsOn LinkKind = "depends_on"
	LinkFile      LinkKind = "file"
	LinkCommit    LinkKind = "commit"
	LinkPR        LinkKind = "pr"
	LinkURL       LinkKind = "url"
	LinkDoc       LinkKind = "doc"
)

type HistoryAction string
const (
	ActionCreate     HistoryAction = "create"
	ActionUpdate     HistoryAction = "update"
	ActionTransition HistoryAction = "transition"
	ActionAssign     HistoryAction = "assign"
	ActionLink       HistoryAction = "link"
	ActionUnlink     HistoryAction = "unlink"
	ActionComment    HistoryAction = "comment"
	ActionVerify     HistoryAction = "verify"
)

// Owners 是固定名冊，順序即優先顯示順序（DATA_MODEL.md §8）。
var Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}

func IsValidOwner(owner string) bool

// IsValidTransition 查 DATA_MODEL.md §6 允許轉移表；相同 from==to 一律 false（不算合法轉移，呼叫端另外處理 no-op）。
func IsValidTransition(from, to Status) bool

// RequiresBlockReason 目前只有 to==StatusBlocked 回 true；store 層據此要求 note 或既有 depends_on link（§11.3）。
func RequiresBlockReason(to Status) bool

// ValidateID 檢查格式（大寫、A-Z0-9-、對應 type 的前綴慣例），不檢查是否已存在於 DB（那是 store 的事）。
func ValidateID(t NodeType, parentID, id string) error

// SlugifyTitle：轉大寫、非 [A-Z0-9] 一律轉 '-'、連續 '-' 收斂成一個、去頭尾 '-'（DATA_MODEL.md §11.5）。
func SlugifyTitle(title string) string

// GenerateID：id 省略時呼叫，回傳依 SlugifyTitle(title) 組出的完整 id（含 parent 前綴、type 慣例前綴 REQ-/ISSUE-/REPORT-<owner>-<date>）。
// 不做撞名檢查（store 建立時若已存在照樣回 ErrIDExists）。
func GenerateID(t NodeType, parentID, title string, owner string, now time.Time) (string, error)

// IsSelfVerified：verify 時 actor==owner 回 true（DATA_MODEL.md §11.2）。
func IsSelfVerified(actor, owner string) bool

type Node struct {
	ID, Title, Body, Tags   string
	Type                    NodeType
	ParentID                string // 根為空字串
	Status                  Status
	Owner                   string
	Priority                Priority
	Sort                    int
	CreatedAt, UpdatedAt    time.Time
}

type Link struct {
	ID         int64
	FromID     string
	Kind       LinkKind
	Target     string
	Note       string
	CreatedAt  time.Time
}

type HistoryEntry struct {
	ID                          int64
	NodeID                      string
	TS                          time.Time
	Actor                       string
	Action                      HistoryAction
	Field, FromVal, ToVal, Note string
}
```

**錯誤慣例（domain 定 sentinel error，store／CLI／MCP 統一用這幾個判斷，不要各自比對字串）：**

```go
var (
	ErrInvalidOwner        = errors.New("invalid owner")
	ErrIllegalTransition   = errors.New("illegal transition")
	ErrMissingBlockReason  = errors.New("missing block reason")
	ErrInvalidID           = errors.New("invalid id")
)
```

---

## 2. `internal/store`（小蝦負責實作，簽名如下）

```go
package store

import (
	"context"
	"time"
	"project_board/internal/domain"
)

type Store struct{ /* 內部持 *sql.DB */ }

// New 開檔＋設定 WAL/busy_timeout/foreign_keys，不跑 migration。
func New(dbPath string) (*Store, error)

// Migrate 依 meta.schema_version 依序套用內建 migration（DATA_MODEL.md §11.7）；
// Init()（CLI 的 `pb init`）與 serve/mcp 啟動時都呼叫這個，不是只呼叫一次的專屬指令。
func (s *Store) Migrate(ctx context.Context) error

func (s *Store) Seed(ctx context.Context, actor string) error

type TreeFilter struct {
	Project, Status, Owner string
	Type                   domain.NodeType
	Depth                  int // 0 = 不限
}
func (s *Store) Tree(ctx context.Context, f TreeFilter) ([]domain.Node, error)

func (s *Store) Get(ctx context.Context, id string) (domain.Node, []domain.Link, []domain.Node /*children*/, error)

type CreateInput struct {
	Type                          domain.NodeType
	Title, ParentID, ID           string // ID 空字串 → 用 domain.GenerateID
	Owner, Priority, Tags, Body   string
}
func (s *Store) Create(ctx context.Context, actor string, in CreateInput) (domain.Node, error)

type UpdateInput struct {
	Title, Body, Owner, Priority, Tags *string
	Sort                               *int
}
// expectedUpdatedAt 為 nil 時不做樂觀鎖檢查（相容舊呼叫）；非 nil 且與現值不符回 ErrConflict（§11.6）。
func (s *Store) Update(ctx context.Context, actor, id string, in UpdateInput, expectedUpdatedAt *time.Time) (domain.Node, error)

func (s *Store) Transition(ctx context.Context, actor, id string, to domain.Status, note string, expectedUpdatedAt *time.Time) (domain.Node, error)

func (s *Store) Assign(ctx context.Context, actor, id, owner string) (domain.Node, error)

// Link：kind==depends_on 時驗證 target 節點存在，不存在回 ErrDependsOnTargetMissing（§11.4）。
func (s *Store) Link(ctx context.Context, actor, fromID string, kind domain.LinkKind, target, note string) (domain.Link, error)
func (s *Store) Unlink(ctx context.Context, actor string, linkID int64) error

// Verify：只能對 status==review 的節點呼叫，成功後 status=done；actor==owner 時 note 前綴加 "[self-verified] "。
func (s *Store) Verify(ctx context.Context, actor, id, note string) (domain.Node, error)

func (s *Store) Comment(ctx context.Context, actor, id, text string) error
func (s *Store) Search(ctx context.Context, query, project string) ([]domain.Node, error)
func (s *Store) History(ctx context.Context, id string, limit int) ([]domain.HistoryEntry, error)

type Stats struct {
	CountByStatus     map[domain.Status]int
	CountByOwner      map[string]int
	ReqProgress       map[string]float64 // req id -> 完成度 0~1
	SelfVerifiedCount int                // §11.2
}
func (s *Store) Stats(ctx context.Context, project string) (Stats, error)

// Delete：僅 report 或「無子節點且無 link」的節點可刪，否則回 ErrCannotDelete。
func (s *Store) Delete(ctx context.Context, actor, id string) error
```

**錯誤慣例（store 定，CLI/MCP/httpapi 統一用這幾個判斷）：**

```go
var (
	ErrNotFound              = errors.New("not found")
	ErrIDExists              = errors.New("id already exists")
	ErrMissingActor          = errors.New("missing actor")
	ErrConflict              = errors.New("conflict: node modified since read")
	ErrDependsOnTargetMissing = errors.New("depends_on target does not exist")
	ErrCannotDelete          = errors.New("node has children or links, cannot delete")
	ErrNotInReview           = errors.New("verify requires status=review")
)
```

- 所有寫入函式的第一件事：`actor==""` → 回 `ErrMissingActor`；`!domain.IsValidOwner(actor)` → 回 `domain.ErrInvalidOwner`。
- 每個寫入函式**一個 transaction**內：改 `nodes` ＋ 寫 `history`（`DATA_MODEL.md` §10）。

---

## 3. 給 CLI／MCP／REST 的呼叫慣例（開碼弟／小蝦／開碼客三邊共用）

- `actor` 一律由呼叫端（CLI flag／MCP tool 參數）取得，`store` 函式**不會**去讀 env——`PB_ACTOR` 的 fallback 邏輯在 CLI／MCP 層做，`store` 只認呼叫時傳進來的字串。
- 錯誤要能分辨種類（給正確的 exit code／MCP `isError`／REST status），所以三邊都用 `errors.Is(err, store.ErrXxx)` 判斷，**不要**比對錯誤訊息字串。
- JSON 欄位命名、REST 路徑、MCP tool 參數名一律照 `../docs/INTERFACE.md` 現有表格，不要另外發明。

---

## 5. CTO 追加決策（PB05-A CR 引出，克勞德 2026-09-20）

> PB05-A（開碼弟）CR 時提出 dashboard 焦點面板兩項資料來源不在原契約內，這裡拍板，**小蝦 PB01 若尚未回報完工請直接照此調整**（已用 herdr 通知）。

1. **`TreeFilter` 新增欄位 `Tag string`**（可選，空字串＝不過濾）：`Tree()` 篩選 `tags` 欄位（逗號分隔）包含該字串的節點。用途：REST `focus.awaiting_decision`（❓ 待裁示）v0.1 的資料來源是「任一節點 `tags` 含 `pending-decision`」——**不新建母表／不建假節點**，沿用既有 `tags` 欄位，符合 D4「先不建母表」的原則。說明文字取該節點的 `body` 或最新一筆 `comment` history。

2. **`Store` 新增方法**：
   ```go
   // RecentHistory：跨節點最近 N 筆事件（project 空字串＝全庫），依 ts desc。
   // 用途：REST focus.recent（🕒 最近異動）——單節點的 History() 不夠用，這裡要看全樹。
   func (s *Store) RecentHistory(ctx context.Context, project string, limit int) ([]domain.HistoryEntry, error)
   ```

3. **`Stats.CountByOwner` 語意澄清**：定義為「**目前未結案**張數」——即 `status` 不是 `done` 也不是 `cancel` 的節點才計入。這樣才對得上 dashboard「👤 每人手上張數」的字面意思（手上＝還沒收掉的），不是歷史總數。若已依「全狀態都算」寫好，請調整這一行的計數條件即可，其餘不受影響。

4. **`focus`（`blocked`／`awaiting_decision`／`recent`／`self_verified`）是 REST／dashboard 專屬的附加聚合**，不進核心 `Stats` struct（MCP `pb_stats`／CLI `stats` 維持原本四欄位精簡輸出）。`internal/httpapi` 組 `focus.blocked` 時：`Tree(status=blocked)` 取節點 → 逐一呼叫 `History(id, 1)` 找最近一筆 `action=transition, to_val=blocked` 的 `note` 當 reason，**不需要額外 Store 方法**。

5. **`internal/httpapi` 對外簽名凍結**（開碼弟×小蝦 2026-09-20，解 PB03 `cmd/pb serve` 的編譯依賴）：

   ```go
   // New：回傳「已掛好 REST ＋ dashboard」的單一 handler。
   // pb serve 以 mux.Handle("/", httpapi.New(st)) 掛載，MCP(HTTP) 走 /mcp 另掛。
   func New(st *store.Store) http.Handler
   ```

   - 回傳值固定為 `http.Handler`（`web.NewHandler` 亦為 handler，無需 error 回傳）。
   - contract 階段先 land **可編譯 stub**（全路徑回 501），PB05-B 才填實作，不改簽名。
   - 不拆成 REST／dashboard 兩個 handler：兩者同掛 `/` 由 web 內部路由，外層只多掛 `/mcp`。

## 6. 變更流程

簽名要改：先在 herdr 講一聲對方（`herdr agent prompt <name> "..."`）＋更新這份檔案＋回報克勞德。
不要「先斬後奏」，兩邊都在等這份契約穩定，改了不講會讓對方的編譯突然壞掉還不知道為什麼。
