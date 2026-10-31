// Package checkup computes the memory checkup from the profile's checkup
// declaration. It only reads (docs/ARCHITECTURE.md section 9, AGENT_KIT.md
// 8.4); interpretation is left to the agent and the UI.
package checkup

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
)

// Run computes the checkup of an enrolled instance at now.
func Run(rec *enroll.Record, p *profile.Profile, now time.Time) *agentkit.CheckupResult {
	res := &agentkit.CheckupResult{InstanceID: rec.InstanceID, At: now.UTC(), Sessions: []agentkit.SessionStat{},
		Daily: []agentkit.DayTokens{}, Memory: []agentkit.MemoryStat{}, Findings: []agentkit.Finding{}}
	ck := p.Checkup
	if ck == nil {
		ck = &profile.Checkup{}
	}
	res.NativeOptionsDoc = ck.NativeOptionsDoc
	th := thresholds(ck.Thresholds)
	daily := map[string]int64{}

	if s := ck.Sessions; s != nil {
		var sessions []agentkit.SessionStat
		switch {
		case s.Format == "jsonl":
			res.SessionsDeclared = true
			sessions = jsonlSessions(rec, s, daily, res)
		case s.Format == "sqlite" && s.SQLite != nil:
			res.SessionsDeclared = true
			sessions = sqliteSessions(rec, s, daily, res, now)
		default:
			res.Notes = append(res.Notes, "profile 没有声明会话的表结构，跳过会话指标")
		}
		res.SessionCount = len(sessions)
		cold := now.AddDate(0, 0, -th.ColdDays)
		for i := range sessions {
			ss := &sessions[i]
			if !ss.First.IsZero() && !ss.Last.IsZero() {
				ss.Days = ss.Last.Sub(ss.First).Hours() / 24
			}
			res.TotalBytes += ss.Bytes
			if !ss.Last.IsZero() && ss.Last.Before(cold) {
				res.ColdBytes += ss.Bytes
			}
			if res.Current == nil || ss.Last.After(res.Current.Last) {
				c := *ss
				res.Current = &c
			}
			if ss.Days >= float64(th.LongSessionDays) || ss.Tokens >= th.LongSessionTokens {
				est := ""
				if ss.Estimated {
					est = "（估算）"
				}
				res.Findings = appendFinding(res.Findings, agentkit.FindingLongSession,
					fmt.Sprintf("会话 %s 持续 %.1f 天，%d token%s", ss.ID, ss.Days, ss.Tokens, est))
			}
		}
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Bytes > sessions[j].Bytes })
		if len(sessions) > 20 {
			sessions = sessions[:20]
		}
		res.Sessions = append(res.Sessions, sessions...)
		if res.TotalBytes >= th.PileupBytes && res.ColdBytes*2 >= res.TotalBytes {
			res.Findings = append(res.Findings, agentkit.Finding{ID: agentkit.FindingSessionPileup,
				Detail: fmt.Sprintf("会话共 %d 字节，其中 %d 天前的冷会话占 %d 字节", res.TotalBytes, th.ColdDays, res.ColdBytes)})
		}
	}

	day0 := now.UTC().Truncate(24 * time.Hour)
	for i := 6; i >= 0; i-- {
		d := day0.AddDate(0, 0, -i).Format("2006-01-02")
		res.Daily = append(res.Daily, agentkit.DayTokens{Day: d, Tokens: daily[d]})
	}
	if n := len(res.Daily); n >= 3 {
		a, b, c := res.Daily[n-3].Tokens, res.Daily[n-2].Tokens, res.Daily[n-1].Tokens
		if a < b && b < c && c*2 >= a*3 && c >= th.CostGrowthMin {
			res.Findings = append(res.Findings, agentkit.Finding{ID: agentkit.FindingCostGrowth,
				Detail: fmt.Sprintf("最近 3 天的 token 量逐日上升：%d → %d → %d", a, b, c)})
		}
	}

	for _, m := range ck.MemoryFiles {
		for _, rel := range matches(rec.Roots[m.Root], m.Glob) {
			lines := countLines(filepath.Join(rec.Roots[m.Root], rel))
			res.Memory = append(res.Memory, agentkit.MemoryStat{Root: m.Root, Path: rel, Lines: lines, WarnLines: m.WarnLines})
			if lines > m.WarnLines {
				res.Findings = append(res.Findings, agentkit.Finding{ID: agentkit.FindingMemoryBloat,
					Detail: fmt.Sprintf("%s 有 %d 行，超过 %d 行", rel, lines, m.WarnLines)})
			}
		}
	}
	for _, mk := range ck.MemoryMechanismMarkers {
		if len(matches(rec.Roots[mk.Root], mk.Glob)) > 0 {
			res.MemoryMechanism = true
		}
	}
	if !res.MemoryMechanism {
		res.Findings = append(res.Findings, agentkit.Finding{ID: agentkit.FindingNoMemoryMechanism, Detail: "没有发现 profile 声明的任何记忆机制标记"})
	}
	return res
}

