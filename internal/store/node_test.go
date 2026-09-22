package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
)

// ---------- 測試小工具 ----------

func mustTransition(t *testing.T, s *Store, actor, id string, to domain.Status, note string) domain.Node {
	t.Helper()
	n, err := s.Transition(context.Background(), actor, id, to, note, nil)
	if err != nil {
		t.Fatalf("Transition(%s → %s): %v", id, to, err)
	}
	return n
}

func mustAssign(t *testing.T, s *Store, actor, id, owner string) domain.Node {
	t.Helper()
	n, err := s.Assign(context.Background(), actor, id, owner)
	if err != nil {
		t.Fatalf("Assign(%s, %s): %v", id, owner, err)
	}
	return n
}

func mustLink(t *testing.T, s *Store, actor, fromID string, kind domain.LinkKind, target, note string) domain.Link {
	t.Helper()
	l, err := s.Link(context.Background(), actor, fromID, kind, target, note)
	if err != nil {
		t.Fatalf("Link(%s %s %s): %v", fromID, kind, target, err)
	}
	return l
}

func mustHistory(t *testing.T, s *Store, id string, limit int) []domain.HistoryEntry {
	t.Helper()
	h, err := s.History(context.Background(), id, limit)
	if err != nil {
		t.Fatalf("History(%s): %v", id, err)
	}
	return h
}

// ---------- Create ----------

func TestCreateDefaultsAndGet(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	if issue.Status != domain.StatusTodo || issue.Owner != "unassigned" || issue.Priority != domain.PriorityMedium {
		t.Errorf("預設值不對: status=%s owner=%s priority=%s", issue.Status, issue.Owner, issue.Priority)
	}
	if !issue.CreatedAt.Equal(issue.UpdatedAt) {
		t.Error("新建節點 CreatedAt 應等於 UpdatedAt")
	}
	if issue.CreatedAt.Nanosecond() != 0 {
		t.Error("時間應截到秒（否則樂觀鎖會誤判）")
	}

	n, links, children, err := s.Get(bg, req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n.ID != req.ID || n.ParentID != project.ID {
		t.Errorf("Get 回錯節點: %+v", n)
	}
	if len(links) != 0 {
		t.Errorf("links = %v, want 空", links)
	}
	if len(children) != 1 || children[0].ID != issue.ID {
		t.Errorf("children = %+v, want [%s]", children, issue.ID)
	}
	if _, _, _, err := s.Get(bg, "Y20990101/NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get 不存在 = %v, want ErrNotFound", err)
	}
}

func TestCreateGeneratedIDs(t *testing.T) {
	s := newStore(t)
	_, req, _ := fixtureTree(t, s)

	issue := mustCreate(t, s, "xiaoxia", CreateInput{
		Type: domain.TypeIssue, ParentID: req.ID, Title: "Fix Login Bug!",
	})
	if want := req.ID + "/ISSUE-FIX-LOGIN-BUG"; issue.ID != want {
		t.Errorf("自動生成 issue id = %q, want %q", issue.ID, want)
	}
	rep := mustCreate(t, s, "xiaoxia", CreateInput{Type: domain.TypeReport, ParentID: issue.ID, Title: "ignored"})
	wantRep := issue.ID + "/REPORT-unassigned-" + time.Now().In(taipei).Format("20060102")
	if rep.ID != wantRep {
		t.Errorf("自動生成 report id = %q, want %q", rep.ID, wantRep)
	}
	proj := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, Title: "ignored"})
	if want := "Y" + time.Now().In(taipei).Format("20060102"); proj.ID != want {
		t.Errorf("自動生成 project id = %q, want %q", proj.ID, want)
	}
}

