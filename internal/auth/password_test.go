package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("clave-del-operador")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := VerifyPassword("clave-del-operador", hash)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("the correct password was rejected")
	}

	ok, err = VerifyPassword("otra-clave", hash)
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if ok {
		t.Fatal("a wrong password was accepted")
	}
}

func TestHashPasswordIsSaltedPerCall(t *testing.T) {
	a, err := HashPassword("misma-clave")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("misma-clave")
	if err != nil {
		t.Fatal(err)
	}
	// Equal hashes would mean no salt, which makes the whole table crackable
	// with one rainbow table.
	if a == b {
		t.Fatal("the same password produced the same hash twice")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "not-a-hash", "$argon2id$broken"} {
		if _, err := VerifyPassword("x", bad); err == nil {
			t.Fatalf("a malformed hash %q was accepted", bad)
		}
	}
}
