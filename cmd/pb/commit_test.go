package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/domain"
)

func TestExtractNodeIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"feat: x (#Y20260916/REQ-ALPHA/ISSUE-ONE)", []string{"Y20260916/REQ-ALPHA/ISSUE-ONE"}},
		{"a Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-COMMIT b", []string{"Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-COMMIT"}},
		{"dup Y20260916/REQ-ALPHA and again Y20260916/REQ-ALPHA", []string{"Y20260916/REQ-ALPHA"}},
		{"no id here, Y123 not a date", nil},
		{"two: Y20260916/REQ-A/ISSUE-B Y20260916/REQ-A/ISSUE-C", []string{"Y20260916/REQ-A/ISSUE-B", "Y20260916/REQ-A/ISSUE-C"}},
	}
	for _, c := range cases {
		got := domain.ExtractNodeIDs(c.in)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("extractNodeIDs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("subject\n\nbody\n"); got != "subject" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("only"); got != "only" {
		t.Errorf("firstLine = %q", got)
	}
}

// writeMsg：把訊息寫成暫存檔（避開 stdin）。
func writeMsg(t *testing.T, s string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "msg.txt")
	if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCommitAttachLinksExisting(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	f := writeMsg(t, "feat: xyz\n\nsee (#"+fxIssue+")\n")

	out := ta.mustRun(t, exitOK, "commit", "attach", "--sha", "abcdef1234567890", "--message-file", f, "--actor", "human")
	if !strings.Contains(out, fxIssue) {
		t.Fatalf("output 應含單號，got %q", out)
	}
	// 建的 link 要是 commit kind，target = sha。
	got := ta.mustRun(t, exitOK, "get", fxIssue)
	if !strings.Contains(got, "commit") || !strings.Contains(got, "abcdef1234567890") {
		t.Fatalf("get 應含 commit link，got %q", got)
	}
}

func TestCommitAttachSkipsUnknown(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	f := writeMsg(t, "feat: x (#Y20260916/REQ-ALPHA/ISSUE-NOPE)\n")

	out := ta.mustRun(t, exitOK, "commit", "attach", "--sha", "deadbeef", "--message-file", f, "--actor", "human")
	if !strings.Contains(out, "略過") {
		t.Fatalf("不存在的單應略過，got %q", out)
	}
	// 不應建立任何 link。
	got := ta.mustRun(t, exitOK, "get", fxIssue)
	if strings.Contains(got, "commit") {
		t.Fatalf("不應有 commit link，got %q", got)
	}
}

func TestCommitAttachNoID(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	f := writeMsg(t, "chore: no ticket here\n")
	out := ta.mustRun(t, exitOK, "commit", "attach", "--sha", "abc", "--message-file", f, "--actor", "human")
	if !strings.Contains(out, "沒有單號") {
		t.Fatalf("got %q", out)
	}
}

func TestCommitAttachDryRun(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	f := writeMsg(t, "feat: x (#"+fxIssue+")\n")

	out := ta.mustRun(t, exitOK, "commit", "attach", "--sha", "abc123", "--message-file", f, "--actor", "human", "--dry-run")
	if !strings.Contains(out, "dry-run") {
		t.Fatalf("dry-run 應標示，got %q", out)
	}
	got := ta.mustRun(t, exitOK, "get", fxIssue)
	if strings.Contains(got, "commit") {
		t.Fatalf("dry-run 不應寫入 link，got %q", got)
	}
}

func TestCommitUsage(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// 少 attach／未知子命令 → 用法錯誤。
	if code := ta.run("commit"); code != exitUsage {
		t.Fatalf("commit 無子命令 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("commit", "attach", "--sha", "x", "--message-file", writeMsg(t, "y"), "--actor", "human", "extra"); code != exitUsage {
		t.Fatalf("多餘參數 code = %d, want %d", code, exitUsage)
	}
}

func TestCommitReadMessageErrors(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// message-file 不存在 → 讀檔失敗。
	if code := ta.run("commit", "attach", "--sha", "x", "--message-file", "/no/such/file", "--actor", "human"); code != exitErr {
		t.Fatalf("壞 message-file code = %d, want %d", code, exitErr)
	}
}

func TestCommitNoShaOutsideGit(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	t.Chdir(t.TempDir()) // 非 git 工作區 → git rev-parse 失敗
	if code := ta.run("commit", "attach", "--message-file", writeMsg(t, "(#"+fxIssue+")"), "--actor", "human"); code != exitErr {
		t.Fatalf("非 git 且無 --sha code = %d, want %d", code, exitErr)
	}
}
