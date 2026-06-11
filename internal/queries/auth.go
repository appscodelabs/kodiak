package queries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/throttle"
)

type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (t *TokenResponse) Expired() bool {
	return t.ExpiresAt.Add(-5 * time.Minute).Before(time.Now().UTC())
}

var (
	installationCacheMu sync.Mutex
	installationCache   = map[string]*TokenResponse{}
)

// GenerateJWT creates an authentication token to make application requests.
// https://developer.github.com/apps/building-github-apps/authenticating-with-github-apps/#authenticating-as-a-github-app
//
// This is different from authenticating as an installation.
func GenerateJWT(privateKey string, appIdentifier string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKey))
	if err != nil {
		return "", fmt.Errorf("parsing github app private key: %w", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(9*time.Minute + 30*time.Second).Unix(),
		"iss": appIdentifier,
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
}

// GetTokenForInstall fetches (and caches) an installation access token.
// https://developer.github.com/apps/building-github-apps/authenticating-with-github-apps/#authenticating-as-an-installation
func GetTokenForInstall(ctx context.Context, session *http.Client, installationID string) (string, error) {
	installationCacheMu.Lock()
	token, ok := installationCache[installationID]
	installationCacheMu.Unlock()
	if ok && token != nil && !token.Expired() {
		return token.Token, nil
	}
	appToken, err := GenerateJWT(appconf.PrivateKey, appconf.GithubAppID)
	if err != nil {
		return "", err
	}
	// ApplicationID isn't a real installation ID, but it provides rate
	// limiting for our GithubApp instead of the installations we typically
	// act as.
	throttler := throttle.ForInstallation(ApplicationID)
	if err := throttler.Acquire(ctx); err != nil {
		return "", err
	}
	res, err := rawRequest(ctx, session, http.MethodPost,
		appconf.V3URL(fmt.Sprintf("/app/installations/%s/access_tokens", installationID)),
		map[string]string{
			"Accept":        "application/vnd.github.machine-man-preview+json",
			"Authorization": "Bearer " + appToken,
		}, nil)
	if err != nil {
		return "", err
	}
	if res.StatusCode > 300 {
		return "", fmt.Errorf("failed to get token, github response: %s", string(res.Body))
	}
	var tokenResponse TokenResponse
	if err := json.Unmarshal(res.Body, &tokenResponse); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	installationCacheMu.Lock()
	installationCache[installationID] = &tokenResponse
	installationCacheMu.Unlock()
	return tokenResponse.Token, nil
}

func getHeaders(ctx context.Context, session *http.Client, installationID string) (map[string]string, error) {
	token, err := GetTokenForInstall(ctx, session, installationID)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"Authorization": "token " + token,
		"Accept":        "application/vnd.github.machine-man-preview+json,application/vnd.github.antiope-preview+json,application/vnd.github.lydian-preview+json",
	}, nil
}
