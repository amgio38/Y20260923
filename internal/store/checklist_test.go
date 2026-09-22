package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-CHECKLIST —— type=item 的 migration 與母表查詢。

// TestItemMigrationAllowsItem：v4 之後 nodes.type 收 'item'，但仍擋其他字串；
// 而且這是改 sqlite_master 的 DDL（不是重建表）→ 既有資料與 FTS 都必須毫髮無傷。
func TestItemMigrationAllowsItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	bg := context.Background()
	if err := s.Migrate(bg); err != nil {
		t.Fatal(err)
	}
	project, req, issue := fixtureTree(t, s)
	mustLink(t, s, "human", issue.ID, domain.LinkDependsOn, project.ID, "") // 既有關聯

	item := mustCreate(t, s, "xiaoxia", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-A1", Title: "四服務界線", ParentID: req.ID, Owner: "xiaoxia",
	})
	if item.Type != domain.TypeItem || item.Status != domain.StatusTodo {
		t.Fatalf("item = %+v", item)
	}
	// 壞 type 仍要被 DB 的 type 約束擋住（v6 起是 node_types FK）
	if err := insertBogusNode(s, req.ID+"/ITEM-Z9"); err == nil {
		t.Error("DB 的 type 約束應擋下 bogus")
	}
	// 另一條連線（模擬別的行程／新連線）也要看到放寬後的 CHECK：
	// 這正是「改 sqlite_master 後必須讓 schema cookie 變動」要驗的那件事。
	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if err := insertBogusNode(s2, req.ID+"/ITEM-Z8"); err == nil {
		t.Error("新連線也該看到放寬後的 CHECK（bogus 要失敗）")
	}
	if _, err := s2.Create(bg, "xiaoxia", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-Z8", Title: "新連線建的 item", ParentID: req.ID,
	}); err != nil {
		t.Errorf("新連線應收得下 item：%v", err)
	}
	// FTS trigger 對 item 也要生效（母表項目一樣搜得到）
	if got, err := s.SearchAdvanced(bg, "四服務界線", "", "", ""); err != nil || len(got) != 1 || got[0].ID != item.ID {
		t.Fatalf("item 應被索引：%v (err=%v)", nodeIDs(got), err)
	}
	// 既有關聯／歷史沒被帶走
	if links, err := s.ListDependsOn(bg, project.ID); err != nil || len(links) != 1 {
		t.Fatalf("既有 links 不該消失：%+v (err=%v)", links, err)
	}
	if hs, err := s.History(bg, issue.ID, 0); err != nil || len(hs) == 0 {
		t.Fatalf("既有 history 不該消失：%+v (err=%v)", hs, err)
	}
	// schema 版本與 nodes 的 DDL（v6 起 type 由 node_types FK 管，不再是 CHECK enum）
	v, err := s.SchemaVersion(bg)
	if err != nil || v != 6 {
		t.Fatalf("schema_version = %d (err=%v), want 6", v, err)
	}
	var ddl string
	if err := s.db.QueryRowContext(bg,
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='nodes'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "REFERENCES node_types(key)") {
		t.Errorf("nodes 的 type 應是 node_types FK：%s", ddl)
	}
}