func TestCreateValidations(t *testing.T) {
	s := newStore(t)
	project, req, _ := fixtureTree(t, s)
	cases := []struct {
		name    string
		in      CreateInput
		wantErr error // nil = 只要回錯就好
	}{
		{"缺 type", CreateInput{Title: "t"}, nil},
		{"缺 title", CreateInput{Type: domain.TypeProject, ID: "Y20990101"}, nil},
		{"title 全空白", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "   "}, nil},
		{"id 格式不合", CreateInput{Type: domain.TypeProject, ID: "BAD", Title: "t"}, domain.ErrInvalidID},
		{"project 帶 parent", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "t", ParentID: project.ID}, domain.ErrInvalidID},
		{"req 沒有 parent", CreateInput{Type: domain.TypeReq, ID: "REQ-X", Title: "t"}, domain.ErrInvalidID},
		{"自動生成但標題無法 slug", CreateInput{Type: domain.TypeReq, Title: "需求", ParentID: project.ID}, domain.ErrInvalidID},
		{"parent 不存在", CreateInput{Type: domain.TypeReq, ID: "Y20990101/REQ-X", Title: "t", ParentID: "Y20990101"}, ErrNotFound},
		{"ID 撞名", CreateInput{Type: domain.TypeReq, ID: req.ID, Title: "dup", ParentID: project.ID}, ErrIDExists},
		{"owner 不在名冊", CreateInput{Type: domain.TypeReq, ID: project.ID + "/REQ-O", Title: "t", ParentID: project.ID, Owner: "nobody"}, domain.ErrInvalidOwner},
		{"priority 非法", CreateInput{Type: domain.TypeReq, ID: project.ID + "/REQ-P", Title: "t", ParentID: project.ID, Priority: "urgent"}, nil},
	}
	bg := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Create(bg, "human", tc.in)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// 所有寫入函式的 actor 檢查（契約 §2 第一件事）。
func TestWriteFuncsRejectBadActor(t *testing.T) {
	s := newStore(t)
	_, req, issue := fixtureTree(t, s)
	calls := map[string]func(actor string) error{
		"Create": func(a string) error {
			_, err := s.Create(context.Background(), a, CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "t"})
			return err
		},
		"Seed": func(a string) error { return s.Seed(context.Background(), a) },
		"Update": func(a string) error {
			body := "b"
			_, err := s.Update(context.Background(), a, issue.ID, UpdateInput{Body: &body}, nil)
			return err
		},
		"Transition": func(a string) error {
			_, err := s.Transition(context.Background(), a, issue.ID, domain.StatusInProgress, "", nil)
			return err
		},
		"Assign": func(a string) error {
			_, err := s.Assign(context.Background(), a, issue.ID, "xiaoxia")
			return err
		},
		"Link": func(a string) error {
			_, err := s.Link(context.Background(), a, req.ID, domain.LinkFile, "f", "")
			return err
		},
		"Unlink": func(a string) error { return s.Unlink(context.Background(), a, 1) },
		"Verify": func(a string) error { _, err := s.Verify(context.Background(), a, issue.ID, "n"); return err },
		"Comment": func(a string) error {
			return s.Comment(context.Background(), a, issue.ID, "hi")
		},
		"Delete": func(a string) error { return s.Delete(context.Background(), a, issue.ID) },
	}
	for name, call := range calls {
		t.Run(name+"/缺 actor", func(t *testing.T) {
			if err := call(""); !errors.Is(err, ErrMissingActor) {
				t.Fatalf("err = %v, want ErrMissingActor", err)
			}
		})
		t.Run(name+"/actor 不在名冊", func(t *testing.T) {
			if err := call("nobody"); !errors.Is(err, domain.ErrInvalidOwner) {
				t.Fatalf("err = %v, want domain.ErrInvalidOwner", err)
			}
		})
	}
}

