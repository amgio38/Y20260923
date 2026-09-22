package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-CHECKLIST 的 PO 裁示（2026-09-20）。
//
//   - 母表＝索引，不複製 ISSUE 正文：一列 item（type=item，id=<req>/ITEM-<KEY>）用
//     depends_on 指向既有的 issue。
//   - pb checklist 依 KEY 第一個字母分組（A–K）輸出；沒有 item 就印空表。
//   - 不准改寫已匯入的 TOTAL_CHECKLIST markdown 節點（本檔只讀）。
//   - 狀態機不加狀態（item 就沿用同一組狀態值）。

// ErrItemParentNotReq：item（母表項目）的 parent 必須是 req。
var ErrItemParentNotReq = errors.New("item parent must be a req")

// ChecklistRow：母表一列（item 自己＋指到的 issue）。
type ChecklistRow struct {
	Key     string        // 母表編號（A1、B3、K12）
	ItemID  string        // <req>/ITEM-<KEY>
	Title   string        // item 標題
	Status  domain.Status // item 狀態
	Owner   string        // item owner
	IssueID string        // depends_on 指到的 issue（沒指＝空字串）
}

// checklistCols：item 自己 ＋ 第一條 depends_on 的目標（母表要指回那張單）。
const checklistCols = `n.id, n.title, n.status, n.owner,
	COALESCE((SELECT l.target FROM links l WHERE l.from_id = n.id AND l.kind = 'depends_on'
	          ORDER BY l.id LIMIT 1), '')`

// Checklist：列出範圍內（project 空字串＝全庫）的母表項目，依 KEY 的母表序（A1、A2、B1…）。
//
// 排序用「自然序」：先比字母段，再比數字段（A2 在 A10 前面；A9 在 B1 前面）。
func (s *Store) Checklist(ctx context.Context, project string) ([]ChecklistRow, error) {
	q := "SELECT " + checklistCols + " FROM nodes n WHERE n.type = 'item'"
	args := []any{}
	if project != "" {
		q += " AND (n.id = ? OR n.id LIKE ? ESCAPE '\\')"
		args = append(args, project, likeEscape(project)+"/%")
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("checklist: %w", err)
	}
	defer rows.Close()
	var out []ChecklistRow
	for rows.Next() {
		var (
			r    ChecklistRow
			key  string
			st   string
			cols = []any{&r.ItemID, &r.Title, &st, &r.Owner, &r.IssueID}
		)
		if err := rows.Scan(cols...); err != nil {
			return nil, fmt.Errorf("checklist: %w", err)
		}
		r.Status = domain.Status(st)
		key = itemKey(r.ItemID)
		r.Key = key
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checklist: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return itemKeyLess(out[i].Key, out[j].Key) })
	return out, nil
}

// itemKey：從 <req>/ITEM-<KEY> 取出 KEY（形狀已由 domain 驗過；取不到就回整串）。
func itemKey(id string) string {
	if i := strings.LastIndex(id, "/ITEM-"); i >= 0 {
		return id[i+len("/ITEM-"):]
	}
	return id
}

// itemKeyLess：母表編號的自然序（字母段先、數字段後；沒有數字段的排前面）。
func itemKeyLess(a, b string) bool {
	al, an, ad := splitItemKey(a)
	bl, bn, bd := splitItemKey(b)
	if al != bl {
		return al < bl
	}
	if ad != bd {
		return !bd // 有數字段的排在純字母之前
	}
	if an != bn {
		return an < bn
	}
	return a < b
}

// splitItemKey：把 KEY 切成（字母段、數字值、有無數字段）。
func splitItemKey(key string) (letters string, num int, hasNum bool) {
	i := 0
	for i < len(key) && key[i] >= 'A' && key[i] <= 'Z' {
		i++
	}
	letters = key[:i]
	digits := key[i:]
	if digits == "" {
		return letters, 0, false
	}
	for _, c := range digits {
		num = num*10 + int(c-'0')
	}
	return letters, num, true
}
