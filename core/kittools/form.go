package kittools

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// The form login of a cloud drive (docs/ARCHITECTURE.md 7.3, 2026-11-10):
// rclone's own non-interactive configuration asks one question at a time
// as JSON; Valise shows each as a form and answers with --result. Only the
// question is kept from rclone's output, never values it holds.

// App is the user's own OAuth app, for entries with AskApp. It is passed
// to rclone once and not kept by Valise.
type App struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// Question is one of rclone's questions; State "" means configuration is
// finished.
type Question struct {
	State    string   `json:"state"`
	Name     string   `json:"name,omitempty"`
	Help     string   `json:"help,omitempty"`
	Default  string   `json:"default,omitempty"`
	Type     string   `json:"type,omitempty"`
	Choices  []Choice `json:"choices,omitempty"`
	Password bool     `json:"password,omitempty"` // sensitive: shown masked, no default
	Only     bool     `json:"only,omitempty"`     // only the choices are allowed
	Required bool     `json:"required,omitempty"`
	Error    string   `json:"error,omitempty"` // rclone's complaint about the last answer
}

// Choice is one suggested answer.
type Choice struct {
	Value string `json:"value"`
	Help  string `json:"help,omitempty"`
}

// Questions the form answers by itself.
const (
	QuestionIsLocal     = "config_is_local"         // the browser step: run through the login relay
	QuestionAdvanced    = "config_fs_advanced"      // never: Valise sets no advanced options
	QuestionSharedApp   = "config_shared_client_id" // asked only when no own app was given
	maxAnswer           = 4096
	answerForbiddenRune = "\x00\r\n"
)

// FormArgs starts the form login: the fixed parameters in key order, then
// the user's own app when the entry allows one.
func (c Cloud) FormArgs(remote string, app App) []string {
	args := []string{"config", "create", remote, c.RcloneType}
	keys := make([]string, 0, len(c.Params))
	for k := range c.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, k+"="+c.Params[k])
	}
	if c.AskApp && app.ClientID != "" {
		args = append(args, "client_id="+app.ClientID, "client_secret="+app.ClientSecret)
	}
	return append(args, "--non-interactive")
}

// ContinueArgs answers one question.
func ContinueArgs(remote, state, result string) []string {
	return []string{"config", "update", remote, "--continue", "--state", state, "--result", result, "--non-interactive"}
}

// CheckApp refuses an app that cannot be passed as one argument.
func CheckApp(a App) error {
	for _, v := range []string{a.ClientID, a.ClientSecret} {
		if len(v) > maxAnswer || strings.ContainsAny(v, answerForbiddenRune) {
			return errors.New("app field invalid")
		}
	}
	if a.ClientID == "" && a.ClientSecret != "" {
		return errors.New("client_secret without client_id")
	}
	return nil
}

// CheckAnswer refuses a state or answer that cannot be one argument.
func CheckAnswer(state, result string) error {
	if state == "" || len(state) > maxAnswer || len(result) > maxAnswer || strings.ContainsAny(state+result, answerForbiddenRune) {
		return errors.New("answer invalid")
	}
	return nil
}

// rcloneQuestion is the part of rclone's output that is read.
type rcloneQuestion struct {
	State  string `json:"State"`
	Option *struct {
		Name       string `json:"Name"`
		Help       string `json:"Help"`
		DefaultStr string `json:"DefaultStr"`
		Type       string `json:"Type"`
		Required   bool   `json:"Required"`
		IsPassword bool   `json:"IsPassword"`
		Sensitive  bool   `json:"Sensitive"`
		Exclusive  bool   `json:"Exclusive"`
		Examples   []struct {
			Value string `json:"Value"`
			Help  string `json:"Help"`
		} `json:"Examples"`
	} `json:"Option"`
	Error string `json:"Error"`
}

// ParseQuestion reads the last JSON object rclone printed (a terminal may
// have put log lines before it) and keeps only the question.
func ParseQuestion(out []byte) (*Question, error) {
	for end := len(out); end > 0; {
		i := bytes.LastIndexByte(out[:end], '{')
		if i < 0 {
			break
		}
		var obj map[string]json.RawMessage
		var raw rcloneQuestion
		if json.NewDecoder(bytes.NewReader(out[i:])).Decode(&obj) == nil && obj["State"] != nil {
			b, _ := json.Marshal(obj)
			if json.Unmarshal(b, &raw) == nil {
				return question(raw), nil
			}
		}
		end = i
	}
	return nil, errors.New("rclone printed no question")
}

func question(raw rcloneQuestion) *Question {
	q := &Question{State: raw.State, Error: raw.Error}
	if o := raw.Option; o != nil {
		q.Name, q.Help, q.Type, q.Required, q.Only = o.Name, o.Help, o.Type, o.Required, o.Exclusive
		q.Password = o.IsPassword || o.Sensitive
		if !q.Password {
			q.Default = o.DefaultStr
		}
		for _, e := range o.Examples {
			q.Choices = append(q.Choices, Choice{Value: e.Value, Help: e.Help})
		}
	}
	return q
}
