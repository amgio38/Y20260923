// Package integrations：外部系統 → ProjectBoard 的寫入端點（v0.4 git 整合）。
//
// 來源：Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-WEBHOOK（2026-09-20 學長裁示）。
// 端點掛在 `pb serve` 的 `/api/integrations/github`（POST）。只建立 link，不動狀態。
package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// maxBody：webhook body 上限（1 MiB），避免無界讀取。
const maxBody = 1 << 20

// actor：外部事件一律當成 human 觸發（名冊內）。
const actor = "human"

// Result：回給呼叫端的摘要。
type Result struct {
	Event   string    `json:"event"`
	Repo    string    `json:"repo,omitempty"`
	Linked  []LinkHit `json:"linked"`
	Skipped []string  `json:"skipped,omitempty"`
}

// LinkHit：一筆成功建立的關聯。
type LinkHit struct {
	Node   string `json:"node"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// GitHub：處理 GitHub webhook 的 handler。
//
//	X-GitHub-Event: push          → 逐 commit：訊息中的單號掛 link kind=commit（target=sha）
//	X-GitHub-Event: pull_request  → 標題＋內文的單號掛 link kind=pr（target=PR html_url）
//	X-GitHub-Event: ping          → 200（設定 webhook 時的握手）
//
// secret 非空時驗 `X-Hub-Signature-256`（HMAC-SHA256）；空＝不驗（限本機）。
func GitHub(st *store.Store, secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if secret != "" && !verifySignature(r.Header.Get("X-Hub-Signature-256"), body, secret) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		res, err := handleEvent(r.Context(), st, r.Header.Get("X-GitHub-Event"), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// verifySignature：比對 `sha256=<hex>`。
func verifySignature(header string, body []byte, secret string) bool {
	const p = "sha256="
	if !strings.HasPrefix(header, p) {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header[len(p):]))
}

// githubPayload：push／pull_request payload 的最小集合。
type githubPayload struct {
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
	} `json:"pull_request"`
}

func handleEvent(ctx context.Context, st *store.Store, event string, body []byte) (Result, error) {
	switch event {
	case "ping":
		return Result{Event: "ping", Linked: []LinkHit{}}, nil
	case "push", "pull_request":
	default:
		return Result{}, errors.New("unsupported event: " + event)
	}
	var p githubPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Result{}, errors.New("invalid payload: " + err.Error())
	}
	res := Result{Event: event, Repo: p.Repository.FullName, Linked: []LinkHit{}}

	switch event {
	case "push":
		for _, c := range p.Commits {
			res.linkAll(ctx, st, c.Message, domain.LinkCommit, c.ID)
		}
	case "pull_request":
		msg := p.PullRequest.Title + "\n" + p.PullRequest.Body
		target := p.PullRequest.HTMLURL
		res.linkAll(ctx, st, msg, domain.LinkPR, target)
	}
	return res, nil
}

// linkAll：訊息中的單號逐一掛 link；不存在的進 Skipped。
func (r *Result) linkAll(ctx context.Context, st *store.Store, message string, kind domain.LinkKind, target string) {
	for _, id := range domain.ExtractNodeIDs(message) {
		if id == "" {
			continue
		}
		if _, _, _, err := st.Get(ctx, id); err != nil {
			r.Skipped = append(r.Skipped, id)
			continue
		}
		if _, err := st.Link(ctx, actor, id, kind, target, ""); err != nil {
			r.Skipped = append(r.Skipped, id)
			continue
		}
		r.Linked = append(r.Linked, LinkHit{Node: id, Kind: string(kind), Target: target})
	}
}
