package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
)

func inviteToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Path != "/invite" {
		t.Fatalf("invite link = %q", link)
	}
	return u.Query().Get("token")
}

func TestTeamInvitesAndRoles(t *testing.T) {
	h := newHarness(t)
	owner := h.newClient()
	me := owner.signup("owner@example.com", "Sharma Sweets")

	// Workspace settings.
	var ws auth.Workspace
	owner.do("GET", "/internal/team/workspace", nil, http.StatusOK, &ws)
	if ws.Name != "Sharma Sweets" || ws.TimeZone != "Asia/Kolkata" {
		t.Fatalf("workspace = %+v", ws)
	}
	owner.do("PATCH", "/internal/team/workspace", map[string]any{"name": "Sharma Sweets", "time_zone": "Mars/Base"}, http.StatusBadRequest, nil)
	owner.do("PATCH", "/internal/team/workspace", map[string]any{
		"name": "Sharma Sweets & Snacks", "legal_name": "Sharma Foods Pvt Ltd", "time_zone": "Asia/Dubai",
	}, http.StatusOK, &ws)
	if ws.Name != "Sharma Sweets & Snacks" || ws.LegalName == nil || ws.TimeZone != "Asia/Dubai" {
		t.Fatalf("updated workspace = %+v", ws)
	}

	// A new person accepts an invite by creating an account.
	var inv auth.Invite
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "Ravi@Example.com", "role": "owner"}, http.StatusBadRequest, nil)
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "owner@example.com", "role": "agent"}, http.StatusConflict, nil)
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "Ravi@Example.com", "role": "agent"}, http.StatusCreated, &inv)
	ravi := h.newClient()
	var info auth.InviteInfo
	ravi.do("GET", "/internal/auth/invites/"+inviteToken(t, inv.Link), nil, http.StatusOK, &info)
	if info.Email != "ravi@example.com" || info.Role != "agent" || info.AccountExists || info.TenantName != "Sharma Sweets & Snacks" {
		t.Fatalf("invite info = %+v", info)
	}
	accept := map[string]string{"token": inviteToken(t, inv.Link), "name": "Ravi", "password": "short"}
	ravi.do("POST", "/internal/auth/invites/accept", accept, http.StatusBadRequest, nil)
	accept["password"] = "a long enough password"
	var raviMe auth.MeResponse
	ravi.do("POST", "/internal/auth/invites/accept", accept, http.StatusOK, &raviMe)
	if raviMe.Tenant == nil || raviMe.Tenant.ID != me.Tenant.ID || raviMe.Tenant.Role != "agent" || !raviMe.User.EmailVerified {
		t.Fatalf("accepted = %+v", raviMe)
	}
	ravi.do("POST", "/internal/auth/invites/accept", accept, http.StatusBadRequest, nil) // used up
	ravi.do("GET", "/internal/team/members", nil, http.StatusForbidden, nil)

	// Someone who already has an account must be logged in as the invited email.
	meera := h.newClient()
	meeraMe := meera.signup("meera@example.com", "Meera Crafts")
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "meera@example.com", "role": "developer"}, http.StatusCreated, &inv)
	tok := inviteToken(t, inv.Link)
	h.newClient().do("POST", "/internal/auth/invites/accept", map[string]string{"token": tok}, http.StatusUnauthorized, nil)
	ravi.do("POST", "/internal/auth/invites/accept", map[string]string{"token": tok}, http.StatusForbidden, nil)
	var joined auth.MeResponse
	meera.do("POST", "/internal/auth/invites/accept", map[string]string{"token": tok}, http.StatusOK, &joined)
	if joined.Tenant == nil || joined.Tenant.ID != me.Tenant.ID || joined.Tenant.Role != "developer" || len(joined.Memberships) != 2 {
		t.Fatalf("meera joined = %+v", joined)
	}

	var members struct{ Data []auth.Member }
	owner.do("GET", "/internal/team/members", nil, http.StatusOK, &members)
	if len(members.Data) != 3 {
		t.Fatalf("members = %+v", members.Data)
	}
	var invites struct{ Data []auth.Invite }
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "arjun@example.com", "role": "admin"}, http.StatusCreated, &inv)
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "arjun@example.com", "role": "agent"}, http.StatusCreated, &inv)
	owner.do("GET", "/internal/team/invites", nil, http.StatusOK, &invites)
	if len(invites.Data) != 1 || invites.Data[0].Role != "agent" || invites.Data[0].Link != "" {
		t.Fatalf("open invites = %+v", invites.Data)
	}
	owner.do("DELETE", "/internal/team/invites/"+inv.ID.String(), nil, http.StatusNoContent, nil)
	owner.do("DELETE", "/internal/team/invites/"+inv.ID.String(), nil, http.StatusNotFound, nil)

	// Roles: admins manage agents and developers, never owners or admins; nobody changes themselves.
	raviID, meeraID := raviMe.User.ID.String(), meeraMe.User.ID.String()
	owner.do("PATCH", "/internal/team/members/"+raviID, map[string]string{"role": "admin"}, http.StatusNoContent, nil)
	ravi.do("PATCH", "/internal/team/members/"+meeraID, map[string]string{"role": "admin"}, http.StatusForbidden, nil)
	ravi.do("PATCH", "/internal/team/members/"+meeraID, map[string]string{"role": "agent"}, http.StatusNoContent, nil)
	ravi.do("PATCH", "/internal/team/members/"+me.User.ID.String(), map[string]string{"role": "agent"}, http.StatusForbidden, nil)
	ravi.do("POST", "/internal/team/invites", map[string]string{"email": "x@example.com", "role": "admin"}, http.StatusForbidden, nil)
	owner.do("PATCH", "/internal/team/members/"+me.User.ID.String(), map[string]string{"role": "admin"}, http.StatusConflict, nil)
	ravi.do("PATCH", "/internal/team/workspace", map[string]any{"name": "X", "time_zone": "Asia/Kolkata"}, http.StatusForbidden, nil)

	// Removing a member takes the workspace away from their sessions at once.
	ravi.do("DELETE", "/internal/team/members/"+meeraID, nil, http.StatusNoContent, nil)
	var after auth.MeResponse
	meera.do("GET", "/internal/auth/me", nil, http.StatusOK, &after)
	if after.Tenant != nil || len(after.Memberships) != 1 {
		t.Fatalf("meera after removal = %+v", after)
	}

	// Changing the password keeps this session and ends the others.
	other := h.newClient()
	other.do("POST", "/internal/auth/login", map[string]string{"email": "owner@example.com", "password": "a long enough password"}, http.StatusOK, nil)
	owner.do("POST", "/internal/auth/password", map[string]string{"current_password": "wrong password!", "new_password": "a brand new password"}, http.StatusBadRequest, nil)
	owner.do("POST", "/internal/auth/password", map[string]string{"current_password": "a long enough password", "new_password": "a brand new password"}, http.StatusNoContent, nil)
	other.do("GET", "/internal/auth/me", nil, http.StatusUnauthorized, nil)
	var renamed auth.MeResponse
	owner.do("PATCH", "/internal/auth/profile", map[string]string{"name": "Asha Sharma"}, http.StatusOK, &renamed)
	if renamed.User.Name != "Asha Sharma" {
		t.Fatalf("renamed = %+v", renamed.User)
	}
	h.newClient().do("POST", "/internal/auth/login", map[string]string{"email": "owner@example.com", "password": "a brand new password"}, http.StatusOK, nil)
}
