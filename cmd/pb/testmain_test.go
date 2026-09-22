package main

import (
	"os"
	"testing"

	"project_board/internal/domain"
)

// TestMain：見 internal/store/testmain_test.go 同樣的說明。也設 PB_OWNERS 環境變數，
// 涵蓋測試裡可能用 exec 拉起 pb 子行程的情境（子行程走自己的 process init，只吃得到
// 環境變數，吃不到這裡對 domain.Owners 的直接指派）。
func TestMain(m *testing.M) {
	domain.Owners = []string{"xiaoxia", "kaimake", "kaimadi", "yilong", "claude", "human", "unassigned"}
	os.Setenv("PB_OWNERS", "xiaoxia,kaimake,kaimadi,yilong,claude,human,unassigned")
	os.Exit(m.Run())
}
