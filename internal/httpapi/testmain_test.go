package httpapi

import (
	"os"
	"testing"

	"project_board/internal/domain"
)

// TestMain：見 internal/store/testmain_test.go 同樣的說明——測試 fixture 用
// xiaoxia／kaimake 等當範例 owner，這裡固定把舊名單設回去，避免改動大量測試檔案。
func TestMain(m *testing.M) {
	domain.Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}
	os.Setenv("PB_OWNERS", "xiaoxia,kaimake,kaimadi,yilong,claude,human,unassigned")
	os.Exit(m.Run())
}
