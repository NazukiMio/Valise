package action

import (
	"testing"

	"valise/core/profile"
)

func TestPick(t *testing.T) {
	js := []byte(`{"url":"http://127.0.0.1:1/#token=abc123&x=1","nested":{"t":"zz"}}`)
	for _, c := range []struct {
		sp   *profile.SecretPick
		out  []byte
		want string
		ok   bool
	}{
		{&profile.SecretPick{JSONField: "url", URLParam: "token"}, js, "abc123", true},
		{&profile.SecretPick{JSONField: "nested.t"}, js, "zz", true},
		{&profile.SecretPick{JSONField: "url", URLParam: "missing"}, js, "", false},
		{&profile.SecretPick{JSONField: "nope"}, js, "", false},
		{&profile.SecretPick{}, []byte("\n  tok-1  \nsecond\n"), "tok-1", true},
		{nil, []byte("\n\n"), "", false},
		{&profile.SecretPick{JSONField: "url"}, []byte("not json"), "", false},
		{&profile.SecretPick{JSONField: "url", URLParam: "q"}, []byte(`{"url":"http://h/?q=v1"}`), "v1", true},
	} {
		got, ok := pick(c.out, c.sp)
		if got != c.want || ok != c.ok {
			t.Errorf("%+v: %q %v", c.sp, got, ok)
		}
	}
	if s, cut := tail("a\nb\nc\n", 2); s != "b\nc" || !cut {
		t.Fatal(s)
	}
}
