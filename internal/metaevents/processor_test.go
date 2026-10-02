package metaevents_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/testdb"
)

const (
	wabaID  = "1100111"
	phoneID = "555001"
)

type fixture struct {
	t        *testing.T
	db       *db.DB
	proc     *metaevents.Processor
	tenantID uuid.UUID
	phone    dbq.PhoneNumber
	account  dbq.WhatsappAccount
}

// newFixture creates a tenant with one connected WABA and number.
func newFixture(t *testing.T) *fixture {
	d := testdb.New(t)
	ctx := context.Background()
	f := &fixture{t: t, db: d, proc: metaevents.NewProcessor(d, slog.New(slog.NewTextHandler(io.Discard, nil))), tenantID: db.NewID()}
	err := d.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.CreateTenant(ctx, dbq.CreateTenantParams{ID: f.tenantID, Name: "Shop", Slug: "shop-" + f.tenantID.String()[:8]})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = d.InTenant(ctx, f.tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		f.account, err = q.UpsertWhatsAppAccount(ctx, dbq.UpsertWhatsAppAccountParams{
			ID: db.NewID(), TenantID: f.tenantID, WabaID: wabaID, BusinessID: "777", OnboardingFlow: dbq.OnboardingFlowStandard,
		})
		if err != nil {
			return err
		}
		tier := "TIER_250"
		f.phone, err = q.UpsertPhoneNumber(ctx, dbq.UpsertPhoneNumberParams{
			ID: db.NewID(), TenantID: f.tenantID, WhatsappAccountID: f.account.ID, PhoneNumberID: phoneID,
			DisplayPhoneNumber: "+91 98765 43210", QualityRating: dbq.QualityRatingGreen, MessagingLimitTier: &tier,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// deliver runs one webhook delivery through the processor, as River would.
func (f *fixture) deliver(body string) {
	f.t.Helper()
	sum := sha256.Sum256([]byte(body))
	err := f.proc.Work(context.Background(), &river.Job[metaevents.ProcessArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 10},
		Args:   metaevents.ProcessArgs{Payload: []byte(body), PayloadHash: hex.EncodeToString(sum[:]), ReceivedAt: time.Now()},
	})
	if err != nil {
		f.t.Fatalf("Work: %v", err)
	}
}

func (f *fixture) query(fn func(q *dbq.Queries, tx pgx.Tx) error) {
	f.t.Helper()
	if err := f.db.InTenant(context.Background(), f.tenantID, fn); err != nil {
		f.t.Fatal(err)
	}
}

func envelope(field, value string) string {
	return fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":%q,"changes":[{"field":%q,"value":%s}]}]}`, wabaID, field, value)
}

func inboundText(wamid, from, name, text string, ts int64) string {
	return envelope("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":%q},
		"contacts":[{"profile":{"name":%q},"wa_id":%q}],
		"messages":[{"from":%q,"id":%q,"timestamp":"%d","type":"text","text":{"body":%q}}]}`,
		phoneID, name, from, from, wamid, ts, text))
}

func statusUpdate(wamid, status string, ts int64) string {
	return envelope("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":%q},
		"statuses":[{"id":%q,"status":%q,"timestamp":"%d","recipient_id":"919000000001",
			"pricing":{"billable":true,"category":"utility","pricing_model":"PMP"}}]}`, phoneID, wamid, status, ts))
}

