package server_test

import (
	"net/http"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
)

func TestNumberSyncQualityAndUsage(t *testing.T) {
	h := newHarness(t)
	c, _, phone := h.connected()
	if phone.RegisteredAt == nil || phone.QualityRating != "green" || phone.QualityDropped ||
		deref32(phone.DailyLimit) != 250 || phone.LimitUsedToday == nil || *phone.LimitUsedToday != 0 {
		t.Fatalf("connected number = %+v", phone)
	}

	h.meta.numbers = map[string]string{"555001": "+91 98765 43210", "555999": "+91 90000 00000"}
	h.meta.listQuality = "YELLOW"
	var res struct{ Synced int }
	c.do("POST", "/internal/numbers/sync", nil, http.StatusOK, &res)
	if res.Synced != 1 {
		t.Fatalf("synced = %d, want 1 (the other number is not connected here)", res.Synced)
	}
	c.do("GET", "/v1/phone-numbers/"+phone.ID.String(), nil, http.StatusOK, &phone)
	if phone.QualityRating != "yellow" || deref(phone.PreviousQualityRating) != "green" || !phone.QualityDropped ||
		phone.QualityChangedAt == nil || deref32(phone.DailyLimit) != 1000 {
		t.Fatalf("after sync = %+v", phone)
	}

	// An onboarding session records when it reached each step.
	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &sess)
	c.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusOK, &sess)
	if len(sess.StepTimes) != 1 || sess.StepTimes["started"].IsZero() {
		t.Fatalf("session = %+v", sess)
	}

	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("POST", "/internal/numbers/sync", nil, http.StatusOK, &res)
	if res.Synced != 0 {
		t.Fatalf("other workspace synced %d", res.Synced)
	}
	var list struct{ Data []numbers.PhoneNumber }
	other.do("GET", "/v1/phone-numbers", nil, http.StatusOK, &list)
	if len(list.Data) != 0 {
		t.Fatal("other workspace sees the number")
	}
}

func deref32(p *int32) int32 {
	if p == nil {
		return -999
	}
	return *p
}
