package store

import (
	"context"
	"math"
	"testing"
	"time"

	"project_board/internal/domain"
)

// setCreatedAt：直接改 DB 的 created_at，讓平均滯留天數不依賴牆鐘（REQ-V03-PROGRESS：測試不准 sleep）。
func setCreatedAt(t *testing.T, s *Store, id string, ts time.Time) {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE nodes SET created_at = ? WHERE id = ?", formatTime(ts), id)
	if err != nil {
		t.Fatalf("setCreatedAt: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("setCreatedAt(%s) rows=%d (err=%v)", id, n, err)
	}
}

// nowAt：測試用的固定時鐘（台北 +08:00）。
func nowAt(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("nowAt(%q): %v", s, err)
	}
	return ts
}

func TestStatsAvgDwellDays(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	now := nowAt(t, "2026-09-20T12:00:00+08:00")

	// xiaoxia：未結案兩張，分別滯留 1 天與 3 天 → 平均 2.0
	issue2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-TWO", Title: "Two", ParentID: req.ID, Owner: "xiaoxia",
	})
	mustAssign(t, s, "human", issue.ID, "xiaoxia")
	setCreatedAt(t, s, issue.ID, now.Add(-24*time.Hour))
	setCreatedAt(t, s, issue2.ID, now.Add(-72*time.Hour))

	// kaimake：未結案一張，滯留 12 小時 → 0.5 天
	issue3 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-THREE", Title: "Three", ParentID: req.ID, Owner: "kaimake",
	})
	setCreatedAt(t, s, issue3.ID, now.Add(-12*time.Hour))

	// unassigned：專案＋req 未結案，各 0 天（剛建立，created_at 不動）
	// → 也要出現（分母不是 0），天數是 now - created_at 的實測值，這裡只斷言「有出現」。
	// yilong：只有已結案的單 → 不該出現（分母 0，不除以零）。
	doneOne := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-DONE", Title: "Done", ParentID: req.ID, Owner: "yilong",
	})
	mustTransition(t, s, "human", doneOne.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "human", doneOne.ID, domain.StatusReview, "")
	mustTransition(t, s, "human", doneOne.ID, domain.StatusDone, "收")
	cancelOne := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-CANCEL", Title: "Cancel", ParentID: req.ID, Owner: "yilong",
	})
	mustTransition(t, s, "human", cancelOne.ID, domain.StatusCancel, "")
	setCreatedAt(t, s, cancelOne.ID, now.Add(-240*time.Hour))

	// 別的專案：不該進本專案的統計
	other := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeProject, ID: "Y20990101", Title: "other", Owner: "claude",
	})
	otherReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: other.ID + "/REQ-X", Title: "X", ParentID: other.ID, Owner: "claude",
	})
	otherIssue := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: otherReq.ID + "/ISSUE-X", Title: "X1", ParentID: otherReq.ID, Owner: "claude",
	})
	setCreatedAt(t, s, otherIssue.ID, now.Add(-96*time.Hour))

	st, err := s.StatsAt(bg, project.ID, now)
	if err != nil {
		t.Fatalf("StatsAt: %v", err)
	}
	if got := st.AvgDwellDays["xiaoxia"]; got != 2.0 {
		t.Errorf("xiaoxia 平均滯留 = %v, want 2.0", got)
	}
	if got := st.AvgDwellDays["kaimake"]; got != 0.5 {
		t.Errorf("kaimake 平均滯留 = %v, want 0.5", got)
	}
	if _, ok := st.AvgDwellDays["yilong"]; ok {
		t.Error("yilong 只有已結案的單，不該出現（分母 0）")
	}
	if _, ok := st.AvgDwellDays["claude"]; ok {
		t.Error("別的專案的 owner 不該出現")
	}
	if st.CountByOwner["xiaoxia"] != 2 || st.CountByOwner["yilong"] != 0 {
		t.Errorf("CountByOwner = %+v（張數不該被滯留天數改掉）", st.CountByOwner)
	}

	// 全庫：claude 的單（別專案）要出現
	all, err := s.StatsAt(bg, "", now)
	if err != nil {
		t.Fatalf("StatsAt 全庫: %v", err)
	}
	if _, ok := all.AvgDwellDays["claude"]; !ok {
		t.Error("全庫時 claude 應出現")
	}
	if _, ok := all.CountByOwner["unassigned"]; !ok {
		t.Logf("unassigned 未結案張數 = %d", all.CountByOwner["unassigned"])
	}
}

