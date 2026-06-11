// Package webapi is the Go port of the Django backend in web_api/.
package webapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	stripesdk "github.com/stripe/stripe-go/v78"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
	"github.com/appscodelabs/kodiak/internal/webapi/session"
	"github.com/appscodelabs/kodiak/internal/webapi/settings"
)

type App struct {
	Cfg      *settings.Config
	DB       *pgxpool.Pool
	Redis    *redis.Client
	Sessions *session.Manager
	// HTTP client for GitHub API calls; the Python code used 5 second
	// timeouts on every request.
	HTTPClient *http.Client
}

func New(cfg *settings.Config, db *pgxpool.Pool, rdb *redis.Client) *App {
	stripesdk.Key = cfg.StripeSecretKey
	return &App{
		Cfg:        cfg,
		DB:         db,
		Redis:      rdb,
		Sessions:   session.New(cfg.SecretKey, !cfg.Debug),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// apiError mirrors web_api.exceptions.ApiException: rendered as JSON
// {"message": ...} with the given status code.
type apiError struct {
	code    int
	message string
}

func (e *apiError) Error() string { return e.message }

func errBadRequest() error       { return &apiError{code: 400, message: "Bad Request"} }
func errAuthRequired() error     { return &apiError{code: 401, message: "Authentication Required"} }
func errPermissionDenied() error { return &apiError{code: 403, message: "Permission Denied"} }
func errUnprocessable(msg string) error {
	return &apiError{code: 422, message: msg}
}

// plainError is rendered as a plain-text response (Django's Http404 and
// HttpResponseBadRequest paths).
type plainError struct {
	code int
	body string
}

func (e *plainError) Error() string { return e.body }

func errNotFound() error { return &plainError{code: 404, body: "Not Found"} }

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// handle maps handler errors to responses like middleware.ExceptionMiddleware.
func handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			writeJSON(w, apiErr.code, map[string]string{"message": apiErr.message})
			return
		}
		var pErr *plainError
		if errors.As(err, &pErr) {
			http.Error(w, pErr.body, pErr.code)
			return
		}
		slog.Error("internal error", "path", r.URL.Path, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

type authedHandlerFunc func(w http.ResponseWriter, r *http.Request, user *models.User) error

// loginRequired ports auth.login_required.
func (a *App) loginRequired(h authedHandlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		user, err := a.currentUser(r)
		if err != nil {
			return err
		}
		if user == nil {
			return errAuthRequired()
		}
		return h(w, r, user)
	}
}

// currentUser ports auth.get_user: returns nil for anonymous users.
func (a *App) currentUser(r *http.Request) (*models.User, error) {
	userID, ok := a.Sessions.UserID(r)
	if !ok {
		return nil, nil
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil
	}
	return models.GetUserByID(r.Context(), a.DB, id)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal JSON response", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// teamAccount loads the account from the {team_id} path segment, requiring
// the user to be a member (get_account_or_404).
func (a *App) teamAccount(r *http.Request, user *models.User) (*models.Account, error) {
	teamID, err := uuid.Parse(r.PathValue("team_id"))
	if err != nil {
		// the Django URL converter rejects non-UUID values with a 404.
		return nil, errNotFound()
	}
	account, err := models.GetAccountForUser(r.Context(), a.DB, teamID, user.ID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, errNotFound()
	}
	return account, nil
}

// djangoDateTime formats a timestamp like DjangoJSONEncoder: ISO-8601 with
// millisecond precision and a "Z" suffix for UTC.
func djangoDateTime(t time.Time) string {
	t = t.UTC()
	s := t.Format("2006-01-02T15:04:05")
	if t.Nanosecond() != 0 {
		s += fmt.Sprintf(".%03d", t.Nanosecond()/1e6)
	}
	return s + "Z"
}

func djangoDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// naiveDateTime mirrors datetime.datetime.fromtimestamp().isoformat():
// local time without a timezone suffix.
func naiveDateTime(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02T15:04:05")
}
