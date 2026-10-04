package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本檔測 loadOwners 的查找順序（Y20260920/ISSUE-OWNERS-TXT-CWD-PB）：
// PB_OWNERS → PB_OWNERS_FILE → cwd/owners.txt → pb 執行檔專案根/owners.txt → minimal。

// writeOwners 建檔（含目錄）。
func writeOwners(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stubExe 把 exePath 換成固定值，模擬「執行檔在哪」。
func stubExe(t *testing.T, path string) {
	t.Helper()
	orig := exePath
	exePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { exePath = orig })
}

// clearOwnersEnv 清掉兩個 owners 環境變數（測試一律從「沒有 env」起跑）。
func clearOwnersEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PB_OWNERS", "")
	t.Setenv("PB_OWNERS_FILE", "")
}

func eqNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 1. env PB_OWNERS 優先於任何檔案。
func TestLoadOwners_EnvWinsOverFiles(t *testing.T) {
	clearOwnersEnv(t)
	t.Setenv("PB_OWNERS", "alpha, beta ,alpha")
	dir := t.TempDir()
	writeOwners(t, filepath.Join(dir, defaultOwnersFile), "fileonly\n")
	t.Chdir(dir)

	got := loadOwners()
	if want := []string{"alpha", "beta", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if OwnersSource() != "env PB_OWNERS" {
		t.Fatalf("OwnersSource() = %q, want env PB_OWNERS", OwnersSource())
	}
}

// 2. PB_OWNERS_FILE 指定檔優先於 cwd／執行檔專案根。
func TestLoadOwners_OwnersFileEnvWins(t *testing.T) {
	clearOwnersEnv(t)
	cwd := t.TempDir()
	writeOwners(t, filepath.Join(cwd, defaultOwnersFile), "cwdname\n")
	t.Chdir(cwd)

	explicit := filepath.Join(t.TempDir(), "team.txt")
	writeOwners(t, explicit, "explicitname\n")
	t.Setenv("PB_OWNERS_FILE", explicit)

	got := loadOwners()
	if want := []string{"explicitname", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if !strings.Contains(OwnersSource(), "PB_OWNERS_FILE") || !strings.Contains(OwnersSource(), explicit) {
		t.Fatalf("OwnersSource() = %q, want 含 PB_OWNERS_FILE 與 %s", OwnersSource(), explicit)
	}
}

// 3. PB_OWNERS_FILE 指向不存在的檔 → 退回 cwd。
func TestLoadOwners_OwnersFileEnvMissingFallsBack(t *testing.T) {
	clearOwnersEnv(t)
	cwd := t.TempDir()
	writeOwners(t, filepath.Join(cwd, defaultOwnersFile), "cwdname\n")
	t.Chdir(cwd)
	t.Setenv("PB_OWNERS_FILE", filepath.Join(t.TempDir(), "nope.txt"))

	got := loadOwners()
	if want := []string{"cwdname", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if !strings.HasPrefix(OwnersSource(), "cwd ") {
		t.Fatalf("OwnersSource() = %q, want 以 cwd 開頭", OwnersSource())
	}
}

// 4. cwd 有 owners.txt 就贏過執行檔專案根那一份。
func TestLoadOwners_CwdWinsOverExeRoot(t *testing.T) {
	clearOwnersEnv(t)
	root := makeExeRoot(t, "rootname\n")
	stubExe(t, filepath.Join(root, "bin", "pb"))

	cwd := t.TempDir()
	writeOwners(t, filepath.Join(cwd, defaultOwnersFile), "cwdname\n")
	t.Chdir(cwd)

	got := loadOwners()
	if want := []string{"cwdname", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if !strings.HasPrefix(OwnersSource(), "cwd ") {
		t.Fatalf("OwnersSource() = %q, want 以 cwd 開頭", OwnersSource())
	}
}

// 5. 核心情境：cwd 沒有 owners.txt、執行檔專案根有 → 要讀到專案根那一份。
func TestLoadOwners_ExeRootFallback(t *testing.T) {
	clearOwnersEnv(t)
	root := makeExeRoot(t, "xiaoxia\nkaimake\n")
	stubExe(t, filepath.Join(root, "bin", "pb"))
	t.Chdir(t.TempDir()) // cwd 沒有 owners.txt

	got := loadOwners()
	if want := []string{"xiaoxia", "kaimake", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if !strings.Contains(OwnersSource(), "專案根") || !strings.Contains(OwnersSource(), root) {
		t.Fatalf("OwnersSource() = %q, want 含專案根與 %s", OwnersSource(), root)
	}
}

// 6. <root>/bin/pb 是 symlink（指向同 bin 目錄的真檔）→ EvalSymlinks 後仍推得回專案根。
func TestLoadOwners_ExeRootViaSymlink(t *testing.T) {
	clearOwnersEnv(t)
	root := makeExeRoot(t, "xiaoxia\n")
	real := filepath.Join(root, "bin", "pb-real")
	writeOwners(t, real, "")
	link := filepath.Join(root, "bin", "pb")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	stubExe(t, link)
	t.Chdir(t.TempDir())

	got := loadOwners()
	if want := []string{"xiaoxia", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
}

// 7. cwd 的 owners.txt 只有註解／空行 → 視為讀不到，退回執行檔專案根。
func TestLoadOwners_EmptyCwdFileFallsBack(t *testing.T) {
	clearOwnersEnv(t)
	root := makeExeRoot(t, "rootname\n")
	stubExe(t, filepath.Join(root, "bin", "pb"))

	cwd := t.TempDir()
	writeOwners(t, filepath.Join(cwd, defaultOwnersFile), "# 只有註解\n\n   \n")
	t.Chdir(cwd)

	got := loadOwners()
	if want := []string{"rootname", "unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
}

// 8. 什麼都沒有 → 只剩 schema 防線 unassigned。
func TestLoadOwners_MinimalFallback(t *testing.T) {
	clearOwnersEnv(t)
	stubExe(t, filepath.Join(t.TempDir(), "pb")) // 不在 <root>/bin → 推不出專案根
	t.Chdir(t.TempDir())

	got := loadOwners()
	if want := []string{"unassigned"}; !eqNames(got, want) {
		t.Fatalf("loadOwners() = %v, want %v", got, want)
	}
	if !strings.Contains(OwnersSource(), "minimal") {
		t.Fatalf("OwnersSource() = %q, want 含 minimal", OwnersSource())
	}
}

// makeExeRoot 造一個假專案根：<root>/go.mod ＋ <root>/bin/（讓 ownersFileFromExeRoot 認得）。
// 回傳 root；owners.txt 內容由呼叫端決定。
func makeExeRoot(t *testing.T, ownersContent string) string {
	t.Helper()
	root := t.TempDir()
	writeOwners(t, filepath.Join(root, "go.mod"), "module fake\n\ngo 1.25\n")
	if ownersContent != "" {
		writeOwners(t, filepath.Join(root, defaultOwnersFile), ownersContent)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	return root
}

// 9. ownersFileFromExeRoot 的判定逐條（不經 loadOwners，直接驗純邏輯）。
func TestOwnersFileFromExeRoot(t *testing.T) {
	root := makeExeRoot(t, "x\n")

	cases := []struct {
		name string
		exe  string
		err  error
		want string
	}{
		{"bin 下且有 go.mod", filepath.Join(root, "bin", "pb"), nil, filepath.Join(root, defaultOwnersFile)},
		{"不在 bin 下", filepath.Join(root, "pb"), nil, ""},
		{"bin 下但無 go.mod", filepath.Join(t.TempDir(), "bin", "pb"), nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := exePath
			exePath = func() (string, error) { return tc.exe, tc.err }
			t.Cleanup(func() { exePath = orig })
			if got := ownersFileFromExeRoot(); got != tc.want {
				t.Fatalf("ownersFileFromExeRoot() = %q, want %q", got, tc.want)
			}
		})
	}

	// exePath 回錯 → ""。
	orig := exePath
	exePath = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { exePath = orig })
	if got := ownersFileFromExeRoot(); got != "" {
		t.Fatalf("exePath 回錯時 ownersFileFromExeRoot() = %q, want \"\"", got)
	}
}

// 10. go.mod 是目錄（不是檔）→ 不算專案根。
func TestOwnersFileFromExeRoot_GoModIsDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "go.mod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	stubExe(t, filepath.Join(root, "bin", "pb"))
	if got := ownersFileFromExeRoot(); got != "" {
		t.Fatalf("go.mod 是目錄時 ownersFileFromExeRoot() = %q, want \"\"", got)
	}
}

// 11. ownersFileCandidates：順序＝PB_OWNERS_FILE（有設才有）→ cwd → 專案根。
func TestOwnersFileCandidates_Order(t *testing.T) {
	clearOwnersEnv(t)
	root := makeExeRoot(t, "x\n")
	stubExe(t, filepath.Join(root, "bin", "pb"))

	// 沒設 PB_OWNERS_FILE：兩個候選（cwd、專案根）。
	got := ownersFileCandidates()
	if len(got) != 2 {
		t.Fatalf("candidates = %d 個, want 2: %+v", len(got), got)
	}
	if got[0].path != defaultOwnersFile || !strings.HasPrefix(got[0].label, "cwd ") {
		t.Fatalf("candidates[0] = %+v, want cwd owners.txt", got[0])
	}
	if got[1].path != filepath.Join(root, defaultOwnersFile) {
		t.Fatalf("candidates[1] = %+v, want 專案根 owners.txt", got[1])
	}

	// 設了 PB_OWNERS_FILE：多一個且排最前。
	t.Setenv("PB_OWNERS_FILE", "/tmp/explicit-owners.txt")
	got = ownersFileCandidates()
	if len(got) != 3 || got[0].path != "/tmp/explicit-owners.txt" {
		t.Fatalf("設 PB_OWNERS_FILE 後 candidates = %+v, want 3 個且首項為指定路徑", got)
	}
}

// 12. 執行檔推不出專案根（不在 bin 下）→ 候選只剩 cwd 一個。
func TestOwnersFileCandidates_NoExeRoot(t *testing.T) {
	clearOwnersEnv(t)
	stubExe(t, filepath.Join(t.TempDir(), "pb"))
	if got := ownersFileCandidates(); len(got) != 1 || got[0].path != defaultOwnersFile {
		t.Fatalf("candidates = %+v, want 只有 cwd 一個", got)
	}
}