// TestStatsAvgDwellDaysRounding：「一位小數」用四捨五入；未結案不足 0.05 天也仍要出現（不是 0 張）。
func TestStatsAvgDwellDaysRounding(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)
	now := nowAt(t, "2026-09-20T12:00:00+08:00")

	a := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-A", Title: "A", ParentID: req.ID, Owner: "xiaoxia",
	})
	b := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-B", Title: "B", ParentID: req.ID, Owner: "xiaoxia",
	})
	c := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-C", Title: "C", ParentID: req.ID, Owner: "xiaoxia",
	})
	// 1 天 + 1 天 + 2 天 = 4/3 = 1.333… → 1.3
	setCreatedAt(t, s, a.ID, now.Add(-24*time.Hour))
	setCreatedAt(t, s, b.ID, now.Add(-24*time.Hour))
	setCreatedAt(t, s, c.ID, now.Add(-48*time.Hour))

	st, err := s.StatsAt(bg, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.AvgDwellDays["xiaoxia"]; math.Abs(got-1.3) > 1e-9 {
		t.Errorf("平均 = %v, want 1.3（一位小數）", got)
	}
	// 1 小時 = 0.0416… 天 → 四捨五入 0.0，但 owner 仍在 map 裡
	setCreatedAt(t, s, a.ID, now.Add(-time.Hour))
	setCreatedAt(t, s, b.ID, now.Add(-time.Hour))
	setCreatedAt(t, s, c.ID, now.Add(-time.Hour))
	st, err = s.StatsAt(bg, "", now)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st.AvgDwellDays["xiaoxia"]
	if !ok || got != 0.0 {
		t.Errorf("xiaoxia = %v (存在=%v), want 0.0 且存在", got, ok)
	}
}

// TestStatsAvgDwellDaysEmptyDB：空庫不得除以零（trace 只是記錄，不出現 panic／NaN）。
func TestStatsAvgDwellDaysEmptyDB(t *testing.T) {
	s := newStore(t)
	st, err := s.StatsAt(context.Background(), "", nowAt(t, "2026-09-20T12:00:00+08:00"))
	if err != nil {
		t.Fatalf("StatsAt: %v", err)
	}
	if len(st.AvgDwellDays) != 0 {
		t.Errorf("空庫 AvgDwellDays = %+v, want 空", st.AvgDwellDays)
	}
}

// TestStatsUsesWallClock：Stats（v0.2 簽名）＝ StatsAt(…, nowSec())，兩者對同一顆庫要同值。
func TestStatsUsesWallClock(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-A", Title: "A", ParentID: req.ID, Owner: "xiaoxia",
	})
	st, err := s.Stats(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.AvgDwellDays) == 0 {
		t.Fatalf("AvgDwellDays = %+v, want 至少 xiaoxia", st.AvgDwellDays)
	}
	if d, ok := st.AvgDwellDays["xiaoxia"]; !ok || d < 0 || d > 0.1 {
		t.Errorf("剛建立的單滯留 = %v 天 (存在=%v), want ~0", d, ok)
	}
}

// round1 直接驗（含負數：時鐘被往回撥時不做特別處理，只是四捨五入）。
func TestRound1(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{1.25, 1.3}, {1.24, 1.2}, {2.0, 2.0}, {0.0, 0.0}, {-1.25, -1.3}, {1.349999, 1.3},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
