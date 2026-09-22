package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-HOOK —— 訂閱存 SQLite、祖先涵蓋、自己改的不叫自己、喚醒游標放 meta。

func mustHook(t *testing.T, s *Store, actor, node, target string) Hook {
	t.Helper()
	h, err := s.Hook(context.Background(), HookInput{Actor: actor, NodeID: node, Harness: HarnessHerdr, Target: target})
	if err != nil {
		t.Fatalf("Hook(%s → %s): %v", node, target, err)
	}
	return h
}

func TestHookAndHooks(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	h := mustHook(t, s, "xiaoxia", issue.ID, "yilong")
	if h.NodeID != issue.ID || h.Harness != HarnessHerdr || h.Target != "yilong" || h.Actor != "xiaoxia" {
		t.Fatalf("Hook = %+v", h)
	}
	if h.ID == 0 || h.CreatedAt.IsZero() {
		t.Errorf("Hook 應帶 id 與 created_at：%+v", h)
	}
	mustHook(t, s, "yilong", req.ID, "xiaoxia")
	mustHook(t, s, "yilong", req.ID, "kaimake")

	all, err := s.Hooks(bg, "")
	if err != nil {
		t.Fatalf("Hooks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("Hooks = %+v, want 3 筆", all)
	}
	// 排序：node_id 短的先（req 比 issue 短）
	if all[0].NodeID != req.ID || all[1].NodeID != req.ID || all[2].NodeID != issue.ID {
		t.Errorf("排序不如預期（先 node_id 再 harness／target）：%+v", all)
	}
	if all[1].Target != "xiaoxia" || all[2].Target != "yilong" {
		t.Errorf("同節點內 target 排序：%+v", all)
	}

	// 只列某節點
	only, err := s.Hooks(bg, req.ID)
	if err != nil || len(only) != 2 {
		t.Fatalf("Hooks(%s) = %+v (err=%v), want 2", req.ID, only, err)
	}
	if got, err := s.Hooks(bg, project.ID); err != nil || len(got) != 0 {
		t.Errorf("沒訂閱的節點應回空：%+v (err=%v)", got, err)
	}
}

// TestHookIdempotentAndResubscribe：同一 (node, harness, target) 重複訂閱＝更新訂閱者（不爆重複列）。
func TestHookIdempotentAndResubscribe(t *testing.T) {
	s := newStore(t)
	_, _, issue := fixtureTree(t, s)
	first := mustHook(t, s, "xiaoxia", issue.ID, "yilong")
	again := mustHook(t, s, "kaimake", issue.ID, "yilong")
	if again.ID != first.ID {
		t.Errorf("重複訂閱應保留同一列：%d → %d", first.ID, again.ID)
	}
	if again.Actor != "kaimake" {
		t.Errorf("重複訂閱應更新訂閱者：%+v", again)
	}
	if hs, _ := s.Hooks(context.Background(), issue.ID); len(hs) != 1 {
		t.Errorf("重複訂閱不該多一列：%+v", hs)
	}
}

func TestHookValidation(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	cases := []struct {
		name string
		in   HookInput
		want error
	}{
		{"缺 actor", HookInput{NodeID: issue.ID, Harness: HarnessHerdr, Target: "yilong"}, ErrMissingActor},
		{"actor 不在名冊", HookInput{Actor: "bob", NodeID: issue.ID, Harness: HarnessHerdr, Target: "yilong"}, domain.ErrInvalidOwner},
		{"缺 node_id", HookInput{Actor: "xiaoxia", Harness: HarnessHerdr, Target: "yilong"}, ErrMissingNode},
		{"harness 空", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Target: "yilong"}, ErrUnknownHarness},
		{"harness 不是 herdr", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: "slack", Target: "yilong"}, ErrUnknownHarness},
		{"target 空", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: HarnessHerdr}, ErrInvalidTarget},
		{"target 大寫", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: HarnessHerdr, Target: "Xiaoxia"}, ErrInvalidTarget},
		{"target 數字起頭", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: HarnessHerdr, Target: "1abc"}, ErrInvalidTarget},
		{"target 有底線可", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: HarnessHerdr, Target: "kai_make-1"}, nil},
		{"target 空白", HookInput{Actor: "xiaoxia", NodeID: issue.ID, Harness: HarnessHerdr, Target: "kai make"}, ErrInvalidTarget},
		{"node 不存在", HookInput{Actor: "xiaoxia", NodeID: "Y20990101/NOPE", Harness: HarnessHerdr, Target: "yilong"}, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Hook(bg, tc.in)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("應成功，得到 %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// target 長度上界 32（31 個尾巴 + 首字）
	if !hookTargetRe.MatchString("a" + strings.Repeat("b", 31)) {
		t.Error("32 字元 target 應合法")
	}
	if hookTargetRe.MatchString("a" + strings.Repeat("b", 32)) {
		t.Error("33 字元 target 應非法")
	}
}

func TestUnhook(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)
	mustHook(t, s, "xiaoxia", issue.ID, "yilong")

	if err := s.Unhook(bg, "xiaoxia", issue.ID, HarnessHerdr, "kaimake"); !errors.Is(err, ErrNotFound) {
		t.Errorf("取消不存在的訂閱 = %v, want ErrNotFound", err)
	}
	if err := s.Unhook(bg, "", issue.ID, HarnessHerdr, "yilong"); !errors.Is(err, ErrMissingActor) {
		t.Errorf("缺 actor = %v", err)
	}
	if err := s.Unhook(bg, "xiaoxia", issue.ID, "slack", "yilong"); !errors.Is(err, ErrUnknownHarness) {
		t.Errorf("harness 錯 = %v", err)
	}
	if err := s.Unhook(bg, "xiaoxia", issue.ID, HarnessHerdr, "yilong"); err != nil {
		t.Fatalf("Unhook: %v", err)
	}
	if hs, _ := s.Hooks(bg, ""); len(hs) != 0 {
		t.Errorf("取消後還有訂閱：%+v", hs)
	}
}

