package importer

import (
	"context"
	"path/filepath"
	"testing"

	"project_board/internal/domain"
	"project_board/internal/store"
)

func TestApplyCreatesAndSkipsCollision(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Seed(ctx, "yilong"); err != nil {
		t.Fatal(err)
	}
	files := []SourceFile{
		{Name: "MEMBER_REQ20260916.md", Path: "MEMBER_REQ20260916.md", Body: "# 會員\n"},
		{Name: "ISSUE-CONC01_downstream_conn_pool_20260919.md", Path: "i.md", Body: "# 連線池\n"},
	}
	items := Plan("Y20260916", files)
	res, err := Apply(ctx, st, "yilong", "Y20260916", items, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 2 {
		t.Fatalf("created=%d，期望 2（req+issue）", res.Created)
	}
	n, _, _, err := st.Get(ctx, "Y20260916/REQ-MEMBER/ISSUE-CONC01-DOWNSTREAM-CONN-POOL")
	if err != nil {
		t.Fatal(err)
	}
	if n.ParentID != "Y20260916/REQ-MEMBER" || n.Status != domain.StatusTodo {
		t.Fatalf("issue=%+v", n)
	}
	again, err := Apply(ctx, st, "yilong", "Y20260916", items, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created != 0 || again.Skipped < 2 {
		t.Fatalf("第二次應撞名略過：%+v", again)
	}
}