// insertBogusNode：繞過 store 的驗證，直接塞一個 type 不在 CHECK 允許值的節點。
func insertBogusNode(s *Store, id string) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO nodes
		(id,type,parent_id,title,status,owner,priority,tags,body,sort,created_at,updated_at)
		VALUES (?, 'bogus', NULL, 'x', 'todo', 'human', 'medium', '', '', 0, ?, ?)`,
		id, "2026-09-20T01:00:00+08:00", "2026-09-20T01:00:00+08:00")
	return err
}

// TestItemMigrationFromV3PreservesEverything：從 v3 就地升級（模擬 PO 換板）→ 資料與關聯全在。
func TestItemMigrationFromV3PreservesEverything(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	bg := context.Background()
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // 只套 0001～0003，停在 v3
		if err := s.applyMigration(bg, ms[i]); err != nil {
			t.Fatalf("套 %s: %v", ms[i].file, err)
		}
	}
	if v, _ := s.SchemaVersion(bg); v != 3 {
		t.Fatalf("前置版本 = %d, want 3", v)
	}
	// v3 的狀態：建節點＋關聯＋事件
	project, req, issue := fixtureTree(t, s)
	mustLink(t, s, "human", issue.ID, domain.LinkDependsOn, project.ID, "既有")
	before := map[string]int{}
	for _, tbl := range []string{"nodes", "links", "history"} {
		var n int
		if err := s.db.QueryRowContext(bg, "SELECT COUNT(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		before[tbl] = n
	}
	if before["nodes"] == 0 || before["links"] == 0 || before["history"] == 0 {
		t.Fatalf("前置資料不足：%v", before)
	}
	// v3 時 item 應被 CHECK 擋下
	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-A1", Title: "A1", ParentID: req.ID,
	}); err == nil {
		t.Fatal("v3 不該收得下 item（CHECK 還沒放寬）")
	}

	if err := s.Migrate(bg); err != nil {
		t.Fatalf("Migrate 到 v4: %v", err)
	}
	for tbl, want := range before {
		var n int
		if err := s.db.QueryRowContext(bg, "SELECT COUNT(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s 筆數 %d → %d（升級不該動資料）", tbl, want, n)
		}
	}
	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-A1", Title: "A1", ParentID: req.ID,
	}); err != nil {
		t.Fatalf("v4 應收得下 item：%v", err)
	}
	if got, err := s.SearchAdvanced(bg, "Alpha", "", "", ""); err != nil {
		t.Fatalf("升級後 FTS 壞了：%v", err)
	} else if len(got) == 0 {
		t.Error("升級後舊節點應還在索引裡")
	}
}

// TestCreateItemParentMustBeReq：item 的 parent 必須是 req（domain 只看字串形狀，這條由 store 驗）。
func TestCreateItemParentMustBeReq(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: issue.ID + "/ITEM-A1", Title: "掛錯地方", ParentID: issue.ID,
	}); !errors.Is(err, ErrItemParentNotReq) {
		t.Errorf("parent 是 issue = %v, want ErrItemParentNotReq", err)
	}
	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: project.ID + "/ITEM-A1", Title: "掛錯地方", ParentID: project.ID,
	}); !errors.Is(err, ErrItemParentNotReq) {
		t.Errorf("parent 是 project = %v, want ErrItemParentNotReq", err)
	}
	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-A1", Title: "正確", ParentID: req.ID,
	}); err != nil {
		t.Errorf("parent 是 req 應成功：%v", err)
	}
	// id 形狀仍由 domain 驗（KEY 要字母＋數字）
	if _, err := s.Create(bg, "human", CreateInput{
		Type: domain.TypeItem, ID: req.ID + "/ITEM-1A", Title: "KEY 反了", ParentID: req.ID,
	}); !errors.Is(err, domain.ErrInvalidID) {
		t.Errorf("KEY 形狀錯 = %v, want domain.ErrInvalidID", err)
	}
}

func TestChecklist(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	issue2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-TWO", Title: "Two", ParentID: req.ID, Owner: "kaimake",
	})

	// 故意不照 KEY 順序建立（驗證排序是查詢層的事）
	mkItem := func(key, title, owner string, linkTo string) domain.Node {
		n := mustCreate(t, s, "human", CreateInput{
			Type: domain.TypeItem, ID: req.ID + "/ITEM-" + key, Title: title, ParentID: req.ID, Owner: owner,
		})
		if linkTo != "" {
			mustLink(t, s, "human", n.ID, domain.LinkDependsOn, linkTo, "母表索引")
		}
		return n
	}
	b1 := mkItem("B1", "慣例", "yilong", issue2.ID)
	a10 := mkItem("A10", "第十項", "human", "")
	a2 := mkItem("A2", "第二項", "xiaoxia", issue.ID)
	_ = a10

	// 別的專案：不該進本專案的母表
	otherProj := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "other"})
	otherReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: otherProj.ID + "/REQ-X", Title: "X", ParentID: otherProj.ID,
	})
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeItem, ID: otherReq.ID + "/ITEM-A1", Title: "別人的 A1", ParentID: otherReq.ID,
	})

	rows, err := s.Checklist(bg, project.ID)
	if err != nil {
		t.Fatalf("Checklist: %v", err)
	}
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	want := []string{"A2", "A10", "B1"} // 自然序：A2 在 A10 前
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v（自然序）", keys, want)
		}
	}
	// 欄位對應
	if rows[0].ItemID != a2.ID || rows[0].Title != "第二項" || rows[0].Owner != "xiaoxia" || rows[0].IssueID != issue.ID {
		t.Errorf("A2 列 = %+v", rows[0])
	}
	if rows[1].IssueID != "" || rows[1].Owner != "human" {
		t.Errorf("A10 列 = %+v", rows[1])
	}
	if rows[2].ItemID != b1.ID || rows[2].IssueID != issue2.ID || rows[2].Owner != "yilong" {
		t.Errorf("B1 列 = %+v", rows[2])
	}

	// 全庫：多一列別專案的
	all, err := s.Checklist(bg, "")
	if err != nil || len(all) != 4 {
		t.Fatalf("全庫 = %d 列 (err=%v), want 4", len(all), err)
	}
	// 沒東西的專案 → 空（不是 nil 也不是錯）
	if got, err := s.Checklist(bg, "Y20990102"); err != nil || len(got) != 0 {
		t.Errorf("空範圍 = %+v (err=%v)", got, err)
	}
}

// TestCreateItemParentMissing：parent 不存在時（比對 req 型別之前就該回 not found）。
func TestCreateItemParentMissing(t *testing.T) {
	s := newStore(t)
	_, err := s.Create(context.Background(), "human", CreateInput{
		Type: domain.TypeItem, ID: "Y20990101/REQ-X/ITEM-A1", Title: "孤兒", ParentID: "Y20990101/REQ-X",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("parent 不存在 = %v, want ErrNotFound", err)
	}
}

func TestChecklistEmptyDB(t *testing.T) {
	s := newStore(t)
	rows, err := s.Checklist(context.Background(), "")
	if err != nil {
		t.Fatalf("Checklist: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("空庫 = %+v", rows)
	}
}

func TestItemKeyAndOrder(t *testing.T) {
	if got := itemKey("Y20260916/REQ-A/ITEM-B12"); got != "B12" {
		t.Errorf("itemKey = %q", got)
	}
	if got := itemKey("不是標準形狀"); got != "不是標準形狀" {
		t.Errorf("取不到 /ITEM- 時回整串：%q", got)
	}
	cases := []struct {
		a, b string
		want bool // a < b
	}{
		{"A1", "A2", true},
		{"A2", "A10", true},
		{"A9", "B1", true},
		{"B1", "A9", false},
		{"A1", "A1", false},
		{"AB1", "B1", true}, // 字母段先比：AB < B
		{"A", "A1", false},  // 純字母排在同字母的數字之前
	}
	for _, c := range cases {
		if got := itemKeyLess(c.a, c.b); got != c.want {
			t.Errorf("itemKeyLess(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if l, n, has := splitItemKey("K12"); l != "K" || n != 12 || !has {
		t.Errorf("splitItemKey(K12) = %q %d %v", l, n, has)
	}
	if l, _, has := splitItemKey("AB"); l != "AB" || has {
		t.Errorf("splitItemKey(AB) = %q (hasNum=%v)", l, has)
	}
}
