package federation

import (
	"net/http"
	"strings"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	pem, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"Follow"}`)
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/users/alice/inbox", strings.NewReader(string(body)))
	if err := SignRequest(req, "https://example.test/users/alice#main-key", pem, body); err != nil {
		t.Fatal(err)
	}
	pub, err := PublicKeyPEM(pem)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRequest(req, body, func(string) (string, error) { return pub, nil }); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifyRequest(req, []byte(`{"type":"Delete"}`), func(string) (string, error) { return pub, nil }); err == nil {
		t.Fatal("tampered body accepted")
	}
}
