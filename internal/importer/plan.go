// Package importer 把既有 markdown 單編成「將建立的節點清單」。
//
// 為什麼獨立成純函數：分類規則要能不開 DB 就測；寫入留給 Apply。
// 規則來源：docs/OPERATIONS.md §6＋Y20260920/REQ-V02-IMPORT-ALL-MD 的 PO 裁示。
// 檔名先走舊規則（*REQ*→req、ISSUE-*→issue、*status*→report）；舊規則對不上的 md
// 一律建成 issue 掛在 <project>/REQ-DEVDOCS 下；母表建成 <project>/REQ-TOTAL-CHECKLIST；
// 對不到 issue 的 status 依檔名補一張 issue 再掛 report，不再 skip。
// 已存在的 id 由 Apply 撞名警告、不覆蓋，Plan 照樣產 create。
package importer

import (
	"sort"
	"strings"
	"unicode/utf8"

	"project_board/internal/domain"
)

// nameOwners：檔名尾綴能對上的 owner，來自 domain.Owners（換團隊用 PB_OWNERS 就會跟著換，
// 不必在這裡另外維護一份名單）；排除 unassigned，那是推不出時的預設，不是檔名尾綴的樣式。
func nameOwners() []string {
	out := make([]string, 0, len(domain.Owners))
	for _, o := range domain.Owners {
		if o != "unassigned" {
			out = append(out, o)
		}
	}
	return out
}

// Item 是一筆匯入決定。Action=create 才會寫 DB；skip 只出現在 dry-run／摘要。
type Item struct {
	Action   string // create / skip
	Reason   string
	Type     domain.NodeType
	ID       string
	ParentID string
	Title    string
	Owner    string
	Status   domain.Status
	Body     string
	Source   string // 來源路徑，落庫時掛成 link kind=file
}

