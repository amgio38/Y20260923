package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"project_board/internal/domain"
)

// B1 驗收核心：每一種寫入都在「同一個交易」內留 history。
// 本檔以表驅動方式，對每種寫入量測「目標節點 history 筆數 跑之前 vs 跑之後」的差，
// 並斷言最新一筆的 action／field／from→to／note。

// historyCoverageWant 是一列期望。
type historyCoverageWant struct {
	n      int                  // 這次寫入新增幾筆（0＝不該留）
	act    domain.HistoryAction // 最新一筆的 action（空＝不檢查）
	fld    string               // 最新一筆的 field（空＝不檢查）
	from   string               // 最新一筆的 from（"$id"＝代入本 case 的節點 id）
	to     string               // 最新一筆的 to
	note   string               // 最新一筆的 note
	fields []string             // 非空＝這 n 筆的 field 集合（排序後全等）
}

func TestHistoryCoverage(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	_, req, _ := fixtureTree(t, s)

	var unlinkID int64

	cases := []struct {
		name    string
		measure func(id string) string // 量 history 的節點；nil＝本 case 的節點
		prepare func(t *testing.T, id string)
		run     func(t *testing.T, id string)
		want    historyCoverageWant
	}{
		{
			name: "update 單欄位",
			run: func(t *testing.T, id string) {
				title := "改過"
				if _, err := s.Update(bg, "human", id, UpdateInput{Title: &title}, nil); err != nil {
					t.Fatalf("Update: %v", err)
				}
			},
			want: historyCoverageWant{n: 1, act: domain.ActionUpdate, fld: "title", from: "HC", to: "改過"},
		},
		{
			name: "update 三欄位 → 3 筆",
			run: func(t *testing.T, id string) {
				title, owner, prio := "T2", "xiaoxia", "high"
				if _, err := s.Update(bg, "human", id, UpdateInput{Title: &title, Owner: &owner, Priority: &prio}, nil); err != nil {
					t.Fatalf("Update: %v", err)
				}
			},
			want: historyCoverageWant{n: 3, act: domain.ActionUpdate, fields: []string{"owner", "priority", "title"}},
		},
		{
			name: "update 改成相同值 → 0 筆",
			run: func(t *testing.T, id string) {
				same := "HC"
				if _, err := s.Update(bg, "human", id, UpdateInput{Title: &same}, nil); err != nil {
					t.Fatalf("Update: %v", err)
				}
			},
			want: historyCoverageWant{n: 0},
		},
		{
			name: "transition",
			run: func(t *testing.T, id string) {
				mustTransition(t, s, "human", id, domain.StatusInProgress, "")
			},
			want: historyCoverageWant{n: 1, act: domain.ActionTransition, fld: "status", from: "todo", to: "in_progress"},
		},
		{
			name: "assign",
			run: func(t *testing.T, id string) {
				mustAssign(t, s, "human", id, "xiaoxia")
			},
			want: historyCoverageWant{n: 1, act: domain.ActionAssign, fld: "owner", from: "unassigned", to: "xiaoxia"},
		},
		{
			name: "verify",
			prepare: func(t *testing.T, id string) {
				mustTransition(t, s, "human", id, domain.StatusInProgress, "")
				mustTransition(t, s, "human", id, domain.StatusReview, "")
			},
			run: func(t *testing.T, id string) {
				if _, err := s.Verify(bg, "claude", id, "CR 過"); err != nil {
					t.Fatalf("Verify: %v", err)
				}
			},
			want: historyCoverageWant{n: 1, act: domain.ActionVerify, fld: "status", from: "review", to: "done", note: "CR 過"},
		},
		{
			name: "comment",
			run: func(t *testing.T, id string) {
				if err := s.Comment(bg, "human", id, "一句話"); err != nil {
					t.Fatalf("Comment: %v", err)
				}
			},
			want: historyCoverageWant{n: 1, act: domain.ActionComment, note: "一句話"},
		},
		{
			name: "link",
			run: func(t *testing.T, id string) {
				mustLink(t, s, "human", id, domain.LinkPR, "https://example.com/pr/1", "PR")
			},
			want: historyCoverageWant{n: 1, act: domain.ActionLink, fld: "pr", to: "https://example.com/pr/1", note: "PR"},
		},
		{
			name: "unlink",
			prepare: func(t *testing.T, id string) {
				l := mustLink(t, s, "human", id, domain.LinkCommit, "abc1234", "")
				unlinkID = l.ID
			},
			run: func(t *testing.T, id string) {
				if err := s.Unlink(bg, "human", unlinkID); err != nil {
					t.Fatalf("Unlink: %v", err)
				}
			},
			want: historyCoverageWant{n: 1, act: domain.ActionUnlink, fld: "commit", from: "abc1234"},
		},
		{
			name:    "delete：事件記在存活的 parent",
			measure: func(string) string { return req.ID },
			run: func(t *testing.T, id string) {
				if err := s.Delete(bg, "human", id); err != nil {
					t.Fatalf("Delete: %v", err)
				}
			},
			want: historyCoverageWant{n: 1, act: actionDelete, fld: "child", from: "$id", to: "", note: "issue|HC"},
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("%s/ISSUE-HC-%d", req.ID, i)
			mustCreate(t, s, "human", CreateInput{
				Type: domain.TypeIssue, ID: id, Title: "HC", ParentID: req.ID,
			})
			// create 本身也要留一筆：每個 case 的節點基底就是 1 筆 create。
			base := mustHistory(t, s, id, 0)
			if len(base) != 1 || base[0].Action != domain.ActionCreate {
				t.Fatalf("create 應留 1 筆 create history，得到 %+v", base)
			}
			measured := id
			if tc.measure != nil {
				measured = tc.measure(id)
			}
			if tc.prepare != nil {
				tc.prepare(t, id)
			}
			before := len(mustHistory(t, s, measured, 0))
			tc.run(t, id)
			entries := mustHistory(t, s, measured, 0)
			delta := len(entries) - before
			if delta != tc.want.n {
				t.Fatalf("history delta = %d, want %d: %+v", delta, tc.want.n, entries[:max(0, delta)])
			}
			if tc.want.n == 0 {
				return
			}
			h := entries[0]
			wantFrom := tc.want.from
			if wantFrom == "$id" {
				wantFrom = id
			}
			if tc.want.act != "" && h.Action != tc.want.act {
				t.Errorf("action = %q, want %q", h.Action, tc.want.act)
			}
			if tc.want.fld != "" && h.Field != tc.want.fld {
				t.Errorf("field = %q, want %q", h.Field, tc.want.fld)
			}
			if tc.want.from != "" && h.FromVal != wantFrom {
				t.Errorf("from = %q, want %q", h.FromVal, wantFrom)
			}
			if tc.want.to != "" && h.ToVal != tc.want.to {
				t.Errorf("to = %q, want %q", h.ToVal, tc.want.to)
			}
			if tc.want.note != "" && h.Note != tc.want.note {
				t.Errorf("note = %q, want %q", h.Note, tc.want.note)
			}
			if len(tc.want.fields) > 0 {
				got := make([]string, 0, delta)
				for _, e := range entries[:delta] {
					if e.Action != domain.ActionUpdate {
						t.Errorf("三欄位更新每筆都該是 update：%+v", e)
					}
					got = append(got, e.Field)
				}
				sort.Strings(got)
				if fmt.Sprint(got) != fmt.Sprint(tc.want.fields) {
					t.Errorf("update 欄位 = %v, want %v", got, tc.want.fields)
				}
			}
		})
	}
}

