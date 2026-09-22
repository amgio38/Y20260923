package main

import (
	"errors"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// errMissingActor：CLI 層的用法錯誤（--actor 與 env PB_ACTOR 都沒有）。
var errMissingActor = errors.New("missing actor")

// errNotWired：常駐子命令的接線缺失（開發中途才可能出現）。
var errNotWired = errors.New("not wired")

// isUsageErr：用法錯誤（結束碼 2）與執行錯誤（結束碼 1）的分界。
func isUsageErr(err error) bool {
	return errors.Is(err, errMissingActor) || errors.Is(err, errNotWired)
}

// humanize 把 sentinel error 轉成人類看得懂的訊息（INTERFACE.md §1 第 6 點：
// 用 errors.Is 分辨種類，不要把 Go 原始錯誤字串直接丟出來）。
func humanize(err error) string {
	switch {
	case errors.Is(err, errMissingActor):
		return "錯誤：缺少 actor —— 請帶 --actor <name> 或設環境變數 PB_ACTOR（名冊見 DATA_MODEL.md §8）"
	case errors.Is(err, store.ErrMissingActor):
		return "錯誤：actor 是空字串，store 拒收（DATA_MODEL.md §11.1）"
	case errors.Is(err, domain.ErrInvalidOwner):
		return "錯誤：actor／owner 不在名冊內（名冊見 DATA_MODEL.md §8）"
	case errors.Is(err, store.ErrItemParentNotReq):
		return "錯誤：母表項目（item）的 parent 必須是 req（DATA_MODEL.md §7）"
	case errors.Is(err, store.ErrNotFound):
		return "錯誤：找不到節點或關聯（" + err.Error() + "）"
	case errors.Is(err, store.ErrIDExists):
		return "錯誤：ID 已存在；系統不自動加尾碼，請換 title 或明確帶 --id"
	case errors.Is(err, store.ErrConflict):
		return "錯誤：節點已被異動，請重新讀取後再試"
	case errors.Is(err, store.ErrDependsOnTargetMissing):
		return "錯誤：depends_on 的目標節點不存在（先把那張單建起來再 link）"
	case errors.Is(err, store.ErrCannotDelete):
		return "錯誤：節點有子節點或關聯，不能刪除"
	case errors.Is(err, store.ErrNotInReview):
		return "錯誤：只有 status=review 的節點可以驗收（先用 pb move 推到 review）"
	case errors.Is(err, domain.ErrIllegalTransition):
		return "錯誤：狀態機不允許這個轉移（允許轉移表見 DATA_MODEL.md §6）"
	case errors.Is(err, domain.ErrMissingBlockReason):
		return "錯誤：轉入 blocked 要附理由（--note）或已存在 depends_on 關聯（DATA_MODEL.md §11.3）"
	case errors.Is(err, domain.ErrInvalidID):
		return "錯誤：ID 格式不合（前綴慣例見 DATA_MODEL.md §7）"
	case errors.Is(err, errNotWired):
		return "錯誤：常駐子命令尚未接線"
	default:
		return "錯誤：" + err.Error()
	}
}
