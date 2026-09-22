package domain

import (
	"os"
	"testing"
)

// TestMain：測試檔裡大量用 xiaoxia／kaimake／kaimadi／yilong／claude 當範例 owner
// （純粹當「一個合法 owner」的測試資料，不是真的依賴這幾個名字的語意）。2026-09-23
// 把 Owners 的預設值改成通用角色（human／unassigned）後，這裡固定把舊名單設回去，
// 不然要逐一改十幾個測試檔的 fixture，維護成本不成比例。
func TestMain(m *testing.M) {
	Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}
	os.Setenv("PB_OWNERS", "xiaoxia,kaimake,kaimadi,yilong,claude,human,unassigned")
	os.Exit(m.Run())
}
