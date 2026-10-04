package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"project_board/internal/domain"
)

// ---------- MoveNode（Y20260920/REQ-MOVE-NODE/ISSUE-MOVE-NODE-CORE）----------

// moveFixture：建兩個專案 P1／P2，P1 下有一棵 REQ-SUB 子樹（含子孫、link、comment、hook）。
func moveFixture(t *testing.T, s *Store) (p1, p2, req domain.Node) {
	t.Helper()
	bg := context.Background()
	p1 = mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20261001", Title: "P1", Owner: "human"})
	p2 = mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20261002", Title: "P2", Owner: "human"})
	req = mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: p1.ID + "/REQ-SUB", Title: "Sub", ParentID: p1.ID, Owner: "xiaoxia",
	})
	iss1 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-ONE", Title: "One", ParentID: req.ID,
	})
	iss2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-TWO", Title: "Two", ParentID: req.ID,
	})
	// 子孫的下游：report（id_shape=report_owner_date）。
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReport, ID: iss1.ID + "/REPORT-xiaoxia-20261001", Title: "R", ParentID: iss1.ID,
	})
	// 連結：commit（字串 target）＋ depends_on（節點 id）＋ 一個外部 depends_on 指向子樹。
	if _, err := s.Link(bg, "human", iss1.ID, domain.LinkCommit, "abc1234", "收單"); err != nil {
		t.Fatalf("Link commit: %v", err)
	}
	if _, err := s.Link(bg, "human", iss2.ID, domain.LinkDependsOn, iss1.ID, "等它"); err != nil {
		t.Fatalf("Link depends_on: %v", err)
	}
	// 留言 ＋ hook（訂在 req 與子節點上）。
	if err := s.Comment(bg, "human", iss1.ID, "進度回報"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	for _, nodeID := range []string{req.ID, iss1.ID} {
		if _, err := s.Hook(bg, HookInput{Actor: "xiaoxia", NodeID: nodeID, Harness: HarnessHerdr, Target: "kelaode"}); err != nil {
			t.Fatalf("Hook %s: %v", nodeID, err)
		}
	}
	return p1, p2, req
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func allNodeIDs(t *testing.T, s *Store) []string {
	t.Helper()
	nodes, err := s.allNodes(context.Background())
	if err != nil {
		t.Fatalf("allNodes: %v", err)
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestMoveNodeSubtreeRewritesAllReferences(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	p1, p2, req := moveFixture(t, s)

	newRoot := p2.ID + "/REQ-SUB"
	wantMoved := map[string]string{
		req.ID:                newRoot,
		req.ID + "/ISSUE-ONE": newRoot + "/ISSUE-ONE",
		req.ID + "/ISSUE-TWO": newRoot + "/ISSUE-TWO",
		req.ID + "/ISSUE-ONE/REPORT-xiaoxia-20261001": newRoot + "/ISSUE-ONE/REPORT-xiaoxia-20261001",
	}
	nodesBefore, historyBefore := countRows(t, s, "nodes"), countRows(t, s, "history")
	linksBefore, hooksBefore := countRows(t, s, "links"), countRows(t, s, "hooks")

	res, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "跨專案搬移（測試）", nil)
	if err != nil {
		t.Fatalf("MoveNode: %v", err)
	}
	if res.OldID != req.ID || res.NewID != newRoot {
		t.Fatalf("res old/new = %q/%q, want %q/%q", res.OldID, res.NewID, req.ID, newRoot)
	}
	if len(res.Moved) != len(wantMoved) {
		t.Fatalf("moved 數量 = %d, want %d（%v）", len(res.Moved), len(wantMoved), res.Moved)
	}
	for oldID, want := range wantMoved {
		if got := res.Moved[oldID]; got != want {
			t.Errorf("moved[%q] = %q, want %q", oldID, got, want)
		}
	}
	// 狀態／owner 原樣保留。
	if res.Node.Status != domain.StatusTodo || res.Node.Owner != "xiaoxia" || res.Node.ParentID != p2.ID {
		t.Fatalf("搬後欄位跑掉：%+v", res.Node)
	}

	// 新位置查得到；舊 id 由 Get 解析到新位置。
	n, _, _, err := s.Get(bg, newRoot)
	if err != nil || n.ID != newRoot {
		t.Fatalf("Get(新 id) = %+v, err=%v", n, err)
	}
	old, _, _, err := s.Get(bg, req.ID)
	if err != nil || old.ID != newRoot {
		t.Fatalf("Get(舊 id) = %+v, err=%v（應解析到 %s）", old, err, newRoot)
	}
	if ids := allNodeIDs(t, s); !contains(ids, newRoot) || contains(ids, req.ID) {
		t.Fatalf("nodes 表的 id 沒改寫：%v", ids)
	}

	// 數量不變。
	if got := countRows(t, s, "nodes"); got != nodesBefore {
		t.Errorf("nodes 數量 = %d, want %d", got, nodesBefore)
	}
	if got := countRows(t, s, "links"); got != linksBefore {
		t.Errorf("links 數量 = %d, want %d", got, linksBefore)
	}
	if got := countRows(t, s, "hooks"); got != hooksBefore {
		t.Errorf("hooks 數量 = %d, want %d", got, hooksBefore)
	}
	if got := countRows(t, s, "history"); got != historyBefore+1 {
		t.Errorf("history 數量 = %d, want %d（原 + 1 筆 move）", got, historyBefore+1)
	}

	// links：from_id 與 depends_on target 都改指新 id。
	var fromID, target string
	if err := s.db.QueryRowContext(bg,
		"SELECT from_id, target FROM links WHERE kind = 'depends_on'").Scan(&fromID, &target); err != nil {
		t.Fatalf("read depends_on link: %v", err)
	}
	if fromID != newRoot+"/ISSUE-TWO" || target != newRoot+"/ISSUE-ONE" {
		t.Fatalf("depends_on link 沒改寫：from=%q target=%q", fromID, target)
	}
	// commit link 的字串 target 不該被動到。
	var commitTarget string
	if err := s.db.QueryRowContext(bg,
		"SELECT target FROM links WHERE kind = 'commit'").Scan(&commitTarget); err != nil {
		t.Fatalf("read commit link: %v", err)
	}
	if commitTarget != "abc1234" {
		t.Fatalf("commit link target 被誤改：%q", commitTarget)
	}

	// hooks：訂閱跟著節點走。
	hooks, err := s.Hooks(bg, "")
	if err != nil {
		t.Fatalf("Hooks: %v", err)
	}
	for _, h := range hooks {
		if h.NodeID == req.ID || h.NodeID == req.ID+"/ISSUE-ONE" {
			t.Fatalf("hook 沒改寫：%+v", h)
		}
	}

	// history：留言／建立事件跟著節點走，且新增一筆 move。
	hist, err := s.History(bg, newRoot, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var sawMove, sawCreate bool
	for _, e := range hist {
		if e.Action == actionMove {
			sawMove = true
			if e.FromVal != p1.ID || e.ToVal != p2.ID || e.Field != "parent" {
				t.Errorf("move 事件內容怪異：%+v", e)
			}
		}
		if e.Action == domain.ActionCreate {
			sawCreate = true
		}
	}
	if !sawMove || !sawCreate {
		t.Fatalf("history 缺 move 或 create：%+v", hist)
	}
	if got := countRows(t, s, "id_aliases"); got != len(wantMoved) {
		t.Errorf("id_aliases 數量 = %d, want %d", got, len(wantMoved))
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestMoveNodeMinimalNoOpAndValidation(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	p1, p2, req := moveFixture(t, s)
	iss1 := req.ID + "/ISSUE-ONE"

	// 搬到目前父節點＝no-op（不寫、不留 history）。
	hBefore := countRows(t, s, "history")
	res, err := s.MoveNode(bg, "xiaoxia", req.ID, p1.ID, "", nil)
	if err != nil {
		t.Fatalf("no-op move: %v", err)
	}
	if res.NewID != req.ID || len(res.Moved) != 0 {
		t.Fatalf("no-op 結果 = %+v", res)
	}
	if got := countRows(t, s, "history"); got != hBefore {
		t.Fatalf("no-op 不該寫 history：%d → %d", hBefore, got)
	}

	cases := []struct {
		name    string
		id      string
		parent  string
		note    string
		wantErr error
	}{
		{"id 不存在", p1.ID + "/REQ-NONE", p2.ID, "", ErrNotFound},
		{"parent 不存在", req.ID, p2.ID + "/REQ-NONE", "", ErrNotFound},
		{"根節點不可搬", p1.ID, p2.ID, "", ErrCannotMove},
		{"搬到自己的子孫底下", req.ID, iss1, "", ErrCannotMove},
		{"搬到自己", req.ID, req.ID, "", ErrCannotMove},
		{"跨專案缺 note", req.ID, p2.ID, "", nil}, // 佔位，下面單獨驗
	}
	for _, tc := range cases[:5] {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.MoveNode(bg, "xiaoxia", tc.id, tc.parent, tc.note, nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
	t.Run("跨專案缺 note", func(t *testing.T) {
		if _, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "", nil); err == nil {
			t.Fatal("跨專案搬移缺 note 應該回錯")
		}
	})
	t.Run("撞名", func(t *testing.T) {
		mustCreate(t, s, "human", CreateInput{
			Type: domain.TypeReq, ID: p2.ID + "/REQ-SUB", Title: "已存在", ParentID: p2.ID,
		})
		if _, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "跨專案（撞名測試）", nil); !errors.Is(err, ErrIDExists) {
			t.Fatalf("撞名 err = %v, want ErrIDExists", err)
		}
	})
	t.Run("樂觀鎖衝突", func(t *testing.T) {
		wrong := req.UpdatedAt.Add(-1)
		if _, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "跨專案", &wrong); !errors.Is(err, ErrConflict) {
			t.Fatalf("optimistic lock err = %v, want ErrConflict", err)
		}
	})
	t.Run("item 的 parent 必須是 req", func(t *testing.T) {
		item := mustCreate(t, s, "human", CreateInput{
			Type: domain.TypeItem, ID: req.ID + "/ITEM-A1", Title: "A1", ParentID: req.ID,
		})
		// 把 item 搬到專案節點底下（parent_type=req 不符）。
		if _, err := s.MoveNode(bg, "xiaoxia", item.ID, p2.ID, "", nil); !errors.Is(err, ErrItemParentNotReq) {
			t.Fatalf("item parent err = %v, want ErrItemParentNotReq", err)
		}
	})
}

func TestMoveNodeAliasChainAndDeleteCleanup(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, p2, req := moveFixture(t, s)
	p3 := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20261003", Title: "P3", Owner: "human"})
	// 一個「無 link 的子節點」用於稍後的刪除測試（有 depends_on 的節點不能刪）。
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-THREE", Title: "Three", ParentID: req.ID,
	})
	// 搬兩次：P1 → P2 → P3；舊 id 一路指向最新位置。
	if _, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "跨 1", nil); err != nil {
		t.Fatalf("move 1: %v", err)
	}
	mid := p2.ID + "/REQ-SUB"
	if _, err := s.MoveNode(bg, "xiaoxia", mid, p3.ID, "跨 2", nil); err != nil {
		t.Fatalf("move 2: %v", err)
	}
	final := p3.ID + "/REQ-SUB"
	for _, oldID := range []string{req.ID, mid} {
		n, _, _, err := s.Get(bg, oldID)
		if err != nil || n.ID != final {
			t.Fatalf("Get(%q) = %+v, err=%v（應解析到 %s）", oldID, n, err, final)
		}
	}
	// 別名都被重指到最終位置，沒有斷鏈。
	var dangling int
	if err := s.db.QueryRowContext(bg,
		"SELECT COUNT(*) FROM id_aliases WHERE new_id IN (?, ?)", req.ID, mid).Scan(&dangling); err != nil {
		t.Fatal(err)
	}
	if dangling != 0 {
		t.Fatalf("id_aliases 有指向舊位置的斷鏈：%d", dangling)
	}
	// 搬移後的節點被刪 → 相關別名一起清掉（不留指向空氣的 alias）。
	leaf := final + "/ISSUE-THREE"
	if err := s.Delete(bg, "xiaoxia", leaf); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var left int
	if err := s.db.QueryRowContext(bg,
		"SELECT COUNT(*) FROM id_aliases WHERE new_id = ?", leaf).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("刪除後仍有 %d 筆別名指向被刪節點", left)
	}
	// 被刪掉的舊 id 不該再由 Get 解析（alias 已清）→ not found。
	if _, _, _, err := s.Get(bg, leaf); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已刪節點 Get err = %v, want ErrNotFound", err)
	}
}

