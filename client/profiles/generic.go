package profiles

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"valise/core/profile"
	"valise/core/valise"
)

// GenericProfileInput describes a user-supplied minimal profile for an
// unlisted or custom harness.
type GenericProfileInput struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name"`
	StateDir    string `json:"state_dir"`
	SystemdUnit string `json:"systemd_unit,omitempty"`
	NativeCLI   string `json:"native_cli,omitempty"`
}

var (
	genericUnitRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
	genericCLIRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type genericYAMLDoc struct {
	Schema         string                `yaml:"schema"`
	ID             string                `yaml:"id"`
	DisplayName    string                `yaml:"display_name"`
	ProfileVersion int                   `yaml:"profile_version"`
	Status         string                `yaml:"status"`
	Detect         genericYAMLDetect     `yaml:"detect"`
	Paths          []profile.PathRule    `yaml:"paths"`
	Provenance     genericYAMLProvenance `yaml:"provenance"`
	Service        *genericYAMLService   `yaml:"service,omitempty"`
	NativeCLI      []string              `yaml:"native_cli,omitempty"`
}

type genericYAMLDetect struct {
	StateDirCandidates []string `yaml:"state_dir_candidates"`
}

type genericYAMLProvenance struct {
	Kind   string            `yaml:"kind"`
	Binary genericYAMLBinary `yaml:"binary"`
}

type genericYAMLBinary struct {
	Name string `yaml:"name"`
}

type genericYAMLService struct {
	Candidates []genericYAMLUnit `yaml:"candidates"`
}

type genericYAMLUnit struct {
	Scope string `yaml:"scope"`
	Unit  string `yaml:"unit"`
}

// BuildGenericProfile validates inputs for an unlisted harness, builds a
// minimal valise-profile/1 YAML document with zero guessed secret rules, parses
// it strictly, and verifies it passes profile.Lint.
func BuildGenericProfile(in GenericProfileInput) (id string, rawYAML []byte, eff *profile.Profile, err error) {
	name := strings.TrimSpace(in.DisplayName)
	if !utf8.ValidString(name) || containsCtrl(name) {
		return "", nil, nil, valise.New("generic_name_invalid", "Harness 名称包含非法字符", "使用可读的普通名称，例如 MyAgent")
	}
	if n := utf8.RuneCountInString(name); n == 0 || n > 60 {
		return "", nil, nil, valise.New("generic_name_invalid", "Harness 名称不能为空且最多 60 个字", "填写要接入的 Harness 名称")
	}

	stateDir, err := normalizeGenericStateDir(in.StateDir)
	if err != nil {
		return "", nil, nil, err
	}

	id, err = resolveGenericID(in.ID, name, stateDir)
	if err != nil {
		return "", nil, nil, err
	}

	var svc *genericYAMLService
	if unit := strings.TrimSpace(in.SystemdUnit); unit != "" {
		if !genericUnitRe.MatchString(unit) || strings.Contains(unit, "..") || strings.HasPrefix(unit, "-") {
			return "", nil, nil, valise.New("generic_unit_invalid", "systemd 服务单元名称格式无效："+unit, "只填写 --user 单元名（如 myagent.service），不要包含路径或空格")
		}
		if !strings.Contains(unit, ".") {
			unit += ".service"
		}
		svc = &genericYAMLService{
			Candidates: []genericYAMLUnit{{Scope: "user", Unit: unit}},
		}
	}

	var nativeCLI []string
	provBin := id
	if cli := strings.TrimSpace(in.NativeCLI); cli != "" {
		if !genericCLIRe.MatchString(cli) {
			return "", nil, nil, valise.New("generic_cli_invalid", "CLI 命令名称无效："+cli, "只填写单个可执行文件名（不含路径、空格或参数），例如 myagent")
		}
		nativeCLI = []string{cli}
		provBin = cli
	}

	doc := genericYAMLDoc{
		Schema:         profile.Schema,
		ID:             id,
		DisplayName:    name,
		ProfileVersion: 1,
		Status:         profile.StatusDraft,
		Detect: genericYAMLDetect{
			StateDirCandidates: []string{stateDir},
		},
		Paths: []profile.PathRule{},
		Provenance: genericYAMLProvenance{
			Kind:   profile.ProvenanceBinary,
			Binary: genericYAMLBinary{Name: provBin},
		},
		Service:   svc,
		NativeCLI: nativeCLI,
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", nil, nil, err
	}
	parsed, err := profile.Parse(raw)
	if err != nil {
		return "", nil, nil, valise.New("generic_profile_invalid", "生成的通用资料格式有误", err.Error())
	}
	parsed.Path = id + ".yaml"
	res := profile.Lint([]*profile.Document{parsed})
	if len(res) != 1 || len(res[0].Issues) > 0 || res[0].Effective == nil {
		var msgs []string
		if len(res) > 0 {
			for _, iss := range res[0].Issues {
				msgs = append(msgs, iss.String())
			}
		}
		return "", nil, nil, valise.New("generic_profile_invalid", "生成的通用资料未通过格式检查", strings.Join(msgs, "; "))
	}
	return id, raw, res[0].Effective, nil
}

func resolveGenericID(rawID, name, stateDir string) (string, error) {
	id := strings.TrimSpace(rawID)
	if id != "" {
		id = strings.ToLower(id)
		if !strings.HasPrefix(id, "custom-") {
			id = "custom-" + id
		}
		if len(id) > 48 || !localIDRe.MatchString(id) {
			return "", valise.New("generic_id_invalid", "自定义标识只能包含小写字母、数字和连字符（最长 48 个字符）："+id, "留空自动生成，或填写例如 custom-myagent")
		}
		return id, nil
	}
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case !prevDash && b.Len() > 0:
			b.WriteByte('-')
			prevDash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		sum := sha256.Sum256([]byte(name + "|" + stateDir))
		slug = hex.EncodeToString(sum[:4])
	}
	return "custom-" + slug, nil
}

func normalizeGenericStateDir(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || containsCtrl(s) || strings.ContainsAny(s, "\"'`;|&<>?*\\ \t") {
		return "", valise.New("generic_state_dir_invalid", "状态目录路径格式无效", "请填写用户主目录下的子目录，例如 ~/.myagent")
	}
	s = strings.TrimRight(s, "/")
	switch {
	case s == "~" || s == "${HOME}":
		return "", valise.New("generic_state_dir_invalid", "不能直接使用整个用户主目录作为状态目录", "请填写子目录路径，例如 ~/.myagent")
	case strings.HasPrefix(s, "~/"):
		s = "${HOME}/" + strings.TrimPrefix(s, "~/")
	case strings.HasPrefix(s, "${HOME}/"):
	default:
		return "", valise.New("generic_state_dir_invalid", "状态目录必须位于当前用户主目录下（以 ~/ 或 ${HOME}/ 开头）", "例如 ~/.myagent 或 ~/.config/myagent")
	}
	rel := strings.TrimPrefix(s, "${HOME}/")
	if rel == "" || strings.Contains(rel, "$") {
		return "", valise.New("generic_state_dir_invalid", "状态目录不能包含额外环境变量或为空", "例如 ~/.myagent")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", valise.New("generic_state_dir_invalid", "状态目录不能包含 .. 上级路径段", "请填写固定相对路径，例如 ~/.myagent")
	}
	return "${HOME}/" + clean, nil
}

func containsCtrl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return true
		}
	}
	return false
}