// parent 型別檢查改查 node_types.parent_type：bug→req；plan／req→可掛 project；
// item 保留既有 sentinel（CLI 訊息映射用）。
func TestCreateParentTypeFromRegistry(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, _ := fixtureTree(t, s)

	// bug 掛 req 下：成功，id 自動生成 <req>/BUG-<slug>
	bug := mustCreate(t, s, "kaimake", CreateInput{
		Type: domain.TypeBug, ParentID: req.ID, Title: "Login crash",
	})
	if want := req.ID + "/BUG-LOGIN-CRASH"; bug.ID != want {
		t.Errorf("bug id = %q, want %q", bug.ID, want)
	}

	// bug 掛 project 下：parent_type='req' → 拒（不再只認 item 一種特例）
	if _, err := s.Create(bg, "kaimake", CreateInput{
		Type: domain.TypeBug, ParentID: project.ID, Title: "wrong parent",
	}); !errors.Is(err, ErrParentTypeMismatch) {
		t.Errorf("bug 掛 project err = %v, want ErrParentTypeMismatch", err)
	}

	// plan 掛 project 下：成功（parent_type 空）
	plan := mustCreate(t, s, "kaimake", CreateInput{
		Type: domain.TypePlan, ParentID: project.ID, Title: "Roadmap",
	})
	if want := project.ID + "/PLAN-ROADMAP"; plan.ID != want {
		t.Errorf("plan id = %q, want %q", plan.ID, want)
	}

	// item 掛 project 下：保留既有 ErrItemParentNotReq
	if _, err := s.Create(bg, "kaimake", CreateInput{
		Type: domain.TypeItem, ID: project.ID + "/ITEM-A1", ParentID: project.ID, Title: "A1",
	}); !errors.Is(err, ErrItemParentNotReq) {
		t.Errorf("item 掛 project err = %v, want ErrItemParentNotReq", err)
	}
}

// ---------- Tree ----------