type limits struct {
	LongSessionDays                               int
	LongSessionTokens, CostGrowthMin, PileupBytes int64
	ColdDays                                      int
}

func thresholds(t *profile.Thresholds) limits {
	l := limits{agentkit.DefaultLongSessionDays, agentkit.DefaultLongSessionTokens, agentkit.DefaultCostGrowthMin,
		agentkit.DefaultPileupBytes, agentkit.DefaultColdDays}
	if t == nil {
		return l
	}
	if t.LongSessionDays > 0 {
		l.LongSessionDays = t.LongSessionDays
	}
	if t.LongSessionTokens > 0 {
		l.LongSessionTokens = t.LongSessionTokens
	}
	if t.CostGrowthMin > 0 {
		l.CostGrowthMin = t.CostGrowthMin
	}
	if t.PileupBytes > 0 {
		l.PileupBytes = t.PileupBytes
	}
	if t.ColdDays > 0 {
		l.ColdDays = t.ColdDays
	}
	return l
}

// appendFinding keeps one entry per id, listing the first offender.
func appendFinding(fs []agentkit.Finding, id, detail string) []agentkit.Finding {
	for _, f := range fs {
		if f.ID == id {
			return fs
		}
	}
	return append(fs, agentkit.Finding{ID: id, Detail: detail})
}

// matches lists files under root matching glob (profile glob semantics),
// relative and slash-separated. Symlinks are not followed.
func matches(root, glob string) []string {
	if root == "" {
		return nil
	}
	if !strings.Contains(glob, "**") {
		paths, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		var out []string
		for _, p := range paths {
			if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
				rel, _ := filepath.Rel(root, p)
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return out
	}
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if profile.MatchGlob(glob, rel) {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		n++
	}
	return n
}

func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

func jsonlSessions(rec *enroll.Record, s *profile.SessionsSpec, daily map[string]int64, res *agentkit.CheckupResult) []agentkit.SessionStat {
	root := rec.Roots[s.Root]
	var out []agentkit.SessionStat
	for _, rel := range matches(root, s.Glob) {
		path := filepath.Join(root, rel)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		ss := agentkit.SessionStat{ID: rel, Bytes: st.Size()}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var r io.Reader = f
		var zr *zstd.Decoder
		if strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".zstd") {
			if zr, err = zstd.NewReader(f, zstd.WithDecoderConcurrency(1)); err != nil {
				f.Close()
				continue
			}
			r = zr
		}
		var plain int64
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 64<<20)
		usage := false
		for sc.Scan() {
			line := sc.Bytes()
			plain += int64(len(line)) + 1
			ss.Events++
			var ev map[string]any
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			var tok int64
			for _, u := range s.UsageFields {
				if v, ok := number(field(ev, u)); ok {
					tok += int64(v)
					usage = true
				}
			}
			if len(s.UsageFields) == 0 {
				tok = int64(len(line)) / 4
			}
			ss.Tokens += tok
			if t, ok := parseTime(field(ev, s.TimeField)); ok {
				if ss.First.IsZero() || t.Before(ss.First) {
					ss.First = t
				}
				if t.After(ss.Last) {
					ss.Last = t
				}
				daily[dayOf(t)] += tok
			}
		}
		if zr != nil {
			zr.Close()
		}
		f.Close()
		if !usage {
			ss.Tokens, ss.Estimated = plain/4, true
		}
		if ss.Last.IsZero() {
			ss.Last = st.ModTime().UTC()
		}
		out = append(out, ss)
	}
	return out
}

func field(ev map[string]any, path string) any {
	if path == "" {
		return nil
	}
	var cur any = ev
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// parseTime accepts RFC 3339 or Unix seconds / milliseconds.
func parseTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case string:
		if p, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return p.UTC(), true
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return unix(f), true
		}
	case float64:
		return unix(t), true
	}
	return time.Time{}, false
}

// unix reads seconds, or milliseconds when the value is too large to be
// seconds (after the year 5138).
func unix(f float64) time.Time {
	if f > 1e11 {
		return time.UnixMilli(int64(f)).UTC()
	}
	return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9)).UTC()
}