func TestHookCursor(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	fixtureTree(t, s)

	if cur, err := s.HookCursor(bg); err != nil || cur != 0 {
		t.Fatalf("初始游標 = %d (err=%v), want 0", cur, err)
	}
	if err := s.SetHookCursor(bg, 7); err != nil {
		t.Fatalf("SetHookCursor: %v", err)
	}
	if cur, err := s.HookCursor(bg); err != nil || cur != 7 {
		t.Fatalf("游標 = %d (err=%v), want 7", cur, err)
	}
	// 只前進不後退
	if err := s.SetHookCursor(bg, 3); err != nil {
		t.Fatalf("SetHookCursor(3): %v", err)
	}
	if cur, _ := s.HookCursor(bg); cur != 7 {
		t.Errorf("游標不該後退：%d", cur)
	}
	if err := s.SetHookCursor(bg, -1); err == nil {
		t.Error("負游標應回錯")
	}
	// meta 被寫壞 → 讀游標要回錯（不靜默歸零）
	if _, err := s.db.ExecContext(bg, "UPDATE meta SET v = 'abc' WHERE k = ?", hookCursorKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HookCursor(bg); err == nil {
		t.Error("游標非整數應回錯")
	}
}

func TestPendingHookEvents(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	// 有人訂了 REQ（旗下 ISSUE 都算）與 ISSUE 本身
	mustHook(t, s, "yilong", req.ID, "yilong") // 訂閱者＝yilong
	mustHook(t, s, "yilong", issue.ID, "kaimake")
	mustHook(t, s, "kaimake", project.ID, "kaimadi")

	// cursor 之後的異動：transition、verify（還有一個 comment 不該觸發）
	if err := s.Comment(bg, "human", issue.ID, "不該喚醒"); err != nil {
		t.Fatal(err)
	}
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "接單")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusReview, "做完")
	if _, err := s.Verify(bg, "claude", issue.ID, "CR 過"); err != nil {
		t.Fatal(err)
	}
	// 同一個節點還有別的欄位 update（field != status）→ 不該觸發
	if _, err := s.Update(bg, "human", issue.ID, UpdateInput{Title: ptrStr("改標題")}, nil); err != nil {
		t.Fatal(err)
	}

	var maxID int64
	if err := s.db.QueryRow("SELECT MAX(id) FROM history").Scan(&maxID); err != nil {
		t.Fatal(err)
	}

	events, next, err := s.PendingHookEvents(bg, 0)
	if err != nil {
		t.Fatalf("PendingHookEvents: %v", err)
	}
	if next != maxID {
		t.Errorf("新游標 = %d, want %d（全部 history 的最大 id）", next, maxID)
	}
	// 3 筆狀態異動 × 3 個訂閱（actor 都不同）－ 訂閱者 yilong 被自己的… yilong 沒動手，所以全中
	if len(events) != 9 {
		t.Fatalf("待喚醒 = %d 筆, want 9\n%+v", len(events), events)
	}
	for _, ev := range events {
		if ev.Message == "" {
			t.Errorf("訊息不該為空：%+v", ev)
		}
		if ev.Event.NodeID != issue.ID {
			t.Errorf("異動節點 = %s, want %s", ev.Event.NodeID, issue.ID)
		}
	}
	// 事件依 id 排序，每個事件配到 3 個訂閱
	if events[0].Event.ID > events[len(events)-1].Event.ID {
		t.Error("事件應依 id 遞增")
	}

	// 游標之後沒有新東西
	if again, next2, err := s.PendingHookEvents(bg, next); err != nil || len(again) != 0 || next2 != next {
		t.Errorf("游標已到頂：%+v (next=%d, err=%v)", again, next2, err)
	}
	// 從中間的游標起算：只回後面的
	mid := events[len(events)-1].Event.ID - 1
	if got, _, err := s.PendingHookEvents(bg, mid); err != nil || len(got) != 3 {
		t.Errorf("從游標 %d 起 = %d 筆 (err=%v), want 3", mid, len(got), err)
	}
}

