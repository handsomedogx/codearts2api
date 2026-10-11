package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const Algorithm = "SDK-HMAC-SHA256"
const EmptyBodySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type SignedRequest struct {
	Headers          http.Header
	Body             []byte
	CanonicalRequest string
	StringToSign     string
}

func SignRequest(method, rawURL string, headers http.Header, data any, accessKeyID, secretAccessKey, securityToken, domainID string, now time.Time) (SignedRequest, error) {
	if accessKeyID == "" || secretAccessKey == "" {
		return SignedRequest{}, fmt.Errorf("access_key_id and secret_access_key are required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return SignedRequest{}, err
	}
	reqHeaders := headers.Clone()
	if reqHeaders == nil {
		reqHeaders = make(http.Header)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sdkDate := reqHeaders.Get("X-Sdk-Date")
	if sdkDate == "" {
		sdkDate = now.UTC().Format("20060102T150405Z")
		reqHeaders.Set("X-Sdk-Date", sdkDate)
	}
	if reqHeaders.Get("Host") == "" {
		reqHeaders.Set("Host", parsed.Host)
	}
	if securityToken != "" && reqHeaders.Get("X-Security-Token") == "" {
		reqHeaders.Set("X-Security-Token", securityToken)
	}
	if domainID != "" && reqHeaders.Get("X-Domain-Id") == "" {
		reqHeaders.Set("X-Domain-Id", domainID)
	}
	body := []byte{}
	if data != nil {
		body, err = json.Marshal(data)
		if err != nil {
			return SignedRequest{}, err
		}
	}
	canonicalHeaders, signedHeaders := canonicalHeaders(reqHeaders)
	payloadHash := reqHeaders.Get("X-Sdk-Content-Sha256")
	if payloadHash == "" {
		if len(body) == 0 {
			payloadHash = EmptyBodySHA256
		} else {
			payloadHash = sha256Hex(body)
		}
	}
	canonicalRequest := strings.Join([]string{strings.ToUpper(method), canonicalURI(parsed.EscapedPath()), canonicalQuery(parsed.Query()), canonicalHeaders, signedHeaders, payloadHash}, "\n")
	stringToSign := strings.Join([]string{Algorithm, sdkDate, sha256Hex([]byte(canonicalRequest))}, "\n")
	mac := hmac.New(sha256.New, []byte(secretAccessKey))
	_, _ = mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	reqHeaders.Set("Authorization", fmt.Sprintf("%s Access=%s, SignedHeaders=%s, Signature=%s", Algorithm, accessKeyID, signedHeaders, signature))
	return SignedRequest{Headers: reqHeaders, Body: body, CanonicalRequest: canonicalRequest, StringToSign: stringToSign}, nil
}

func canonicalURI(path string) string {
	if path == "" {
		return path
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			decoded = part
		}
		parts[i] = encodePathSegment(decoded)
	}
	encoded := strings.Join(parts, "/")
	if !strings.HasSuffix(encoded, "/") {
		encoded += "/"
	}
	return encoded
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0)
	for _, k := range keys {
		vals := append([]string(nil), values[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, queryEscape(k)+"="+queryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func canonicalHeaders(headers http.Header) (string, string) {
	type item struct{ key, value string }
	items := make([]item, 0, len(headers))
	for key, values := range headers {
		items = append(items, item{strings.ToLower(key), strings.Join(values, ",")})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	var canonical strings.Builder
	signed := make([]string, 0, len(items))
	for _, it := range items {
		canonical.WriteString(it.key + ":" + it.value + "\n")
		signed = append(signed, it.key)
	}
	return canonical.String(), strings.Join(signed, ";")
}

func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func queryEscape(v string) string { return strings.ReplaceAll(url.QueryEscape(v), "+", "%20") }

func encodePathSegment(v string) string {
	encoded := url.PathEscape(v)
	replacer := strings.NewReplacer("%21", "!", "%7E", "~", "%2A", "*", "%27", "'", "%28", "(", "%29", ")")
	return replacer.Replace(encoded)
}