func TestInboundMessageCreatesContactConversationAndMessage(t *testing.T) {
	f := newFixture(t)
	ts := time.Now().Add(-time.Minute).Unix()
	body := inboundText("wamid.A1", "919000000001", "Ravi", "Is the shop open today?", ts)
	f.deliver(body)
	f.deliver(body) // Meta retried the same delivery
	// The same message inside a different delivery (different bytes) is also stored once.
	f.deliver(inboundText("wamid.A1", "919000000001", "Ravi K", "Is the shop open today?", ts))

	ctx := context.Background()
	f.query(func(q *dbq.Queries, tx pgx.Tx) error {
		var contacts, convs, msgs int
		if err := tx.QueryRow(ctx, "SELECT (SELECT count(*) FROM contacts), (SELECT count(*) FROM conversations), (SELECT count(*) FROM messages)").
			Scan(&contacts, &convs, &msgs); err != nil {
			return err
		}
		if contacts != 1 || convs != 1 || msgs != 1 {
			t.Errorf("contacts=%d conversations=%d messages=%d, want 1 each", contacts, convs, msgs)
		}
		m, err := q.GetMessageByWamid(ctx, ptr("wamid.A1"))
		if err != nil {
			return err
		}
		if m.Direction != dbq.MessageDirectionInbound || m.Status != dbq.MessageStatusReceived || m.Type != dbq.MessageTypeText ||
			m.Origin != dbq.MessageOriginCustomer || m.MetaTimestamp == nil || m.MetaTimestamp.Unix() != ts {
			t.Errorf("message = %+v", m)
		}
		var preview string
		var unread int
		var lastInbound time.Time
		if err := tx.QueryRow(ctx, "SELECT last_message_preview, unread_count, last_inbound_at FROM conversations").Scan(&preview, &unread, &lastInbound); err != nil {
			return err
		}
		if preview != "Is the shop open today?" || unread != 1 || lastInbound.Unix() != ts {
			t.Errorf("conversation preview=%q unread=%d last_inbound=%v", preview, unread, lastInbound)
		}
		var profile string
		if err := tx.QueryRow(ctx, "SELECT profile_name FROM contacts").Scan(&profile); err != nil {
			return err
		}
		if profile != "Ravi K" {
			t.Errorf("profile name = %q, want the latest", profile)
		}
		return nil
	})

	var events, processed int
	err := f.db.Global(ctx, func(_ *dbq.Queries, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*), count(processed_at) FROM meta_webhook_events WHERE tenant_id = $1", f.tenantID).Scan(&events, &processed)
	})
	if err != nil || events != 2 || processed != 2 {
		t.Fatalf("meta_webhook_events = %d (processed %d), err %v; want 2 distinct deliveries", events, processed, err)
	}
}

func TestStatusesOnlyMoveForward(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// An outbound message the (upcoming) send path would have created.
	f.query(func(q *dbq.Queries, tx pgx.Tx) error {
		c, err := q.UpsertContactFromWhatsApp(ctx, dbq.UpsertContactFromWhatsAppParams{ID: db.NewID(), TenantID: f.tenantID, WaID: "919000000001"})
		if err != nil {
			return err
		}
		conv, err := q.UpsertConversation(ctx, dbq.UpsertConversationParams{ID: db.NewID(), TenantID: f.tenantID, PhoneNumberID: f.phone.ID, ContactID: c.ID})
		if err != nil {
			return err
		}
		_, err = q.InsertWhatsAppMessage(ctx, dbq.InsertWhatsAppMessageParams{
			ID: db.NewID(), TenantID: f.tenantID, ConversationID: conv.ID, PhoneNumberID: f.phone.ID, ContactID: c.ID,
			Direction: dbq.MessageDirectionOutbound, Origin: dbq.MessageOriginDashboard, Wamid: ptr("wamid.OUT1"),
			Type: dbq.MessageTypeText, Content: []byte(`{"text":{"body":"Yes"}}`), Status: dbq.MessageStatusSent,
		})
		return err
	})

	now := time.Now().Unix()
	f.deliver(statusUpdate("wamid.OUT1", "read", now))
	f.deliver(statusUpdate("wamid.OUT1", "delivered", now-5))  // arrives late
	f.deliver(statusUpdate("wamid.UNKNOWN", "delivered", now)) // not ours: ignored

	f.query(func(q *dbq.Queries, tx pgx.Tx) error {
		m, err := q.GetMessageByWamid(ctx, ptr("wamid.OUT1"))
		if err != nil {
			return err
		}
		if m.Status != dbq.MessageStatusRead {
			t.Errorf("status = %s, want read (delivered arrived after read)", m.Status)
		}
		if m.PricingCategory == nil || *m.PricingCategory != "utility" || m.PricingBillable == nil || !*m.PricingBillable {
			t.Errorf("pricing = %v %v", m.PricingCategory, m.PricingBillable)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM message_status_events WHERE message_id = $1", m.ID).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("status events = %d, want both kept", n)
		}
		return nil
	})
}

