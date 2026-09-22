// Command pbdemo：PB05-B 實測用的獨立 server（直接以 internal/httpapi ＋
// internal/store 起服務，不經 cmd/pb，避免相依到其他人的 WIP）。
// 正式入口是 `pb serve`（小蝦 cmd/pb）；本檔只為手動實測／截圖方便。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"time"

	"project_board/internal/domain"
	"project_board/internal/httpapi"
	"project_board/internal/store"
)

const (
	projectID = "Y20260916"
	reqID     = projectID + "/REQ-MEMBER-CORE"
	issueA    = reqID + "/ISSUE-ADMIN-BFF-UT90-A"
	conc01    = reqID + "/ISSUE-CONC01"
	conc03    = reqID + "/ISSUE-CONC03"
	archReq   = projectID + "/REQ-ARCH"
	archStore = archReq + "/ISSUE-ARCH-STORE"
)

func main() {
	db := flag.String("db", "var/board.db", "SQLite DB 路徑")
	addr := flag.String("addr", "127.0.0.1:8787", "listen 位址")
	demo := flag.Bool("demo", false, "建立示範資料（冪等）")
	flag.Parse()

	st, err := store.New(*db)
	if err != nil {
		log.Fatalf("store.New: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("Migrate: %v", err)
	}
	if *demo {
		if err := seedDemo(ctx, st); err != nil {
			log.Fatalf("seedDemo: %v", err)
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(st),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("ProjectBoard（真 store）on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// seedDemo 建一組可看 dashboard 全貌的資料；已建過就 no-op。
func seedDemo(ctx context.Context, st *store.Store) error {
	if _, _, _, err := st.Get(ctx, projectID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := st.Seed(ctx, "human"); err != nil {
		return err
	}
	create := func(actor string, in store.CreateInput) error {
		_, err := st.Create(ctx, actor, in)
		return err
	}
	setup := []struct {
		actor string
		in    store.CreateInput
	}{
		{"human", store.CreateInput{Type: domain.TypeReq, ID: reqID, Title: "會員核心", ParentID: projectID, Owner: "yilong", Priority: "high"}},
		{"xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: issueA, Title: "admin-bff UT90 軌A", ParentID: reqID, Owner: "xiaoxia", Priority: "high", Tags: "admin-bff,ut90", Body: "補齊核心路徑單測"}},
		{"xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: conc01, Title: "併發案例", ParentID: reqID, Owner: "xiaoxia", Priority: "high"}},
		{"xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: conc03, Title: "併發寫入保護", ParentID: reqID, Priority: "high"}},
		{"yilong", store.CreateInput{Type: domain.TypeReq, ID: archReq, Title: "架構體檢", ParentID: projectID, Owner: "yilong", Tags: "arch,pending-decision", Body: "D4 母表建模待裁示"}},
		{"claude", store.CreateInput{Type: domain.TypeIssue, ID: archStore, Title: "store 分層整理", ParentID: archReq, Owner: "kaimadi", Tags: "arch,store,pending-decision"}},
	}
	for _, s := range setup {
		if err := create(s.actor, s.in); err != nil {
			return err
		}
	}
	steps := []func() error{
		func() error {
			_, err := st.Transition(ctx, "xiaoxia", issueA, domain.StatusInProgress, "", nil)
			return err
		},
		func() error {
			_, err := st.Transition(ctx, "xiaoxia", issueA, domain.StatusReview, "", nil)
			return err
		},
		func() error { _, err := st.Link(ctx, "xiaoxia", issueA, domain.LinkCommit, "8094064", ""); return err },
		func() error { _, err := st.Verify(ctx, "xiaoxia", issueA, "覆蓋率 98%"); return err },
		func() error {
			_, err := st.Transition(ctx, "xiaoxia", conc01, domain.StatusInProgress, "", nil)
			return err
		},
		func() error {
			_, err := st.Link(ctx, "xiaoxia", conc03, domain.LinkDependsOn, conc01, "等 CONC01 併發案例")
			return err
		},
		func() error {
			_, err := st.Transition(ctx, "xiaoxia", conc03, domain.StatusBlocked, "等 CONC01 併發案例", nil)
			return err
		},
		func() error { return st.Comment(ctx, "claude", archStore, "A7 CI Go 版本待裁示") },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
