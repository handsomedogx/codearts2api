package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d,omitempty"`
}

type DPoPKeyPair struct {
	PrivateJWK JWK `json:"private_jwk"`
	PublicJWK  JWK `json:"public_jwk"`
}

type PKCEPair struct {
	CodeVerifier        string `json:"code_verifier"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
}

func GeneratePKCE() (PKCEPair, error) {
	random := make([]byte, 64)
	if _, err := rand.Read(random); err != nil {
		return PKCEPair{}, err
	}
	verifier := hex.EncodeToString(random)
	sum := sha256.Sum256([]byte(verifier))
	return PKCEPair{CodeVerifier: verifier, CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), CodeChallengeMethod: "SHA-256"}, nil
}

func GenerateDPoPKeyPair() (DPoPKeyPair, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return DPoPKeyPair{}, err
	}
	enc := func(v *big.Int) string {
		buf := make([]byte, 32)
		v.FillBytes(buf)
		return base64.RawURLEncoding.EncodeToString(buf)
	}
	public := JWK{Kty: "EC", Crv: "P-256", X: enc(key.X), Y: enc(key.Y)}
	private := public
	private.D = enc(key.D)
	return DPoPKeyPair{PrivateJWK: private, PublicJWK: public}, nil
}

func SignDPoP(pair DPoPKeyPair, method, targetURL string, now time.Time, jti string) (string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if jti == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		jti = hex.EncodeToString(buf)
	}
	header := map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": pair.PublicJWK}
	payload := map[string]any{"htm": strings.ToUpper(method), "htu": targetURL, "iat": now.Unix(), "jti": jti}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)

	dBytes, err := base64.RawURLEncoding.DecodeString(pair.PrivateJWK.D)
	if err != nil {
		return "", err
	}
	d := new(big.Int).SetBytes(dBytes)
	xBytes, err := base64.RawURLEncoding.DecodeString(pair.PrivateJWK.X)
	if err != nil {
		return "", err
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(pair.PrivateJWK.Y)
	if err != nil {
		return "", err
	}
	privateKey := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}, D: d}
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	if !ecdsa.Verify(&privateKey.PublicKey, digest[:], r, s) {
		return "", fmt.Errorf("generated DPoP signature failed verification")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
