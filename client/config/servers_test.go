package config

import "testing"

func TestLoginAgentOption(t *testing.T) {
	s := Server{Name: "a", Source: SourceValise, HostName: "h", Login: LoginKey, LoginKey: "/k.pub"}
	if s.LoginAgentOption() != "none" {
		t.Fatal(s.LoginAgentOption())
	}
	s.LoginAgent = "/Users/u/Library/Group Containers/agent.sock"
	if err := s.Validate(); err != nil || s.LoginAgentOption() != `"/Users/u/Library/Group Containers/agent.sock"` {
		t.Fatal(err, s.LoginAgentOption())
	}
	s.LoginAgent = "relative/sock"
	if s.Validate() == nil {
		t.Fatal("a relative agent path must be refused")
	}
}
