package relay

import "testing"

// Output shaped like a real loopback OAuth CLI (box drawing, colors, the
// link printed twice and split across reads).
const sample = "\x1b[32m◇\x1b[39m  OAuth URL ready\r\n\r\nOpen this URL in your LOCAL browser:\r\n\r\n" +
	"https://auth.example.com/oauth/authorize?response_type=code&client_id=app_X&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&state=abc\r\n" +
	"│  https://auth.example.com/oauth/authorize?response_type=code&client_id=app_X&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&state=abc  │\r\n"

func TestScannerAcrossReads(t *testing.T) {
	s, _ := NewScanner("")
	for i := 0; i < len(sample); i += 7 {
		end := min(i+7, len(sample))
		s.Write([]byte(sample[i:end]))
	}
	links := s.Links()
	if len(links) != 1 {
		t.Fatalf("%q", links)
	}
	if Domain(links[0]) != "auth.example.com" {
		t.Fatal(Domain(links[0]))
	}
	cb, ok := LoopbackCallback(links[0])
	if !ok || cb.Host != "localhost" || cb.Port != 1455 {
		t.Fatalf("%+v %v", cb, ok)
	}
}

func TestPatternAndPartial(t *testing.T) {
	s, err := NewScanner(`visit (https://\S+/device)`)
	if err != nil {
		t.Fatal(err)
	}
	s.Write([]byte("Please visit https://github.com/login/device and enter ABCD-1234"))
	if len(s.Links()) != 0 {
		t.Fatal("scanned an unfinished line")
	}
	s.Flush()
	if l := s.Links(); len(l) != 1 || l[0] != "https://github.com/login/device" {
		t.Fatalf("%q", l)
	}
	if _, ok := LoopbackCallback(s.Links()[0]); ok {
		t.Fatal("device link treated as loopback")
	}
}

func TestLoopbackForms(t *testing.T) {
	for link, want := range map[string]Callback{
		"http://127.0.0.1:8085/cb":                               {"127.0.0.1", 8085},
		"http://[::1]:9000/":                                     {"::1", 9000},
		"https://x.test/a?redirect_url=http://localhost/cb":      {"localhost", 80},
		"https://x.test/a?next=http%3A%2F%2F127.0.0.1%3A3000%2F": {"127.0.0.1", 3000},
	} {
		got, ok := LoopbackCallback(link)
		if !ok || got != want {
			t.Errorf("%s: %+v %v", link, got, ok)
		}
	}
	for _, link := range []string{"https://evil.test/?redirect_uri=http://evil.test:1455/", "ftp://localhost:21/", "http://10.0.0.1:80/"} {
		if _, ok := LoopbackCallback(link); ok {
			t.Errorf("%s accepted", link)
		}
	}
}
