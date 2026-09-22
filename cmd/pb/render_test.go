package main

import (
	"flag"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

func TestIconFallback(t *testing.T) {
	if got := icon(domain.Status("bogus")); got != "❔" {
		t.Errorf("icon(未知) = %q, want ❔", got)
	}
	for s, want := range statusIcon {
		if got := icon(s); got != want {
			t.Errorf("icon(%s) = %q, want %q", s, got, want)
		}
	}
}

func TestFmtTimeIsTaipeiRFC3339(t *testing.T) {
	ts := time.Date(2026, 9, 20, 1, 3, 0, 0, time.FixedZone("CST", 8*3600))
	if got := fmtTime(ts); got != "2026-09-20T01:03:00+08:00" {
		t.Errorf("fmtTime = %q", got)
	}
}

func TestToStatsJSONNilMaps(t *testing.T) {
	got := toStatsJSON(store.Stats{})
	if got.CountByStatus == nil || got.CountByOwner == nil || got.ReqProgress == nil {
		t.Fatalf("nil map 應轉成空 map: %+v", got)
	}
	if got.SelfVerifiedCount != 0 {
		t.Errorf("SelfVerifiedCount = %d", got.SelfVerifiedCount)
	}
	if got.CountByStatus["todo"] != 0 || len(got.CountByStatus) != 0 {
		t.Errorf("CountByStatus = %v", got.CountByStatus)
	}
}

func TestPrintHistoryVariants(t *testing.T) {
	ta := newTestApp(t)
	ts := time.Date(2026, 9, 20, 1, 3, 0, 0, time.FixedZone("CST", 8*3600))

	// 空 → 提示
	ta.a.printHistory(nil)
	if !strings.Contains(ta.stdout(), "（沒有事件）") {
		t.Errorf("空 history 輸出 = %q", ta.stdout())
	}

	// 只有 action（field／from→to／note 全空）
	ta.out.Reset()
	ta.a.printHistory([]domain.HistoryEntry{{TS: ts, Actor: "human", Action: domain.ActionCreate}})
	line := strings.TrimSpace(ta.stdout())
	if line != "2026-09-20T01:03:00+08:00  human  create" {
		t.Errorf("精簡列 = %q", line)
	}

	// 全欄位
	ta.out.Reset()
	ta.a.printHistory([]domain.HistoryEntry{{
		TS: ts, Actor: "xiaoxia", Action: domain.ActionTransition,
		Field: "status", FromVal: "todo", ToVal: "in_progress", Note: "開工",
	}})
	line = strings.TrimSpace(ta.stdout())
	for _, want := range []string{"status", "todo → in_progress", "開工"} {
		if !strings.Contains(line, want) {
			t.Errorf("列 %q 缺 %q", line, want)
		}
	}
}

func TestPrintNodeVariants(t *testing.T) {
	ta := newTestApp(t)
	ts := time.Date(2026, 9, 20, 1, 3, 0, 0, time.FixedZone("CST", 8*3600))

	// 根節點（無 parent）、無 body、無 link／子節點
	ta.a.printNode(domain.Node{
		ID: "Y20260916", Type: domain.TypeProject, Title: "專案", Status: domain.StatusTodo,
		Owner: "human", Priority: domain.PriorityMedium, CreatedAt: ts, UpdatedAt: ts,
	}, nil, nil)
	out := ta.stdout()
	if strings.Contains(out, "Parent   :") {
		t.Errorf("根節點不應印 Parent：\n%s", out)
	}
	if strings.Contains(out, "body：") {
		t.Errorf("無 body 不應印 body 區塊：\n%s", out)
	}
	if !strings.Contains(out, "關聯 (0)：") || !strings.Contains(out, "子節點 (0)：") {
		t.Errorf("應印空關聯／子節點：\n%s", out)
	}

	// 有 parent、tags、body、link、子節點
	ta.out.Reset()
	ta.a.printNode(domain.Node{
		ID: "Y20260916/REQ-A", Type: domain.TypeReq, ParentID: "Y20260916", Title: "需求",
		Status: domain.StatusBlocked, Owner: "xiaoxia", Priority: domain.PriorityHigh,
		Tags: "a,b", Body: "正文", CreatedAt: ts, UpdatedAt: ts,
	}, []domain.Link{{ID: 7, Kind: domain.LinkDependsOn, Target: "Y20260916", Note: "等專案"}},
		[]domain.Node{{ID: "Y20260916/REQ-A/ISSUE-1", Status: domain.StatusTodo, Owner: "kaimake"}})
	out = ta.stdout()
	for _, want := range []string{"Parent   : Y20260916", "Tags     : a,b", "#7 depends_on → Y20260916 等專案", "ISSUE-1", "body：", "正文"} {
		if !strings.Contains(out, want) {
			t.Errorf("輸出缺 %q：\n%s", want, out)
		}
	}
}

func TestPrintNodeListEmpty(t *testing.T) {
	ta := newTestApp(t)
	ta.a.printNodeList(nil)
	if !strings.Contains(ta.stdout(), "（沒有命中）") {
		t.Errorf("輸出 = %q", ta.stdout())
	}
}

func TestPrintStatsEmpty(t *testing.T) {
	ta := newTestApp(t)
	ta.a.printStats(store.Stats{})
	out := ta.stdout()
	if strings.Count(out, "（無）") != 3 {
		t.Errorf("空統計應印三個（無）：手上張數／平均滯留／REQ 完成度\n%s", out)
	}
	if !strings.Contains(out, "自我驗收張數：0") {
		t.Errorf("輸出 = %q", out)
	}
}

func TestTreeDepthOutsideSet(t *testing.T) {
	// 父節點不在集合內 → 該節點視為深度 0
	nodes := []domain.Node{{ID: "b", ParentID: "a"}, {ID: "a"}}
	if got := treeDepth(nodes, nodes[0]); got != 1 {
		t.Errorf("depth(b) = %d, want 1", got)
	}
	if got := treeDepth(nodes, domain.Node{ID: "z", ParentID: "missing"}); got != 0 {
		t.Errorf("depth(孤兒) = %d, want 0", got)
	}
}

func TestReorderArgs(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	kind := fs.String("kind", "", "")
	target := fs.String("target", "", "")
	asJSON := fs.Bool("json", false, "")

	// 位置參數在前、旗標在後（INTERFACE.md §1 的寫法）
	got := reorderArgs(fs, []string{"Y2026/ISSUE-1", "--kind", "commit", "--target", "8094064", "--json"})
	want := []string{"--kind", "commit", "--target", "8094064", "--json", "Y2026/ISSUE-1"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
	if err := fs.Parse(got); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *kind != "commit" || *target != "8094064" || !*asJSON || fs.NArg() != 1 {
		t.Fatalf("解析結果 kind=%s target=%s json=%v narg=%d", *kind, *target, *asJSON, fs.NArg())
	}

	// --key=value 形式與 -- 分隔
	fs2 := flag.NewFlagSet("t2", flag.ContinueOnError)
	k2 := fs2.String("kind", "", "")
	if err := fs2.Parse(reorderArgs(fs2, []string{"id", "--kind=file", "--", "--not-a-flag"})); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *k2 != "file" {
		t.Errorf("kind = %q, want file", *k2)
	}
	if fs2.NArg() != 2 || fs2.Arg(1) != "--not-a-flag" {
		t.Errorf("位置參數 = %v", fs2.Args())
	}

	// 未知旗標 → Parse 回錯（呼叫端轉用法錯誤）
	fs3 := flag.NewFlagSet("t3", flag.ContinueOnError)
	fs3.SetOutput(&strings.Builder{})
	if err := fs3.Parse(reorderArgs(fs3, []string{"--nope"})); err == nil {
		t.Error("未知旗標應回錯")
	}
}
