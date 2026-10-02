package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
	"github.com/arshadm25/whatsapp_crm/internal/server"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
	"github.com/arshadm25/whatsapp_crm/internal/testdb"
)

// fakeMeta stands in for graph.facebook.com.
type fakeMeta struct {
	mu          sync.Mutex
	calls       []string
	registerErr string            // JSON error body for /register, if set
	numbers     map[string]string // phone number ID -> display number, per WABA listing
	sendErr     string            // JSON error body for sends, if set
	sent        []map[string]any  // bodies of message sends
	wamids      int
	templates   []string         // JSON objects returned by GET message_templates
	created     []map[string]any // bodies of template creates
}

func (f *fakeMeta) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v24.0")
	f.calls = append(f.calls, r.Method+" "+path)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case path == "/oauth/access_token":
		fmt.Fprintf(w, `{"access_token":"EAAG-%s"}`, r.URL.Query().Get("code"))
	case len(parts) == 2 && parts[1] == "subscribed_apps":
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 2 && parts[1] == "phone_numbers":
		var data []string
		for id, display := range f.numbers {
			data = append(data, fmt.Sprintf(`{"id":%q,"display_phone_number":%q,"is_on_biz_app":true}`, id, display))
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(data, ","))
	case len(parts) == 2 && parts[1] == "register":
		if f.registerErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, f.registerErr)
			return
		}
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 2 && parts[1] == "smb_app_data":
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 2 && parts[1] == "messages":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["status"] == "read" {
			fmt.Fprint(w, `{"success":true}`)
			return
		}
		f.sent = append(f.sent, body)
		if f.sendErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, f.sendErr)
			return
		}
		f.wamids++
		fmt.Fprintf(w, `{"messaging_product":"whatsapp","contacts":[{"input":%q,"wa_id":%q}],"messages":[{"id":"wamid.TEST%d"}]}`, body["to"], body["to"], f.wamids)
	case len(parts) == 2 && parts[1] == "message_templates":
		switch r.Method {
		case http.MethodGet:
			fmt.Fprintf(w, `{"data":[%s],"paging":{"cursors":{"before":"a","after":"b"}}}`, strings.Join(f.templates, ","))
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.created = append(f.created, body)
			fmt.Fprintf(w, `{"id":"9%03d","status":"PENDING","category":%q}`, len(f.created), body["category"])
		case http.MethodDelete:
			fmt.Fprint(w, `{"success":true}`)
		}
	case len(parts) == 1 && r.Method == http.MethodPost:
		fmt.Fprint(w, `{"success":true}`) // template edit
	case len(parts) == 1 && strings.HasPrefix(parts[0], "11"):
		fmt.Fprintf(w, `{"id":%q,"name":"Sharma Sweets","currency":"INR","timezone_id":"71"}`, parts[0])
	case len(parts) == 1:
		fmt.Fprintf(w, `{"id":%q,"display_phone_number":"+91 98765 43210","verified_name":"Sharma Sweets",
			"quality_rating":"GREEN","messaging_limit_tier":"TIER_250","code_verification_status":"VERIFIED"}`, parts[0])
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"unknown path","code":100}}`)
	}
}

func (f *fakeMeta) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type harness struct {
	t      *testing.T
	db     *db.DB
	meta   *fakeMeta
	api    *httptest.Server
	worker *onboarding.Worker
	sender *messaging.Worker
	events *metaevents.Processor
}

func newHarness(t *testing.T) *harness {
	d := testdb.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fm := &fakeMeta{numbers: map[string]string{}}
	metaSrv := httptest.NewServer(fm)
	t.Cleanup(metaSrv.Close)

	mk := make([]byte, 32)
	_, _ = rand.Read(mk)
	keys, err := envelope.NewKeyring(map[int][]byte{1: mk}, 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Env: "test", PublicAppURL: "http://localhost", SessionTTL: time.Hour,
		AppSecret: []byte("test-secret-test-secret-test-secret"),
		Meta:      config.Meta{AppID: "app", AppSecret: "secret", ConfigID: "cfg", GraphAPIVersion: "v24.0"},
	}
	meta := metaclient.New(metaSrv.URL, "v24.0", "app", "secret")
	rc, err := jobs.NewInsertOnly(d.Pool, log)
	if err != nil {
		t.Fatal(err)
	}
	h := server.NewAPI(server.APIDeps{
		Config: cfg, DB: d, Log: log,
		Auth:       auth.NewService(d, cfg, mailer.Log{Logger: log}, log),
		Onboarding: onboarding.NewService(d, keys, meta, rc, log),
		Numbers:    numbers.NewService(d, log),
		Messaging:  messaging.NewService(d, keys, meta, rc, log),
		Templates:  templates.NewService(d, keys, meta, log),
	})
	api := httptest.NewServer(h)
	t.Cleanup(api.Close)
	return &harness{
		t: t, db: d, meta: fm, api: api,
		worker: onboarding.NewWorker(d, keys, meta, templates.NewSyncer(d, meta), log),
		sender: messaging.NewWorker(d, keys, meta, log),
		events: metaevents.NewProcessor(d, log),
	}
}

// client is a browser-like client: it keeps cookies and echoes the CSRF cookie in the header.
type client struct {
	h    *harness
	http *http.Client
}

func (h *harness) newClient() *client {
	jar, _ := cookiejar.New(nil)
	c := &client{h: h, http: &http.Client{Jar: jar}}
	c.do("GET", "/internal/config", nil, http.StatusOK, nil)
	return c
}

func (c *client) do(method, path string, body any, wantStatus int, out any) {
	c.h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.h.api.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	u, _ := url.Parse(c.h.api.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookie {
			req.Header.Set(auth.CSRFHeader, ck.Value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		c.h.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.h.t.Fatalf("decode %s: %v: %s", path, err, raw)
		}
	}
}

func (c *client) signup(email, business string) auth.MeResponse {
	var me auth.MeResponse
	c.do("POST", "/internal/auth/signup", map[string]string{
		"name": "Asha", "email": email, "password": "a long enough password", "business_name": business,
	}, http.StatusCreated, &me)
	return me
}

// runJob runs the onboarding worker for a session the way River would, on attempt n.
func (h *harness) runJob(tenantID, sessionID uuid.UUID, attempt int) error {
	return h.worker.Work(context.Background(), &river.Job[onboarding.Args]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 5},
		Args:   onboarding.Args{SessionID: sessionID, TenantID: tenantID},
	})
}

func TestSignupLoginAndCSRF(t *testing.T) {
	h := newHarness(t)
	c := h.newClient()
	me := c.signup("asha@example.com", "Sharma Sweets")
	if me.Tenant == nil || me.Tenant.Role != "owner" || me.User.EmailVerified {
		t.Fatalf("signup me = %+v", me)
	}

	// Same email again is a conflict.
	c2 := h.newClient()
	c2.do("POST", "/internal/auth/signup", map[string]string{
		"name": "X", "email": "ASHA@example.com", "password": "a long enough password", "business_name": "Y",
	}, http.StatusConflict, nil)

	// Logout ends the session; login brings back the same tenant.
	c.do("POST", "/internal/auth/logout", nil, http.StatusNoContent, nil)
	c.do("GET", "/internal/auth/me", nil, http.StatusUnauthorized, nil)
	c.do("POST", "/internal/auth/login", map[string]string{"email": "asha@example.com", "password": "wrong password!"}, http.StatusUnauthorized, nil)
	var again auth.MeResponse
	c.do("POST", "/internal/auth/login", map[string]string{"email": "asha@example.com", "password": "a long enough password"}, http.StatusOK, &again)
	if again.Tenant == nil || again.Tenant.ID != me.Tenant.ID {
		t.Fatalf("login tenant = %+v, want %v", again.Tenant, me.Tenant.ID)
	}

	// A POST without the CSRF header is refused.
	req, _ := http.NewRequest("POST", h.api.URL+"/internal/onboarding/sessions", strings.NewReader(`{"flow":"standard"}`))
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestStandardOnboardingEndToEnd(t *testing.T) {
	h := newHarness(t)
	c := h.newClient()
	me := c.signup("owner@example.com", "Sharma Sweets")

	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &sess)
	if sess.State != "awaiting_signup" {
		t.Fatalf("state = %s", sess.State)
	}
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/complete", map[string]string{
		"code": "code1", "waba_id": "1100111", "phone_number_id": "555001", "business_id": "777",
	}, http.StatusOK, &sess)
	if sess.Step != "token_exchanged" || sess.State != "in_progress" {
		t.Fatalf("after complete: %+v", sess)
	}

	// The job was enqueued in the same transaction, and the token is stored only encrypted.
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(q *dbq.Queries, tx pgx.Tx) error {
		var jobsN int
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM river_job WHERE kind = 'onboarding'").Scan(&jobsN); err != nil {
			return err
		}
		if jobsN != 1 {
			t.Errorf("onboarding jobs = %d, want 1", jobsN)
		}
		var leaked bool
		if err := tx.QueryRow(context.Background(),
			"SELECT bool_or(position('EAAG' in encode(token_ciphertext, 'escape')) > 0) FROM meta_credentials").Scan(&leaked); err != nil {
			return err
		}
		if leaked {
			t.Error("plain token found in meta_credentials")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.runJob(me.Tenant.ID, sess.ID, 1); err != nil {
		t.Fatalf("worker: %v", err)
	}
	c.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusOK, &sess)
	if sess.State != "completed" {
		t.Fatalf("final session = %+v", sess)
	}
	for _, call := range []string{"POST /1100111/subscribed_apps", "POST /555001/register"} {
		if h.meta.called(call) != 1 {
			t.Errorf("%s called %d times, want 1", call, h.meta.called(call))
		}
	}
	if h.meta.called("POST /555001/smb_app_data") != 0 {
		t.Error("standard flow requested coexistence sync")
	}

	var list struct{ Data []numbers.PhoneNumber }
	c.do("GET", "/v1/phone-numbers", nil, http.StatusOK, &list)
	if len(list.Data) != 1 {
		t.Fatalf("numbers = %+v", list.Data)
	}
	n := list.Data[0]
	if n.Status != "connected" || n.QualityRating != "green" || n.WabaID != "1100111" || n.IsCoexistence ||
		n.MessagingLimitTier == nil || *n.MessagingLimitTier != "TIER_250" {
		t.Fatalf("number = %+v", n)
	}

	// Another workspace cannot claim the same WABA, and cannot see this tenant's number.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	var s2 onboarding.SessionView
	other.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &s2)
	other.do("POST", "/internal/onboarding/sessions/"+s2.ID.String()+"/complete", map[string]string{
		"code": "code2", "waba_id": "1100111", "phone_number_id": "555001",
	}, http.StatusConflict, nil)
	other.do("GET", "/v1/phone-numbers/"+n.ID.String(), nil, http.StatusNotFound, nil)
	other.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusNotFound, nil)
}

func TestCoexistenceOnboarding(t *testing.T) {
	h := newHarness(t)
	h.meta.numbers["555002"] = "+91 90000 00002"
	c := h.newClient()
	me := c.signup("coex@example.com", "Kerala Spices")

	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "coexistence"}, http.StatusCreated, &sess)
	// Coexistence sign-ups may report only the WABA.
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/complete", map[string]string{
		"code": "code3", "waba_id": "1100222",
	}, http.StatusOK, &sess)
	if err := h.runJob(me.Tenant.ID, sess.ID, 1); err != nil {
		t.Fatalf("worker: %v", err)
	}
	c.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusOK, &sess)
	if sess.State != "completed" || sess.PhoneNumberID == nil || *sess.PhoneNumberID != "555002" {
		t.Fatalf("session = %+v", sess)
	}
	if h.meta.called("POST /555002/register") != 0 {
		t.Error("coexistence number was re-registered")
	}
	if h.meta.called("POST /555002/smb_app_data") != 2 {
		t.Errorf("smb_app_data calls = %d, want 2 (contacts, history)", h.meta.called("POST /555002/smb_app_data"))
	}
	var list struct{ Data []numbers.PhoneNumber }
	c.do("GET", "/v1/phone-numbers", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || !list.Data[0].IsCoexistence {
		t.Fatalf("numbers = %+v", list.Data)
	}
}

func TestFailedStepCanBeRetried(t *testing.T) {
	h := newHarness(t)
	h.meta.registerErr = `{"error":{"message":"PIN mismatch","code":133005}}`
	c := h.newClient()
	me := c.signup("retry@example.com", "Retry Traders")

	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &sess)
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/complete", map[string]string{
		"code": "code4", "waba_id": "1100333", "phone_number_id": "555003",
	}, http.StatusOK, &sess)

	err := h.runJob(me.Tenant.ID, sess.ID, 1)
	var cancel *river.JobCancelError
	if err == nil || !errorsAs(err, &cancel) {
		t.Fatalf("worker err = %v, want JobCancel", err)
	}
	c.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusOK, &sess)
	if sess.State != "failed" || sess.Step != "webhooks_subscribed" || sess.Error == nil || sess.Error.Code != "meta_133005" {
		t.Fatalf("failed session = %+v", sess)
	}

	// The client turns off two-step verification, then retries from the failed step.
	h.meta.registerErr = ""
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/retry", nil, http.StatusOK, &sess)
	if sess.State != "in_progress" {
		t.Fatalf("after retry: %+v", sess)
	}
	if err := h.runJob(me.Tenant.ID, sess.ID, 1); err != nil {
		t.Fatalf("worker after retry: %v", err)
	}
	c.do("GET", "/internal/onboarding/sessions/"+sess.ID.String(), nil, http.StatusOK, &sess)
	if sess.State != "completed" {
		t.Fatalf("after retry run: %+v", sess)
	}
	if h.meta.called("POST /1100333/subscribed_apps") != 1 {
		t.Error("retry repeated a step that had already succeeded")
	}
}

func TestRowLevelSecurity(t *testing.T) {
	h := newHarness(t)
	a := h.newClient().signup("a@example.com", "Tenant A")
	b := h.newClient().signup("b@example.com", "Tenant B")
	ctx := context.Background()

	err := h.db.InTenant(ctx, a.Tenant.ID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.CreateOnboardingSession(ctx, dbq.CreateOnboardingSessionParams{
			ID: db.NewID(), TenantID: a.Tenant.ID, UserID: a.User.ID, Flow: dbq.OnboardingFlowStandard,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Tenant B sees none of A's rows, even with no WHERE clause.
	err = h.db.InTenant(ctx, b.Tenant.ID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListOnboardingSessions(ctx, 100)
		if len(rows) != 0 {
			t.Errorf("tenant B sees %d of A's sessions", len(rows))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writing a row for A while scoped to B is refused by the policy.
	err = h.db.InTenant(ctx, b.Tenant.ID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.CreateOnboardingSession(ctx, dbq.CreateOnboardingSessionParams{
			ID: db.NewID(), TenantID: a.Tenant.ID, UserID: b.User.ID, Flow: dbq.OnboardingFlowStandard,
		})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("cross-tenant insert err = %v, want RLS violation", err)
	}
	// With no tenant set, tenant tables are empty.
	err = h.db.Global(ctx, func(_ *dbq.Queries, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM memberships").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("memberships visible without tenant: %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
