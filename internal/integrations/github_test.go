package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/domain"
	"project_board/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

const (
	proj  = "Y20260920"
	req   = "Y20260920/REQ-G"
	issue = "Y20260920/REQ-G/ISSUE-ONE"
)

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	bg := context.Background()
	mk := func(typ domain.NodeType, id, parent, title string) {
		if _, err := st.Create(bg, "human", store.CreateInput{Type: typ, ID: id, ParentID: parent, Title: title}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk(domain.TypeProject, proj, "", "p")
	mk(domain.TypeReq, req, proj, "g")
	mk(domain.TypeIssue, issue, req, "one")
	if _, err := st.Link(bg, "human", proj, domain.LinkRepo, "https://github.com/x/y", ""); err != nil {
		t.Fatalf("repo link: %v", err)
	}
}

func post(t *testing.T, h http.HandlerFunc, event, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/github", strings.NewReader(body))
	r.Header.Set("X-GitHub-Event", event)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	rr := httptest.NewRecorder()
	h(rr, r)
	return rr
}

func TestGitHubPushLinksCommit(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "")
	body := `{"repository":{"full_name":"x/y"},"commits":[{"id":"abc123def","message":"feat: x (#` + issue + `)"}]}`

	rr := post(t, h, "push", "", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), issue) {
		t.Fatalf("回應應含單號：%s", rr.Body.String())
	}
	_, links, _, err := st.Get(context.Background(), issue)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range links {
		if l.Kind == domain.LinkCommit && l.Target == "abc123def" {
			found = true
		}
	}
	if !found {
		t.Fatalf("應有 commit link abc123def：%+v", links)
	}
}

func TestGitHubPullRequestLinksPR(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "")
	body := `{"repository":{"full_name":"x/y"},"pull_request":{"html_url":"https://github.com/x/y/pull/7","number":7,"title":"fix ` + issue + `"}}`

	rr := post(t, h, "pull_request", "", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
	}
	_, links, _, _ := st.Get(context.Background(), issue)
	found := false
	for _, l := range links {
		if l.Kind == domain.LinkPR && l.Target == "https://github.com/x/y/pull/7" {
			found = true
		}
	}
	if !found {
		t.Fatalf("應有 pr link：%+v", links)
	}
}

func TestGitHubSignature(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "topsecret")
	body := `{"repository":{"full_name":"x/y"},"commits":[{"id":"s1","message":"(#` + issue + `)"}]}`

	// 沒帶簽章 → 401。
	if rr := post(t, h, "push", "", body); rr.Code != http.StatusUnauthorized {
		t.Fatalf("無簽章 status = %d, want 401", rr.Code)
	}
	// 正確簽章 → 200。
	if rr := post(t, h, "push", "topsecret", body); rr.Code != http.StatusOK {
		t.Fatalf("正確簽章 status = %d, want 200", rr.Code)
	}
}

func TestGitHubEvents(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "")

	// ping → 200，event=ping。
	rr := post(t, h, "ping", "", `{"zen":"x"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"event":"ping"`) {
		t.Fatalf("ping = %d %s", rr.Code, rr.Body.String())
	}
	// 未支援事件 → 400。
	if rr := post(t, h, "issues", "", `{}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("unsupported event = %d, want 400", rr.Code)
	}
	// 非 POST → 405。
	r := httptest.NewRequest(http.MethodGet, "/api/integrations/github", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", w.Code)
	}
	// 壞 JSON → 400。
	if rr := post(t, h, "push", "", `{not json`); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", rr.Code)
	}
	// 回傳是合法 JSON。
	var got map[string]any
	if err := json.Unmarshal(post(t, h, "push", "", `{"commits":[]}`).Body.Bytes(), &got); err != nil {
		t.Fatalf("回應非 JSON：%v", err)
	}
}

func TestGitHubSkipUnknownNode(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "")
	// 訊息含查無此單的 id → 進 skipped，不建 link。
	body := `{"commits":[{"id":"z1","message":"(#Y20260920/REQ-G/ISSUE-NOPE)"}]}`
	rr := post(t, h, "push", "", body)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ISSUE-NOPE") {
		t.Fatalf("unknown node 應列 skipped：%d %s", rr.Code, rr.Body.String())
	}
}

func TestGitHubBadSignaturePrefix(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	h := GitHub(st, "s")
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/github", strings.NewReader(`{"commits":[]}`))
	r.Header.Set("X-GitHub-Event", "push")
	r.Header.Set("X-Hub-Signature-256", "sha1=deadbeef") // 前綴錯
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("錯前綴簽章 = %d, want 401", w.Code)
	}
}
