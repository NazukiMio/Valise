package service

import (
	"context"
	"testing"

	"valise/core/kittools"
)

// The form answers fixed questions itself, sends the browser step to the
// relay, and asks the person the rest, again when rclone complains.
func TestRunForm(t *testing.T) {
	script := []kittools.Question{
		{State: "a", Name: kittools.QuestionSharedApp},
		{State: "b", Name: kittools.QuestionAdvanced},
		{State: "c", Name: kittools.QuestionIsLocal},
		{State: "d", Name: "config_change_team_drive", Default: "false"},
		{State: "d", Name: "config_change_team_drive", Error: "bad"},
		{State: ""},
	}
	var answers, asked []string
	i := 0
	next := func() *kittools.Question { q := script[i]; i++; return &q }
	step := func(state, result string) (*kittools.Question, error) {
		if state != "" {
			answers = append(answers, state+"="+result)
		}
		return next(), nil
	}
	browser := func(state string) (*kittools.Question, error) {
		answers = append(answers, state+"=browser")
		return next(), nil
	}
	f := CloudForm{Ask: func(q kittools.Question) (string, error) {
		asked = append(asked, q.Name+":"+q.Error)
		return "false", nil
	}}
	if err := runForm(context.Background(), f, step, browser); err != nil {
		t.Fatal(err)
	}
	want := []string{"a=true", "b=false", "c=browser", "d=false", "d=false"}
	if len(answers) != len(want) {
		t.Fatalf("%v", answers)
	}
	for k := range want {
		if answers[k] != want[k] {
			t.Fatalf("%v", answers)
		}
	}
	if len(asked) != 2 || asked[1] != "config_change_team_drive:bad" {
		t.Fatalf("%v", asked)
	}
	// Without a form an unknown question cannot be answered.
	i = 3
	if err := runForm(context.Background(), CloudForm{}, func(string, string) (*kittools.Question, error) { return next(), nil }, browser); err != ErrFormUnavailable {
		t.Fatal(err)
	}
}
