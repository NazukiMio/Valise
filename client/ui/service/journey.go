package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/valise"
	"valise/core/vault"
)

// JourneyReader owns disposable, process-local fixed query views. Replacing it
// after repository configuration changes invalidates all its cursors.
type JourneyReader struct {
	mu              sync.Mutex
	repos           []config.Repo
	views           map[string]*journeyView
	cursors         map[string]journeyCursor
	now             func() time.Time
	nextRevision    uint64
	remote          func(context.Context) journey.Result
	remoteCache     map[journey.LocationRef]journey.Result
	locationTimeout time.Duration
	generation      uint64
}
type journeyView struct {
	query    string
	result   journey.Result
	expires  time.Time
	revision string
}
type journeyCursor struct {
	view   string
	offset int
}

func NewJourneyReader(repos []config.Repo) *JourneyReader {
	return &JourneyReader{repos: append([]config.Repo(nil), repos...), views: map[string]*journeyView{}, cursors: map[string]journeyCursor{}, now: time.Now}
}

type JourneyListRequest struct {
	AllLocations bool `json:"all_locations,omitempty"`
	// RepoNames selects registered local repositories. With no selection, all
	// registered repositories are read. Vaults are explicit user local selections.
	RepoNames []string         `json:"repo_names,omitempty"`
	Vaults    []journey.Source `json:"vaults,omitempty"`
	From      *time.Time       `json:"from,omitempty"`
	To        *time.Time       `json:"to,omitempty"`
	Tier      string           `json:"tier,omitempty"`
	Cursor    string           `json:"cursor,omitempty"`
	Limit     int              `json:"limit,omitempty"`
}
type JourneyPage struct {
	Items        []journey.TimelinePoint `json:"items"`
	NextCursor   string                  `json:"next_cursor,omitempty"`
	ViewRevision string                  `json:"view_revision"`
	Observations []journey.Observation   `json:"observations"`
	Gaps         []journey.Gap           `json:"gaps"`
	Partial      bool                    `json:"partial"`
}
type JourneyPointRequest struct {
	SnapshotRef  journey.SnapshotRef   `json:"snapshot_ref"`
	RepoNames    []string              `json:"repo_names,omitempty"`
	Vaults       []journey.Source      `json:"vaults,omitempty"`
	LocationRefs []journey.LocationRef `json:"location_refs,omitempty"`
}

func journeyError(code, reason string) error {
	return valise.New(code, reason, "重新选择备份位置并查询；位置不可用时请检查挂载与权限")
}

func (r *JourneyReader) sources(req JourneyListRequest) ([]journey.Source, error) {
	var sources []journey.Source
	names := append([]string(nil), req.RepoNames...)
	if len(names) == 0 && len(req.Vaults) == 0 {
		for _, repo := range r.repos {
			names = append(names, repo.Name)
		}
	}
	seen := map[journey.LocationRef]bool{}
	for _, name := range names {
		found := false
		for _, repo := range r.repos {
			if repo.Name == name {
				found = true
				loc := journey.LocationRef{Kind: "local_repo", ID: repo.Name, AuthorityID: "local"}
				if !seen[loc] {
					sources = append(sources, journey.Source{LocationRef: loc, Root: repo.Path})
					seen[loc] = true
				}
				break
			}
		}
		if !found {
			return nil, journeyError("repo_unknown", "所选仓库未登记")
		}
	}
	for _, source := range req.Vaults {
		loc := source.LocationRef
		if (loc.Kind != "local_repo" && loc.Kind != "recovery_cache") || loc.ID == "" || loc.AuthorityID == "" || !filepath.IsAbs(source.Root) || seen[loc] {
			return nil, journeyError("source_conflict", "本机备份库选择无效或位置身份重复")
		}
		source.Root = filepath.Clean(source.Root)
		sources = append(sources, source)
		seen[loc] = true
	}
	sort.Slice(sources, func(i, j int) bool {
		a, _ := json.Marshal(sources[i].LocationRef)
		b, _ := json.Marshal(sources[j].LocationRef)
		return string(a) < string(b)
	})
	return sources, nil
}

