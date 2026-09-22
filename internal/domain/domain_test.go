package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 狀態機窮舉（DATA_MODEL.md §6 允許轉移表逐條對照）
// ---------------------------------------------------------------------------

// allStatuses 與 allowedTransitions 的 key／常數必須同步：少一個狀態，
// 此測試會先失敗，逼實作者補上。
var allStatuses = []Status{
	StatusTodo,
	StatusInProgress,
	StatusReview,
	StatusBlocked,
	StatusHold,
	StatusDone,
	StatusCancel,
}

// legalTransitions 是 §6 表的期望合法集合（from→to），共 20 組。
var legalTransitions = map[[2]Status]bool{
	{StatusTodo, StatusInProgress}: true,
	{StatusTodo, StatusBlocked}:    true,
	{StatusTodo, StatusHold}:       true,
	{StatusTodo, StatusCancel}:     true,

	{StatusInProgress, StatusReview}:  true,
	{StatusInProgress, StatusBlocked}: true,
	{StatusInProgress, StatusHold}:    true,
	{StatusInProgress, StatusCancel}:  true,

	{StatusReview, StatusDone}:       true,
	{StatusReview, StatusInProgress}: true,
	{StatusReview, StatusBlocked}:    true,
	{StatusReview, StatusCancel}:     true,

	{StatusBlocked, StatusInProgress}: true,
	{StatusBlocked, StatusHold}:       true,
	{StatusBlocked, StatusCancel}:     true,

	{StatusHold, StatusTodo}:       true,
	{StatusHold, StatusInProgress}: true,
	{StatusHold, StatusCancel}:     true,

	{StatusDone, StatusInProgress}: true,

	{StatusCancel, StatusTodo}: true,
}

func TestAllStatusesKnown(t *testing.T) {
	if len(allStatuses) != 7 {
		t.Fatalf("狀態數應為 7，實得 %d（§6 表有增減？同步更新 allowedTransitions）", len(allStatuses))
	}
	seen := map[Status]bool{}
	for _, s := range allStatuses {
		if seen[s] {
			t.Fatalf("重複狀態 %q", s)
		}
		seen[s] = true
	}
	// 合法組數鎖死 20：§6 表增減任一條，此數即變，逼實作者有意識地改。
	if len(legalTransitions) != 20 {
		t.Fatalf("合法轉移應為 20 組，實得 %d", len(legalTransitions))
	}
}

// TestIsValidTransition_Exhaustive：7×7=49 組全跑。
// 合法的 20 組須回 true；其餘 29 組（含 7 組 self）一律 false。
func TestIsValidTransition_Exhaustive(t *testing.T) {
	count := 0
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			count++
			want := legalTransitions[[2]Status{from, to}]
			if got := IsValidTransition(from, to); got != want {
				t.Errorf("IsValidTransition(%q → %q) = %v，§6 表期望 %v", from, to, got, want)
			}
		}
	}
	if count != 49 {
		t.Fatalf("應跑滿 49 組，實跑 %d", count)
	}
}

// TestIsValidTransition_SelfAlwaysFalse：同狀態 self-loop 明確列出，防迴歸。
func TestIsValidTransition_SelfAlwaysFalse(t *testing.T) {
	for _, s := range allStatuses {
		if IsValidTransition(s, s) {
			t.Errorf("IsValidTransition(%q → %q) 應為 false（相同 from==to 不算合法轉移）", s, s)
		}
	}
}

// TestIsValidTransition_UnknownStatus：表外狀態一律 false，不 panic。
func TestIsValidTransition_UnknownStatus(t *testing.T) {
	if IsValidTransition(Status("archived"), StatusTodo) {
		t.Error("未知 from 應回 false")
	}
	if IsValidTransition(StatusTodo, Status("archived")) {
		t.Error("未知 to 應回 false")
	}
	if IsValidTransition(Status("a"), Status("b")) {
		t.Error("雙未知應回 false")
	}
}

