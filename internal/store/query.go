package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"project_board/internal/domain"
)

// likeEscape：跳脫 LIKE 的萬用字元，讓使用者輸入的 % ／ _ 當字面值。
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ftsMinRunes：trigram tokenizer 的最小查詢長度。trigram 只切得出 3 字元以上的片段，
// 少於 3 字元的查詢（例：中文兩字詞「會員」）在 FTS5 一律不命中 → 這種查詢退回 LIKE。
const ftsMinRunes = 3

// ftsPhrase：把使用者輸入包成 FTS5 的字面字串（雙引號），避免 MATCH 語法被
// 特殊字元（" - * : ^ ( ) 等）當成運算子。內部的雙引號依 FTS5 規則寫成兩個雙引號。
func ftsPhrase(q string) string { return `"` + strings.ReplaceAll(q, `"`, `""`) + `"` }

// SearchFilter 是 SearchFiltered 的查詢條件；零值（全空）＝回全部節點。
//
//   - Query：走 nodes_fts（title／body／tags）子字串比對；空字串＝不比對關鍵字。
//   - Project：非空時只搜該專案子樹（路徑式 id：project 本身或其 project/ 前綴）。
//   - Tag：tags 是逗號分隔欄位，這裡是**整段相符**（不是子字串）；"v0.2" 不會命中 "v0.21"。
//   - Owner：全等。
//   - Statuses：非空時只回這些狀態（多選）。「我的未結單」＝Owner＋Statuses{todo,in_progress,review,blocked}。
type SearchFilter struct {
	Query    string
	Project  string
	Tag      string
	Owner    string
	Statuses []domain.Status
}

// SearchFiltered：平列（flat）多條件查詢，v0.3 在 SearchAdvanced 的基礎上加 status 多選。
//
// 關鍵差異（選它而不是 Tree）：回**扁平清單、不補祖先**——Tree 為了畫樹會把命中節點的
// 父層一起帶出，拿來當「我的未結單」會混進別人的 project／req。這裡只回真正命中的節點。
//
// 少於 3 字元的 query 沒有 trigram 可用，退回 LIKE（與 v0.1 相同語意），
// 讓「中文兩字詞也查得到」這件事不會因為換索引而退步。
func (s *Store) SearchFiltered(ctx context.Context, f SearchFilter) ([]domain.Node, error) {
	query, project, tag, owner := f.Query, f.Project, f.Tag, f.Owner
	q := "SELECT " + nodeCols + " FROM nodes n"
	var where []string
	var args []any

	if project != "" {
		where = append(where, "(n.id = ? OR n.id LIKE ? ESCAPE '\\')")
		args = append(args, project, likeEscape(project)+"/%")
	}
	if owner != "" {
		where = append(where, "n.owner = ?")
		args = append(args, owner)
	}
	if tag != "" {
		// 整段相符：前後補逗號夾住整串，再比對「,tag,」。第二式容忍「, 」這種逗號後帶空格的寫法。
		where = append(where, "((',' || n.tags || ',') LIKE ? ESCAPE '\\'"+
			" OR (',' || REPLACE(n.tags, ', ', ',') || ',') LIKE ? ESCAPE '\\')")
		seg := "%," + likeEscape(tag) + ",%"
		args = append(args, seg, seg)
	}
	if query != "" {
		if utf8.RuneCountInString(query) < ftsMinRunes {
			where = append(where, "(n.title LIKE ? ESCAPE '\\' OR n.body LIKE ? ESCAPE '\\' OR n.tags LIKE ? ESCAPE '\\')")
			pat := "%" + likeEscape(query) + "%"
			args = append(args, pat, pat, pat)
		} else {
			where = append(where, "n.id IN (SELECT node_id FROM nodes_fts WHERE nodes_fts MATCH ?)")
			args = append(args, ftsPhrase(query))
		}
	}
	if len(f.Statuses) > 0 {
		ph := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "n.status IN ("+strings.Join(ph, ", ")+")")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY n.id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []domain.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return out, nil
}

// SearchAdvanced：v0.2 的四參數簽名保留（既有呼叫端不用改都能編譯），
// 內部改走 SearchFiltered（Statuses 留空＝不過濾，語意與 v0.2 相同）。
func (s *Store) SearchAdvanced(ctx context.Context, query, project, tag, owner string) ([]domain.Node, error) {
	return s.SearchFiltered(ctx, SearchFilter{Query: query, Project: project, Tag: tag, Owner: owner})
}

// Search：v0.1 簽名保留（呼叫端先不用改也能編譯），內部改走 SearchAdvanced。
// 舊語意＝LIKE 比對 title／body／tags，project 非空時只搜該專案子樹。
func (s *Store) Search(ctx context.Context, query, project string) ([]domain.Node, error) {
	return s.SearchAdvanced(ctx, query, project, "", "")
}

const historyCols = "h.id, h.node_id, h.ts, h.actor, h.action, h.field, h.from_val, h.to_val, h.note"