// TestCreateHistory：Create 在建節點當下、同交易留一筆 action=create。
func TestCreateHistory(t *testing.T) {
	s := newStore(t)
	_, req, _ := fixtureTree(t, s)
	n := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-HC-CREATE", Title: "新單", ParentID: req.ID,
	})
	h := mustHistory(t, s, n.ID, 0)
	if len(h) != 1 || h[0].Action != domain.ActionCreate {
		t.Fatalf("history = %+v, want 1 筆 create", h)
	}
}

// TestSeedHistory：seed 走 Create＋Transition，事件已由那兩者產生（不另寫）。
func TestSeedHistory(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	if err := s.Seed(bg, "human"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	h := mustHistory(t, s, seedProjectID, 0)
	// History 為 id DESC：最新一筆是 transition（todo→in_progress，note=seed），最舊是 create。
	if len(h) != 2 {
		t.Fatalf("seed history = %d 筆, want 2: %+v", len(h), h)
	}
	if h[0].Action != domain.ActionTransition || h[0].ToVal != string(domain.StatusInProgress) {
		t.Errorf("最新一筆 = %+v, want transition → in_progress", h[0])
	}
	if h[1].Action != domain.ActionCreate {
		t.Errorf("最舊一筆 = %+v, want create", h[1])
	}
}

// TestHookNoHistory：Hook／Unhook 是訂閱設定、不是看板內容，刻意不寫 history
// （CTO 2026-09-25 裁定 3；寫進去會讓 hook 自己觸發喚醒）。
func TestHookNoHistory(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	_, _, issue := fixtureTree(t, s)
	before := len(mustHistory(t, s, issue.ID, 0))
	mustHook(t, s, "xiaoxia", issue.ID, "yilong")
	if err := s.Unhook(bg, "xiaoxia", issue.ID, HarnessHerdr, "yilong"); err != nil {
		t.Fatalf("Unhook: %v", err)
	}
	if after := len(mustHistory(t, s, issue.ID, 0)); after != before {
		t.Errorf("Hook/Unhook 不該留 history：%d → %d", before, after)
	}
}

// TestHistoryActionsMigrationFromV6：v6 舊 DB（history.action CHECK 尚無 'delete'）升到 v7
// ——原本的 history 一筆不少、升完才寫得進 delete，且 migration 可重跑（冪等）。
func TestHistoryActionsMigrationFromV6(t *testing.T) {
	bg := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ { // 只套 0001～0006，停在 v6
		if err := s.applyMigration(bg, ms[i]); err != nil {
			t.Fatalf("套 %s: %v", ms[i].file, err)
		}
	}
	if v, _ := s.SchemaVersion(bg); v != 6 {
		t.Fatalf("前置版本 = %d, want 6", v)
	}
	_, req, issue := fixtureTree(t, s)
	mustLink(t, s, "human", req.ID, domain.LinkFile, "dev_docs/x.md", "既有")
	var before int
	if err := s.db.QueryRowContext(bg, "SELECT COUNT(*) FROM history").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("前置 history 為 0")
	}
	// v6 時 delete 應被 CHECK 擋下（證明放寬真的有作用）。
	if _, err := s.db.ExecContext(bg,
		`INSERT INTO history (node_id, ts, actor, action, field, from_val, to_val, note)
		 VALUES (?, '2026-09-20T01:00:00+08:00', 'human', 'delete', 'child', 'x', '', '')`,
		req.ID); err == nil {
		t.Fatal("v6 不該收得下 action=delete（CHECK 還沒放寬）")
	}
	if err := s.Migrate(bg); err != nil {
		t.Fatalf("Migrate 到最新版: %v", err)
	}
	if err := s.Migrate(bg); err != nil { // 冪等：可重跑
		t.Fatalf("二次 Migrate: %v", err)
	}
	if v, _ := s.SchemaVersion(bg); v != 8 {
		t.Fatalf("升級後版本 = %d, want 8", v)
	}
	var after int
	if err := s.db.QueryRowContext(bg, "SELECT COUNT(*) FROM history").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("升級後 history 筆數 %d → %d（一筆都不能少）", before, after)
	}
	// 升完之後真的寫得進 delete：刪掉乾淨的 issue，parent req 多一筆。
	if err := s.Delete(bg, "human", issue.ID); err != nil {
		t.Fatalf("v7 Delete 應可寫 delete history: %v", err)
	}
	h := mustHistory(t, s, req.ID, 1)
	if len(h) != 1 || h[0].Action != actionDelete {
		t.Fatalf("Delete 後 history = %+v, want 1 筆 delete", h)
	}
}
