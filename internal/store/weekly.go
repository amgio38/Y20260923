package store

import (
	"context"
	"fmt"
	"time"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-WEEKLY 裁示（2026-09-20）。
//   - 週界：Asia/Taipei 週一 00:00 到下週一 00:00（不含下週一）。
//   - 三塊：誰做了什麼（history）、收了什麼（action=verify）、還開著什麼（issue 的 in_progress／review）。
//   - 只讀，不發通知、不寫信。

// WeekStart：t 所屬那一週的週一 00:00（Asia/Taipei）。
// 台灣無日光節約，用固定 +08:00（taipei）即正確，也免掉容器沒 tzdata 的問題。
func WeekStart(t time.Time) time.Time {
	lt := t.In(taipei)
	// time.Weekday：Sunday=0…Saturday=6 → 換成「距離本週一幾天」（週一=0）。
	back := (int(lt.Weekday()) + 6) % 7
	d := lt.AddDate(0, 0, -back)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, taipei)
}

// WeeklyReport：一週的三塊內容（JSON 由 cmd/pb／REST 各自序列化）。
type WeeklyReport struct {
	WeekStart time.Time             // 含
	WeekEnd   time.Time             // 不含（下週一 00:00）
	Did       []domain.HistoryEntry // 誰做了什麼（本週全部 history，依時間序）
	Closed    []domain.HistoryEntry // 收了什麼（本週的 action=verify）
	Open      []domain.Node         // 還開著什麼（type=issue 且 status 為 in_progress／review）
}

// Weekly：week 任意時刻 → 取該時刻所屬那一週的週報；project 空字串＝全庫。
//
// history.ts 一律以 +08:00 的 RFC3339 存入（formatTime），固定位移下字串序＝時間序，
// 故週界比較直接用字串比對即可。
func (s *Store) Weekly(ctx context.Context, project string, week time.Time) (WeeklyReport, error) {
	start := WeekStart(week)
	end := start.AddDate(0, 0, 7)
	rep := WeeklyReport{WeekStart: start, WeekEnd: end}

	q := "SELECT " + historyCols + " FROM history h JOIN nodes n ON n.id = h.node_id WHERE h.ts >= ? AND h.ts < ?"
	args := []any{formatTime(start), formatTime(end)}
	if project != "" {
		q += " AND (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		args = append(args, project, likeEscape(project)+"/%")
	}
	q += " ORDER BY h.ts, h.id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return WeeklyReport{}, fmt.Errorf("weekly history: %w", err)
	}
	all, err := scanHistoryRows(rows, "weekly")
	if err != nil {
		return WeeklyReport{}, err
	}
	for _, h := range all {
		rep.Did = append(rep.Did, h)
		if h.Action == domain.ActionVerify {
			rep.Closed = append(rep.Closed, h)
		}
	}

	// 還開著什麼：該範圍的 issue，status in_progress／review（只列這兩個狀態）。
	oq := "SELECT " + nodeCols + " FROM nodes n WHERE n.type = 'issue' AND n.status IN ('in_progress','review')"
	oargs := []any{}
	if project != "" {
		oq += " AND (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		oargs = append(oargs, project, likeEscape(project)+"/%")
	}
	oq += " ORDER BY n.id"
	orows, err := s.db.QueryContext(ctx, oq, oargs...)
	if err != nil {
		return WeeklyReport{}, fmt.Errorf("weekly open: %w", err)
	}
	defer orows.Close()
	for orows.Next() {
		n, err := scanNode(orows)
		if err != nil {
			return WeeklyReport{}, err
		}
		rep.Open = append(rep.Open, n)
	}
	if err := orows.Err(); err != nil {
		return WeeklyReport{}, fmt.Errorf("weekly open: %w", err)
	}
	return rep, nil
}