// TestPendingHookEventsAncestor：訂 REQ 收得到旗下 ISSUE 的變動；訂別條子樹收不到。
func TestPendingHookEventsAncestor(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	otherReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-BETA", Title: "Beta", ParentID: project.ID,
	})
	otherIssue := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: otherReq.ID + "/ISSUE-B", Title: "B", ParentID: otherReq.ID,
	})
	_ = req

	mustHook(t, s, "yilong", otherReq.ID, "kaimake") // 只訂 REQ-BETA 子樹
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "別的子樹")
	if got, _, err := s.PendingHookEvents(bg, 0); err != nil || len(got) != 0 {
		t.Errorf("別的子樹不該命中：%+v (err=%v)", got, err)
	}

	mustTransition(t, s, "xiaoxia", otherIssue.ID, domain.StatusInProgress, "同子樹")
	got, _, err := s.PendingHookEvents(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event.NodeID != otherIssue.ID || got[0].Hook.NodeID != otherReq.ID {
		t.Fatalf("祖先訂閱應命中：%+v", got)
	}
	if !strings.Contains(got[0].Message, otherIssue.ID) {
		t.Errorf("訊息應含異動節點：%q", got[0].Message)
	}
}

// TestPendingHookEventsSkipSelfActor：自己改的不叫自己（history.actor == hook.actor）。
func TestPendingHookEventsSkipSelfActor(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)
	mustHook(t, s, "xiaoxia", issue.ID, "xiaoxia")
	mustHook(t, s, "yilong", issue.ID, "yilong")

	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "自己改")
	got, _, err := s.PendingHookEvents(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hook.Target != "yilong" {
		t.Fatalf("只該叫別人：%+v", got)
	}
}