func TestTreeFiltering(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, _, _ := fixtureTree(t, s)
	req2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-BETA", Title: "Beta", ParentID: project.ID,
	})
	issue2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req2.ID + "/ISSUE-TWO", Title: "Issue two", ParentID: req2.ID,
	})
	mustAssign(t, s, "human", issue2.ID, "kaimake")
	mustTransition(t, s, "kaimake", issue2.ID, domain.StatusInProgress, "")

	ids := func(ns []domain.Node) []string {
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}

	all, err := s.Tree(bg, TreeFilter{})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("Tree 全部 = %v, want 5 顆", ids(all))
	}
	// 排序：深度優先（根在最前）
	if all[0].ID != project.ID {
		t.Errorf("第一個應為根節點，得到 %s", all[0].ID)
	}

	sub, err := s.Tree(bg, TreeFilter{Project: project.ID})
	if err != nil || len(sub) != 5 {
		t.Fatalf("Tree(project) = %v (err=%v), want 5 顆", ids(sub), err)
	}

	byStatus, err := s.Tree(bg, TreeFilter{Project: project.ID, Status: string(domain.StatusInProgress)})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(byStatus)
	// 命中 issue2，並帶上祖先 req2 與 project
	if len(got) != 3 || got[0] != project.ID {
		t.Fatalf("Tree(status) = %v, want [project req2 issue2]", got)
	}

	byOwner, err := s.Tree(bg, TreeFilter{Owner: "kaimake"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byOwner); len(got) != 3 || got[0] != project.ID {
		t.Fatalf("Tree(owner) = %v, want [project req2 issue2]", got)
	}

	byType, err := s.Tree(bg, TreeFilter{Type: domain.TypeIssue})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byType); len(got) != 5 {
		t.Fatalf("Tree(type=issue) = %v, want 2 issues + 2 reqs + 根（祖先）", got)
	}

	depth1, err := s.Tree(bg, TreeFilter{Project: project.ID, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(depth1); len(got) != 3 {
		t.Fatalf("Tree(depth=1) = %v, want project + 2 req", got)
	}

	// Tag 過濾（API_CONTRACT.md §5-1）
	tags := "member-core,pending-decision"
	if _, err := s.Update(bg, "human", req2.ID, UpdateInput{Tags: &tags}, nil); err != nil {
		t.Fatal(err)
	}
	byTag, err := s.Tree(bg, TreeFilter{Tag: "pending-decision"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byTag); len(got) != 2 || got[0] != project.ID || got[1] != req2.ID {
		t.Fatalf("Tree(tag) = %v, want [project req2]", got)
	}
	if none, err := s.Tree(bg, TreeFilter{Tag: "no-such-tag"}); err != nil || len(none) != 0 {
		t.Fatalf("Tree(tag=無) = %v (err=%v), want 空", none, err)
	}

	if _, err := s.Tree(bg, TreeFilter{Project: "Y20990101"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tree(不存在 project) = %v, want ErrNotFound", err)
	}
}

// ---------- Update ----------

func TestUpdateFieldsAndHistory(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)

	title, body, prio, tags := "Alpha v2", "正文", string(domain.PriorityHigh), "a,b"
	sortBy := 7
	got, err := s.Update(bg, "xiaoxia", req.ID, UpdateInput{
		Title: &title, Body: &body, Priority: &prio, Tags: &tags, Sort: &sortBy,
	}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != title || got.Body != body || got.Priority != domain.PriorityHigh || got.Tags != tags || got.Sort != 7 {
		t.Fatalf("更新後節點 = %+v", got)
	}
	if len(mustHistory(t, s, req.ID, 0)) != 6 { // create + 5 欄位
		t.Fatalf("history 筆數 = %d, want 6", len(mustHistory(t, s, req.ID, 0)))
	}
	h := mustHistory(t, s, req.ID, 5)
	wantFields := map[string]string{"title": req.Title, "body": "", "priority": "medium", "tags": "", "sort": "0"}
	for _, e := range h {
		if e.Action != domain.ActionUpdate {
			t.Errorf("action = %s, want update", e.Action)
		}
		if e.Actor != "xiaoxia" {
			t.Errorf("actor = %s", e.Actor)
		}
		if from, ok := wantFields[e.Field]; !ok || e.FromVal != from {
			t.Errorf("field=%s from=%q, want %q", e.Field, e.FromVal, from)
		}
	}
}

func TestUpdateNoopAndOptimisticLock(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)

	// 把 updated_at 調成固定舊值，版本比較才可預期
	setUpdatedAt(t, s, req.ID, "2020-01-01T00:00:00+08:00")
	cur, _, _, err := s.Get(bg, req.ID)
	if err != nil {
		t.Fatal(err)
	}

	// no-op：帶入同值 → 不寫 DB、不留 history、updated_at 不動
	same := cur.Title
	got, err := s.Update(bg, "xiaoxia", req.ID, UpdateInput{Title: &same}, &cur.UpdatedAt)
	if err != nil {
		t.Fatalf("no-op Update: %v", err)
	}
	if !got.UpdatedAt.Equal(cur.UpdatedAt) {
		t.Error("no-op 不應改 updated_at")
	}
	if len(mustHistory(t, s, req.ID, 0)) != 1 {
		t.Fatal("no-op 不應留 history")
	}

	// 帶對的版本 token → 通過，且 updated_at 往前
	newBody := "b1"
	upd, err := s.Update(bg, "xiaoxia", req.ID, UpdateInput{Body: &newBody}, &cur.UpdatedAt)
	if err != nil {
		t.Fatalf("樂觀鎖應通過: %v", err)
	}
	if !upd.UpdatedAt.After(cur.UpdatedAt) {
		t.Errorf("updated_at 應往前: %v → %v", cur.UpdatedAt, upd.UpdatedAt)
	}
	if len(mustHistory(t, s, req.ID, 0)) != 2 {
		t.Fatal("成功的 Update 應留一筆 history")
	}

	// 舊版本 → ErrConflict，且不寫入、不留 history
	before := len(mustHistory(t, s, req.ID, 0))
	if _, err := s.Update(bg, "xiaoxia", req.ID, UpdateInput{Body: &newBody}, &cur.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if after := len(mustHistory(t, s, req.ID, 0)); after != before {
		t.Errorf("衝突不應留 history: %d → %d", before, after)
	}
	if n, _, _, _ := s.Get(bg, req.ID); !n.UpdatedAt.Equal(upd.UpdatedAt) || n.Body != newBody {
		t.Error("衝突不應改動節點")
	}
}

func TestUpdateErrors(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)
	empty, badOwner, badPrio := "   ", "nobody", "urgent"
	cases := []struct {
		name string
		id   string
		in   UpdateInput
		want error
	}{
		{"節點不存在", "Y20990101/NOPE", UpdateInput{Title: &empty}, ErrNotFound},
		{"title 清空", req.ID, UpdateInput{Title: &empty}, nil},
		{"owner 不在名冊", req.ID, UpdateInput{Owner: &badOwner}, domain.ErrInvalidOwner},
		{"priority 非法", req.ID, UpdateInput{Priority: &badPrio}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Update(bg, "human", tc.id, tc.in, nil)
			if err == nil {
				t.Fatal("want error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// ---------- Transition ----------

func TestTransitionLifecycleAndHistory(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	review := mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusReview, "")
	if review.Status != domain.StatusReview {
		t.Fatalf("status = %s", review.Status)
	}
	done := mustTransition(t, s, "claude", issue.ID, domain.StatusDone, "看過了")
	if done.Status != domain.StatusDone {
		t.Fatalf("status = %s", done.Status)
	}
	h := mustHistory(t, s, issue.ID, 0)
	if len(h) != 4 { // create + 3 transition
		t.Fatalf("history = %d, want 4", len(h))
	}
	if h[0].Action != domain.ActionTransition || h[0].FromVal != "review" || h[0].ToVal != "done" || h[0].Note != "看過了" {
		t.Errorf("最新事件 = %+v", h[0])
	}
	if h[0].Actor != "claude" || h[0].TS.IsZero() {
		t.Errorf("actor/ts 不對: %+v", h[0])
	}

	// done → in_progress（reopen）要 note
	if _, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusInProgress, "  ", nil); err == nil {
		t.Error("reopen 沒帶 note 應回錯")
	}
	re := mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "學長要求補測")
	if re.Status != domain.StatusInProgress {
		t.Fatalf("reopen 後 status = %s", re.Status)
	}
}

func TestTransitionRejectsIllegal(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	for _, to := range []domain.Status{domain.StatusDone, domain.StatusReview} {
		_, err := s.Transition(bg, "xiaoxia", issue.ID, to, "", nil)
		if !errors.Is(err, domain.ErrIllegalTransition) {
			t.Errorf("todo → %s err = %v, want ErrIllegalTransition", to, err)
		}
	}
	if n, _, _, _ := s.Get(bg, issue.ID); n.Status != domain.StatusTodo {
		t.Error("非法轉移不應改狀態")
	}
	if len(mustHistory(t, s, issue.ID, 0)) != 1 {
		t.Error("非法轉移不應留 history")
	}
	if _, err := s.Transition(bg, "xiaoxia", "Y20990101/NOPE", domain.StatusInProgress, "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在節點 err = %v, want ErrNotFound", err)
	}
}

// from==to＝no-op（CR 2026-09-20 裁示）：成功、不寫、不留 history、不檢查 note／expectedUpdatedAt。
func TestTransitionSameStatusIsNoop(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	// ❶ 一般節點：帶著「過期」的 expectedUpdatedAt 也應照樣 no-op
	stale := issue.UpdatedAt.Add(-time.Hour)
	got, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusTodo, "", &stale)
	if err != nil {
		t.Fatalf("from==to 應為 no-op: %v", err)
	}
	if got.Status != domain.StatusTodo || !got.UpdatedAt.Equal(issue.UpdatedAt) {
		t.Errorf("no-op 不應改動節點: %+v", got)
	}
	if len(mustHistory(t, s, issue.ID, 0)) != 1 {
		t.Error("no-op 不應留 history")
	}

	// ❷ blocked 節點：to=blocked 且空 note，也不該去要 block reason
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusBlocked, "等 CONC01")
	before := len(mustHistory(t, s, issue.ID, 0))
	blocked, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusBlocked, "", nil)
	if err != nil {
		t.Fatalf("blocked → blocked 應為 no-op: %v", err)
	}
	if blocked.Status != domain.StatusBlocked {
		t.Errorf("status = %s, want blocked", blocked.Status)
	}
	if after := len(mustHistory(t, s, issue.ID, 0)); after != before {
		t.Errorf("no-op 不應留 history: %d → %d", before, after)
	}

	// ❸ 真的非法跳躍仍要回 ErrIllegalTransition（不要被 no-op 分支蓋掉）
	if _, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusDone, "", nil); !errors.Is(err, domain.ErrIllegalTransition) {
		t.Errorf("blocked → done err = %v, want ErrIllegalTransition", err)
	}
}

