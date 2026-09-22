package integrations

import (
	"os"
	"testing"

	"project_board/internal/domain"
)

// TestMain：見 internal/store/testmain_test.go 同樣的說明。
func TestMain(m *testing.M) {
	domain.Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}
	os.Setenv("PB_OWNERS", "xiaoxia,kaimake,kaimadi,yilong,claude,human,unassigned")
	os.Exit(m.Run())
}
