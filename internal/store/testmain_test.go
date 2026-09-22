package store

import (
	"os"
	"testing"

	"project_board/internal/domain"
)

// TestMain：測試檔大量用 xiaoxia／kaimake／kaimadi／yilong／claude 當範例 owner（純粹
// 當「一個合法 owner」的測試資料）。2026-09-23 domain.Owners 的預設值改成只認
// "unassigned"（設定檔/環境變數驅動，不再內建任何團隊代號）後，這裡固定把舊名單設回去，
// 不然要逐一改十幾個測試檔的 fixture。
func TestMain(m *testing.M) {
	domain.Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}
	os.Setenv("PB_OWNERS", "xiaoxia,kaimake,kaimadi,yilong,claude,human,unassigned")
	os.Exit(m.Run())
}