func TestTransitionBlockedNeedsReason(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	// 1) 無 note、無 depends_on → 拒
	if _, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusBlocked, "", nil); !errors.Is(err, domain.ErrMissingBlockReason) {
		t.Fatalf("err = %v, want ErrMissingBlockReason", err)
	}
	if n, _, _, _ := s.Get(bg, issue.ID); n.Status != domain.StatusTodo {
		t.Error("被拒的轉移不應改狀態")
	}

	// 2) 有 note → 過
	blocked := mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusBlocked, "等 CONC01")
	if blocked.Status != domain.StatusBlocked {
		t.Fatalf("status = %s", blocked.Status)
	}

	// 3) 只有 note 是空白也不算
	b2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-B2", Title: "B2", ParentID: req.ID,
	})
	if _, err := s.Transition(bg, "human", b2.ID, domain.StatusBlocked, " \n ", nil); !errors.Is(err, domain.ErrMissingBlockReason) {
		t.Fatalf("空白 note err = %v, want ErrMissingBlockReason", err)
	}

	// 4) 無 note 但已有 depends_on link → 過
	b3 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-B3", Title: "B3", ParentID: req.ID,
	})
	mustLink(t, s, "human", b3.ID, domain.LinkDependsOn, project.ID, "")
	if _, err := s.Transition(bg, "human", b3.ID, domain.StatusBlocked, "", nil); err != nil {
		t.Fatalf("有 depends_on 應可轉 blocked: %v", err)
	}
}

