package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Accounts (SPEC.md R55, R84; ADR 0025, ADR 0033): everyone who has signed
// in, and the admin promoting and demoting, disabling, enabling and
// deleting them. Names and emails are in the answers, which only admins
// get, and never in a log line: the log names accounts by their IDs.

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
	// AuthentikPending: the status was changed here and hasn't reached
	// Authentik yet (ADR 0033). Disabling or enabling again tries again.
	AuthentikPending bool `json:"authentik_pending"`
}

func accountInfo(a store.Account) AccountInfo {
	return AccountInfo{ID: a.Subject, Name: a.Name, Email: a.Email, Role: a.Role, Status: a.Status,
		FirstSignedIn: a.FirstSignedIn.UTC(), LastSignedIn: a.LastSignedIn.UTC(), AuthentikPending: a.AuthentikPending}
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

// AuthentikMissed is the answer when the radio made its change and
// Authentik didn't take it (502), or a deletion Authentik refused, which
// changed nothing (502, or 409 for a protected user): the R78 error and
// message, and for a disabling or an enabling the account as the radio
// changed it.
type AuthentikMissed struct {
	Code    string       `json:"error"`
	Message string       `json:"message"`
	Account *AccountInfo `json:"account,omitempty"`
}

// accountRefusal is the store's and the account's refusals of a change to
// an account, as the admin API answers them; nil for any other error.
func accountRefusal(err error) error {
	switch {
	case errors.Is(err, store.ErrOwnRole):
		return refuseAdmin(http.StatusConflict, "own_role", "An admin can't change their own role. Another admin can.")
	case errors.Is(err, store.ErrOwnAccount):
		return refuseAdmin(http.StatusConflict, "own_account", "An admin can't disable, enable or delete their own account. Another admin can.")
	case errors.Is(err, store.ErrLastAdmin):
		return refuseAdmin(http.StatusConflict, "last_admin", "That's the last active admin. Make another account an admin first.")
	case errors.Is(err, store.ErrNoAccount):
		return refuseAdmin(http.StatusNotFound, "account_not_found", "No account has that ID.")
	case errors.Is(err, store.ErrNotAdmin):
		// Demoted or disabled since this request was checked.
		return refuseAdmin(http.StatusForbidden, "admin_required", "This account isn't an admin.")
	}
	return nil
}

// authentikMissed is Authentik's part of a change gone wrong, in words:
// what stands in the radio (done), and what the admin can do.
func authentikMissed(err error, done string, acc *AccountInfo) (int, any, bool) {
	switch {
	case errors.Is(err, account.ErrAuthentikProtected):
		return http.StatusConflict, AuthentikMissed{Code: "authentik_protected", Account: acc,
			Message: done + " Authentik didn't change: that user is an Authentik superuser or service account, which the radio never changes. Do it in Authentik's admin interface."}, true
	case errors.Is(err, account.ErrAuthentikOff):
		return http.StatusBadGateway, AuthentikMissed{Code: "authentik_off", Account: acc,
			Message: done + " Authentik wasn't told: the radio has no Authentik API token (AUTHENTIK_API_TOKEN). Do it in Authentik's admin interface, or set the token and try again."}, true
	case errors.Is(err, account.ErrAuthentikUnreachable):
		return http.StatusBadGateway, AuthentikMissed{Code: "authentik_unreachable", Account: acc,
			Message: done + " Authentik couldn't be reached just now. Try again: repeating it tries Authentik again."}, true
	}
	return 0, nil, false
}

// patchAccount changes one thing about an account: its role,
// {"role": "admin"} or {"role": "listener"} (ADR 0025), or its status,
// {"status": "disabled"} or {"status": "active"} (ADR 0033), never both
// at once. An admin never changes their own account, and the last active
// admin can't be demoted or disabled: both are checked in the same
// transaction as the change, with the active admins locked.
//
// Disabling signs the account out of every browser at its next request
// and refuses its next sign-in, then switches its Authentik user off. If
// Authentik doesn't take it, the radio's change stands and the answer
// (502, or 409 for an Authentik superuser or service account) says so;
// the same request again tries Authentik again. Enabling mirrors it.
//
// The log names the admin and the account by their IDs.
func (a *Admin) patchAccount(r *http.Request, who string) (int, any, error) {
	subject := r.PathValue("subject")
	if subject == "" || len(subject) > 255 {
		return 0, nil, refuseAdmin(http.StatusNotFound, "account_not_found", "No account has that ID.")
	}
	var in struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	by, _ := AdminFrom(r.Context())
	switch {
	case (in.Role == nil) == (in.Status == nil):
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_body", `Send one of "role" or "status".`)
	case in.Role != nil:
		if *in.Role != store.RoleAdmin && *in.Role != store.RoleListener {
			return 0, nil, refuseAdmin(http.StatusBadRequest, "bad_role", `The role is "admin" or "listener".`)
		}
		acc, err := a.accs.SetRole(r.Context(), by, subject, *in.Role)
		if ref := accountRefusal(err); ref != nil {
			return 0, nil, ref
		} else if err != nil {
			return 0, nil, err
		}
		a.log.Info("admin set an account's role", "admin", who, "account", subject, "role", acc.Role)
		return http.StatusOK, accountInfo(acc), nil
	}
	status := *in.Status
	if status != store.StatusDisabled && status != store.StatusActive {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "bad_status", `The status is "disabled" or "active".`)
	}
	acc, err := a.accs.SetStatus(r.Context(), by, subject, status)
	if ref := accountRefusal(err); ref != nil {
		return 0, nil, ref
	}
	info := accountInfo(acc)
	done := "Enabled in the radio: they can sign in to it again once Authentik lets them."
	if status == store.StatusDisabled {
		done = "Disabled in the radio: they're signed out of it and can't sign in to it."
	}
	if code, body, ok := authentikMissed(err, done, &info); ok {
		a.log.Warn("admin set an account's status; Authentik didn't take it", "admin", who, "account", subject, "status", status)
		return code, body, nil
	}
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin set an account's status", "admin", who, "account", subject, "status", status)
	return http.StatusOK, info, nil
}

// deleteAccount deletes an account (ADR 0033; owner, 2026-11-25): its
// row, its saved profile and its sessions, and in the standalone
// deployment its Authentik user, all or nothing. The audit entry keeps
// what was deleted. Under disabling's rules: never an admin's own
// account, never the last active admin. If Authentik doesn't take it,
// nothing is deleted and the answer says so; repeating it tries again.
// It answers the account as it was.
func (a *Admin) deleteAccount(r *http.Request, who string) (int, any, error) {
	subject := r.PathValue("subject")
	if subject == "" || len(subject) > 255 {
		return 0, nil, refuseAdmin(http.StatusNotFound, "account_not_found", "No account has that ID.")
	}
	by, _ := AdminFrom(r.Context())
	acc, err := a.accs.Delete(r.Context(), by, subject)
	if ref := accountRefusal(err); ref != nil {
		return 0, nil, ref
	}
	if code, body, ok := authentikMissed(err, "Nothing was deleted.", nil); ok {
		a.log.Warn("admin's account deletion refused: Authentik didn't take it", "admin", who, "account", subject)
		return code, body, nil
	}
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin deleted an account", "admin", who, "account", subject)
	return http.StatusOK, accountInfo(acc), nil
}
