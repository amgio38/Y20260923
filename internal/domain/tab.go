package domain

// TabDef 是 dashboard header 分頁的顯示 metadata（key／label／顯示序）。
//
// 純顯示資料——分頁有哪些、叫什麼、怎麼排，全部由 `/api/meta` 供應，
// 前端不再自存字面量（V05-STATUS-META-API 追加範圍，與 StatusDefs 同模式）。
type TabDef struct {
	Key   string // 分頁代號（對應 dashboard 的 panel id／data-tab）
	Label string // 顯示名稱
	Sort  int    // 顯示順序（1 起算）
}

// TabDefs 是 dashboard 四個分頁的顯示 metadata，順序即顯示順序（sort 1..4）。
// 與 dashboard.html 舊有的 `var TAB_LABEL` 一字對應；2026-09-22 起改由
// `/api/meta` 供應，前端不再自存一份字面量。
func TabDefs() []TabDef {
	return []TabDef{
		{Key: "focus", Label: "焦點", Sort: 1},
		{Key: "progress", Label: "進度", Sort: 2},
		{Key: "checklist", Label: "母表", Sort: 3},
		{Key: "weekly", Label: "週報", Sort: 4},
	}
}
