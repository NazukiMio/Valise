// Package enroll defines the data exchanged during enrollment
// (docs/ARCHITECTURE.md section 4): detection candidates, the enrollment
// report, provenance records and the instance record stored on the server.
package enroll

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"valise/core/identity"
	"valise/core/profile"
)

// RPC method names and capabilities.
const (
	MethodDetect         = "detect"
	MethodScan           = "enroll_scan"
	MethodInstanceGet    = "instance_get"
	MethodInstanceWrite  = "instance_write"
	MethodInstanceList   = "instance_list"
	MethodInstanceForget = "instance_forget"
	MethodReactivate     = "instance_reactivate"
	MethodEntryPortSet   = "entry_port_set"
	MethodLabelSet       = "agent_label_set"
	MethodDossierUpdate  = "dossier_update"

	ForgetCapability     = "instance-forget/1"
	ReactivateCapability = "instance-reactivate/1"
	DossierCapability    = "dossier-roots/1"

	ReactivateModeSwitchBack = "switch_back"
	ReactivateModeCoexist    = "coexist"
)

var (
	idRe       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	toolNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

// ValidInstanceID reports whether id follows the profile id naming rule.
func ValidInstanceID(id string) error {
	if !idRe.MatchString(id) || len(id) > 64 {
		return fmt.Errorf("实例 id 只能包含小写字母、数字和连字符（最长 64 个字符）：%q", id)
	}
	return nil
}

// ValidRootID reports whether id is a valid user-attached local root ID.
func ValidRootID(id string) error {
	if !idRe.MatchString(id) || len(id) > 48 || id == profile.StateRoot || id == profile.CategoryBodyPatch {
		return fmt.Errorf("附加目录 id 无效（仅限小写字母、数字和连字符，最长 48 个字符，且不能为 state 或 body_patch）：%q", id)
	}
	return nil
}

// ValidToolName reports whether name is a valid companion CLI executable name.
func ValidToolName(name string) error {
	if !toolNameRe.MatchString(name) {
		return fmt.Errorf("工具名称无效（仅限字母、数字及 . _ + -，最长 64 个字符，不含路径或空格）：%q", name)
	}
	return nil
}

// DetectParams carries the effective profiles; porter has none of its own.
type DetectParams struct {
	Profiles []profile.Profile `json:"profiles"`
}

// DetectResult lists candidate instances.
type DetectResult struct {
	Candidates []Candidate `json:"candidates"`
}

// Candidate is one detected instance.
type Candidate struct {
	ProfileID string `json:"profile_id"`
	// Name is a named instance's name (detect.named), "" for the default;
	// Env is what selects it for the harness's commands.
	Name     string            `json:"name,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	StateDir string            `json:"state_dir"`
	// Markers are the markers_any entries present in StateDir.
	Markers []string `json:"markers"`
	// Binary is the first detect.binaries entry found on the login PATH.
	Binary  string `json:"binary,omitempty"`
	Version string `json:"version,omitempty"`
	// Children are child profiles whose when condition holds here.
	Children []string `json:"children,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ScanParams asks porter to resolve and classify one candidate.
type ScanParams struct {
	Profile    profile.Profile    `json:"profile"`
	StateDir   string             `json:"state_dir"`
	InstanceID string             `json:"instance_id"`
	Overrides  []profile.Override `json:"overrides,omitempty"`
	// Name and Env: a named instance (Candidate.Name, Candidate.Env).
	Name string            `json:"name,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// Root is a resolved root.
type Root struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Source is "detect", "default" or "discover".
	Source string `json:"source"`
	Exists bool   `json:"exists"`
	// Resolved is set when Path is (or passes through) a symlink: the real
	// directory that is scanned.
	Resolved string `json:"resolved,omitempty"`
}

// Dir returns the directory actually scanned.
func (r Root) Dir() string {
	if r.Resolved != "" {
		return r.Resolved
	}
	return r.Path
}

// Tally is a file count and byte total.
type Tally struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// FileRef is a root-relative path. Reports carry paths only, never contents.
type FileRef struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// SQLiteFile is a declared database found on disk.
type SQLiteFile struct {
	FileRef
	Category     string   `json:"category"`
	SecretTables []string `json:"secret_tables,omitempty"`
	Sidecars     []string `json:"sidecars,omitempty"`
}

// UnclassifiedGroup aggregates unclassified files by their first two path
// segments so the report stays readable.
type UnclassifiedGroup struct {
	Root   string `json:"root"`
	Prefix string `json:"prefix"`
	Tally
}

// ServiceState is the observed state of a service candidate.
type ServiceState struct {
	Scope     string `json:"scope"`
	Unit      string `json:"unit"`
	LoadState string `json:"load_state"`
	Active    string `json:"active_state"`
	Enabled   string `json:"unit_file_state"`
}

// Exists reports whether systemd knows the unit.
func (s ServiceState) Exists() bool { return s.LoadState == "loaded" }

// EntryState is the observed state of an entry port on the server.
type EntryState struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Listening   bool   `json:"listening"`
}

// Report is the enrollment report.
type Report struct {
	InstanceID     string              `json:"instance_id"`
	Name           string              `json:"name,omitempty"`
	Env            map[string]string   `json:"env,omitempty"`
	ProfileID      string              `json:"profile_id"`
	ProfileVersion int                 `json:"profile_version"`
	ProfileStatus  string              `json:"profile_status"`
	Roots          []Root              `json:"roots"`
	Categories     map[string]Tally    `json:"categories"`
	Secrets        []FileRef           `json:"secrets"`
	SQLite         []SQLiteFile        `json:"sqlite"`
	Unclassified   Tally               `json:"unclassified"`
	UnclassGroups  []UnclassifiedGroup `json:"unclassified_groups"`
	Symlinks       int                 `json:"symlinks"`
	Provenance     Provenance          `json:"provenance"`
	Service        []ServiceState      `json:"service"`
	Entries        []EntryState        `json:"entries"`
	Pending        []profile.Pending   `json:"pending,omitempty"`
	Overrides      []profile.Override  `json:"local_overrides,omitempty"`
	EntryPorts     map[string]int      `json:"entry_ports,omitempty"`
	Warnings       []string            `json:"warnings,omitempty"`
	Previous       *Record             `json:"previous,omitempty"`
	Changes        []Change            `json:"changes,omitempty"`
	ScannedAt      time.Time           `json:"scanned_at"`
	// HarnessName is the name the harness gives this agent, HarnessAgents
	// every agent name in the state directory (profile identity).
	HarnessName   string    `json:"harness_name,omitempty"`
	HarnessAgents []string  `json:"harness_agents,omitempty"`
	Unreadable    []FileRef `json:"unreadable,omitempty"`
}

// Provenance records where the body came from.
type Provenance struct {
	Kind     string        `json:"kind"`
	NPM      *NPMRecord    `json:"npm,omitempty"`
	Git      *GitRecord    `json:"git,omitempty"`
	Tool     *ToolRecord   `json:"tool,omitempty"`
	Binary   *BinaryRecord `json:"binary,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// NPMRecord is an npm global install.
type NPMRecord struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	Registry  string `json:"registry,omitempty"`
	Integrity string `json:"integrity,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

// GitRecord is a git work tree.
type GitRecord struct {
	Path        string `json:"path"`
	Remote      string `json:"remote"`
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	PatchBytes  int64  `json:"patch_bytes,omitempty"`
	PatchSHA256 string `json:"patch_sha256,omitempty"`
	Untracked   int    `json:"untracked,omitempty"`
}

// BodyPaths are the program locations this provenance records (INSTALL.md
// 5.2.1): an npm global package directory, a git work tree or a single
// binary. A uv/pipx tool records no location and yields none.
func (p Provenance) BodyPaths() []string {
	var out []string
	if p.NPM != nil && p.NPM.Prefix != "" && p.NPM.Package != "" {
		out = append(out, filepath.Join(p.NPM.Prefix, "lib", "node_modules", filepath.FromSlash(p.NPM.Package)))
	}
	if p.Git != nil && p.Git.Path != "" {
		out = append(out, filepath.Clean(p.Git.Path))
	}
	if p.Binary != nil && p.Binary.Path != "" {
		out = append(out, filepath.Clean(p.Binary.Path))
	}
	return out
}

// ToolRecord is a uv tool or pipx package.
type ToolRecord struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

// BinaryRecord is a hashed executable.
type BinaryRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ExternalRef records a user-confirmed reference outside the instance's roots
// (reported as a coverage gap in v0.1, without implicit sealing).
type ExternalRef struct {
	Path   string `json:"path"`
	Kind   string `json:"ref_kind"` // "external_dir" | "symlink_target"
	Reason string `json:"reason,omitempty"`
}

// LocalRoot records a user-attached extra directory included in backups with a
// default classification category.
type LocalRoot struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Category string `json:"category"`
	Reason   string `json:"reason,omitempty"`
}

// ToolDep records a companion CLI executable checked read-only during migration
// to surface environment gaps (Valise never auto-installs arbitrary CLI tools).
type ToolDep struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// LocalEntry records a user-approved, declarative loopback web entry reported
// by an agent or added locally to the instance dossier. It holds no commands.
type LocalEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Port        int    `json:"port"`
	Path        string `json:"path"`
	Hint        string `json:"hint,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

var localEntryIDRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func hasCtrlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func truncateUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// ValidLocalEntry normalizes and validates a declarative LocalEntry.
func ValidLocalEntry(e *LocalEntry) error {
	if e == nil {
		return fmt.Errorf("入口定义不能为空")
	}
	e.ID = strings.TrimSpace(e.ID)
	if e.ID == "" || len(e.ID) > 32 || !localEntryIDRe.MatchString(e.ID) {
		return fmt.Errorf("入口 id 无效（仅限小写字母、数字和连字符，最长 32 个字符）：%q", e.ID)
	}
	e.DisplayName = strings.TrimSpace(e.DisplayName)
	if !utf8.ValidString(e.DisplayName) || hasCtrlChar(e.DisplayName) {
		return fmt.Errorf("入口名称包含非法控制字符")
	}
	if n := utf8.RuneCountInString(e.DisplayName); n == 0 || n > 60 {
		return fmt.Errorf("入口名称长度必须在 1 到 60 个字符之间")
	}
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("入口端口必须在 1 到 65535 之间：%d", e.Port)
	}
	p := strings.TrimSpace(e.Path)
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") || len(p) > 128 || !utf8.ValidString(p) || hasCtrlChar(p) ||
		strings.ContainsAny(p, "?#\\ \t\r\n") || strings.Contains(p, "..") {
		return fmt.Errorf("入口路径必须是以 / 开头、不含查询参数或特殊字符的相对路径：%q", e.Path)
	}
	e.Path = p

	hint := strings.TrimSpace(e.Hint)
	if !utf8.ValidString(hint) {
		return fmt.Errorf("入口说明必须是合法 UTF-8 文本")
	}
	hint = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(hint)
	hint = strings.Join(strings.Fields(hint), " ")
	if hasCtrlChar(hint) {
		return fmt.Errorf("入口说明不能包含控制字符")
	}
	if runes := []rune(hint); len(runes) > 200 {
		hint = string(runes[:200])
	}
	e.Hint = hint

	reason := strings.TrimSpace(e.Reason)
	if !utf8.ValidString(reason) || hasCtrlChar(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(reason)) {
		return fmt.Errorf("入口原因包含非法控制字符")
	}
	e.Reason = truncateUTF8Bytes(reason, 500)
	return nil
}

// DossierUpdateParams directly manages an instance's local_roots, tool_deps,
// and local_entries.
type DossierUpdateParams struct {
	InstanceID      string       `json:"instance_id"`
	AddRoots        []LocalRoot  `json:"add_roots,omitempty"`
	RemoveRootIDs   []string     `json:"remove_root_ids,omitempty"`
	AddToolDeps     []ToolDep    `json:"add_tool_deps,omitempty"`
	RemoveToolNames []string     `json:"remove_tool_names,omitempty"`
	AddEntries      []LocalEntry `json:"add_entries,omitempty"`
	RemoveEntryIDs  []string     `json:"remove_entry_ids,omitempty"`
}

// DossierUpdateResult returns the updated instance record.
type DossierUpdateResult struct {
	Record Record `json:"record"`
}

// Record is the instance record at ~/.config/valise/instances/<id>.json.
type Record struct {
	InstanceID     string             `json:"instance_id"`
	ProfileID      string             `json:"profile_id"`
	ProfileVersion int                `json:"profile_version"`
	Roots          map[string]string  `json:"roots"`
	Provenance     Provenance         `json:"provenance"`
	Service        []ServiceState     `json:"service"`
	Entries        []EntryState       `json:"entries"`
	LocalOverrides []profile.Override `json:"local_overrides"`
	// LocalSQLite holds user-confirmed SQLite VACUUM INTO rules supplementing the profile.
	LocalSQLite []profile.SQLiteRef `json:"local_sqlite,omitempty"`
	// LocalRoots holds user-confirmed extra directories backed up alongside profile roots.
	LocalRoots []LocalRoot `json:"local_roots,omitempty"`
	// ExternalRefs holds user-confirmed out-of-root paths noted by onboard.
	ExternalRefs []ExternalRef `json:"external_refs,omitempty"`
	// ToolDeps holds user-confirmed companion CLI tools checked read-only on migration.
	ToolDeps []ToolDep `json:"tool_deps,omitempty"`
	// LocalEntries holds user-approved declarative web entries reported by an agent.
	LocalEntries []LocalEntry `json:"local_entries,omitempty"`
	// EntryPorts override the profile's entry ports for this instance after
	// a port change (docs/LOGIN_RELAY.md 4.2).
	EntryPorts map[string]int `json:"entry_ports,omitempty"`
	EnrolledAt time.Time      `json:"enrolled_at"`
	// Superseded is set when a migrate or clone replaced this instance's
	// directories with another instance (ARCHITECTURE.md 8.2.1).
	Superseded *Supersession `json:"superseded,omitempty"`
	// Name and Env: a named instance (docs/PROFILES.md 2, 多实例); Env
	// is set on every harness command run for it.
	Name string            `json:"name,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	// DisplayName is the name the user gave the agent in Valise ("" to
	// follow HarnessName); the instance id never changes with it.
	DisplayName   string   `json:"display_name,omitempty"`
	HarnessName   string   `json:"harness_name,omitempty"`
	HarnessAgents []string `json:"harness_agents,omitempty"`
}

// Label is what the agent is called: the user's name, the harness's, or "".
func (r *Record) Label() string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	return r.HarnessName
}

// Supersession says which operation replaced an instance.
type Supersession struct {
	By   string    `json:"by"`
	OpID string    `json:"op_id"`
	At   time.Time `json:"at"`
}

// RecordFrom builds the record the user accepts from a report.
func RecordFrom(r *Report, now time.Time) Record {
	roots := map[string]string{}
	for _, rt := range r.Roots {
		roots[rt.ID] = rt.Path
	}
	ov := r.Overrides
	if ov == nil {
		ov = []profile.Override{}
	}
	var localSQLite []profile.SQLiteRef
	var localRoots []LocalRoot
	var externalRefs []ExternalRef
	var toolDeps []ToolDep
	var localEntries []LocalEntry
	var displayName string
	if r.Previous != nil {
		if r.Overrides == nil && len(r.Previous.LocalOverrides) > 0 {
			ov = append([]profile.Override(nil), r.Previous.LocalOverrides...)
		}
		if len(r.Previous.LocalSQLite) > 0 {
			localSQLite = append([]profile.SQLiteRef(nil), r.Previous.LocalSQLite...)
		}
		if len(r.Previous.LocalRoots) > 0 {
			localRoots = append([]LocalRoot(nil), r.Previous.LocalRoots...)
			for _, lr := range localRoots {
				if lr.ID != "" && lr.Path != "" {
					if _, exists := roots[lr.ID]; !exists {
						roots[lr.ID] = lr.Path
					}
				}
			}
		}
		if len(r.Previous.ExternalRefs) > 0 {
			externalRefs = append([]ExternalRef(nil), r.Previous.ExternalRefs...)
		}
		if len(r.Previous.ToolDeps) > 0 {
			toolDeps = append([]ToolDep(nil), r.Previous.ToolDeps...)
		}
		if len(r.Previous.LocalEntries) > 0 {
			localEntries = append([]LocalEntry(nil), r.Previous.LocalEntries...)
		}
		displayName = r.Previous.DisplayName
	}
	return Record{
		InstanceID: r.InstanceID, ProfileID: r.ProfileID, ProfileVersion: r.ProfileVersion,
		Roots: roots, Provenance: r.Provenance, Service: r.Service, Entries: r.Entries,
		LocalOverrides: ov, LocalSQLite: localSQLite, LocalRoots: localRoots,
		ExternalRefs: externalRefs, ToolDeps: toolDeps, LocalEntries: localEntries,
		EntryPorts: r.EntryPorts, EnrolledAt: now.UTC(),
		Name: r.Name, Env: r.Env, DisplayName: displayName,
		HarnessName: r.HarnessName, HarnessAgents: r.HarnessAgents,
	}
}

// ApplyEntryPorts sets p's entry ports to the instance's overrides.
func (r *Record) ApplyEntryPorts(p *profile.Profile) {
	if r == nil || len(r.EntryPorts) == 0 {
		return
	}
	entries := append([]profile.Entry{}, p.Entries...)
	for i, e := range entries {
		if port, ok := r.EntryPorts[e.ID]; ok {
			entries[i].Port = port
		}
	}
	p.Entries = entries
}

// ApplyLocalEntries appends user-approved dossier LocalEntries onto p.Entries
// without overriding official profile entries (and skipping local entries
// shadowed by an official entry's id or managed port).
func (r *Record) ApplyLocalEntries(p *profile.Profile) (shadowed []string) {
	if r == nil || p == nil || len(r.LocalEntries) == 0 {
		return nil
	}
	entries := append([]profile.Entry{}, p.Entries...)
	usedID := make(map[string]bool, len(entries))
	managedPort := make(map[int]bool, len(entries))
	usedPort := make(map[int]bool, len(entries))
	for _, e := range entries {
		usedID[e.ID] = true
		usedPort[e.Port] = true
		if e.Prepare != nil || e.Start != nil {
			managedPort[e.Port] = true
		}
	}
	for _, le := range r.LocalEntries {
		cp := le
		if ValidLocalEntry(&cp) != nil {
			continue
		}
		if usedID[cp.ID] || managedPort[cp.Port] || usedPort[cp.Port] {
			shadowed = append(shadowed, cp.ID)
			continue
		}
		usedID[cp.ID] = true
		usedPort[cp.Port] = true
		entries = append(entries, profile.Entry{
			ID:          cp.ID,
			DisplayName: cp.DisplayName,
			Kind:        "web",
			Host:        "127.0.0.1",
			Port:        cp.Port,
			Path:        cp.Path,
			AgentHint:   cp.Hint,
			LocalSource: "agent_dossier",
		})
	}
	p.Entries = entries
	return shadowed
}

// Change is one difference from the previous enrollment.
type Change struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// InstanceGetParams names an instance.
type InstanceGetParams struct {
	InstanceID string `json:"instance_id"`
}

// InstanceGetResult is nil Record when not enrolled.
type InstanceGetResult struct {
	Record *Record `json:"record"`
}

// InstanceSummary is one enrolled instance on a server.
type InstanceSummary struct {
	// NativeCLI means the accepted profile declares the typed instance entry;
	// it is not proof of programme integrity, service or conversation health.
	NativeCLI      bool              `json:"native_cli_available,omitempty"`
	InstanceID     string            `json:"instance_id"`
	ProfileID      string            `json:"profile_id"`
	ProfileVersion int               `json:"profile_version"`
	Roots          map[string]string `json:"roots"`
	Superseded     *Supersession     `json:"superseded,omitempty"`
	ProfileStatus  string            `json:"profile_status"`
	// InstallID is the installation (dossier) the agent belongs to; Name
	// the agent's name within it, "" for the default agent.
	InstallID string `json:"install_id,omitempty"`
	Name      string `json:"name,omitempty"`
	// Label is the agent's name for display (Record.Label), "" when none;
	// HarnessAgents lists the harness's agents in its state directory.
	Label         string   `json:"label,omitempty"`
	HarnessAgents []string `json:"harness_agents,omitempty"`
	// Live state read when listing (systemctl show, a TCP dial).
	Service []ServiceState `json:"service"`
	Entries []EntryState   `json:"entries"`
	// Identity is filled only by porters declaring identity/2; absent
	// means the client must read identity_get (identity/1) itself.
	Identity *identity.Binding `json:"identity,omitempty"`
	// MissingRoots lists enrolled root IDs whose directory on the server is
	// missing or no longer a directory (e.g. uninstalled outside Valise).
	MissingRoots []string `json:"missing_roots,omitempty"`
	// HasSecretRules reports whether the effective profile + dossier declares
	// any secrets path rules or SQLite secret_tables (nil on older porters).
	HasSecretRules *bool `json:"has_secret_rules,omitempty"`
}

// StateMissing reports whether the instance's state root directory is missing.
func (s InstanceSummary) StateMissing() bool {
	return slices.Contains(s.MissingRoots, profile.StateRoot)
}

// InstanceForgetParams asks porter to remove an instance's enrollment record,
// timers, protection plan, proposals and identity binding without touching
// vaults, live directories or .valise-prev-* leftovers.
type InstanceForgetParams struct {
	InstanceID string `json:"instance_id"`
}

// InstanceForgetResult reports what server metadata was removed.
type InstanceForgetResult struct {
	InstanceID string   `json:"instance_id"`
	Removed    []string `json:"removed,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// InstanceListResult lists the enrolled instances, sorted by id.
type InstanceListResult struct {
	Instances []InstanceSummary `json:"instances"`
}

// InstanceWriteParams writes an accepted record and the effective profile
// it was enrolled with (used for sealing while the client is offline).
type InstanceWriteParams struct {
	Record  Record          `json:"record"`
	Profile profile.Profile `json:"profile"`
}

// LabelSetParams sets the name an agent shows in Valise ("" clears it).
type LabelSetParams struct {
	InstanceID  string `json:"instance_id"`
	DisplayName string `json:"display_name"`
}

// EntryPortSetParams records a new port for one entry of an instance
// after a two-sided port change (docs/LOGIN_RELAY.md section 4.2).
type EntryPortSetParams struct {
	InstanceID string `json:"instance_id"`
	Entry      string `json:"entry"`
	Port       int    `json:"port"`
}

// EntryPortSetResult is the instance's entry ports after the change.
type EntryPortSetResult struct {
	EntryPorts map[string]int `json:"entry_ports"`
}

// InstanceWriteResult reports where the record was written.
type InstanceWriteResult struct {
	Path string `json:"path"`
}

// ReactivateParams re-activates a previously migrated-out enrolled instance by
// issuing a new generation (and optionally recording a reverse migration
// relation when switching back from the current active target).
type ReactivateParams struct {
	InstanceID        string             `json:"instance_id"`
	Mode              string             `json:"mode"` // "switch_back" | "coexist"
	SwitchFrom        *identity.AgentRef `json:"switch_from,omitempty"`
	SourceDisposition string             `json:"source_disposition,omitempty"`
}

// ReactivateResult reports the new active generation and optional reverse
// migration relation committed on the reactivated server.
type ReactivateResult struct {
	InstanceID string             `json:"instance_id"`
	Mode       string             `json:"mode"`
	Agent      identity.Agent     `json:"agent"`
	Relation   *identity.Relation `json:"relation,omitempty"`
}