func TestTransitionOptimisticLock(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	setUpdatedAt(t, s, issue.ID, "2020-01-01T00:00:00+08:00")
	cur, _, _, err := s.Get(bg, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := cur.UpdatedAt.Add(-time.Hour)
	if _, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusInProgress, "", &stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if n, _, _, _ := s.Get(bg, issue.ID); n.Status != domain.StatusTodo {
		t.Error("衝突不應改狀態")
	}
	if len(mustHistory(t, s, issue.ID, 0)) != 1 {
		t.Error("衝突不應留 history")
	}
	if _, err := s.Transition(bg, "xiaoxia", issue.ID, domain.StatusInProgress, "", &cur.UpdatedAt); err != nil {
		t.Fatalf("帶最新 updated_at 應通過: %v", err)
	}
}

// ---------- Assign ----------

func TestAssign(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	got := mustAssign(t, s, "human", issue.ID, "xiaoxia")
	if got.Owner != "xiaoxia" {
		t.Fatalf("owner = %s", got.Owner)
	}
	h := mustHistory(t, s, issue.ID, 1)[0]
	if h.Action != domain.ActionAssign || h.Field != "owner" || h.FromVal != "unassigned" || h.ToVal != "xiaoxia" {
		t.Errorf("assign 事件 = %+v", h)
	}
	// 同 owner → no-op
	same, err := s.Assign(bg, "human", issue.ID, "xiaoxia")
	if err != nil || !same.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("no-op assign 應不動: %v %v", err, same.UpdatedAt)
	}
	if len(mustHistory(t, s, issue.ID, 0)) != 2 {
		t.Error("no-op assign 不應留 history")
	}
	if _, err := s.Assign(bg, "human", issue.ID, "nobody"); !errors.Is(err, domain.ErrInvalidOwner) {
		t.Errorf("err = %v, want ErrInvalidOwner", err)
	}
	if _, err := s.Assign(bg, "human", "Y20990101/NOPE", "xiaoxia"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// ---------- Verify ----------

func TestVerify(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, issue := fixtureTree(t, s)

	// 非 review → 拒
	if _, err := s.Verify(bg, "xiaoxia", issue.ID, "n"); !errors.Is(err, ErrNotInReview) {
		t.Fatalf("err = %v, want ErrNotInReview", err)
	}
	// 缺 note → 拒
	if _, err := s.Verify(bg, "xiaoxia", issue.ID, "  "); err == nil {
		t.Fatal("缺 note 應回錯")
	}
	if _, err := s.Verify(bg, "xiaoxia", "Y20990101/NOPE", "n"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	// actor == owner → self-verified
	mustAssign(t, s, "human", issue.ID, "xiaoxia")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusReview, "")
	got, err := s.Verify(bg, "xiaoxia", issue.ID, "覆蓋率 91.2%")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Status != domain.StatusDone {
		t.Fatalf("status = %s", got.Status)
	}
	h := mustHistory(t, s, issue.ID, 1)[0]
	if h.Action != domain.ActionVerify || h.FromVal != "review" || h.ToVal != "done" {
		t.Errorf("verify 事件 = %+v", h)
	}
	if !strings.HasPrefix(h.Note, "[self-verified] ") || !strings.Contains(h.Note, "91.2%") {
		t.Errorf("note = %q, want [self-verified] 前綴", h.Note)
	}

	// actor != owner → 不加前綴
	other := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-V2", Title: "V2", ParentID: req.ID, Owner: "kaimake",
	})
	mustTransition(t, s, "kaimake", other.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "kaimake", other.ID, domain.StatusReview, "")
	if _, err := s.Verify(bg, "claude", other.ID, "CR 通過"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if note := mustHistory(t, s, other.ID, 1)[0].Note; note != "CR 通過" {
		t.Errorf("note = %q, want 原樣", note)
	}
}

// ---------- Comment ----------

func TestComment(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	if err := s.Comment(bg, "xiaoxia", issue.ID, " "); err == nil {
		t.Fatal("空留言應回錯")
	}
	if err := s.Comment(bg, "xiaoxia", "Y20990101/NOPE", "hi"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := s.Comment(bg, "xiaoxia", issue.ID, "先做 UT90"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	h := mustHistory(t, s, issue.ID, 1)[0]
	if h.Action != domain.ActionComment || h.Note != "先做 UT90" || h.Field != "" {
		t.Errorf("comment 事件 = %+v", h)
	}
	// 留言不動節點 updated_at
	if n, _, _, _ := s.Get(bg, issue.ID); !n.UpdatedAt.Equal(issue.UpdatedAt) {
		t.Error("留言不應改 updated_at")
	}
}

// ---------- Delete ----------

func TestDelete(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, issue := fixtureTree(t, s)

	// 有子節點 → 拒
	if err := s.Delete(bg, "human", req.ID); !errors.Is(err, ErrCannotDelete) {
		t.Fatalf("有子節點 err = %v, want ErrCannotDelete", err)
	}
	// 有出向 link → 拒
	mustLink(t, s, "human", issue.ID, domain.LinkCommit, "8094064", "")
	if err := s.Delete(bg, "human", issue.ID); !errors.Is(err, ErrCannotDelete) {
		t.Fatalf("有 link err = %v, want ErrCannotDelete", err)
	}
	// 被 depends_on 指到 → 拒
	dep := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-DEP", Title: "DEP", ParentID: req.ID,
	})
	mustLink(t, s, "human", dep.ID, domain.LinkDependsOn, issue.ID, "")
	if err := s.Delete(bg, "human", issue.ID); !errors.Is(err, ErrCannotDelete) {
		t.Fatalf("被 depends_on 指到 err = %v, want ErrCannotDelete", err)
	}

	// 乾淨葉節點（無子、無 link）可刪
	clean := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-CLEAN", Title: "Clean", ParentID: req.ID,
	})
	if err := s.Delete(bg, "human", clean.ID); err != nil {
		t.Fatalf("乾淨葉節點應可刪: %v", err)
	}
	if _, _, _, err := s.Get(bg, clean.ID); !errors.Is(err, ErrNotFound) {
		t.Error("刪除後不應取得節點")
	}

	// report 即使有 link 也可刪，且 links／history 依 CASCADE 清掉
	rep := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReport, ParentID: issue.ID, Title: "r", Owner: "xiaoxia",
	})
	mustLink(t, s, "xiaoxia", rep.ID, domain.LinkFile, "dev_docs/x.md", "")
	if err := s.Delete(bg, "human", rep.ID); err != nil {
		t.Fatalf("report 應可刪: %v", err)
	}
	for _, tc := range []struct{ table, col string }{{"links", "from_id"}, {"history", "node_id"}} {
		var n int
		if err := s.db.QueryRowContext(bg,
			"SELECT COUNT(*) FROM "+tc.table+" WHERE "+tc.col+" = ?", rep.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s 應隨節點刪除清空（CASCADE），剩 %d 筆", tc.table, n)
		}
	}

	// 不存在
	if err := s.Delete(bg, "human", "Y20990101/NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
