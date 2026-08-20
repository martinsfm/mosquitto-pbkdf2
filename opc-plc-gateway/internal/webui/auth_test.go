package webui

import "testing"

func TestAuthDisabledWhenNoPassword(t *testing.T) {
	a := newAuth("")
	if a.enabled() {
		t.Fatal("auth should be disabled with an empty password")
	}
}

func TestAuthLoginAndValidate(t *testing.T) {
	a := newAuth("s3nha")
	if !a.enabled() {
		t.Fatal("auth should be enabled with a non-empty password")
	}

	if _, ok := a.login("wrong"); ok {
		t.Fatal("login with wrong password should fail")
	}

	token, ok := a.login("s3nha")
	if !ok || token == "" {
		t.Fatal("login with correct password should succeed and return a token")
	}
	if !a.validate(token) {
		t.Fatal("freshly issued token should validate")
	}
	if a.validate("bogus-token") {
		t.Fatal("random token should not validate")
	}
}

func TestAuthLogout(t *testing.T) {
	a := newAuth("s3nha")
	token, _ := a.login("s3nha")
	a.logout(token)
	if a.validate(token) {
		t.Fatal("token should not validate after logout")
	}
}
