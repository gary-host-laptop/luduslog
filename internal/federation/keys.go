// Package federation implements the ActivityPub actor, signing and delivery
// primitives used by LudusLog.
package federation

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GenerateKey returns a new RSA private key as PKCS#8 PEM.
func GenerateKey() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func parseKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("federation: invalid private key PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := k.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// PublicKeyPEM derives the public key PEM from a private key PEM.
func PublicKeyPEM(privatePEM string) (string, error) {
	key, err := parseKey(privatePEM)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

func signingString(r *http.Request, headers []string) string {
	var lines []string
	for _, h := range headers {
		h = strings.ToLower(strings.TrimSpace(h))
		switch h {
		case "(request-target)":
			lines = append(lines, "(request-target): "+strings.ToLower(r.Method)+" "+r.URL.RequestURI())
		case "host":
			lines = append(lines, "host: "+r.Host)
		default:
			lines = append(lines, h+": "+r.Header.Get(h))
		}
	}
	return strings.Join(lines, "\n")
}

// SignRequest adds an HTTP Signature and Digest to an outgoing request.
func SignRequest(r *http.Request, keyID, privatePEM string, body []byte) error {
	key, err := parseKey(privatePEM)
	if err != nil {
		return err
	}
	if body != nil {
		r.Header.Set("Digest", digestOf(body))
	}
	r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	headers := []string{"(request-target)", "host", "date", "digest", "content-type"}
	r.Header.Set("Content-Type", "application/activity+json")
	signed := signingString(r, headers)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return err
	}
	r.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headers, " "), base64.StdEncoding.EncodeToString(sig)))
	return nil
}

// VerifyRequest checks the HTTP signature on an incoming request. fetchKey is
// called with the signature keyId and must return the actor's public key PEM.
func VerifyRequest(r *http.Request, body []byte, fetchKey func(keyID string) (string, error)) error {
	header := r.Header.Get("Signature")
	if header == "" {
		header = r.Header.Get("Authorization")
		header = strings.TrimPrefix(header, "Signature ")
	}
	if header == "" {
		return errors.New("federation: missing signature")
	}
	params := map[string]string{}
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = strings.Trim(kv[1], `"`)
		}
	}
	keyID, sigB64 := params["keyId"], params["signature"]
	if keyID == "" || sigB64 == "" {
		return errors.New("federation: malformed signature")
	}
	if d := r.Header.Get("Digest"); d != "" {
		sum := sha256.Sum256(body)
		if d != "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]) {
			return errors.New("federation: digest mismatch")
		}
	}
	headers := strings.Fields(params["headers"])
	if len(headers) == 0 {
		headers = []string{"(request-target)", "host", "date"}
	}
	pubPEM, err := fetchKey(keyID)
	if err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return errors.New("federation: invalid public key")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return errors.New("federation: unsupported key type")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(signingString(r, headers)))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
}

// FetchPublicKey resolves a keyId such as https://host/users/name#main-key by
// fetching the actor document.
func FetchPublicKey(client *http.Client, keyID string) (string, error) {
	actorURL := keyID
	if i := strings.Index(keyID, "#"); i > 0 {
		actorURL = keyID[:i]
	}
	req, err := http.NewRequest(http.MethodGet, actorURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/activity+json, application/ld+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("federation: actor fetch %s: %s", actorURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return publicKeyFromActorJSON(body)
}
