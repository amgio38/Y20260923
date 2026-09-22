package importer

import (
	"testing"

	"project_board/internal/domain"
)

func planByID(items []Item) (map[string]Item, int) {
	got := map[string]Item{}
	var skips int
	for _, it := range items {
		if it.Action == "skip" {
			skips++
			continue
		}
		got[it.ID] = it
	}
	return got, skips
}

func TestPlanMemberTree(t *testing.T) {
	files := []SourceFile{
		{Name: "MEMBER_REQ20260916.md", Path: "MEMBER_REQ20260916.md", Body: "# 會員中心\n\n開工中\n"},
		{Name: "ISSUE-ADMIN-BFF-P1_OVERVIEW_20260918.md", Path: "o.md", Body: "# 總覽\n"},
		{Name: "ISSUE-ADMIN-BFF-P1-A_xiaoxia_20260918.md", Path: "a.md", Body: "# A軌\n"},
		{Name: "xiaoxia_ADMIN_BFF_P1_A_status_20260918.md", Path: "s.md", Body: "結果 ✅ 完成\n"},
		{Name: "TOTAL_CHECKLIST_TODO_REQ20260919_2027.md", Path: "c.md", Body: "# 母表\n"},
		{Name: "CACHE_HOWTO_20260918.md", Path: "h.md", Body: "# howto\n"},
		{Name: "kaimake_ROUTE01_status_20260918.md", Path: "u.md", Body: "✅\n"},
	}
	items := Plan("Y20260916", files)
	got, skips := planByID(items)
	member := got["Y20260916/REQ-MEMBER"]
	if member.Type != domain.TypeReq || member.ParentID != "Y20260916" {
		t.Fatalf("會員 req：%+v", member)
	}
	ov := got["Y20260916/REQ-MEMBER/ISSUE-ADMIN-BFF-P1-OVERVIEW"]
	if ov.ParentID != member.ID {
		t.Fatalf("overview 應掛會員 req，實得 %s", ov.ParentID)
	}
	child := got["Y20260916/REQ-MEMBER/ISSUE-ADMIN-BFF-P1-OVERVIEW/ISSUE-ADMIN-BFF-P1-A"]
	if child.ParentID != ov.ID || child.Owner != "xiaoxia" {
		t.Fatalf("分軌：%+v", child)
	}
	rep := got[child.ID+"/REPORT-xiaoxia-20260918"]
	if rep.Type != domain.TypeReport || rep.Status != domain.StatusDone {
		t.Fatalf("status：%+v", rep)
	}
	// 母表這次要收，不留到 v0.3。
	check := got["Y20260916/REQ-TOTAL-CHECKLIST"]
	if check.Type != domain.TypeReq || check.ParentID != "Y20260916" {
		t.Fatalf("母表應建成 REQ-TOTAL-CHECKLIST：%+v", check)
	}
	// 雜項進 REQ-DEVDOCS。
	dev := got["Y20260916/REQ-DEVDOCS"]
	if dev.Type != domain.TypeReq || dev.ParentID != "Y20260916" {
		t.Fatalf("雜項掛點：%+v", dev)
	}
	howto := got["Y20260916/REQ-DEVDOCS/ISSUE-CACHE-HOWTO-20260918"]
	if howto.Type != domain.TypeIssue || howto.ParentID != dev.ID {
		t.Fatalf("howto 應掛 REQ-DEVDOCS：%+v", howto)
	}
	// 對不到的 status 補父 issue 再掛 report，不准 skip。
	comp := got["Y20260916/REQ-MEMBER/ISSUE-ROUTE01"]
	if comp.Type != domain.TypeIssue || comp.ParentID != member.ID {
		t.Fatalf("補建 issue：%+v", comp)
	}
	compRep := got[comp.ID+"/REPORT-kaimake-20260918"]
	if compRep.Type != domain.TypeReport {
		t.Fatalf("補建 issue 下的 report：%+v", compRep)
	}
	if skips != 0 {
		t.Fatalf("本批應零略過，實得 %d", skips)
		for _, it := range items {
			if it.Action == "skip" {
				t.Logf("skip %s：%s", it.Title, it.Reason)
			}
		}
	}
}

// 凍結目錄的 ISSUE（無日期、無 owner 尾綴）仍要建成 issue，掛 REQ-MEMBER。
func TestPlanFrozenIssue(t *testing.T) {
	files := []SourceFile{
		{Name: "ISSUE-CACHE01_abstraction_consolidation.md", Path: "f/i.md", Body: "# 快取抽象\n"},
		{Name: "ISSUE-SEC-DPMP01_remove_hardcoded_password.md", Path: "f/j.md", Body: "# 去硬編碼\n"},
	}
	got, skips := planByID(Plan("Y20260916", files))
	for _, id := range []string{
		"Y20260916/REQ-MEMBER/ISSUE-CACHE01-ABSTRACTION-CONSOLIDATION",
		"Y20260916/REQ-MEMBER/ISSUE-SEC-DPMP01-REMOVE-HARDCODED-PASSWORD",
	} {
		it, ok := got[id]
		if !ok || it.Type != domain.TypeIssue || it.ParentID != "Y20260916/REQ-MEMBER" {
			t.Fatalf("%s：%+v", id, it)
		}
		if it.Owner != "unassigned" {
			t.Fatalf("%s owner 應為 unassigned：%+v", id, it)
		}
	}
	if skips != 0 {
		t.Fatalf("凍結 ISSUE 應零略過，實得 %d", skips)
	}
}