// ListTimeline requires no SSH connection, owner unlock, or original dossier.
func (r *JourneyReader) ListTimeline(ctx context.Context, req JourneyListRequest) (JourneyPage, error) {
	if err := ctx.Err(); err != nil {
		return JourneyPage{}, err
	}
	if req.Limit == 0 {
		req.Limit = 50
	}
	if req.Limit < 1 || req.Limit > 200 {
		return JourneyPage{}, journeyError("invalid_limit", "每页数量须介于 1 与 200")
	}
	if req.Tier != "" {
		if _, ok := vault.TierCategories[req.Tier]; !ok {
			return JourneyPage{}, journeyError("unsupported_scope", "未知备份范围")
		}
	}
	if req.From != nil && req.To != nil && req.From.After(*req.To) {
		return JourneyPage{}, journeyError("invalid_query", "开始时间晚于结束时间")
	}
	sources, err := r.sources(req)
	if err != nil {
		return JourneyPage{}, err
	}
	queryReq := req
	queryReq.Cursor = ""
	queryReq.Limit = 0
	queryReq.RepoNames = nil
	queryReq.Vaults = nil
	query, _ := json.Marshal(struct {
		Request JourneyListRequest
		Sources []journey.Source
		Roots   []string
	}{queryReq, sources, sourceRoots(sources)})
	var scanned journey.Result
	r.mu.Lock()
	generation := r.generation
	r.mu.Unlock()
	if req.Cursor == "" {
		scanned, err = journey.Scan(ctx, sources)
		if err != nil {
			return JourneyPage{}, err
		}
		if req.AllLocations && r.remote != nil {
			scanned = journey.Merge(scanned, r.remote(ctx))
		}
		if err := ctx.Err(); err != nil {
			return JourneyPage{}, journeyError("location_timeout", "部分备份位置没有及时回应")
		}
	}
	digest := vault.SHA256Hex(query)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireViews()
	var view *journeyView
	var id string
	offset := 0
	if req.Cursor != "" {
		cursor, ok := r.cursors[req.Cursor]
		if ok {
			id = cursor.view
			offset = cursor.offset
			view = r.views[id]
		}
		if view == nil || view.query != digest {
			return JourneyPage{}, journeyError("cursor_expired", "查询条件已变化或分页视图已失效")
		}
	} else {
		if r.generation != generation {
			return JourneyPage{}, journeyError("cursor_expired", "备份位置已变化，请重新读取")
		}
		result := r.retainRemote(scanned)
		items := make([]journey.TimelinePoint, 0, len(result.Points))
		for _, p := range result.Points {
			// Conflicting variants may have different timestamps/scopes. Preserve the
			// conflict in every filtered view rather than hide it using the first copy.
			if p.MetadataState != "conflict" && ((req.From != nil && p.CreatedAt.Before(*req.From)) || (req.To != nil && p.CreatedAt.After(*req.To)) || (req.Tier != "" && p.Scope.Tier != req.Tier)) {
				continue
			}
			items = append(items, p)
		}
		result.Points = items
		id, err = randomJourneyID()
		if err != nil {
			return JourneyPage{}, err
		}
		if len(r.views) >= 64 {
			r.clearViews()
		}
		r.nextRevision++
		view = &journeyView{query: digest, result: result, expires: r.now().Add(10 * time.Minute), revision: strconv.FormatUint(r.nextRevision, 10)}
		r.views[id] = view
	}
	end := min(offset+req.Limit, len(view.result.Points))
	page := JourneyPage{Items: view.result.Points[offset:end], ViewRevision: view.revision, Observations: view.result.Observations, Gaps: view.result.Gaps, Partial: view.result.Partial}
	if end < len(view.result.Points) {
		// Reuse a cursor for the same view offset to bound memory on repeated reads.
		for token, c := range r.cursors {
			if c.view == id && c.offset == end {
				page.NextCursor = token
				break
			}
		}
		if page.NextCursor == "" {
			token, err := randomJourneyID()
			if err != nil {
				return JourneyPage{}, err
			}
			r.cursors[token] = journeyCursor{id, end}
			page.NextCursor = token
		}
	}
	// Callers cannot mutate an existing query view through returned slice aliases.
	b, _ := json.Marshal(page)
	var detached JourneyPage
	_ = json.Unmarshal(b, &detached)
	return detached, nil
}

func sourceRoots(sources []journey.Source) []string {
	roots := make([]string, len(sources))
	for i, s := range sources {
		roots[i] = s.Root
	}
	return roots
}
func randomJourneyID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func (r *JourneyReader) clearViews() {
	r.views = map[string]*journeyView{}
	r.cursors = map[string]journeyCursor{}
}
func (r *JourneyReader) expireViews() {
	for id, v := range r.views {
		if !r.now().Before(v.expires) {
			delete(r.views, id)
		}
	}
	for token, c := range r.cursors {
		if r.views[c.view] == nil {
			delete(r.cursors, token)
		}
	}
}

// Invalidate discards views after a caller rebuilds or rebinds the collection.
func (r *JourneyReader) Invalidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	r.clearViews()
}

func (r *JourneyReader) GetTimelinePoint(ctx context.Context, req JourneyPointRequest) (journey.TimelinePoint, error) {
	sources, err := r.sources(JourneyListRequest{RepoNames: req.RepoNames, Vaults: req.Vaults})
	if err != nil {
		return journey.TimelinePoint{}, err
	}
	if len(req.LocationRefs) > 0 {
		available := map[journey.LocationRef]bool{}
		for _, s := range sources {
			available[s.LocationRef] = true
		}
		for _, loc := range req.LocationRefs {
			if !available[loc] {
				return journey.TimelinePoint{}, journeyError("source_conflict", "详情筛选引用了尚未打开的备份位置")
			}
		}
		chosen := sources[:0]
		for _, s := range sources {
			for _, loc := range req.LocationRefs {
				if s.LocationRef == loc {
					chosen = append(chosen, s)
					break
				}
			}
		}
		sources = chosen
	}
	result, err := journey.Scan(ctx, sources)
	if err != nil {
		return journey.TimelinePoint{}, err
	}
	for _, p := range result.Points {
		if p.SnapshotRef == req.SnapshotRef {
			return p, nil
		}
	}
	if result.Partial {
		return journey.TimelinePoint{}, journeyError("result_unknown", "位置读取不完整，无法确定该时间点是否存在")
	}
	return journey.TimelinePoint{}, journeyError("snapshot_not_found", "所选位置未发现该时间点")
}
