package recovery

import "testing"

func TestCode(t *testing.T) {
	a := Code("00112233445566778899aabbccddeeff", []byte("x"))
	if len(a) != 8 || a == Code("00112233445566778899aabbccddeeff", []byte("y")) || a != Code("00112233445566778899aabbccddeeff", []byte("x")) {
		t.Fatal(a)
	}
	if !ValidID("00112233445566778899aabbccddeeff") || ValidID("../x") {
		t.Fatal("id")
	}
}