// TestPendingHookEventsNoHooks：沒有訂閱時不查（游標照樣前進，之後不會補叫）。
func TestPendingHookEventsNoHooks(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)
	mustTransition(t, s, "human", issue.ID, domain.StatusInProgress, "")
	got, next, err := s.PendingHookEvents(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("沒訂閱不該有事件：%+v", got)
	}
	if next == 0 {
		t.Error("游標仍應前進到 history 的最大 id")
	}
}

// TestHookCascadeOnDelete：節點被刪，訂閱一起走（與 links 同慣例）。
func TestHookCascadeOnDelete(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)
	report := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReport, ID: issue.ID + "/REPORT-human-20260920", Title: "收工", ParentID: issue.ID,
	})
	mustHook(t, s, "xiaoxia", report.ID, "yilong")
	if err := s.Delete(bg, "human", report.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if hs, _ := s.Hooks(bg, ""); len(hs) != 0 {
		t.Errorf("節點刪除後訂閱應一起走：%+v", hs)
	}
}

func TestHookMessage(t *testing.T) {
	base := domain.HistoryEntry{NodeID: "P/REQ-A/ISSUE-B", Actor: "xiaoxia", Field: "status"}
	cases := []struct {
		name string
		in   domain.HistoryEntry
		want []string
	}{
		{"進 review", withStatus(base, domain.StatusInProgress, domain.StatusReview, ""), []string{"進 review", "請看單驗收", "xiaoxia"}},
		{"進 review 帶 note", withStatus(base, domain.StatusInProgress, domain.StatusReview, "做完"), []string{"進 review", "說明：做完"}},
		{"退回修改", withStatus(base, domain.StatusReview, domain.StatusInProgress, "測試沒過"), []string{"被退回修改", "退回原因：測試沒過", "先讀單再改"}},
		{"verify 收單", domain.HistoryEntry{
			NodeID: base.NodeID, Actor: "claude", Action: domain.ActionVerify,
			Field: "status", FromVal: string(domain.StatusReview), ToVal: string(domain.StatusDone), Note: "CR 過",
		}, []string{"已驗收成 done", "claude", "驗收說明：CR 過", "請看單"}},
		{"其他轉移", withStatus(base, domain.StatusTodo, domain.StatusInProgress, ""), []string{"todo → in_progress", "xiaoxia"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HookMessage(tc.in)
			if !strings.HasPrefix(got, "[ProjectBoard] "+base.NodeID) {
				t.Errorf("訊息應以 [ProjectBoard] <node> 開頭：%q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("訊息缺 %q：%q", w, got)
				}
			}
			if strings.Contains(got, "\n") {
				t.Errorf("訊息不該帶換行：%q", got)
			}
		})
	}
	// note 多行要壓平（herdr prompt 是單則字串）
	multi := withStatus(base, domain.StatusReview, domain.StatusInProgress, "第一行\n第二行")
	if strings.Contains(HookMessage(multi), "\n") {
		t.Errorf("多行 note 應壓平：%q", HookMessage(multi))
	}
}

// TestHookMessageEmptyNote：沒有 note 時不該留下「說明：」空殼。
func TestHookMessageEmptyNote(t *testing.T) {
	h := domain.HistoryEntry{
		NodeID: "P/REQ-A/ISSUE-B", Actor: "xiaoxia", Action: domain.ActionTransition, Field: "status",
		FromVal: string(domain.StatusTodo), ToVal: string(domain.StatusInProgress), Note: "  ",
	}
	if got := HookMessage(h); strings.Contains(got, "說明：") {
		t.Errorf("空 note 不該有說明欄：%q", got)
	}
}

func ptrStr(s string) *string { return &s }

func withStatus(h domain.HistoryEntry, from, to domain.Status, note string) domain.HistoryEntry {
	h.Action = domain.ActionTransition
	h.FromVal, h.ToVal, h.Note = string(from), string(to), note
	return h
}

// TestHookCursorTimeIndependent：SetHookCursor 只寫 meta，不動 nodes／history。
func TestHookCursorTimeIndependent(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	fixtureTree(t, s)
	var before int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM history").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHookCursor(bg, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM history").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("游標不該寫 history：%d → %d", before, after)
	}
}
