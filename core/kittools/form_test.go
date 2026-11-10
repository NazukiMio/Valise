package kittools

import (
	"slices"
	"strings"
	"testing"
)

// The question is read from rclone 1.75.2's output (seen on ubuntu-vm),
// with log lines before it; values rclone holds are never kept.
func TestParseQuestion(t *testing.T) {
	out := []byte("2026/11/10 NOTICE: Config file not found - using defaults\r\n{\r\n\t\"State\": \"client_id_warning\",\r\n\t\"Option\": {\r\n\t\t\"Name\": \"config_shared_client_id\",\r\n\t\t\"Help\": \"rclone's shared client_id is being retired.\",\r\n\t\t\"DefaultStr\": \"false\",\r\n\t\t\"Value\": \"held\",\r\n\t\t\"ValueStr\": \"held\",\r\n\t\t\"Exclusive\": true,\r\n\t\t\"Type\": \"bool\",\r\n\t\t\"Examples\": [{\"Value\": \"true\", \"Help\": \"Yes\"}, {\"Value\": \"false\", \"Help\": \"No\"}]\r\n\t},\r\n\t\"Error\": \"\",\r\n\t\"Result\": \"\"\r\n}\r\n")
	q, err := ParseQuestion(out)
	if err != nil || q.State != "client_id_warning" || q.Name != QuestionSharedApp || q.Default != "false" || !q.Only || len(q.Choices) != 2 {
		t.Fatalf("%+v %v", q, err)
	}
	if strings.Contains(strings.Join([]string{q.Help, q.Default, q.Name}, ""), "held") {
		t.Fatal("kept a value rclone holds")
	}
	secret, _ := ParseQuestion([]byte(`{"State":"s","Option":{"Name":"client_secret","DefaultStr":"old","Sensitive":true},"Error":""}`))
	if !secret.Password || secret.Default != "" {
		t.Fatalf("sensitive default kept: %+v", secret)
	}
	done, err := ParseQuestion([]byte(`{"State":"","Option":null,"Error":"","Result":""}`))
	if err != nil || done.State != "" {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := ParseQuestion([]byte("Waiting for code...")); err == nil {
		t.Fatal("no question accepted")
	}
}

func TestFormArgs(t *testing.T) {
	c := Cloud{RcloneType: "drive", Params: map[string]string{"scope": "drive.file"}, AskApp: true}
	got := c.FormArgs("valise-gdrive", App{ClientID: "id", ClientSecret: "sec"})
	want := []string{"config", "create", "valise-gdrive", "drive", "scope=drive.file", "client_id=id", "client_secret=sec", "--non-interactive"}
	if !slices.Equal(got, want) {
		t.Fatal(got)
	}
	c.AskApp = false
	if slices.Contains(c.FormArgs("r", App{ClientID: "id"}), "client_id=id") {
		t.Fatal("app passed to an entry that does not allow one")
	}
	if CheckApp(App{ClientSecret: "x"}) == nil || CheckApp(App{ClientID: "a\nb"}) == nil || CheckAnswer("", "x") == nil || CheckAnswer("s", "a\x00") == nil {
		t.Fatal("bad input accepted")
	}
}
