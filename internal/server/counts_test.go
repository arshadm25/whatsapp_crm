package server_test

import (
	"net/http"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
)

func TestInboxCounts(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	h.inbound(customer, "wamid.C1", "one")
	h.inbound(customer, "wamid.C2", "two")
	h.inbound("919800000002", "wamid.C3", "hello")

	var page convPage
	c.do("GET", "/v1/conversations", nil, http.StatusOK, &page)
	c.do("PATCH", "/v1/conversations/"+page.Data[0].ID.String(), map[string]any{"assignee_id": me.User.ID}, http.StatusOK, nil)

	var got inbox.Counts
	c.do("GET", "/internal/inbox/counts", nil, http.StatusOK, &got)
	want := inbox.Counts{Open: 2, Mine: 1, Unassigned: 1, UnreadConversations: 2, UnreadMessages: 3}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	c.do("PATCH", "/v1/conversations/"+page.Data[1].ID.String(), map[string]any{"status": "closed"}, http.StatusOK, nil)
	c.do("GET", "/internal/inbox/counts", nil, http.StatusOK, &got)
	if got.Open != 1 || got.Closed != 1 || got.Unassigned != 0 {
		t.Fatalf("after close = %+v", got)
	}
	c.do("GET", "/internal/inbox/counts?phone_number_id=x", nil, http.StatusBadRequest, nil)

	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/internal/inbox/counts", nil, http.StatusOK, &got)
	if got != (inbox.Counts{}) {
		t.Fatalf("other tenant counts = %+v", got)
	}
}

func TestContactTotalsAndActivity(t *testing.T) {
	h := newHarness(t)
	c, _, _ := h.connected()
	h.inbound(customer, "wamid.T1", "hi") // creates the contact with a conversation
	optIn := map[string]any{"status": "opted_in", "source": "csv_import", "evidence": "shop form"}
	c.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000011", "opt_in": optIn}, http.StatusCreated, nil)
	c.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000012", "opt_in": optIn}, http.StatusCreated, nil)
	var blocked contacts.Contact
	c.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000013"}, http.StatusCreated, &blocked)
	c.do("PATCH", "/v1/contacts/"+blocked.ID.String(), map[string]any{"blocked": true}, http.StatusOK, nil)

	var sum contacts.Summary
	c.do("GET", "/internal/contacts/summary", nil, http.StatusOK, &sum)
	if sum != (contacts.Summary{Total: 4, OptedIn: 2, Unknown: 1, Blocked: 1}) {
		t.Fatalf("summary = %+v", sum)
	}

	type page struct {
		Data       []contacts.Listed
		NextCursor *string `json:"next_cursor"`
		Total      int32
	}
	var p page
	c.do("GET", "/v1/contacts?limit=1&opt_in_status=opted_in", nil, http.StatusOK, &p)
	if len(p.Data) != 1 || p.Total != 2 || p.NextCursor == nil || deref(p.Data[0].OptInSource) != "csv_import" {
		t.Fatalf("opted-in page = %+v", p)
	}
	c.do("GET", "/v1/contacts?blocked=true", nil, http.StatusOK, &p)
	if len(p.Data) != 1 || p.Total != 1 || p.Data[0].ID != blocked.ID {
		t.Fatalf("blocked = %+v", p)
	}
	c.do("GET", "/v1/contacts?blocked=maybe", nil, http.StatusBadRequest, nil)

	c.do("GET", "/v1/contacts?q="+customer, nil, http.StatusOK, &p)
	if len(p.Data) != 1 || p.Data[0].LastMessageAt == nil || p.Data[0].OptInSource != nil {
		t.Fatalf("customer = %+v", p.Data)
	}
	var one contacts.Listed
	c.do("GET", "/v1/contacts/"+p.Data[0].ID.String(), nil, http.StatusOK, &one)
	if one.ConversationCount == nil || *one.ConversationCount != 1 || one.LastMessageAt == nil {
		t.Fatalf("contact = %+v", one)
	}
}