func sqliteSessions(rec *enroll.Record, s *profile.SessionsSpec, daily map[string]int64, res *agentkit.CheckupResult, now time.Time) []agentkit.SessionStat {
	t := s.SQLite
	root := rec.Roots[s.Root]
	div := "1.0"
	if t.TimeUnit == "ms" {
		div = "1000.0"
	}
	q := func(id string) string { return `"` + id + `"` }
	size := "0"
	if len(t.BytesColumns) > 0 {
		var parts []string
		for _, c := range t.BytesColumns {
			parts = append(parts, "coalesce(length("+q(c)+"),0)")
		}
		size = strings.Join(parts, "+")
	}
	if t.SizeColumn != "" {
		size = "coalesce(" + q(t.SizeColumn) + "," + size + ")"
	}
	tokens := "NULL"
	switch {
	case t.TokensColumn != "":
		tokens = q(t.TokensColumn)
	case t.TokensJSON != nil:
		// Identifiers and the path are validated by profile lint.
		tokens = "CAST(json_extract(" + q(t.TokensJSON.Column) + ", '$." + t.TokensJSON.Path + "') AS INTEGER)"
	}
	ts := q(t.TimeColumn) + "/" + div
	sessQuery := fmt.Sprintf(`SELECT %s, COUNT(*), MIN(%s), MAX(%s), SUM(%s), SUM(%s), COUNT(%s) FROM %s GROUP BY 1`,
		q(t.SessionColumn), ts, ts, size, tokens, tokens, q(t.Table))
	dayQuery := fmt.Sprintf(`SELECT CAST(%s/86400 AS INTEGER), SUM(coalesce(%s, (%s)/4)) FROM %s WHERE %s >= ? GROUP BY 1`,
		ts, tokens, size, q(t.Table), ts)

	var out []agentkit.SessionStat
	files := matches(root, s.Glob)
	for _, rel := range files {
		u := url.URL{Scheme: "file", Path: filepath.Join(root, rel), RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			continue
		}
		rows, err := db.Query(sessQuery)
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("无法读取 %s 的会话表：%v", rel, err))
			db.Close()
			continue
		}
		for rows.Next() {
			var id sql.NullString
			var n, withTokens int64
			var first, last sql.NullFloat64
			var bytes, tok sql.NullInt64
			if rows.Scan(&id, &n, &first, &last, &bytes, &tok, &withTokens) != nil {
				continue
			}
			ss := agentkit.SessionStat{ID: id.String, Events: n, Bytes: bytes.Int64, Tokens: tok.Int64}
			if len(files) > 1 {
				ss.ID = rel + ":" + id.String
			}
			if withTokens == 0 {
				ss.Tokens, ss.Estimated = ss.Bytes/4, true
			}
			if first.Valid {
				ss.First = unix(first.Float64)
			}
			if last.Valid {
				ss.Last = unix(last.Float64)
			}
			out = append(out, ss)
		}
		rows.Close()
		since := float64(now.AddDate(0, 0, -8).Unix())
		if u := t.Usage; u != nil {
			usageTotals(db, u, out, daily, since, res, rel)
			db.Close()
			continue
		}
		if drows, err := db.Query(dayQuery, since); err == nil {
			for drows.Next() {
				var d int64
				var tok sql.NullFloat64
				if drows.Scan(&d, &tok) == nil {
					daily[time.Unix(d*86400, 0).UTC().Format("2006-01-02")] += int64(tok.Float64)
				}
			}
			drows.Close()
		}
		db.Close()
	}
	return out
}

// usageTotals replaces the token counts of the sessions read from db with
// the per-session totals of the usage table, and fills the daily totals
// from it (attributed to the day the usage was last updated).
func usageTotals(db *sql.DB, u *profile.UsageTable, sessions []agentkit.SessionStat, daily map[string]int64, since float64, res *agentkit.CheckupResult, rel string) {
	q := func(id string) string { return `"` + id + `"` }
	var cols []string
	for _, c := range u.TokensColumns {
		cols = append(cols, "coalesce("+q(c)+",0)")
	}
	sum := "SUM(" + strings.Join(cols, "+") + ")"
	rows, err := db.Query(fmt.Sprintf(`SELECT %s, %s FROM %s GROUP BY 1`, q(u.SessionColumn), sum, q(u.Table)))
	if err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("无法读取 %s 的用量表：%v", rel, err))
		return
	}
	totals := map[string]int64{}
	for rows.Next() {
		var id sql.NullString
		var n sql.NullInt64
		if rows.Scan(&id, &n) == nil {
			totals[id.String] = n.Int64
		}
	}
	rows.Close()
	for i := range sessions {
		id := sessions[i].ID
		if j := strings.LastIndex(id, ":"); j >= 0 && strings.HasPrefix(id, rel+":") {
			id = id[j+1:]
		}
		if n, ok := totals[id]; ok {
			sessions[i].Tokens, sessions[i].Estimated = n, false
		}
	}
	if u.TimeColumn == "" {
		return
	}
	div := "1.0"
	if u.TimeUnit == "ms" {
		div = "1000.0"
	}
	ts := q(u.TimeColumn) + "/" + div
	drows, err := db.Query(fmt.Sprintf(`SELECT CAST(%s/86400 AS INTEGER), %s FROM %s WHERE %s >= ? GROUP BY 1`, ts, sum, q(u.Table), ts), since)
	if err != nil {
		return
	}
	defer drows.Close()
	for drows.Next() {
		var d int64
		var n sql.NullInt64
		if drows.Scan(&d, &n) == nil {
			daily[time.Unix(d*86400, 0).UTC().Format("2006-01-02")] += n.Int64
		}
	}
}
