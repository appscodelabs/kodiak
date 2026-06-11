package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// errSyncAccounts ports models.SyncAccountsError.
var errSyncAccounts = errors.New("sync accounts failed")

type ghInstallationAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghInstallation struct {
	ID      int64                 `json:"id"`
	Account ghInstallationAccount `json:"account"`
}

func (a *App) fetchUserInstallations(ctx context.Context, accessToken string) ([]ghInstallation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/installations", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github.machine-man-preview+json")
	res, err := a.HTTPClient.Do(req)
	if err != nil {
		slog.Warn("sync_installation failed", "error", err)
		return nil, errSyncAccounts
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		slog.Warn("sync_installation failed", "status", res.StatusCode)
		return nil, errSyncAccounts
	}
	// TODO(chdsbd): Handle multiple pages of installations
	if link := res.Header.Get("Link"); strings.Contains(link, `rel="next"`) {
		slog.Warn("user has multiple pages")
	}
	var data struct {
		Installations []ghInstallation `json:"installations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		slog.Warn("sync_installation failed", "error", err)
		return nil, errSyncAccounts
	}
	return data.Installations, nil
}

// fetchOrgRole returns the user's role in the organization, or "" if the user
// doesn't have access (e.g. repository collaborators get a 403).
func (a *App) fetchOrgRole(ctx context.Context, accessToken, orgLogin, userLogin string) (string, error) {
	u := fmt.Sprintf("https://api.github.com/orgs/%s/memberships/%s", orgLogin, userLogin)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		slog.Warn("problem fetching account membership",
			"status", res.StatusCode,
			"installation_account_login", orgLogin,
			"user_github_login", userLogin)
		return "", nil
	}
	var data struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", err
	}
	return data.Role, nil
}

// syncAccounts ports User.sync_accounts:
//   - create any missing installations
//   - add memberships of user for installations
//   - remove memberships of installations that aren't included
func (a *App) syncAccounts(ctx context.Context, user *models.User) error {
	installations, err := a.fetchUserInstallations(ctx, user.GithubAccessToken)
	if err != nil {
		return err
	}

	var accountIDs []uuid.UUID
	for _, installation := range installations {
		var role string
		switch {
		case installation.Account.Type == "Organization":
			role, err = a.fetchOrgRole(ctx, user.GithubAccessToken, installation.Account.Login, user.GithubLogin)
			if err != nil {
				return errSyncAccounts
			}
			if role == "" {
				continue
			}
		case installation.Account.Type == "User" && installation.Account.Login == user.GithubLogin:
			role = models.RoleAdmin
		default:
			role = models.RoleMember
		}

		account, err := models.GetAccountByGithubAccountID(ctx, a.DB, installation.Account.ID)
		if err != nil {
			return err
		}
		if account == nil {
			account, err = models.CreateAccount(ctx, a.DB, installation.ID, installation.Account.ID,
				installation.Account.Login, installation.Account.Type)
			if err != nil {
				return err
			}
		} else {
			account.GithubInstallationID = installation.ID
			account.GithubAccountID = installation.Account.ID
			account.GithubAccountLogin = installation.Account.Login
			account.GithubAccountType = installation.Account.Type
			if err := models.SaveAccount(ctx, a.DB, account); err != nil {
				return err
			}
		}

		if err := models.UpsertMembership(ctx, a.DB, account.ID, user.ID, role); err != nil {
			return err
		}
		accountIDs = append(accountIDs, account.ID)
	}

	// remove installations to which the user no longer has access.
	return models.DeleteMembershipsExcluding(ctx, a.DB, user.ID, accountIDs)
}

// exchangeOAuthCode trades the OAuth code for an access token. The GitHub
// response body is querystring-encoded.
func (a *App) exchangeOAuthCode(ctx context.Context, code string) (url.Values, error) {
	form := url.Values{
		"client_id":     {a.Cfg.KodiakAPIGithubClientID},
		"client_secret": {a.Cfg.KodiakAPIGithubClientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth access token request failed: %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return url.ParseQuery(string(body))
}

type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

func (a *App) fetchGithubUser(ctx context.Context, accessToken string) (*ghUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("github user request failed: %d", res.StatusCode)
	}
	var u ghUser
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}
