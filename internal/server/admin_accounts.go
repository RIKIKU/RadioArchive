package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Accounts and their roles (SPEC.md R55; ADR 0025): everyone who has
// signed in, and the admin promoting and demoting them. The Accounts page
// and disabling come with issue #105.

// AccountInfo is one account, as admins see it.
type AccountInfo struct {
	// ID is Authentik's user ID (the OIDC subject).
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	FirstSignedIn time.Time `json:"first_signed_in"`
	LastSignedIn  time.Time `json:"last_signed_in"`
}

func accountInfo(a store.Account) AccountInfo {
	return AccountInfo{ID: a.Subject, Name: a.Name, Email: a.Email, Role: a.Role, Status: a.Status,
		FirstSignedIn: a.FirstSignedIn.UTC(), LastSignedIn: a.LastSignedIn.UTC()}
}

// Me is GET /api/v1/admin/me: the admin making the request, as the admin
// check found them, and how they signed in (ADR 0028). The PowerShell
// module asks it when it connects, since the account's route
// (/api/v1/account) is the browser's.
type Me struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// Via is "browser" or "module".
	Via string `json:"via"`
}

func (a *Admin) me(r *http.Request, _ string) (int, any, error) {
	adm, ok := AdminFrom(r.Context())
	if !ok {
		return 0, nil, errors.New("me: no admin in the request")
	}
	return http.StatusOK, Me{ID: adm.Subject, Name: adm.Name, Email: adm.Email, Role: store.RoleAdmin, Via: adm.Via}, nil
}

func (a *Admin) listAccounts(r *http.Request, _ string) (int, any, error) {
	all, err := a.accs.Accounts(r.Context())
	if err != nil {
		return 0, nil, err
	}
	out := make([]AccountInfo, 0, len(all))
	for _, acc := range all {
		out = append(out, accountInfo(acc))
	}
	return http.StatusOK, out, nil
}

// patchAccount sets an account's role: {"role": "admin"} or
// {"role": "listener"}. An admin never changes their own role, and the
// last active admin can't be demoted; both are checked in the same
// transaction as the change, with the active admins locked. The log names
// the admin and the account by their IDs.
func (a *Admin) patchAccount(r *http.Request, who string) (int, any, error) {
	subject := r.PathValue("subject")
	if subject == "" || len(subject) > 255 {
		return 0, nil, refuseAdmin(http.StatusNotFound, "account_not_found", "No account has that ID.")
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.Role != store.RoleAdmin && in.Role != store.RoleListener {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "bad_role", `The role is "admin" or "listener".`)
	}
	by, _ := AdminFrom(r.Context())
	acc, err := a.accs.SetRole(r.Context(), by, subject, in.Role)
	switch {
	case errors.Is(err, store.ErrOwnRole):
		return 0, nil, refuseAdmin(http.StatusConflict, "own_role", "An admin can't change their own role. Another admin can.")
	case errors.Is(err, store.ErrLastAdmin):
		return 0, nil, refuseAdmin(http.StatusConflict, "last_admin", "That's the last active admin. Make another account an admin first.")
	case errors.Is(err, store.ErrNoAccount):
		return 0, nil, refuseAdmin(http.StatusNotFound, "account_not_found", "No account has that ID.")
	case errors.Is(err, store.ErrNotAdmin):
		// Demoted or disabled since this request was checked.
		return 0, nil, refuseAdmin(http.StatusForbidden, "admin_required", "This account isn't an admin.")
	case err != nil:
		return 0, nil, err
	}
	a.log.Info("admin set an account's role", "admin", who, "account", subject, "role", acc.Role)
	return http.StatusOK, accountInfo(acc), nil
}