// 非名冊前綴的 status（pi_／shrimp_／opencode_）：補 issue＋unassigned report，不准 skip。
func TestPlanUnknownOwnerStatus(t *testing.T) {
	files := []SourceFile{
		{Name: "pi_cache_consolidation_status_20260917.md", Path: "f/p.md", Body: "進展中 🔶\n"},
		{Name: "shrimp_status_20260916.md", Path: "f/s.md", Body: "待辦\n"},
		{Name: "opencode_status_20260916.md", Path: "f/o.md", Body: "待辦\n"},
	}
	got, skips := planByID(Plan("Y20260916", files))
	for _, key := range []string{"PI-CACHE-CONSOLIDATION", "SHRIMP", "OPENCODE"} {
		comp := got["Y20260916/REQ-MEMBER/ISSUE-"+key]
		if comp.Type != domain.TypeIssue {
			t.Fatalf("補建 %s：%+v", key, comp)
		}
	}
	rep := got["Y20260916/REQ-MEMBER/ISSUE-PI-CACHE-CONSOLIDATION/REPORT-unassigned-20260917"]
	if rep.Type != domain.TypeReport || rep.Status != domain.StatusInProgress {
		t.Fatalf("pi status report：%+v", rep)
	}
	if skips != 0 {
		t.Fatalf("未知 owner status 應零略過，實得 %d", skips)
	}
}

// _status_ 只是路徑片段、沒有日期的檔（如 DIVERGENCE-006）：組不出合法 report id，改當雜項收。
func TestPlanStatusLookalikeGoesDevdocs(t *testing.T) {
	files := []SourceFile{
		{Name: "DIVERGENCE-006_nstock_status_code_dropped.md", Path: "f/d.md", Body: "# 分歧\n"},
	}
	got, skips := planByID(Plan("Y20260916", files))
	it := got["Y20260916/REQ-DEVDOCS/ISSUE-DIVERGENCE-006-NSTOCK-STATUS-CODE-DROPPED"]
	if it.Type != domain.TypeIssue {
		t.Fatalf("DIVERGENCE-006 應進 REQ-DEVDOCS：%+v", it)
	}
	if skips != 0 {
		t.Fatalf("應零略過，實得 %d", skips)
	}
}

// 根目錄雜項（AGENTS／spec／HANDOFF／rule）全進 REQ-DEVDOCS；MEMBER_REQ 仍是 REQ-MEMBER。
func TestPlanRootMisc(t *testing.T) {
	files := []SourceFile{
		{Name: "MEMBER_REQ20260916.md", Path: "MEMBER_REQ20260916.md", Body: "# 會員中心\n"},
		{Name: "AGENTS.md", Path: "AGENTS.md", Body: "# 規範\n"},
		{Name: "spec.md", Path: "spec.md", Body: "# 規格\n"},
		{Name: "HANDOFF_20260916.md", Path: "HANDOFF_20260916.md", Body: "# 交接\n"},
		{Name: "rule.md", Path: "rule.md", Body: "# 規則\n"},
		{Name: "opencode_reply_20260916.md", Path: "opencode_reply_20260916.md", Body: "回覆\n"},
	}
	got, skips := planByID(Plan("Y20260916", files))
	if got["Y20260916/REQ-MEMBER"].Type != domain.TypeReq {
		t.Fatal("MEMBER_REQ 應仍是 REQ-MEMBER")
	}
	for _, id := range []string{
		"Y20260916/REQ-DEVDOCS/ISSUE-AGENTS",
		"Y20260916/REQ-DEVDOCS/ISSUE-SPEC",
		"Y20260916/REQ-DEVDOCS/ISSUE-HANDOFF-20260916",
		"Y20260916/REQ-DEVDOCS/ISSUE-RULE",
		"Y20260916/REQ-DEVDOCS/ISSUE-OPENCODE-REPLY-20260916",
	} {
		if got[id].Type != domain.TypeIssue {
			t.Fatalf("%s：%+v", id, got[id])
		}
	}
	if skips != 0 {
		t.Fatalf("根目錄雜項應零略過，實得 %d", skips)
	}
}

// 同批內同檔名撞名（不同目錄同名）只警告，不多建。
func TestPlanDevdocsCollisionSkips(t *testing.T) {
	files := []SourceFile{
		{Name: "HANDOFF_20260916.md", Path: "a/HANDOFF_20260916.md", Body: "# 甲\n"},
		{Name: "HANDOFF_20260916.md", Path: "b/HANDOFF_20260916.md", Body: "# 乙\n"},
	}
	got, skips := planByID(Plan("Y20260916", files))
	if got["Y20260916/REQ-DEVDOCS/ISSUE-HANDOFF-20260916"].Body != "# 甲\n" {
		t.Fatal("首份應保留")
	}
	if skips != 1 {
		t.Fatalf("重複檔名應略過 1，實得 %d", skips)
	}
}

func TestInferStatusFirstMarkWins(t *testing.T) {
	if inferStatus("🟡 待命\n後來 ✅") != domain.StatusTodo {
		t.Fatal("先出現的 🟡 應是 todo")
	}
	if inferStatus("沒記號") != domain.StatusTodo {
		t.Fatal("沒記號應是 todo")
	}
}
