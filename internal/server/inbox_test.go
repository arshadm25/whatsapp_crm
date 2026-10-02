package server_test

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

type convPage struct {
	Data       []inbox.Conversation `json:"data"`
	NextCursor *string              `json:"next_cursor"`
}

func TestInboxConversations(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.A1", "Is my order ready?")
	h.inbound("919800000002", "wamid.B1", "Hello")

	var page convPage
	c.do("GET", "/v1/conversations", nil, http.StatusOK, &page)
	if len(page.Data) != 2 {
		t.Fatalf("conversations = %d, want 2", len(page.Data))
	}
	conv := page.Data[1] // the first customer wrote first, so it is older
	if conv.Contact.WaID != customer || deref(conv.Contact.ProfileName) != "Ravi" || !conv.Window.Open ||
		conv.UnreadCount != 1 || conv.Status != "open" || conv.PhoneNumberID != phone.ID {
		t.Fatalf("conversation = %+v", conv)
	}

	for q, want := range map[string]int{
		"window=closed": 0, "window=open": 2, "assignee_id=none": 2, "q=9800000001": 1, "q=Ravi": 2, "q=zzz": 0, "status=closed": 0,
	} {
		c.do("GET", "/v1/conversations?"+q, nil, http.StatusOK, &page)
		if len(page.Data) != want {
			t.Errorf("%s: %d conversations, want %d", q, len(page.Data), want)
		}
	}
	c.do("GET", "/v1/conversations?limit=1", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	first := page.Data[0].ID
	c.do("GET", "/v1/conversations?limit=1&cursor="+url.QueryEscape(*page.NextCursor), nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].ID == first || page.NextCursor != nil {
		t.Fatalf("second page = %+v", page)
	}

	// Assign to me, then close.
	c.do("PATCH", "/v1/conversations/"+conv.ID.String(), map[string]any{"assignee_id": me.User.ID}, http.StatusOK, &conv)
	if conv.AssigneeID == nil || *conv.AssigneeID != me.User.ID {
		t.Fatalf("assigned = %+v", conv)
	}
	c.do("GET", "/v1/conversations?assignee_id=me", nil, http.StatusOK, &page)
	if len(page.Data) != 1 {
		t.Fatalf("mine = %d", len(page.Data))
	}
	c.do("PATCH", "/v1/conversations/"+conv.ID.String(), map[string]any{"assignee_id": uuid.New()}, http.StatusUnprocessableEntity, nil)
	c.do("PATCH", "/v1/conversations/"+conv.ID.String(), map[string]any{"status": "closed", "assignee_id": nil}, http.StatusOK, &conv)
	if conv.Status != "closed" || conv.AssigneeID != nil {
		t.Fatalf("closed = %+v", conv)
	}
	// A new customer message reopens it.
	h.inbound(customer, "wamid.A2", "Hello?")
	c.do("GET", "/v1/conversations/"+conv.ID.String(), nil, http.StatusOK, &conv)
	if conv.Status != "open" || conv.UnreadCount != 2 {
		t.Fatalf("after new message = %+v", conv)
	}

	// Messages, newest first, including our reply.
	c.send(text(phone.ID, "Yes, it ships today"), "", http.StatusAccepted, nil)
	var msgs struct {
		Data       []messaging.Message `json:"data"`
		NextCursor *string             `json:"next_cursor"`
	}
	c.do("GET", "/v1/conversations/"+conv.ID.String()+"/messages", nil, http.StatusOK, &msgs)
	if len(msgs.Data) != 3 || msgs.Data[0].Direction != "outbound" || msgs.Data[2].Wamid == nil || *msgs.Data[2].Wamid != "wamid.A1" {
		t.Fatalf("messages = %+v", msgs.Data)
	}
	c.do("GET", "/v1/conversations/"+conv.ID.String()+"/messages?limit=2", nil, http.StatusOK, &msgs)
	c.do("GET", "/v1/conversations/"+conv.ID.String()+"/messages?limit=2&cursor="+*msgs.NextCursor, nil, http.StatusOK, &msgs)
	if len(msgs.Data) != 1 || msgs.NextCursor != nil {
		t.Fatalf("second messages page = %+v", msgs)
	}

	// Another workspace sees none of it.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/v1/conversations", nil, http.StatusOK, &page)
	if len(page.Data) != 0 {
		t.Fatalf("other tenant sees %d conversations", len(page.Data))
	}
	other.do("GET", "/v1/conversations/"+conv.ID.String()+"/messages", nil, http.StatusNotFound, nil)
}

func TestInboxNotesAndQuickReplies(t *testing.T) {
	h := newHarness(t)
	c, _, _ := h.connected()
	h.inbound(customer, "wamid.N1", "hi")
	var page convPage
	c.do("GET", "/v1/conversations", nil, http.StatusOK, &page)
	conv := page.Data[0]

	c.do("POST", "/internal/inbox/conversations/"+conv.ID.String()+"/notes", map[string]string{"body": "VIP customer"}, http.StatusCreated, nil)
	var notes struct{ Data []inbox.Note }
	c.do("GET", "/internal/inbox/conversations/"+conv.ID.String()+"/notes", nil, http.StatusOK, &notes)
	if len(notes.Data) != 1 || notes.Data[0].AuthorName != "Asha" || notes.Data[0].Body != "VIP customer" {
		t.Fatalf("notes = %+v", notes.Data)
	}

	var qr inbox.QuickReply
	c.do("POST", "/internal/inbox/quick-replies", map[string]string{"shortcut": "/hours", "body": "We are open 9 to 6."}, http.StatusCreated, &qr)
	c.do("POST", "/internal/inbox/quick-replies", map[string]string{"shortcut": "HOURS", "body": "dup"}, http.StatusConflict, nil)
	var qrs struct{ Data []inbox.QuickReply }
	c.do("GET", "/internal/inbox/quick-replies", nil, http.StatusOK, &qrs)
	if len(qrs.Data) != 1 || qrs.Data[0].Shortcut != "hours" {
		t.Fatalf("quick replies = %+v", qrs.Data)
	}
	c.do("DELETE", "/internal/inbox/quick-replies/"+qr.ID.String(), nil, http.StatusNoContent, nil)

	var members struct{ Data []inbox.Member }
	c.do("GET", "/internal/inbox/members", nil, http.StatusOK, &members)
	if len(members.Data) != 1 || members.Data[0].Role != "owner" {
		t.Fatalf("members = %+v", members.Data)
	}
}

func TestLiveEvents(t *testing.T) {
	h := newHarness(t)
	c, _, _ := h.connected()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.api.URL+"/internal/events", nil)
	u, _ := url.Parse(h.api.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookie {
			req.Header.Set(auth.CSRFHeader, ck.Value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(resp.Body)
	lines.Scan() // "retry: 3000" sent once subscribed

	// The listener may still be connecting; deliver until an event arrives.
	got := make(chan string, 1)
	go func() {
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), "event: message") {
				got <- lines.Text()
				return
			}
		}
	}()
	for i := 0; ; i++ {
		h.inbound(customer, "wamid.E"+string(rune('a'+i)), "ping")
		select {
		case <-got:
			return
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("no live event received")
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