// Plan 依 OPERATIONS.md §6＋PO 裁示把一批檔編成節點。projectID 是匯入目標（會員中心為 Y20260916）。
// files 的 Name 是檔名（不含目錄）。順序不影響結果：內部會先 req（含 REQ-DEVDOCS／REQ-TOTAL-CHECKLIST 掛點）、
// 再 overview、再分軌、再雜項 issue、再補建 issue、再 report，最後才是 skip（只剩撞名）。
func Plan(projectID string, files []SourceFile) []Item {
	files = append([]SourceFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	memberID := projectID + "/REQ-MEMBER"
	devdocsID := projectID + "/REQ-DEVDOCS"
	checklistID := projectID + "/REQ-TOTAL-CHECKLIST"
	var reqs, overviews, issues, devdocs, compIssues, reports, skips []Item

	byKey := map[string]string{} // issue key → 節點 id

	// kindOther 先收一輪：有半份雜項就要有 REQ-DEVDOCS 掛點。
	var others []SourceFile
	for _, f := range files {
		if classify(f.Name) == kindOther {
			others = append(others, f)
		}
	}

	for _, f := range files {
		switch classify(f.Name) {
		case kindOther:
			continue // 後面建成 devdocs issue
		case kindChecklist:
			reqs = append(reqs, checklistItem(projectID, checklistID, f))
		case kindReq:
			it := reqItem(projectID, memberID, f)
			reqs = append(reqs, it)
		}
	}
	// 沒有 MEMBER_REQ 檔時仍要有掛點，否則 issue 沒父節點。用固定 id，標題標明是匯入掛點。
	if !hasID(reqs, memberID) {
		reqs = append([]Item{{
			Action:   "create",
			Type:     domain.TypeReq,
			ID:       memberID,
			ParentID: projectID,
			Title:    "會員中心 PHP→Go",
			Owner:    "unassigned",
			Status:   domain.StatusTodo,
			Body:     "匯入掛點。本次目錄沒有 MEMBER_REQ 檔，issue 仍掛在這張 req 下。",
			Source:   "",
		}}, reqs...)
	}
	// 雜項掛點：舊規則對不上的 md 全掛這張 req 下（PO 裁示）。
	if len(others) > 0 && !hasID(reqs, devdocsID) {
		reqs = append(reqs, Item{
			Action:   "create",
			Type:     domain.TypeReq,
			ID:       devdocsID,
			ParentID: projectID,
			Title:    "開發文件雜項",
			Owner:    "unassigned",
			Status:   domain.StatusTodo,
			Body:     "匯入掛點。舊規則（REQ／ISSUE／status）對不上的 md 建成 issue 掛在這張 req 下。",
			Source:   "",
		})
	}

	for _, f := range files {
		if classify(f.Name) != kindOverview {
			continue
		}
		key, owner, _ := issueParts(f.Name)
		id := memberID + "/ISSUE-" + key
		it := issueItem(id, memberID, owner, f)
		overviews = append(overviews, it)
		byKey[key] = id
		byKey[strings.TrimSuffix(key, "-OVERVIEW")] = id
	}

	for _, f := range files {
		if classify(f.Name) != kindIssue {
			continue
		}
		key, owner, _ := issueParts(f.Name)
		parent := memberID
		if ov, ok := byKey[trackStem(key)]; ok {
			parent = ov
		}
		id := parent + "/ISSUE-" + key
		if prev, ok := byKey[key]; ok && prev != id {
			skips = append(skips, Item{Action: "skip", Reason: "撞名 " + prev, Source: f.Path, Title: f.Name})
			continue
		}
		it := issueItem(id, parent, owner, f)
		issues = append(issues, it)
		byKey[key] = id
	}

	// 雜項 issue：CR／INIT／HOWTO／HANDOFF／spec 等舊規則對不上的檔，全進 REQ-DEVDOCS（PO 裁示）。
	for _, f := range others {
		id := devdocsIssueID(devdocsID, f.Name)
		if hasID(devdocs, id) {
			skips = append(skips, Item{Action: "skip", Reason: "撞名 " + id, Source: f.Path, Title: f.Name})
			continue
		}
		owner, _, _ := ownerAnywhere(f.Name)
		devdocs = append(devdocs, issueItem(id, devdocsID, owner, f))
	}

	for _, f := range files {
		if classify(f.Name) != kindStatus {
			continue
		}
		owner, key, date := statusParts(f.Name)
		if key == "" || date == "" {
			// 不是真正的回報檔（例如 DIVERGENCE-006_nstock_status_code_dropped.md，
			// _status_ 只是路徑片段、沒有日期）：report id 組不出合法值，改當雜項收，仍要產節點。
			id := devdocsIssueID(devdocsID, f.Name)
			if hasID(devdocs, id) {
				skips = append(skips, Item{Action: "skip", Reason: "撞名 " + id, Source: f.Path, Title: f.Name})
				continue
			}
			devOwner, _, _ := ownerAnywhere(f.Name)
			devdocs = append(devdocs, issueItem(id, devdocsID, devOwner, f))
			continue
		}
		parent, ok := byKey[key]
		if !ok {
			if ov, ok2 := byKey[key+"-OVERVIEW"]; ok2 {
				parent = ov
				ok = true
			}
		}
		if !ok {
			// 對不到 issue：依檔名補一張 issue 再掛 report，不准 skip（PO 裁示）。
			// 補的 issue 掛 REQ-MEMBER，與正常 issue 同樹，report 路徑才一致。
			compID := memberID + "/ISSUE-" + key
			if prev, ok2 := byKey[key]; ok2 {
				parent = prev
			} else {
				if !hasID(compIssues, compID) {
					compOwner := owner
					if compOwner == "" {
						compOwner = "unassigned"
					}
					compIssues = append(compIssues, Item{
						Action:   "create",
						Type:     domain.TypeIssue,
						ID:       compID,
						ParentID: memberID,
						Title:    key + "（匯入補建）",
						Owner:    compOwner,
						Status:   domain.StatusTodo,
						Body:     "由匯入自動補建：回報檔 " + f.Name + " 對不到既有 issue，先立 issue 再掛 report。",
						Source:   f.Path,
					})
				}
				byKey[key] = compID
				parent = compID
			}
			ok = true
		}
		if owner == "" {
			// 凍結目錄的 pi_／shrimp_／opencode_ 等非名冊前綴：掛 unassigned，仍要產 report。
			owner = "unassigned"
		}
		id := parent + "/REPORT-" + owner + "-" + date
		if hasID(reports, id) {
			skips = append(skips, Item{Action: "skip", Reason: "report 撞名 " + id, Source: f.Path, Title: f.Name})
			continue
		}
		reports = append(reports, Item{
			Action:   "create",
			Type:     domain.TypeReport,
			ID:       id,
			ParentID: parent,
			Title:    titleOf(f),
			Owner:    owner,
			Status:   inferStatus(f.Body),
			Body:     f.Body,
			Source:   f.Path,
		})
	}

	out := make([]Item, 0, len(reqs)+len(overviews)+len(issues)+len(devdocs)+len(compIssues)+len(reports)+len(skips))
	out = append(out, reqs...)
	out = append(out, overviews...)
	out = append(out, issues...)
	out = append(out, devdocs...)
	out = append(out, compIssues...)
	out = append(out, reports...)
	out = append(out, skips...)
	return out
}

func hasID(items []Item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func reqItem(projectID, memberID string, f SourceFile) Item {
	id := memberID
	if !isMemberReq(f.Name) {
		slug := domain.SlugifyTitle(strings.TrimSuffix(f.Name, ".md"))
		id = projectID + "/REQ-" + slug
	}
	owner, _, _ := ownerAnywhere(f.Name)
	if owner == "" {
		owner = "unassigned"
	}
	return Item{
		Action:   "create",
		Type:     domain.TypeReq,
		ID:       id,
		ParentID: projectID,
		Title:    titleOf(f),
		Owner:    owner,
		Status:   inferStatus(f.Body),
		Body:     f.Body,
		Source:   f.Path,
	}
}

func issueItem(id, parent, owner string, f SourceFile) Item {
	if owner == "" {
		owner = "unassigned"
	}
	return Item{
		Action:   "create",
		Type:     domain.TypeIssue,
		ID:       id,
		ParentID: parent,
		Title:    titleOf(f),
		Owner:    owner,
		Status:   inferStatus(f.Body),
		Body:     f.Body,
		Source:   f.Path,
	}
}

// checklistItem：母表這次要收，建成 <project>/REQ-TOTAL-CHECKLIST，不留到 v0.3（PO 裁示）。
func checklistItem(projectID, checklistID string, f SourceFile) Item {
	owner, _, _ := ownerAnywhere(f.Name)
	if owner == "" {
		owner = "unassigned"
	}
	return Item{
		Action:   "create",
		Type:     domain.TypeReq,
		ID:       checklistID,
		ParentID: projectID,
		Title:    titleOf(f),
		Owner:    owner,
		Status:   inferStatus(f.Body),
		Body:     f.Body,
		Source:   f.Path,
	}
}

// devdocsIssueID：雜項檔名轉 REQ-DEVDOCS 下的 issue id。檔名整段 slug 化（含日期）保留唯一性；
// 全中文這類 slug 為空時退回 DOC（呼叫端再以撞名去重）。
func devdocsIssueID(devdocsID, name string) string {
	slug := domain.SlugifyTitle(strings.TrimSuffix(name, ".md"))
	if slug == "" {
		slug = "DOC"
	}
	return devdocsID + "/ISSUE-" + slug
}

type fileKind int

const (
	kindOther fileKind = iota
	kindChecklist
	kindReq
	kindOverview
	kindIssue
	kindStatus
)

func classify(name string) fileKind {
	upper := strings.ToUpper(name)
	switch {
	case strings.Contains(upper, "TOTAL_CHECKLIST"):
		return kindChecklist
	case strings.Contains(name, "_status_") || strings.Contains(name, "_status."):
		return kindStatus
	case strings.HasPrefix(name, "ISSUE-") && strings.Contains(upper, "OVERVIEW"):
		return kindOverview
	case strings.HasPrefix(name, "ISSUE-"):
		return kindIssue
	case strings.Contains(upper, "REQ"):
		return kindReq
	default:
		return kindOther
	}
}

func isMemberReq(name string) bool {
	return strings.Contains(strings.ToUpper(name), "MEMBER_REQ")
}

// issueParts：ISSUE-ADMIN-BFF-P1-A_xiaoxia_20260918.md → key、owner、date。
func issueParts(name string) (key, owner, date string) {
	stem := strings.TrimSuffix(name, ".md")
	stem = strings.TrimPrefix(stem, "ISSUE-")
	stem, date = splitDate(stem)
	stem, owner = splitOwnerSuffix(stem)
	key = domain.SlugifyTitle(stem)
	return key, owner, date
}

// statusParts：xiaoxia_ADMIN_BFF_UT90_A_status_20260919.md → owner、key、date。
func statusParts(name string) (owner, key, date string) {
	stem := strings.TrimSuffix(name, ".md")
	stem, date = splitDate(stem)
	stem = strings.TrimSuffix(stem, "_status")
	owner, rest := splitOwnerPrefix(stem)
	if owner == "" {
		rest, owner = splitOwnerSuffix(stem)
	}
	key = domain.SlugifyTitle(rest)
	return owner, key, date
}

func splitDate(stem string) (rest, date string) {
	if len(stem) < 9 || stem[len(stem)-9] != '_' {
		return stem, ""
	}
	d := stem[len(stem)-8:]
	for _, r := range d {
		if r < '0' || r > '9' {
			return stem, ""
		}
	}
	return stem[:len(stem)-9], d
}

func splitOwnerSuffix(stem string) (rest, owner string) {
	for _, o := range nameOwners() {
		suf := "_" + o
		if strings.HasSuffix(stem, suf) {
			return strings.TrimSuffix(stem, suf), o
		}
	}
	return stem, ""
}

func splitOwnerPrefix(stem string) (owner, rest string) {
	for _, o := range nameOwners() {
		pre := o + "_"
		if strings.HasPrefix(stem, pre) {
			return o, strings.TrimPrefix(stem, pre)
		}
	}
	return "", stem
}

func ownerAnywhere(name string) (owner, rest, date string) {
	stem := strings.TrimSuffix(name, ".md")
	stem, date = splitDate(stem)
	if o, r := splitOwnerPrefix(stem); o != "" {
		return o, r, date
	}
	r, o := splitOwnerSuffix(stem)
	return o, r, date
}

// trackStem：分軌 A/B/C 去掉尾碼，用來找 OVERVIEW。ADMIN-BFF-P1-A → ADMIN-BFF-P1。
func trackStem(key string) string {
	if len(key) >= 2 && key[len(key)-2] == '-' {
		switch key[len(key)-1] {
		case 'A', 'B', 'C':
			return key[:len(key)-2]
		}
	}
	return key
}

func titleOf(f SourceFile) string {
	for _, line := range strings.Split(f.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			t := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if t != "" {
				return t
			}
		}
		if line != "" {
			break
		}
	}
	return strings.TrimSuffix(f.Name, ".md")
}

// inferStatus 取內文裡最先出現的狀態記號。🟡 是舊檔的「待命」，對到 todo。沒記號 → todo。
func inferStatus(body string) domain.Status {
	type mark struct {
		s  string
		st domain.Status
	}
	marks := []mark{
		{"✅", domain.StatusDone},
		{"🔶", domain.StatusInProgress},
		{"👀", domain.StatusReview},
		{"🚧", domain.StatusBlocked},
		{"⏸", domain.StatusHold},
		{"⏸️", domain.StatusHold},
		{"❌", domain.StatusCancel},
		{"⬜", domain.StatusTodo},
		{"🟡", domain.StatusTodo},
	}
	best := domain.StatusTodo
	bestAt := utf8.RuneCountInString(body) + 1
	found := false
	for _, m := range marks {
		if i := strings.Index(body, m.s); i >= 0 {
			at := utf8.RuneCountInString(body[:i])
			if !found || at < bestAt {
				found = true
				bestAt = at
				best = m.st
			}
		}
	}
	return best
}
