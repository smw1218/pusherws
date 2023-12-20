package pusherws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PusherSigner signs using the key and secret. This should only be used server-side
// where the secret can be kept secret. The Auth method matches the Client's AuthCallback type.
// This is great for testing.
type PusherSigner struct {
	Key    string
	Secret string
}

// Auth may be used as the Client's AuthCallback
func (ps *PusherSigner) Auth(socketID, channelName string) (string, error) {
	signingString := strings.Join([]string{socketID, channelName}, ":")
	hmacSigner := hmac.New(sha256.New, []byte(ps.Secret))
	hmacSigner.Write([]byte(signingString))
	signed := hmacSigner.Sum(nil)
	signature := hex.EncodeToString(signed)
	return strings.Join([]string{ps.Key, signature}, ":"), nil
}

// NewHTTPAuth will POST to the authURL with additional headers specified
// in header. This uses the http.DefaultClient by default.
func NewHTTPAuth(authURL string, header http.Header) (*HTTPAuth, error) {
	return &HTTPAuth{
		header:  header,
		authURL: authURL,
		Client:  http.DefaultClient,
	}, nil
}

// HTTPAuth implements an AuthCallback that uses the default webhook
// format in the Pusher docs and used by their lib. The call is a
// www-form-urlencoded POST including "channel_name" and "socket_id".
// The response is expected to be a JSON object with a single key "auth"
// containing the signing string.
type HTTPAuth struct {
	header  http.Header
	authURL string
	Client  *http.Client
}

// Auth may be used as the Client's AuthCallback
func (ha *HTTPAuth) Auth(socketID, channelName string) (string, error) {
	body := url.Values{
		"channel_name": []string{channelName},
		"socket_id":    []string{socketID},
	}.Encode()
	req, err := http.NewRequest(http.MethodPost, ha.authURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range ha.header {
		req.Header[k] = v
	}
	req.Header.Add("Content-type", "application/x-www-form-urlencoded")
	resp, err := ha.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed auth check with status %v", resp.Status)
	}

	authResp := make(map[string]string, 1)
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&authResp)
	if err != nil {
		return "", fmt.Errorf("failed auth response JSON decode: %w", err)
	}
	//map[string]string{"auth": authString}
	authString, ok := authResp["auth"]
	if !ok {
		return "", fmt.Errorf("no auth key in response")
	}
	return authString, nil
}
