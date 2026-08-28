package secure

import "testing"

func TestBoxRoundTripAndAAD(t *testing.T) {
	key := make([]byte, 32)
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("secret"), "settings:api-key:v1")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Open(sealed, "settings:api-key:v1")
	if err != nil || string(plain) != "secret" {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
	if _, err := box.Open(sealed, "wrong"); err == nil {
		t.Fatal("wrong AAD must fail")
	}
}

func TestPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(encoded, "incorrect password") {
		t.Fatal("invalid password accepted")
	}
}
