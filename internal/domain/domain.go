// Package domain：節點型別／狀態機／ID 規則。
//
// 權威規格：docs/DATA_MODEL.md（狀態機 §6、ID 慣例 §7、owner 名冊 §8、
// CTO 補充 §11）。Go 簽名凍結於 dev_docs/API_CONTRACT.md §1——
// 本檔的匯出簽名（型別名、常數值、函式簽名、struct 欄位）必須與該契約
// 一字不差；語意問題以 DATA_MODEL.md 為準。
package domain

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 型別與常數（API_CONTRACT.md §1 原樣）
// ---------------------------------------------------------------------------

// NodeType 是節點種類（DATA_MODEL.md §2）。
type NodeType string

// 以下是出廠內建的 type 代號（與 node_types 表的 seed 對應）。新增／修改 type
// 改走資料（INSERT node_types），不必再動本檔或任何 schema；型別常數只是既有集合的
// 方便寫法，並非 type 存在的唯一來源（存在性以 TypeRegistry 查表為準）。
const (
	TypeProject NodeType = "project"
	TypeReq     NodeType = "req"
	TypeIssue   NodeType = "issue"
	TypeReport  NodeType = "report"
	TypeItem    NodeType = "item"
	TypeBug     NodeType = "bug"
	TypePlan    NodeType = "plan"
)

// Status 是節點狀態（DATA_MODEL.md §6）。
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

// Priority 是優先序（DATA_MODEL.md §2）。
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// LinkKind 是關聯種類（DATA_MODEL.md §3）。
type LinkKind string

const (
	LinkDependsOn LinkKind = "depends_on"
	LinkFile      LinkKind = "file"
	LinkCommit    LinkKind = "commit"
	LinkPR        LinkKind = "pr"
	LinkURL       LinkKind = "url"
	LinkDoc       LinkKind = "doc"
	LinkRepo      LinkKind = "repo"
)

// HistoryAction 是事件種類（DATA_MODEL.md §4）。
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

// minimalOwners：連設定檔都沒有時的最後防線，只放 schema 要求一定要存在的
// "unassigned"（nodes.owner DEFAULT 'unassigned'，見 DATA_MODEL.md §2）。不放任何
// 團隊/人名——這一層不該讓任何具體名字（不管是誰的）出現在原始碼裡（2026-09-23 導演拍板）。
var minimalOwners = []string{"unassigned"}

// Owners 是目前生效中的名冊，順序即優先顯示順序（DATA_MODEL.md §8）。actor（見 §11.1）
// 與匯入推斷 owner（見 importer）都只認這份清單。載入順序（先到先贏）：
//  1. 環境變數 PB_OWNERS（逗號分隔，如 "alice,bob,human,unassigned"）
//  2. 設定檔（環境變數 PB_OWNERS_FILE 指定路徑，預設 "owners.txt"；每行一個名字，
//     支援 # 開頭註解與空行）——這是給「裝好就要能用、還沒設定環境變數」的情境用的，
//     複製 owners.example.txt 成 owners.txt 填自己團隊的名字即可，不用重編。
//  3. minimalOwners（見上）——原始碼裡唯一內建、不代表任何人的最後防線。
//
// "unassigned" 是 schema 層的預設 owner，不管走哪一層都強制併入，避免自訂名冊漏掉它
// 導致既有資料的 owner 驗證失敗。
var Owners = loadOwners()

const defaultOwnersFile = "owners.txt"

func loadOwners() []string {
	if names := parseOwnersCSV(os.Getenv("PB_OWNERS")); len(names) > 0 {
		return withUnassigned(names)
	}
	file := os.Getenv("PB_OWNERS_FILE")
	if file == "" {
		file = defaultOwnersFile
	}
	if names := readOwnersFile(file); len(names) > 0 {
		return withUnassigned(names)
	}
	return minimalOwners
}

func parseOwnersCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// readOwnersFile：讀 owners.txt 這類設定檔，每行一個名字；'#' 開頭與空行忽略。
// 檔案不存在或讀不到就回 nil（呼叫端退回下一層），不是錯誤。
func readOwnersFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := make([]string, 0, 8)
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" || strings.HasPrefix(name, "#") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func withUnassigned(names []string) []string {
	for _, n := range names {
		if n == "unassigned" {
			return names
		}
	}
	return append(names, "unassigned")
}

// ---------------------------------------------------------------------------
// sentinel errors（API_CONTRACT.md §1 錯誤慣例）
// ---------------------------------------------------------------------------

var (
	ErrInvalidOwner       = errors.New("invalid owner")
	ErrIllegalTransition  = errors.New("illegal transition")
	ErrMissingBlockReason = errors.New("missing block reason")
	ErrInvalidID          = errors.New("invalid id")
)

// ---------------------------------------------------------------------------
// Owner
// ---------------------------------------------------------------------------

// IsValidOwner 純粹查表（大小寫敏感，須與 Owners 名冊完全一致）。
func IsValidOwner(owner string) bool {
	for _, o := range Owners {
		if o == owner {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 狀態機（DATA_MODEL.md §6 允許轉移表原樣）
// ---------------------------------------------------------------------------

// allowedTransitions 是 §6「允許的轉移」表的逐字轉寫：
//
//	todo        → in_progress、blocked、hold、cancel
//	in_progress → review、blocked、hold、cancel
//	review      → done、in_progress、blocked、cancel
//	blocked     → in_progress、hold、cancel
//	hold        → todo、in_progress、cancel
//	done        → in_progress（reopen，需 note——note 由 store 層檢查）
//	cancel      → todo（revive）
var allowedTransitions = map[Status]map[Status]bool{
	StatusTodo: {
		StatusInProgress: true,
		StatusBlocked:    true,
		StatusHold:       true,
		StatusCancel:     true,
	},
	StatusInProgress: {
		StatusReview:  true,
		StatusBlocked: true,
		StatusHold:    true,
		StatusCancel:  true,
	},
	StatusReview: {
		StatusDone:       true,
		StatusInProgress: true,
		StatusBlocked:    true,
		StatusCancel:     true,
	},
	StatusBlocked: {
		StatusInProgress: true,
		StatusHold:       true,
		StatusCancel:     true,
	},
	StatusHold: {
		StatusTodo:       true,
		StatusInProgress: true,
		StatusCancel:     true,
	},
	StatusDone: {
		StatusInProgress: true,
	},
	StatusCancel: {
		StatusTodo: true,
	},
}

// IsValidTransition 查 §6 允許轉移表；相同 from==to 一律 false
// （不算合法轉移，呼叫端另外處理 no-op）。
func IsValidTransition(from, to Status) bool {
	if from == to {
		return false
	}
	return allowedTransitions[from][to]
}

// RequiresBlockReason 目前只有 to==StatusBlocked 回 true；
// store 層據此要求 note 或既有 depends_on link（§11.3）。
func RequiresBlockReason(to Status) bool {
	return to == StatusBlocked
}

// ---------------------------------------------------------------------------
// ID 規則（DATA_MODEL.md §7、§11.5）
// ---------------------------------------------------------------------------

// isSlug 檢查 SLUG／KEY：非空、全由 [A-Z0-9-] 組成、
// 無頭尾 '-'、無連續 "--"（與 SlugifyTitle 的輸出不變量一致）。
func isSlug(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	if strings.Contains(s, "--") {
		return false
	}
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// isItemKey 檢查母表編號 KEY（Y20260920/REQ-V03-CHECKLIST 裁示）：
// 1 個以上大寫英文字母接 1 個以上數字，如 A1、B3、K12。
// 分組字母即首字元；parent 必須是 req 由 store 層驗（ValidateID 只看字串形狀）。
func isItemKey(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	if i == 0 {
		return false
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return j > i && j == len(s)
}

// isDate8 檢查 YYYYMMDD（8 位數字；不驗證月份日期是否真實存在，
// 只擋格式錯誤——日期真偽是呼叫端時鐘的責任）。
func isDate8(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IDShape 是 ID 形狀的識別字（node_types.id_shape 的值）。
// 形狀本身是結構化邏輯，留在 Go 當固定形狀庫（下面 4 個 validate／generate 形狀函式）；
// 「哪個 type 用哪個形狀」改由 node_types 表決定，新增 type 不必再改本檔。
type IDShape string

const (
	ShapeProjectDate     IDShape = "project_date"      // Y<YYYYMMDD>
	ShapeSlug            IDShape = "slug"              // <parent>/<PREFIX>-<SLUG>
	ShapeItemKey         IDShape = "item_key"          // <parent>/<PREFIX>-<KEY>（大寫字母＋數字，如 A1）
	ShapeReportOwnerDate IDShape = "report_owner_date" // <parent>/<PREFIX>-<owner>-<YYYYMMDD>
)

// TypeDef 是一列 node_types：type 的資料驅動定義。
type TypeDef struct {
	Key        NodeType // node_types.key
	IDPrefix   string   // ID 慣用前綴（project 為空字串）
	IDShape    IDShape  // 決定 ValidateID／GenerateID 走哪個形狀
	Label      string   // 顯示名稱
	ParentType NodeType // 非空＝parent 必須是該 type；空＝可掛 project 下（parent 有無由形狀自驗）
	Sort       int
}

// BuiltinTypeDefs 是出廠內建 type 定義，與 migration 0006 的 seed 一字對應。
// runtime 的權威是 DB 的 node_types 表（store 由它建 TypeRegistry）；這份是純函式
// 呼叫端（直接呼叫 ValidateID／GenerateID）與測試用的預設值。
func BuiltinTypeDefs() []TypeDef {
	return []TypeDef{
		{Key: TypeProject, IDPrefix: "", IDShape: ShapeProjectDate, Label: "專案", ParentType: "", Sort: 1},
		{Key: TypeReq, IDPrefix: "REQ", IDShape: ShapeSlug, Label: "需求", ParentType: "", Sort: 2},
		{Key: TypeIssue, IDPrefix: "ISSUE", IDShape: ShapeSlug, Label: "議題", ParentType: "", Sort: 3},
		{Key: TypeReport, IDPrefix: "REPORT", IDShape: ShapeReportOwnerDate, Label: "報告", ParentType: "", Sort: 4},
		{Key: TypeItem, IDPrefix: "ITEM", IDShape: ShapeItemKey, Label: "母表項目", ParentType: TypeReq, Sort: 5},
		{Key: TypeBug, IDPrefix: "BUG", IDShape: ShapeSlug, Label: "BUG", ParentType: TypeReq, Sort: 6},
		{Key: TypePlan, IDPrefix: "PLAN", IDShape: ShapeSlug, Label: "計畫", ParentType: "", Sort: 7},
	}
}

// TypeRegistry 是 type 定義的查表（由 node_types 各列建構）。
type TypeRegistry struct{ byKey map[NodeType]TypeDef }

// NewTypeRegistry 建 registry；key 為空或重複回錯（避免同一 type 兩種定義）。
func NewTypeRegistry(defs []TypeDef) (*TypeRegistry, error) {
	r := &TypeRegistry{byKey: make(map[NodeType]TypeDef, len(defs))}
	for _, d := range defs {
		if d.Key == "" {
			return nil, fmt.Errorf("node type def: empty key")
		}
		if _, dup := r.byKey[d.Key]; dup {
			return nil, fmt.Errorf("node type def: duplicate key %q", d.Key)
		}
		r.byKey[d.Key] = d
	}
	return r, nil
}

// Lookup 查 type 定義；不存在回 (TypeDef{}, false)。
func (r *TypeRegistry) Lookup(t NodeType) (TypeDef, bool) {
	d, ok := r.byKey[t]
	return d, ok
}

var builtinRegistry = sync.OnceValue(func() *TypeRegistry {
	r, err := NewTypeRegistry(BuiltinTypeDefs())
	if err != nil {
		panic("domain: builtin type defs invalid: " + err.Error())
	}
	return r
})

// BuiltinRegistry 回出廠 registry（供直接呼叫 ValidateID／GenerateID 的純函式路徑）。
func BuiltinRegistry() *TypeRegistry { return builtinRegistry() }

// ValidateID 檢查格式（大寫、A-Z0-9-、對應 type 的前綴慣例、掛在 parent 下），
// 不檢查是否已存在於 DB（那是 store 的事）。
//
// 本函式走內建 registry（BuiltinRegistry）；store 層應呼叫自己的 registry
// （由 DB node_types 建構）版本，才會把執行期新增的 type 算進去。
func ValidateID(t NodeType, parentID, id string) error {
	return BuiltinRegistry().ValidateID(t, parentID, id)
}

// ValidateID 依 registry 中該 type 的 id_shape dispatch 到形狀函式；
// type 是否存在＝查表（查不到回 ErrInvalidID，不再是 switch-per-type 的 default 分支）。
func (r *TypeRegistry) ValidateID(t NodeType, parentID, id string) error {
	if id == "" {
		return ErrInvalidID
	}
	def, ok := r.Lookup(t)
	if !ok {
		return ErrInvalidID
	}
	switch def.IDShape {
	case ShapeProjectDate:
		return validateProjectDate(parentID, id)
	case ShapeSlug:
		return validateSlug(parentID, def.IDPrefix, id)
	case ShapeItemKey:
		return validateItemKey(parentID, def.IDPrefix, id)
	case ShapeReportOwnerDate:
		return validateReportOwnerDate(parentID, def.IDPrefix, id)
	default:
		return ErrInvalidID
	}
}

// validateProjectDate：parentID 須為空，id 為 Y<YYYYMMDD>（如 Y20260916）。
func validateProjectDate(parentID, id string) error {
	if parentID != "" {
		return ErrInvalidID
	}
	if len(id) != 9 || id[0] != 'Y' || !isDate8(id[1:]) {
		return ErrInvalidID
	}
	return nil
}

// validateSlug：id 須為 <parentID>/<prefix>-<SLUG>。
func validateSlug(parentID, prefix, id string) error {
	if parentID == "" {
		return ErrInvalidID
	}
	slug, ok := strings.CutPrefix(id, parentID+"/"+prefix+"-")
	if !ok || !isSlug(slug) {
		return ErrInvalidID
	}
	return nil
}

// validateItemKey：id 須為 <parentID>/<prefix>-<KEY>，KEY 為母表編號（大寫字母＋數字，如 A1）。
func validateItemKey(parentID, prefix, id string) error {
	if parentID == "" {
		return ErrInvalidID
	}
	key, ok := strings.CutPrefix(id, parentID+"/"+prefix+"-")
	if !ok || !isItemKey(key) {
		return ErrInvalidID
	}
	return nil
}

// validateReportOwnerDate：id 須為 <parentID>/<prefix>-<owner>-<YYYYMMDD>，owner 須在名冊內。
func validateReportOwnerDate(parentID, prefix, id string) error {
	if parentID == "" {
		return ErrInvalidID
	}
	rest, ok := strings.CutPrefix(id, parentID+"/"+prefix+"-")
	if !ok {
		return ErrInvalidID
	}
	// rest = <owner>-<YYYYMMDD>；owner 名冊內無含 '-' 者，用最後一個 '-' 切分。
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 || idx == len(rest)-1 {
		return ErrInvalidID
	}
	if !IsValidOwner(rest[:idx]) || !isDate8(rest[idx+1:]) {
		return ErrInvalidID
	}
	return nil
}

// nodeIDRe：節點 id 形狀（Y<8 碼日期> 起頭，之後每段大寫字母／數字／-）。
var nodeIDRe = regexp.MustCompile(`Y[0-9]{8}(?:/[A-Z0-9][A-Z0-9-]*)+`)

// ExtractNodeIDs：回傳文字中出現（依序、去重複）的節點 id。
// 用於 commit 訊息 → 單號（Y20260920/REQ-V04-GIT-INTEGRATION）。
func ExtractNodeIDs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range nodeIDRe.FindAllString(text, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// SlugifyTitle：轉大寫、非 [A-Z0-9] 一律轉 '-'、連續 '-' 收斂成一個、
// 去頭尾 '-'（DATA_MODEL.md §11.5、§7）。
// 非 ASCII（如中文）轉大寫後仍非 [A-Z0-9]，故一律變 '-'（全中文標題回空字串）。
func SlugifyTitle(title string) string {
	upper := strings.ToUpper(title)
	var b strings.Builder
	b.Grow(len(upper))
	prevDash := false
	for _, r := range upper {
		var ch rune
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			ch = r
		} else {
			ch = '-'
		}
		if ch == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(ch)
	}
	return strings.Trim(b.String(), "-")
}

// GenerateID：id 省略時呼叫，回傳依該 type 的 id_shape 組出的完整 id。
// 本函式走內建 registry（BuiltinRegistry）；store 層應呼叫自己的 registry
// （由 DB node_types 建構）版本，才會把執行期新增的 type 算進去。
func GenerateID(t NodeType, parentID, title string, owner string, now time.Time) (string, error) {
	return BuiltinRegistry().GenerateID(t, parentID, title, owner, now)
}

// GenerateID 依 registry 中該 type 的 id_shape 生成 id（未知 type 回 ErrInvalidID）。
// 不做撞名檢查（store 建立時若已存在照樣回 ErrIDExists）。
//
//	project_date      → Y<now.YYYYMMDD>（title 不用，parentID 須為空）。
//	slug              → <parentID>/<prefix>-<slug(title)>（slug 為空回 ErrInvalidID）。
//	report_owner_date → <parentID>/<prefix>-<owner>-<now.YYYYMMDD>（title 不用，owner 須在名冊內）。
//	item_key          → 不支援（母表編號 KEY 由呼叫端顯式給 id），一律回 ErrInvalidID。
func (r *TypeRegistry) GenerateID(t NodeType, parentID, title string, owner string, now time.Time) (string, error) {
	def, ok := r.Lookup(t)
	if !ok {
		return "", ErrInvalidID
	}
	switch def.IDShape {
	case ShapeProjectDate:
		if parentID != "" {
			return "", ErrInvalidID
		}
		return "Y" + now.Format("20060102"), nil
	case ShapeSlug:
		if parentID == "" {
			return "", ErrInvalidID
		}
		slug := SlugifyTitle(title)
		if slug == "" {
			return "", ErrInvalidID
		}
		return parentID + "/" + def.IDPrefix + "-" + slug, nil
	case ShapeReportOwnerDate:
		if parentID == "" {
			return "", ErrInvalidID
		}
		if !IsValidOwner(owner) {
			return "", ErrInvalidOwner
		}
		return parentID + "/" + def.IDPrefix + "-" + owner + "-" + now.Format("20060102"), nil
	case ShapeItemKey:
		return "", ErrInvalidID
	default:
		return "", ErrInvalidID
	}
}

// IsSelfVerified：verify 時 actor==owner 回 true（DATA_MODEL.md §11.2）。
func IsSelfVerified(actor, owner string) bool {
	return actor == owner
}

// ---------------------------------------------------------------------------
// Struct（API_CONTRACT.md §1 欄位原樣——store 直接用的型別，欄位名/型別禁改）
// ---------------------------------------------------------------------------

// Node 是樹上的節點（根的 ParentID 為空字串）。
type Node struct {
	ID, Title, Body, Tags string
	Type                  NodeType
	ParentID              string // 根為空字串
	Status                Status
	Owner                 string
	Priority              Priority
	Sort                  int
	CreatedAt, UpdatedAt  time.Time
}

// Link 是節點對外關聯與依賴。
type Link struct {
	ID        int64
	FromID    string
	Kind      LinkKind
	Target    string
	Note      string
	CreatedAt time.Time
}

// HistoryEntry 是 append-only 事件流中的一筆。
type HistoryEntry struct {
	ID                          int64
	NodeID                      string
	TS                          time.Time
	Actor                       string
	Action                      HistoryAction
	Field, FromVal, ToVal, Note string
}