func TestMoveNodeRollbackOnMidWriteFault(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, p2, req := moveFixture(t, s)

	idsBefore := allNodeIDs(t, s)
	nodes, linksBefore := countRows(t, s, "nodes"), countRows(t, s, "links")
	hist, hooksBefore := countRows(t, s, "history"), countRows(t, s, "hooks")
	aliasesBefore := countRows(t, s, "id_aliases")

	boom := errors.New("注入失敗")
	moveAfterWriteFault = func() error { return boom }
	defer func() { moveAfterWriteFault = nil }()

	if _, err := s.MoveNode(bg, "xiaoxia", req.ID, p2.ID, "跨專案（rollback 測試）", nil); !errors.Is(err, boom) {
		t.Fatalf("注入的錯誤沒傳出：%v", err)
	}

	// 逐項與搬之前相同（整筆回滾）。
	if got := allNodeIDs(t, s); !equalStrings(got, idsBefore) {
		t.Fatalf("回滾後 node ids 變了：\n got=%v\nwant=%v", got, idsBefore)
	}
	if got := countRows(t, s, "nodes"); got != nodes {
		t.Errorf("nodes = %d, want %d", got, nodes)
	}
	if got := countRows(t, s, "links"); got != linksBefore {
		t.Errorf("links = %d, want %d", got, linksBefore)
	}
	if got := countRows(t, s, "history"); got != hist {
		t.Errorf("history = %d, want %d", got, hist)
	}
	if got := countRows(t, s, "hooks"); got != hooksBefore {
		t.Errorf("hooks = %d, want %d", got, hooksBefore)
	}
	if got := countRows(t, s, "id_aliases"); got != aliasesBefore {
		t.Errorf("id_aliases = %d, want %d", got, aliasesBefore)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) == len(b) {
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	return false
}

func TestMoveNodeInputValidation(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, p2, req := moveFixture(t, s)

	if _, err := s.MoveNode(bg, "", req.ID, p2.ID, "", nil); !errors.Is(err, ErrMissingActor) {
		t.Fatalf("空 actor err = %v, want ErrMissingActor", err)
	}
	if _, err := s.MoveNode(bg, "nobody", req.ID, p2.ID, "", nil); !errors.Is(err, domain.ErrInvalidOwner) {
		t.Fatalf("非法 actor err = %v, want ErrInvalidOwner", err)
	}
	if _, err := s.MoveNode(bg, "xiaoxia", "  ", p2.ID, "", nil); err == nil {
		t.Fatal("空 id 應回錯")
	}
	if _, err := s.MoveNode(bg, "xiaoxia", req.ID, "  ", "", nil); err == nil {
		t.Fatal("空 parent 應回錯")
	}
}

func TestMoveNodeTypeMismatchNonItem(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	p1, p2, req := moveFixture(t, s)
	_ = p1
	// bug 的 parent_type 是 req：搬到專案節點底下要回 ErrParentTypeMismatch（非 item 分支）。
	bug := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeBug, ID: req.ID + "/BUG-CRASH", Title: "Crash", ParentID: req.ID,
	})
	if _, err := s.MoveNode(bg, "xiaoxia", bug.ID, p2.ID, "跨專案", nil); !errors.Is(err, ErrParentTypeMismatch) {
		t.Fatalf("bug 的 parent err = %v, want ErrParentTypeMismatch", err)
	}
}