// scanHistoryRows 把 history 查詢結果掃成 []domain.HistoryEntry（History 與 RecentHistory 共用）。
func scanHistoryRows(rows *sql.Rows, what string) ([]domain.HistoryEntry, error) {
	defer rows.Close()
	var out []domain.HistoryEntry
	for rows.Next() {
		var (
			h          domain.HistoryEntry
			ts, action string
		)
		if err := rows.Scan(&h.ID, &h.NodeID, &ts, &h.Actor, &action, &h.Field, &h.FromVal, &h.ToVal, &h.Note); err != nil {
			return nil, fmt.Errorf("history %s: %w", what, err)
		}
		h.Action = domain.HistoryAction(action)
		parsed, err := parseTime(ts)
		if err != nil {
			return nil, err
		}
		h.TS = parsed
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history %s: %w", what, err)
	}
	return out, nil
}

// History：單一節點最近的事件優先（id DESC）；limit <= 0 代表不限。
func (s *Store) History(ctx context.Context, id string, limit int) ([]domain.HistoryEntry, error) {
	if _, err := s.getNode(ctx, id); err != nil {
		return nil, err
	}
	q := "SELECT " + historyCols + " FROM history h WHERE h.node_id = ? ORDER BY h.id DESC"
	args := []any{id}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("history of %q: %w", id, err)
	}
	return scanHistoryRows(rows, "of "+id)
}

// RecentHistory：跨節點最近 N 筆事件（project 空字串＝全庫），依 ts desc（同秒以 id desc 收尾）。
// 用途：REST `focus.recent`（🕒 最近異動）——要看全樹，不是單一節點（API_CONTRACT.md §5-2）。
func (s *Store) RecentHistory(ctx context.Context, project string, limit int) ([]domain.HistoryEntry, error) {
	q := "SELECT " + historyCols + " FROM history h"
	args := []any{}
	if project != "" {
		q += " JOIN nodes n ON n.id = h.node_id WHERE (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		args = append(args, project, likeEscape(project)+"/%")
	}
	q += " ORDER BY h.ts DESC, h.id DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("recent history: %w", err)
	}
	return scanHistoryRows(rows, "recent")
}

// HistorySince：讀 id > since 的 history（升冪，id 序＝寫入序），limit <= 0 代表不限。
// 供即時推送的 broadcaster 追新紀錄用（Y20260920/REQ-DASHBOARD-LIVE-UPDATES/B2）：
// 純唯讀、不持有寫鎖，CLI／MCP 等別的 process 寫進來的也讀得到。
func (s *Store) HistorySince(ctx context.Context, since int64, limit int) ([]domain.HistoryEntry, error) {
	q := "SELECT " + historyCols + " FROM history h WHERE h.id > ? ORDER BY h.id"
	args := []any{since}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("history since %d: %w", since, err)
	}
	return scanHistoryRows(rows, fmt.Sprintf("since %d", since))
}

// MaxHistoryID：history 目前最大 id（空表＝0）。供 SSE 決定「從現在開始」的起點。
func (s *Store) MaxHistoryID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM history").Scan(&id); err != nil {
		return 0, fmt.Errorf("max history id: %w", err)
	}
	return id, nil
}

// NodesByID：批次取節點（id → Node），不存在的不在 map 裡。
// 供 SSE 一次補齊多筆事件的 node_type／parent_id／ancestors，避免每筆一次查詢。
func (s *Store) NodesByID(ctx context.Context, ids []string) (map[string]domain.Node, error) {
	out := make(map[string]domain.Node, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeCols+" FROM nodes WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, fmt.Errorf("nodes by id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out[n.ID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nodes by id: %w", err)
	}
	return out, nil
}

// SelfVerifiedHistory：窮舉(不是掃最近N筆)所有「actor==owner、目前status=done」的verify事件，
// 每個node_id只留最新一筆。跟 StatsAt 的 self_verified_count 用同一條WHERE，數字才會跟
// 這裡列出來的清單對得上——RecentHistory是有limit的視窗，節點多的專案舊事件會被擠出視窗外，
// 導致「⚠自我驗收 共N張」的標題數字對，底下卻列不出東西（Y20260916 撞過這個bug）。
func (s *Store) SelfVerifiedHistory(ctx context.Context, project string) ([]domain.HistoryEntry, error) {
	q := `SELECT ` + historyCols + ` FROM history h JOIN nodes n ON n.id = h.node_id
	      WHERE h.action = 'verify' AND h.note LIKE '[self-verified]%' AND n.status IN ('done','archived')`
	args := []any{}
	if project != "" {
		q += " AND (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		args = append(args, project, likeEscape(project)+"/%")
	}
	q += " ORDER BY h.ts DESC, h.id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("self-verified history: %w", err)
	}
	entries, err := scanHistoryRows(rows, "self-verified")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	out := entries[:0]
	for _, e := range entries {
		if seen[e.NodeID] {
			continue
		}
		seen[e.NodeID] = true
		out = append(out, e)
	}
	return out, nil
}