// 重點語意鎖死：done 只能從 review 進（必經驗收）；verify 正規路徑以外無他路。
func TestTransition_ReviewIsOnlyWayToDone(t *testing.T) {
	for _, from := range allStatuses {
		if from == StatusReview {
			continue
		}
		if IsValidTransition(from, StatusDone) {
			t.Errorf("%q → done 應被拒（done 只能從 review 進）", from)
		}
	}
	if !IsValidTransition(StatusReview, StatusDone) {
		t.Error("review → done 必須合法")
	}
}

// review 不能直轉 hold（§6 表無此條）；blocked 轉入僅 blocked 本身要理由（見下）。
func TestTransition_ReviewCannotHold(t *testing.T) {
	if IsValidTransition(StatusReview, StatusHold) {
		t.Error("review → hold §6 表無此條，應為 false")
	}
}

func TestRequiresBlockReason(t *testing.T) {
	for _, s := range allStatuses {
		want := s == StatusBlocked
		if got := RequiresBlockReason(s); got != want {
			t.Errorf("RequiresBlockReason(%q) = %v，期望 %v", s, got, want)
		}
	}
	if !RequiresBlockReason(StatusBlocked) {
		t.Error("to==blocked 必須回 true")
	}
}

// ---------------------------------------------------------------------------
// Owner
// ---------------------------------------------------------------------------

func TestIsValidOwner(t *testing.T) {
	for _, o := range Owners {
		if !IsValidOwner(o) {
			t.Errorf("名冊內 %q 應合法", o)
		}
	}
	bads := []string{"", " ", "unassigned ", " unassigned", "XIAOXIA", "Xiaoxia", "bob", "human2", "kaimake\n"}
	for _, b := range bads {
		if IsValidOwner(b) {
			t.Errorf("%q 應非法", b)
		}
	}
}

// ---------------------------------------------------------------------------
// SlugifyTitle（§11.5／§7）
// ---------------------------------------------------------------------------

func TestSlugifyTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Hello World", "HELLO-WORLD"},
		{"hello-world", "HELLO-WORLD"},
		{"REQ-MEMBER-CORE", "REQ-MEMBER-CORE"},
		{"  a  b  ", "A-B"},        // 頭尾空白去 '-'
		{"a--b", "A-B"},            // 連續 '-' 收斂
		{"-hello-", "HELLO"},       // 去頭尾 '-'
		{"---", ""},                // 全是分隔符 → 空
		{"a/b_c.d:e", "A-B-C-D-E"}, // 非 [A-Z0-9] 全轉 '-'
		{"ABC123", "ABC123"},       // 數字保留
		{"中文測試", ""},               // 非 ASCII 全轉 '-' 後去頭尾 → 空
		{"混中文Mix123", "MIX123"},    // 中文段變 '-'，收斂＋去頭尾
		{"UT90-A", "UT90-A"},       // 既有 KEY 格式不變
		{"a   b", "A-B"},           // 連續空白收斂成一 '-'
		{"  ", ""},
		{"Member Core Module", "MEMBER-CORE-MODULE"},
	}
	for _, c := range cases {
		if got := SlugifyTitle(c.in); got != c.want {
			t.Errorf("SlugifyTitle(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyTitle_Long(t *testing.T) {
	long := strings.Repeat("ab ", 200) // 超長標題：不截斷、不 panic
	got := SlugifyTitle(long)
	if got == "" {
		t.Fatal("超長標題不應回空")
	}
	if strings.Contains(got, "--") || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("超長標題輸出不變量破功：%q…", got[:40])
	}
}

// ---------------------------------------------------------------------------
// ValidateID（§7）
// ---------------------------------------------------------------------------

func TestValidateID_Project(t *testing.T) {
	valid := []string{"Y20260916", "Y20000101", "Y20991231"}
	for _, id := range valid {
		if err := ValidateID(TypeProject, "", id); err != nil {
			t.Errorf("project %q 應合法：%v", id, err)
		}
	}
	invalid := []struct{ parent, id string }{
		{"", ""},                // 空
		{"", "y20260916"},       // 小寫
		{"", "Y2026091"},        // 日期不足 8 位
		{"", "Y202609166"},      // 日期超過 8 位
		{"", "Y2026ABCD"},       // 非數字
		{"", "20260916"},        // 缺 Y
		{"", " Y20260916"},      // 空白
		{"", "Y20260916 "},      // 空白
		{"X", "Y20260916"},      // project 不准有 parent
		{"", "Y20260916/REQ-A"}, // project 不帶路徑
		{"", "Y2026-0916"},      // 含 '-'
	}
	for _, c := range invalid {
		if err := ValidateID(TypeProject, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("project parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}
}

func TestValidateID_Req(t *testing.T) {
	p := "Y20260916"
	valid := []string{
		"Y20260916/REQ-MEMBER-CORE",
		"Y20260916/REQ-A",
		"Y20260916/REQ-UT90-A",
		"Y20260916/REQ-ADMIN-BFF-UT90-A", // 既有單名沿用
	}
	for _, id := range valid {
		if err := ValidateID(TypeReq, p, id); err != nil {
			t.Errorf("req %q 應合法：%v", id, err)
		}
	}
	invalid := []struct{ parent, id string }{
		{"", "Y20260916/REQ-A"},  // 缺 parent
		{p, ""},                  // 空 id
		{p, "Y20260916/REQ-"},    // 空 SLUG
		{p, "Y20260916/REQ-a"},   // 小寫
		{p, "Y20260916/REQ-A B"}, // 空白
		{p, "Y20260916/REQ-A/B"}, // 多餘 '/'
		{p, "Y20260916/REQ--A"},  // 頭 '-'（連續 '--'）
		{p, "Y20260916/REQ-A-"},  // 尾 '-'
		{p, "Y20260916/ISSUE-A"}, // 前綴錯
		{p, "Y20260917/REQ-A"},   // 沒掛在 parent 下
		{p, "REQ-A"},             // 缺 parent 前綴
		{p, "Y20260916/REQ-A_1"}, // '_' 非法
		{p, "Y20260916/REQ-中文"},  // 中文非法
		{p, "OTHER/REQ-A"},       // parent 不符
	}
	for _, c := range invalid {
		if err := ValidateID(TypeReq, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("req parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}
}

func TestValidateID_Issue(t *testing.T) {
	p := "Y20260916/REQ-MEMBER-CORE"
	valid := []string{
		"Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A",
		"Y20260916/REQ-MEMBER-CORE/ISSUE-CONC03",
		"Y20260916/REQ-MEMBER-CORE/ISSUE-ADMIN-BFF-UT90-A",
	}
	for _, id := range valid {
		if err := ValidateID(TypeIssue, p, id); err != nil {
			t.Errorf("issue %q 應合法：%v", id, err)
		}
	}
	invalid := []struct{ parent, id string }{
		{"", "Y20260916/REQ-MEMBER-CORE/ISSUE-A"}, // 缺 parent
		{p, ""},
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-"},    // 空 KEY
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-a"},   // 小寫
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-A B"}, // 空白
		{p, "Y20260916/REQ-MEMBER-CORE/REQ-A"},     // 前綴錯
		{p, "Y20260916/REQ-OTHER/ISSUE-A"},         // 沒掛在 parent 下
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE--A"},  // 連續 '--'
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-A-"},  // 尾 '-'
	}
	for _, c := range invalid {
		if err := ValidateID(TypeIssue, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("issue parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}
}

func TestValidateID_Report(t *testing.T) {
	p := "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A"
	valid := []string{
		"Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-20260919",
		"Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-human-20260920",
		"Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-unassigned-20260920",
	}
	for _, id := range valid {
		if err := ValidateID(TypeReport, p, id); err != nil {
			t.Errorf("report %q 應合法：%v", id, err)
		}
	}
	invalid := []struct{ parent, id string }{
		{"", "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-20260919"},
		{p, ""},
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-"},                  // 空
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia"},           // 缺日期
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-20260919"},          // 缺 owner
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-bob-20260919"},      // owner 不在名冊
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-XIAOXIA-20260919"},  // owner 大小寫敏感
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-2026919"},   // 日期 7 位
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-202609199"}, // 日期 9 位
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-abcdefgh"},  // 日期非數字
		{p, "Y20260916/REQ-OTHER/ISSUE-X/REPORT-xiaoxia-20260919"},             // 沒掛在 parent 下
		{p, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REQ-xiaoxia-20260919"},     // 前綴錯
	}
	for _, c := range invalid {
		if err := ValidateID(TypeReport, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("report parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}
}

func TestValidateID_Item(t *testing.T) {
	p := "Y20260920/REQ-M"
	valid := []string{
		"Y20260920/REQ-M/ITEM-A1",
		"Y20260920/REQ-M/ITEM-B3",
		"Y20260920/REQ-M/ITEM-K12",
		"Y20260920/REQ-M/ITEM-AB12",
	}
	for _, id := range valid {
		if err := ValidateID(TypeItem, p, id); err != nil {
			t.Errorf("item %q 應合法：%v", id, err)
		}
	}
	invalid := []struct{ parent, id string }{
		{"", "Y20260920/REQ-M/ITEM-A1"},    // 缺 parent
		{p, ""},                            // 空 id
		{p, "Y20260920/REQ-M/ITEM-"},       // 空 KEY
		{p, "Y20260920/REQ-M/ITEM-a1"},     // 小寫
		{p, "Y20260920/REQ-M/ITEM-A"},      // 只有字母
		{p, "Y20260920/REQ-M/ITEM-1"},      // 只有數字
		{p, "Y20260920/REQ-M/ITEM-1A"},     // 數字在前
		{p, "Y20260920/REQ-M/ITEM-A-1"},    // 含 '-'
		{p, "Y20260920/REQ-M/ITEM-A 1"},    // 空白
		{p, "Y20260920/REQ-M/ITEM-A1-"},    // 尾 '-'
		{p, "Y20260920/REQ-M/ITEM-中文"},     // 中文非法
		{p, "Y20260920/REQ-M/ISSUE-A1"},    // 前綴錯
		{p, "Y20260920/REQ-OTHER/ITEM-A1"}, // 沒掛在 parent 下
		{p, "Y20260920/REQ-M/ITEM-A1/B"},   // 多餘 '/'
	}
	for _, c := range invalid {
		if err := ValidateID(TypeItem, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("item parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}
}

func TestValidateID_UnknownType(t *testing.T) {
	if err := ValidateID(NodeType("task"), "", "X"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("未知 type 應回 ErrInvalidID，實得 %v", err)
	}
	if err := ValidateID(NodeType(""), "", "X"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("空 type 應回 ErrInvalidID，實得 %v", err)
	}
}

// ---------------------------------------------------------------------------
// GenerateID（§11.5＋§7）
// ---------------------------------------------------------------------------

func TestGenerateID(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got, err := GenerateID(TypeProject, "", "anything", "human", now)
	if err != nil || got != "Y20260920" {
		t.Errorf("project GenerateID = %q, %v；期望 Y20260920", got, err)
	}

	got, err = GenerateID(TypeReq, "Y20260916", "Member Core", "", now)
	if err != nil || got != "Y20260916/REQ-MEMBER-CORE" {
		t.Errorf("req GenerateID = %q, %v", got, err)
	}

	got, err = GenerateID(TypeIssue, "Y20260916/REQ-MEMBER-CORE", "UT90 B", "", now)
	if err != nil || got != "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-B" {
		t.Errorf("issue GenerateID = %q, %v", got, err)
	}

	got, err = GenerateID(TypeReport, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A", "週報", "xiaoxia", now)
	if want := "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A/REPORT-xiaoxia-20260920"; err != nil || got != want {
		t.Errorf("report GenerateID = %q, %v；期望 %q", got, err, want)
	}

	// 生成結果必須能通過自家 ValidateID（round-trip）。
	roundtrip := []struct {
		typ    NodeType
		parent string
		title  string
		owner  string
	}{
		{TypeReq, "Y20260916", "Member Core", ""},
		{TypeIssue, "Y20260916/REQ-MEMBER-CORE", "Conc 03: admin BFF", ""},
		{TypeReport, "Y20260916/REQ-MEMBER-CORE/ISSUE-UT90-A", "whatever", "kaimake"},
	}
	for _, c := range roundtrip {
		id, err := GenerateID(c.typ, c.parent, c.title, c.owner, now)
		if err != nil {
			t.Errorf("GenerateID(%v) 失敗：%v", c.typ, err)
			continue
		}
		if err := ValidateID(c.typ, c.parent, id); err != nil {
			t.Errorf("GenerateID 產出 %q 過不了自家 ValidateID：%v", id, err)
		}
	}
}

func TestGenerateID_Errors(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	if _, err := GenerateID(TypeProject, "Y20260916", "t", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("project 帶 parent 應回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeReq, "", "title", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("req 空 parent 應回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeIssue, "", "title", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("issue 空 parent 應回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeReport, "", "t", "xiaoxia", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("report 空 parent 應回 ErrInvalidID，實得 %v", err)
	}
	// slug 化後為空（中文／全符號標題）→ 拒絕，不發空 KEY。
	if _, err := GenerateID(TypeReq, "Y20260916", "中文", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("req 全中文標題應回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeIssue, "Y20260916/REQ-A", "---", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("issue 空 slug 應回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeReport, "P", "t", "bob", now); !errors.Is(err, ErrInvalidOwner) {
		t.Errorf("report 非名冊 owner 應回 ErrInvalidOwner，實得 %v", err)
	}
	if _, err := GenerateID(TypeReport, "P", "t", "", now); !errors.Is(err, ErrInvalidOwner) {
		t.Errorf("report 空 owner 應回 ErrInvalidOwner，實得 %v", err)
	}
	if _, err := GenerateID(NodeType("task"), "", "t", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("未知 type 應回 ErrInvalidID，實得 %v", err)
	}
}

// item 不支援自動產生：母表編號 KEY 由呼叫端顯式給 id。
func TestGenerateID_Item(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	if _, err := GenerateID(TypeItem, "Y20260920/REQ-M", "A1 登入", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("item 應一律回 ErrInvalidID，實得 %v", err)
	}
	if _, err := GenerateID(TypeItem, "", "t", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("item 空 parent 也應回 ErrInvalidID，實得 %v", err)
	}
}

// ---------------------------------------------------------------------------
// 資料驅動 type（node_types）與 bug／plan 形狀
// ---------------------------------------------------------------------------

func TestValidateID_BugAndPlan(t *testing.T) {
	req := "Y20260920/REQ-V05-X"
	for _, id := range []string{req + "/BUG-LOGIN-CRASH", req + "/BUG-A"} {
		if err := ValidateID(TypeBug, req, id); err != nil {
			t.Errorf("bug %q 應合法：%v", id, err)
		}
	}
	invalidBug := []struct{ parent, id string }{
		{"", req + "/BUG-A"},
		{req, ""},
		{req, req + "/BUG-"},
		{req, req + "/BUG-a"},
		{req, req + "/ISSUE-A"},
		{req, req + "/BUG-A/B"},
	}
	for _, c := range invalidBug {
		if err := ValidateID(TypeBug, c.parent, c.id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("bug parent=%q id=%q 應回 ErrInvalidID，實得 %v", c.parent, c.id, err)
		}
	}

	proj := "Y20260920"
	if err := ValidateID(TypePlan, proj, proj+"/PLAN-ROADMAP"); err != nil {
		t.Errorf("plan 應合法：%v", err)
	}
	if err := ValidateID(TypePlan, proj, proj+"/BUG-A"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("plan 前綴錯應回 ErrInvalidID，實得 %v", err)
	}
	if err := ValidateID(TypePlan, "", "PLAN-A"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("plan 缺 parent 應回 ErrInvalidID，實得 %v", err)
	}
}

func TestGenerateID_BugAndPlan(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	req := "Y20260920/REQ-V05-X"
	got, err := GenerateID(TypeBug, req, "Login Crash", "", now)
	if err != nil || got != req+"/BUG-LOGIN-CRASH" {
		t.Errorf("bug GenerateID = %q, %v", got, err)
	}
	got, err = GenerateID(TypePlan, "Y20260920", "Roadmap 2026", "", now)
	if err != nil || got != "Y20260920/PLAN-ROADMAP-2026" {
		t.Errorf("plan GenerateID = %q, %v", got, err)
	}
	// 生成結果須過自家 ValidateID（round-trip）
	for _, c := range []struct {
		typ           NodeType
		parent, title string
	}{{TypeBug, req, "Crash on login"}, {TypePlan, "Y20260920", "Roadmap"}} {
		id, err := GenerateID(c.typ, c.parent, c.title, "", now)
		if err != nil {
			t.Errorf("GenerateID(%v): %v", c.typ, err)
			continue
		}
		if err := ValidateID(c.typ, c.parent, id); err != nil {
			t.Errorf("GenerateID 產出 %q 過不了 ValidateID：%v", id, err)
		}
	}
	if _, err := GenerateID(TypePlan, "", "Roadmap", "", now); !errors.Is(err, ErrInvalidID) {
		t.Errorf("plan 空 parent = %v, want ErrInvalidID", err)
	}
}

func TestTypeRegistry(t *testing.T) {
	reg, err := NewTypeRegistry([]TypeDef{
		{Key: "x", IDPrefix: "X", IDShape: ShapeSlug, Label: "X", Sort: 1},
		{Key: "y", IDPrefix: "Y", IDShape: ShapeProjectDate, Label: "Y", Sort: 2},
	})
	if err != nil {
		t.Fatalf("NewTypeRegistry: %v", err)
	}
	if d, ok := reg.Lookup("x"); !ok || d.IDPrefix != "X" || d.IDShape != ShapeSlug {
		t.Errorf("Lookup(x) = %+v, %v", d, ok)
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Error("Lookup(nope) 應回 false")
	}
	if _, err := NewTypeRegistry([]TypeDef{{Key: "", IDShape: ShapeSlug}}); err == nil {
		t.Error("空 key 應回錯")
	}
	if _, err := NewTypeRegistry([]TypeDef{
		{Key: "x", IDPrefix: "X", IDShape: ShapeSlug},
		{Key: "x", IDPrefix: "X2", IDShape: ShapeSlug},
	}); err == nil {
		t.Error("重複 key 應回錯")
	}

	// 空 registry：任何 type 都查不到 → ErrInvalidID（存在性＝查表）
	empty, err := NewTypeRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := empty.ValidateID(TypeReq, "p", "p/REQ-A"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("空 registry ValidateID = %v, want ErrInvalidID", err)
	}
	if _, err := empty.GenerateID(TypeReq, "p", "t", "", time.Now()); !errors.Is(err, ErrInvalidID) {
		t.Errorf("空 registry GenerateID = %v, want ErrInvalidID", err)
	}
}

func TestBuiltinTypeDefs(t *testing.T) {
	defs := BuiltinTypeDefs()
	if len(defs) != 7 {
		t.Fatalf("BuiltinTypeDefs 筆數 = %d, want 7", len(defs))
	}
	shapes := map[IDShape]bool{
		ShapeProjectDate: true, ShapeSlug: true, ShapeItemKey: true, ShapeReportOwnerDate: true,
	}
	seen := map[NodeType]bool{}
	for _, d := range defs {
		if seen[d.Key] {
			t.Errorf("重複 type %q", d.Key)
		}
		seen[d.Key] = true
		if !shapes[d.IDShape] {
			t.Errorf("type %q 形狀 %q 非固定形狀庫成員", d.Key, d.IDShape)
		}
		if d.IDShape == ShapeSlug && d.IDPrefix == "" {
			t.Errorf("type %q 用 slug 形狀但 prefix 為空", d.Key)
		}
		if d.Label == "" {
			t.Errorf("type %q label 為空", d.Key)
		}
	}
	for _, d := range defs {
		switch d.Key {
		case TypeBug:
			if d.ParentType != TypeReq {
				t.Errorf("bug parent_type = %q, want req", d.ParentType)
			}
		case TypePlan, TypeReq:
			if d.ParentType != "" {
				t.Errorf("%s parent_type = %q, want 空", d.Key, d.ParentType)
			}
		}
	}
}

// TestStatusDefs：顯示 metadata 7 筆、依 sort、值與 dashboard.html 舊 var STATUS 一字一致
// （V05-STATUS-META-API：搬家不是改內容）。
func TestStatusDefs(t *testing.T) {
	defs := StatusDefs()
	if len(defs) != 7 {
		t.Fatalf("StatusDefs 筆數 = %d, want 7", len(defs))
	}
	want := []StatusDef{
		{Key: StatusTodo, Label: "未開始", Icon: "○", Color: "#5f6368", Sort: 1},
		{Key: StatusInProgress, Label: "進行中", Icon: "◐", Color: "#1a73e8", Sort: 2},
		{Key: StatusReview, Label: "待驗收", Icon: "◑", Color: "#f9ab00", Sort: 3},
		{Key: StatusBlocked, Label: "卡住", Icon: "●", Color: "#d93025", Sort: 4},
		{Key: StatusHold, Label: "暫緩", Icon: "◌", Color: "#80868b", Sort: 5},
		{Key: StatusDone, Label: "完成", Icon: "✔", Color: "#188038", Sort: 6},
		{Key: StatusCancel, Label: "不做", Icon: "✕", Color: "#9aa0a6", Sort: 7},
	}
	seen := map[Status]bool{}
	for i, d := range defs {
		if d != want[i] {
			t.Errorf("StatusDefs[%d] = %+v, want %+v", i, d, want[i])
		}
		if d.Sort != i+1 {
			t.Errorf("StatusDefs[%d].Sort = %d, want %d（依 sort 排序）", i, d.Sort, i+1)
		}
		if seen[d.Key] {
			t.Errorf("重複 status %q", d.Key)
		}
		seen[d.Key] = true
	}
	// 7 種狀態一個都不能漏（狀態機常數的完整集合）。
	for _, s := range []Status{
		StatusTodo, StatusInProgress, StatusReview, StatusBlocked,
		StatusHold, StatusDone, StatusCancel,
	} {
		if !seen[s] {
			t.Errorf("StatusDefs 缺 %q", s)
		}
	}
}

// TestTabDefs：dashboard 分頁顯示 metadata 4 筆、依 sort、值與舊 var TAB_LABEL 一字一致。
func TestTabDefs(t *testing.T) {
	defs := TabDefs()
	if len(defs) != 4 {
		t.Fatalf("TabDefs 筆數 = %d, want 4", len(defs))
	}
	want := []TabDef{
		{Key: "focus", Label: "焦點", Sort: 1},
		{Key: "progress", Label: "進度", Sort: 2},
		{Key: "checklist", Label: "母表", Sort: 3},
		{Key: "weekly", Label: "週報", Sort: 4},
	}
	seen := map[string]bool{}
	for i, d := range defs {
		if d != want[i] {
			t.Errorf("TabDefs[%d] = %+v, want %+v", i, d, want[i])
		}
		if d.Sort != i+1 {
			t.Errorf("TabDefs[%d].Sort = %d, want %d（依 sort 排序）", i, d.Sort, i+1)
		}
		if seen[d.Key] {
			t.Errorf("重複 tab %q", d.Key)
		}
		seen[d.Key] = true
	}
}

// ---------------------------------------------------------------------------
// IsSelfVerified（§11.2）
// ---------------------------------------------------------------------------

func TestIsSelfVerified(t *testing.T) {
	if !IsSelfVerified("xiaoxia", "xiaoxia") {
		t.Error("actor==owner 應回 true")
	}
	if IsSelfVerified("xiaoxia", "kaimake") {
		t.Error("actor!=owner 應回 false")
	}
	if !IsSelfVerified("", "") {
		t.Error("雙空字串按 == 語意應回 true；store 層禁空 actor，本函式不代勞、不要過度設計")
	}
}

// ---------------------------------------------------------------------------
// sentinel errors 去重：store／CLI／MCP 靠 errors.Is 判斷，訊息改了也不能斷。
// ---------------------------------------------------------------------------

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{ErrInvalidOwner, ErrIllegalTransition, ErrMissingBlockReason, ErrInvalidID}
	for i, a := range errs {
		if a == nil {
			t.Fatalf("sentinel error %d 為 nil", i)
		}
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d 與 %d 互相 Is，應互斥", i, j)
			}
		}
	}
}