func TestCoexistenceEchoAndContactSync(t *testing.T) {
	f := newFixture(t)
	ts := time.Now().Unix()
	f.deliver(envelope("smb_message_echoes", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":%q},
		"message_echoes":[{"from":"919876543210","to":"919000000002","id":"wamid.ECHO1","timestamp":"%d","type":"text","text":{"body":"Sent from my phone"}}]}`, phoneID, ts)))
	f.deliver(envelope("smb_app_state_sync", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":%q},
		"state_sync":[{"type":"contact","action":"add","contact":{"full_name":"Meera S","first_name":"Meera","phone_number":"919000000003"},"metadata":{"timestamp":"%d"}},
		              {"type":"contact","action":"remove","contact":{"phone_number":"919000000002"},"metadata":{"timestamp":"%d"}}]}`, phoneID, ts, ts)))

	ctx := context.Background()
	f.query(func(q *dbq.Queries, tx pgx.Tx) error {
		m, err := q.GetMessageByWamid(ctx, ptr("wamid.ECHO1"))
		if err != nil {
			return err
		}
		if m.Direction != dbq.MessageDirectionOutbound || m.Origin != dbq.MessageOriginPhoneApp || m.Status != dbq.MessageStatusSent {
			t.Errorf("echo = %+v", m)
		}
		var lastInbound *time.Time
		if err := tx.QueryRow(ctx, "SELECT last_inbound_at FROM conversations WHERE id = $1", m.ConversationID).Scan(&lastInbound); err != nil {
			return err
		}
		if lastInbound != nil {
			t.Error("an echo opened the 24-hour window")
		}
		var name string
		if err := tx.QueryRow(ctx, "SELECT name FROM contacts WHERE wa_id = '919000000003'").Scan(&name); err != nil {
			return err
		}
		if name != "Meera S" {
			t.Errorf("synced contact name = %q", name)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM contacts WHERE wa_id = '919000000002'").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Error("a removal from the phone deleted the contact here")
		}
		return nil
	})
}

func TestTemplateQualityAndAccountEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.query(func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO templates (tenant_id, whatsapp_account_id, meta_template_id, name, language, category, status, components)
			VALUES ($1, $2, '998877', 'order_update', 'en', 'utility', 'pending', '[]')`, f.tenantID, f.account.ID)
		return err
	})

	f.deliver(envelope("message_template_status_update",
		`{"event":"REJECTED","message_template_id":998877,"message_template_name":"order_update","message_template_language":"en","reason":"INVALID_FORMAT"}`))
	f.deliver(envelope("phone_number_quality_update",
		`{"display_phone_number":"919876543210","event":"UPGRADE","current_limit":"TIER_2K"}`))

	f.query(func(_ *dbq.Queries, tx pgx.Tx) error {
		var status, reason, tier string
		if err := tx.QueryRow(ctx, "SELECT status::text, rejected_reason FROM templates").Scan(&status, &reason); err != nil {
			return err
		}
		if status != "rejected" || reason != "INVALID_FORMAT" {
			t.Errorf("template = %s %q", status, reason)
		}
		if err := tx.QueryRow(ctx, "SELECT messaging_limit_tier FROM phone_numbers").Scan(&tier); err != nil {
			return err
		}
		if tier != "TIER_2K" {
			t.Errorf("tier = %s", tier)
		}
		return nil
	})

	f.deliver(envelope("account_update", `{"event":"PARTNER_REMOVED"}`))
	f.query(func(_ *dbq.Queries, tx pgx.Tx) error {
		var acct, phone string
		if err := tx.QueryRow(ctx, "SELECT w.status::text, p.status::text FROM whatsapp_accounts w JOIN phone_numbers p ON p.whatsapp_account_id = w.id").
			Scan(&acct, &phone); err != nil {
			return err
		}
		if acct != "revoked" || phone != "revoked" {
			t.Errorf("after PARTNER_REMOVED: account %s, phone %s", acct, phone)
		}
		return nil
	})
}

func TestUnknownNumberIsStoredButNotApplied(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"999","changes":[{"field":"messages","value":
		{"metadata":{"phone_number_id":"000"},"messages":[{"from":"919000000001","id":"wamid.X","timestamp":"1","type":"text","text":{"body":"hi"}}]}}]}]}`)
	f.deliver(body)
	ctx := context.Background()
	var tenant *uuid.UUID
	var processed *time.Time
	err := f.db.Global(ctx, func(_ *dbq.Queries, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT tenant_id, processed_at FROM meta_webhook_events").Scan(&tenant, &processed)
	})
	if err != nil || tenant != nil || processed == nil {
		t.Fatalf("unrouted event tenant=%v processed=%v err=%v", tenant, processed, err)
	}
}

func ptr(s string) *string { return &s }