// Stats：各狀態計數、**未結案**的每人張數、每 REQ 完成度、平均滯留天數、自我驗收張數
// （§11.2、§5-3）；project 空字串＝全庫。
//
// 時鐘用 nowSec()（= StatsAt(…, nowSec())），保留 v0.1 簽名不動。
func (s *Store) Stats(ctx context.Context, project string) (Stats, error) {
	return s.StatsAt(ctx, project, nowSec())
}

// StatsAt：Stats 的注入時鐘版本（Y20260920/REQ-V03-PROGRESS 裁示：時鐘由呼叫端傳入，測試不准 sleep）。
// now 只用來算平均滯留天數（未結案節點：now - created_at，單位天，一位小數）。
func (s *Store) StatsAt(ctx context.Context, project string, now time.Time) (Stats, error) {
	all, err := s.allNodes(ctx)
	if err != nil {
		return Stats{}, err
	}
	byID := make(map[string]domain.Node, len(all))
	children := make(map[string][]string, len(all))
	for _, n := range all {
		byID[n.ID] = n
		if n.ParentID != "" {
			children[n.ParentID] = append(children[n.ParentID], n.ID)
		}
	}
	scope := func(id string) bool {
		if project == "" {
			return true
		}
		return isSelfOrDescendant(byID, id, project)
	}

	st := Stats{
		CountByStatus: map[domain.Status]int{},
		CountByOwner:  map[string]int{},
		ReqProgress:   map[string]float64{},
		AvgDwellDays:  map[string]float64{},
	}
	// 滯留天數先累加再平均（分子分母都只算未結案節點；分母 0 的 owner 自然不進 map）。
	dwellSum := map[string]float64{}
	dwellCnt := map[string]int{}
	for _, n := range all {
		if !scope(n.ID) {
			continue
		}
		st.CountByStatus[n.Status]++
		// CountByOwner＝手上未結案張數（done／cancel／archived 不算；API_CONTRACT.md §5-3。
		// archived 是 done 的封存態，跟 done 一樣不算未結案）
		if n.Status != domain.StatusDone && n.Status != domain.StatusCancel && n.Status != domain.StatusArchived {
			st.CountByOwner[n.Owner]++
			dwellSum[n.Owner] += now.Sub(n.CreatedAt).Hours() / 24
			dwellCnt[n.Owner]++
		}
	}
	for owner, cnt := range dwellCnt {
		if cnt > 0 {
			st.AvgDwellDays[owner] = round1(dwellSum[owner] / float64(cnt))
		}
	}
	// 每 REQ 完成度＝其樹下 issue 的 done 比例（cancel 不做，不計入分子分母）。
	// 若無可計子單（total==0：無子單、或子單全 cancel），分母為 0 無從計算 →
	// 退回看 REQ 自身狀態（done→100%，其餘→0%），避免「REQ 已 done 卻誤標 0%」。
	for _, n := range all {
		if n.Type != domain.TypeReq || !scope(n.ID) {
			continue
		}
		done, total := 0, 0
		for _, id := range descendants(children, n.ID) {
			d := byID[id]
			if d.Type != domain.TypeIssue || d.Status == domain.StatusCancel {
				continue
			}
			total++
			// archived 是 done 的封存態，完成度計算把它當 done 算，不能因為封存反而漏算
			if d.Status == domain.StatusDone || d.Status == domain.StatusArchived {
				done++
			}
		}
		progress := 0.0
		switch {
		case total > 0:
			progress = float64(done) / float64(total)
		case n.Status == domain.StatusDone || n.Status == domain.StatusArchived:
			progress = 1.0
		}
		st.ReqProgress[n.ID] = progress
	}
	// 自我驗收張數：目前 status=done 或 archived（archived 是 done 的封存態，稽核數字不能因為
	// 封存就消失），且該節點有 verify 事件帶 [self-verified] 前綴（§11.2）。
	q := `SELECT COUNT(DISTINCT h.node_id) FROM history h JOIN nodes n ON n.id = h.node_id
	      WHERE h.action = 'verify' AND h.note LIKE '[self-verified]%' AND n.status IN ('done','archived')`
	qargs := []any{}
	if project != "" {
		q += " AND (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		qargs = append(qargs, project, likeEscape(project)+"/%")
	}
	if err := s.db.QueryRowContext(ctx, q, qargs...).Scan(&st.SelfVerifiedCount); err != nil {
		return Stats{}, fmt.Errorf("self-verified count: %w", err)
	}
	return st, nil
}

// descendants：沿 children 索引蒐集所有子孫（BFS，帶防環上界）。
func descendants(children map[string][]string, id string) []string {
	var out []string
	queue := append([]string{}, children[id]...)
	seen := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		if seen++; seen > 1_000_000 {
			break
		}
		queue = append(queue, children[cur]...)
	}
	return out
}
