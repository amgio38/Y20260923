package httpapi

// REST 回應的 DTO（欄位名對齊 docs/INTERFACE.md §3；不直接把 domain struct
// 拿去 marshal，避免內部欄位名（ParentID／CreatedAt…）洩漏成不同 JSON 形狀）。

// treeNode：`/api/tree` 的巢狀節點（dashboard 樹用）。
type treeNode struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Owner    string     `json:"owner"`
	Priority string     `json:"priority"`
	Children []treeNode `json:"children"`
}

// nodeDTO：`/api/node/{id}`（INTERFACE.md §3 範例逐欄對齊）。
type nodeDTO struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Owner     string     `json:"owner"`
	Priority  string     `json:"priority"`
	Tags      string     `json:"tags"`
	Body      string     `json:"body"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	Links     []linkDTO  `json:"links"`
	Children  []childDTO `json:"children"`
}

type linkDTO struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Note   string `json:"note"`
}

type childDTO struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// historyDTO：`/api/node/{id}/history` 的事件（欄位對齊 domain.HistoryEntry）。
type historyDTO struct {
	ID      int64  `json:"id"`
	NodeID  string `json:"node_id"`
	TS      string `json:"ts"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Field   string `json:"field"`
	FromVal string `json:"from_val"`
	ToVal   string `json:"to_val"`
	Note    string `json:"note"`
}

// searchHit：`/api/search` 命中（沿用 MCP `pb_tree` 的精簡欄位）。
type searchHit struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

// depDTO：`/api/deps` 的一筆依賴（kind=depends_on）。沿用 domain.Link 的
// FromID／Target／Note（v0.2，Y20260920/REQ-V02-DEPS 裁示）。
type depDTO struct {
	FromID string `json:"from_id"`
	Target string `json:"target"`
	Note   string `json:"note"`
}

// statsDTO：`/api/stats`。前三欄＋自我驗收張數來自 store.Stats；
// focus 是 REST／dashboard 專屬聚合（API_CONTRACT.md §5.4）。
type statsDTO struct {
	CountByStatus     map[string]int     `json:"count_by_status"`
	CountByOwner      map[string]int     `json:"count_by_owner"`
	AvgDwellDays      map[string]float64 `json:"avg_dwell_days"`
	ReqProgress       map[string]float64 `json:"req_progress"`
	SelfVerifiedCount int                `json:"self_verified_count"`
	Focus             focusDTO           `json:"focus"`
}

// focusDTO：焦點面板五項的後四項資料來源（每人張數用 count_by_owner）。
type focusDTO struct {
	Blocked          []focusBlocked  `json:"blocked"`
	AwaitingDecision []focusDecision `json:"awaiting_decision"`
	Recent           []focusRecent   `json:"recent"`
	SelfVerified     []focusSelf     `json:"self_verified"`
}

type focusBlocked struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Owner  string `json:"owner"`
	Reason string `json:"reason"`
}

type focusDecision struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note"`
}

type focusRecent struct {
	TS      string `json:"ts"`
	Actor   string `json:"actor"`
	NodeID  string `json:"node_id"`
	Action  string `json:"action"`
	Summary string `json:"summary"`
}

type focusSelf struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Owner string `json:"owner"`
	Note  string `json:"note"`
}

// checklistDTO：`/api/checklist` 一列（欄位對齊 cmd/pb 的 checklistJSON）。
// 母表只做索引：一列 item 用 depends_on 指向已存在的 issue（issue_id 可空）。
type checklistDTO struct {
	Key     string `json:"key"`
	ItemID  string `json:"item_id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Owner   string `json:"owner"`
	IssueID string `json:"issue_id,omitempty"`
}

// reportDTO：`/api/report`（v0.3 週報；欄位對齊 cmd/pb weeklyJSON）。
type reportDTO struct {
	WeekStart string          `json:"week_start"`
	WeekEnd   string          `json:"week_end"`
	Did       []historyDTO    `json:"did"`
	Closed    []historyDTO    `json:"closed"`
	Open      []reportNodeDTO `json:"open"`
}

// reportNodeDTO：週報「還開著什麼」的 issue 精簡（只列 id／type／title／owner／status）。
type reportNodeDTO struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

// metaDTO：`/api/meta`（V05-DASHBOARD-META-API）。types 來自 DB `node_types`
// （依 sort 排序，非寫死），owners 來自 domain.Owners，statuses 來自
// domain.StatusDefs，tabs 來自 domain.TabDefs（皆為顯示 metadata）；dashboard
// 改吃這支後就不必自己複製清單。
type metaDTO struct {
	Types    []metaTypeDTO   `json:"types"`
	Owners   []string        `json:"owners"`
	Statuses []metaStatusDTO `json:"statuses"`
	Tabs     []metaTabDTO    `json:"tabs"`
}

// metaTypeDTO：一個 node type（欄位名照 REQ-V05-DASHBOARD-META-API 凍結的 snake_case）。
type metaTypeDTO struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	IDPrefix   string `json:"id_prefix"`
	ParentType string `json:"parent_type"`
	Sort       int    `json:"sort"`
}

// metaStatusDTO：一個狀態的顯示 metadata（V05-STATUS-META-API）。只有顯示用的
// label／icon／color；狀態機轉移規則不在這裡（留在 domain 的 allowedTransitions）。
type metaStatusDTO struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
	Sort  int    `json:"sort"`
}

// metaTabDTO：dashboard header 一個分頁的顯示 metadata（V05-STATUS-META-API 追加）。
type metaTabDTO struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Sort  int    `json:"sort"`
}