func TestResolveAliasQSelfAndMissing(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	// 自己指自己的別名（理論上不會產生，但查表要能收斂、不無限迴圈）。
	if _, err := s.db.ExecContext(bg,
		"INSERT INTO id_aliases (old_id, new_id, moved_at, actor) VALUES ('Y20260916/X', 'Y20260916/X', '', 'human')"); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveAliasQ(bg, s.db, "Y20260916/X"); err != nil || got != "Y20260916/X" {
		t.Fatalf("self alias = %q, err=%v", got, err)
	}
	if got, err := resolveAliasQ(bg, s.db, "沒這個"); err != nil || got != "沒這個" {
		t.Fatalf("missing alias = %q, err=%v", got, err)
	}
}

// chainAliases 建一條 old→…→new 的長別名鏈，測 resolveAliasQ 的迴圈上界收斂。
func TestResolveAliasQLongChainAndError(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	for i := 0; i < 70; i++ {
		if _, err := s.db.ExecContext(bg,
			"INSERT INTO id_aliases (old_id, new_id, moved_at, actor) VALUES (?, ?, '', 'human')",
			fmt.Sprintf("A%02d", i), fmt.Sprintf("A%02d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveAliasQ(bg, s.db, "A00")
	if err != nil {
		t.Fatalf("長鏈解析回錯：%v", err)
	}
	if got == "A00" {
		t.Fatalf("長鏈沒有前進：%q", got)
	}

	// 查表失敗（表被拿掉）→ 回錯而非吞掉。
	s2 := newStore(t)
	if _, err := s2.db.ExecContext(bg, "DROP TABLE id_aliases"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAliasQ(bg, s2.db, "X"); err == nil {
		t.Fatal("id_aliases 不存在時 resolveAliasQ 應回錯")
	}
	if _, err := s2.MoveNode(bg, "xiaoxia", "X", "Y", "", nil); err == nil {
		t.Fatal("查 alias 失敗時 MoveNode 應回錯")
	}

	// nodes 表被拿掉 → MoveNode 讀取節點清單即失敗。
	s3 := newStore(t)
	if _, err := s3.db.ExecContext(bg, "DROP TABLE history"); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.db.ExecContext(bg, "DROP TABLE nodes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.MoveNode(bg, "xiaoxia", "X", "Y", "", nil); err == nil {
		t.Fatal("nodes 不存在時 MoveNode 應回錯")
	}
}
