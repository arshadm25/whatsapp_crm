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

	"github.com/arshadm25/whatsapp_crm/internal/admin"
	"github.com/arshadm25/whatsapp_crm/internal/ai"
	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/bots"
	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/deletion"
	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/events"
	"github.com/arshadm25/whatsapp_crm/internal/flows"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
	"github.com/arshadm25/whatsapp_crm/internal/media"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/metrics"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
	"github.com/arshadm25/whatsapp_crm/internal/razorpay"
	"github.com/arshadm25/whatsapp_crm/internal/server"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
	"github.com/arshadm25/whatsapp_crm/internal/testdb"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
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
	templates   []string          // JSON objects returned by GET message_templates
	created     []map[string]any  // bodies of template creates
	uploads     [][]byte          // files uploaded to POST /{phone}/media
	inbound     map[string][]byte // inbound media ID -> file served for download
	profile     map[string]any    // business profile returned by GET whatsapp_business_profile
	pictures    [][]byte          // files sent through the resumable upload API
	flowN       int               // Flows created
	flowAssets  [][]byte          // Flow JSON files uploaded
	flowErrs    string            // JSON array of validation errors returned for Flow uploads
	flowStatus  map[string]string // Flow ID -> status Meta reports
}

func (f *fakeMeta) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v24.0")
	f.calls = append(f.calls, r.Method+" "+path)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case len(parts) == 2 && parts[0] == "download":
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(f.inbound[parts[1]])
	case len(parts) == 2 && parts[1] == "media" && r.Method == http.MethodPost:
		file, _, err := r.FormFile("file")
		if err != nil || r.FormValue("messaging_product") != "whatsapp" || r.FormValue("type") == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"bad upload","code":100}}`)
			return
		}
		b, _ := io.ReadAll(file)
		f.uploads = append(f.uploads, b)
		fmt.Fprintf(w, `{"id":"metamedia-%d"}`, len(f.uploads))
	case len(parts) == 1 && strings.HasPrefix(parts[0], "media-"):
		b, ok := f.inbound[parts[0]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"unknown media","code":100}}`)
			return
		}
		fmt.Fprintf(w, `{"url":"http://%s/download/%s","mime_type":"image/jpeg","file_size":%d}`, r.Host, parts[0], len(b))
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
	case len(parts) == 2 && parts[1] == "deregister":
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 2 && parts[1] == "whatsapp_business_profile":
		if r.Method == http.MethodGet {
			p := f.profile
			if p == nil {
				p = map[string]any{}
			}
			b, _ := json.Marshal(map[string]any{"data": []any{p}})
			_, _ = w.Write(b)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.profile == nil {
			f.profile = map[string]any{}
		}
		for k, v := range body {
			if k == "messaging_product" {
				continue
			}
			if k == "profile_picture_handle" {
				k, v = "profile_picture_url", "https://cdn.example/"+fmt.Sprint(v)
			}
			f.profile[k] = v
		}
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 2 && parts[1] == "uploads":
		fmt.Fprint(w, `{"id":"upload:abc"}`)
	case len(parts) == 1 && strings.HasPrefix(parts[0], "upload:"):
		if r.Header.Get("Authorization") != "OAuth EAAG-cmeta" && !strings.HasPrefix(r.Header.Get("Authorization"), "OAuth ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.pictures = append(f.pictures, b)
		fmt.Fprint(w, `{"h":"4:handle"}`)
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
	case len(parts) == 2 && parts[1] == "flows" && r.Method == http.MethodPost:
		f.flowN++
		id := fmt.Sprintf("flow-%d", f.flowN)
		if f.flowStatus == nil {
			f.flowStatus = map[string]string{}
		}
		f.flowStatus[id] = "DRAFT"
		fmt.Fprintf(w, `{"id":%q,"success":true}`, id)
	case len(parts) == 2 && parts[1] == "assets":
		file, _, err := r.FormFile("file")
		if err != nil || r.FormValue("asset_type") != "FLOW_JSON" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"bad asset","code":100}}`)
			return
		}
		b, _ := io.ReadAll(file)
		f.flowAssets = append(f.flowAssets, b)
		errs := f.flowErrs
		if errs == "" {
			errs = "[]"
		}
		fmt.Fprintf(w, `{"success":true,"validation_errors":%s}`, errs)
	case len(parts) == 2 && (parts[1] == "publish" || parts[1] == "deprecate") && strings.HasPrefix(parts[0], "flow-"):
		f.flowStatus[parts[0]] = map[string]string{"publish": "PUBLISHED", "deprecate": "DEPRECATED"}[parts[1]]
		fmt.Fprint(w, `{"success":true}`)
	case len(parts) == 1 && strings.HasPrefix(parts[0], "flow-") && r.Method == http.MethodGet:
		errs := f.flowErrs
		if errs == "" {
			errs = "[]"
		}
		fmt.Fprintf(w, `{"id":%q,"name":"Flow","status":%q,"categories":["OTHER"],"validation_errors":%s,"preview":{"preview_url":"https://business.facebook.com/wa/manage/flows/preview/%s","expires_at":"2030-01-01T00:00:00+0000"}}`,
			parts[0], f.flowStatus[parts[0]], errs, parts[0])
	case len(parts) == 1 && strings.HasPrefix(parts[0], "flow-") && r.Method == http.MethodDelete:
		delete(f.flowStatus, parts[0])
		fmt.Fprint(w, `{"success":true}`)
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
	// downloader copies inbound media from Meta.
	downloader *media.DownloadWorker
	campaigns  *campaigns.Worker
	bots       *bots.Worker
	ingest     *ai.IngestWorker
	ai         *fakeAI
	razorpay   *fakeRazorpay
	keys       *envelope.Keyring
	log        *slog.Logger
}

func newHarness(t *testing.T) *harness {
	d := testdb.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fm := &fakeMeta{numbers: map[string]string{}, inbound: map[string][]byte{}}
	metaSrv := httptest.NewServer(fm)
	t.Cleanup(metaSrv.Close)
	rp := &fakeRazorpay{}
	rpSrv := httptest.NewServer(rp)
	t.Cleanup(rpSrv.Close)

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
	store, err := storage.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(d.Pool, log)
	hubCtx, stopHub := context.WithCancel(context.Background())
	t.Cleanup(stopHub)
	go hub.Run(hubCtx)
	fai := &fakeAI{}
	agent := ai.NewAgent(fai, log)
	h := server.NewAPI(server.APIDeps{
		Config: cfg, DB: d, Log: log,
		Auth:       auth.NewService(d, keys, cfg, mailer.Log{Logger: log}, log),
		Onboarding: onboarding.NewService(d, keys, meta, rc, log),
		Numbers:    numbers.NewService(d, keys, meta, log),
		Messaging:  messaging.NewService(d, keys, meta, rc, log),
		Templates:  templates.NewService(d, keys, meta, log),
		Inbox:      inbox.NewService(d, log),
		Media:      media.NewService(d, store, media.NewSigner(cfg.AppSecret), log),
		Developers: devportal.NewService(d, log),
		Keys:       devportal.NewAuthenticator(d, devportal.NewLimiter(5), log),
		Webhooks:   webhooks.NewService(d, keys, rc, log),
		Contacts:   contacts.NewService(d, log),
		Campaigns:  campaigns.NewService(d, rc, log),
		Bots:       bots.NewService(d, rc, log),
		Flows:      flows.NewService(d, keys, meta, log),
		AI:         ai.NewService(d, agent, rc, log).AllowPrivateURLs(),
		Analytics:  analytics.NewService(d, log),
		Admin:      admin.NewService(d, log),
		Billing:    billing.NewService(d, razorpay.New(rpSrv.URL, "rzp_test", "rzp_secret"), "whsec", sellerCfg, rc, log),
		Events:     hub,
		Deletion:   deletion.NewHandler(d, "app-secret", "https://app.example", log),
		Metrics:    metrics.New(d),
	})
	api := httptest.NewServer(h)
	t.Cleanup(api.Close)
	proc := metaevents.NewProcessor(d, log)
	proc.Jobs = rc
	sender := messaging.NewWorker(d, keys, meta, media.NewUploader(store, meta), log)
	sender.Jobs = rc
	runner := campaigns.NewWorker(d, log)
	runner.Jobs = rc
	botRunner := bots.NewWorker(d, log)
	botRunner.Jobs = rc
	botRunner.AI = agent
	return &harness{
		t: t, db: d, meta: fm, api: api,
		worker:     onboarding.NewWorker(d, keys, meta, templates.NewSyncer(d, meta), log),
		sender:     sender,
		events:     proc,
		downloader: media.NewDownloadWorker(d, keys, meta, store, log),
		campaigns:  runner,
		bots:       botRunner,
		ingest:     ai.NewIngestWorker(d, ai.Fetcher{AllowPrivate: true}, log),
		ai:         fai,
		razorpay:   rp,
		keys:       keys,
		log:        log,
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

// sellerCfg is Ecogo's side of the GST invoices the tests issue.
var sellerCfg = config.Seller{Name: "Ecogo Software Solutions Pvt Ltd", GSTIN: "32AABCE1234F1Z5",
	Address: "Kochi, Kerala", SAC: "998439", GSTRateBP: 1800}
