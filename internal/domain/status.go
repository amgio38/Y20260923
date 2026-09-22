package domain

// StatusDef 是狀態的顯示 metadata（label／icon／color／顯示序）：給 dashboard 畫
// 狀態徽章／圖例用。
//
// 純顯示資料——狀態機的合法轉移仍由 allowedTransitions／IsValidTransition／
// RequiresBlockReason 決定，這裡不參與任何轉移判斷
// （V05-STATUS-META-API 裁示：顯示 metadata 一律走 API，轉移規則留在 Go／DB CHECK）。
type StatusDef struct {
	Key   Status // 對應 domain.Status 常數
	Label string // 顯示名稱
	Icon  string // 單色字符記號
	Color string // 顯示色（十六進位）
	Sort  int    // 顯示順序（1 起算）
}

// StatusDefs 是 7 種狀態的顯示 metadata，順序即顯示順序（sort 1..7）。
// 與 dashboard.html 舊有的 `var STATUS` 一字對應；2026-09-22 起改由 `/api/meta`
// 供應，前端不再自存一份字面量（跟 BuiltinTypeDefs 同樣的資料化模式）。
func StatusDefs() []StatusDef {
	return []StatusDef{
		{Key: StatusTodo, Label: "未開始", Icon: "○", Color: "#5f6368", Sort: 1},
		{Key: StatusInProgress, Label: "進行中", Icon: "◐", Color: "#1a73e8", Sort: 2},
		{Key: StatusReview, Label: "待驗收", Icon: "◑", Color: "#f9ab00", Sort: 3},
		{Key: StatusBlocked, Label: "卡住", Icon: "●", Color: "#d93025", Sort: 4},
		{Key: StatusHold, Label: "暫緩", Icon: "◌", Color: "#80868b", Sort: 5},
		{Key: StatusDone, Label: "完成", Icon: "✔", Color: "#188038", Sort: 6},
		{Key: StatusCancel, Label: "不做", Icon: "✕", Color: "#9aa0a6", Sort: 7},
	}
}
