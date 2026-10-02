package campaigns

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

// RunArgs is the River job that starts a campaign and then queues one batch of its messages.
// Each run schedules the next until no recipient is pending.
type RunArgs struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	// Generation is the campaign's run_generation when the job was queued. An edit, pause or
	// resume changes it, which retires jobs queued before.
	Generation int32 `json:"generation,omitempty"`
}

func (RunArgs) Kind() string { return "run_campaign" }

func (RunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueCampaigns, MaxAttempts: 10}
}

const (
	// maxBatch caps the messages queued in one transaction.
	maxBatch = 1000
	// limitWait is how long a campaign waits when the number's messaging limit is used up.
	limitWait = 15 * time.Minute
)

// tierLimits is how many customers Meta lets a number start conversations with in 24 hours.
var tierLimits = map[string]int32{
	"TIER_250": 250, "TIER_1K": 1000, "TIER_2K": 2000, "TIER_10K": 10000, "TIER_100K": 100000,
}

// dailyLimit returns the number's messaging limit; -1 means unlimited. A number Meta has not
// reported a tier for yet gets the starting limit.
func dailyLimit(tier *string) int32 {
	if tier == nil {
		return 250
	}
	if *tier == "TIER_UNLIMITED" {
		return -1
	}
	if n, ok := tierLimits[*tier]; ok {
		return n
	}
	return 250
}

type Worker struct {
	river.WorkerDefaults[RunArgs]
	db  *db.DB
	log *slog.Logger
	// Jobs enqueues send jobs and the next run; when nil, the River client working the job is used.
	Jobs jobs.Inserter
	now  func() time.Time
}

func NewWorker(d *db.DB, log *slog.Logger) *Worker {
	return &Worker{db: d, log: log, now: time.Now}
}

func (w *Worker) Timeout(*river.Job[RunArgs]) time.Duration { return 2 * time.Minute }

func (w *Worker) Work(ctx context.Context, job *river.Job[RunArgs]) error {
	_, err := w.Run(ctx, job.Args)
	return err
}

// Run does one step of a campaign and returns when the next step should run (zero when none).
func (w *Worker) Run(ctx context.Context, a RunArgs) (time.Time, error) {
	ins, err := jobs.From(ctx, w.Jobs)
	if err != nil {
		return time.Time{}, err
	}
	log := w.log.With("campaign_id", a.CampaignID, "tenant_id", a.TenantID)
	var next time.Time
	err = w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		c, err := q.GetCampaignForUpdate(ctx, a.CampaignID)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if c.RunGeneration != a.Generation {
			return nil
		}
		switch c.Status {
		case dbq.CampaignStatusScheduled:
			var aud Audience
			_ = json.Unmarshal(c.Audience, &aud)
			ids, tags := aud.params()
			n, err := q.ExpandCampaignAudience(ctx, dbq.ExpandCampaignAudienceParams{CampaignID: c.ID, ContactIds: ids, Tags: tags})
			if err != nil {
				return err
			}
			if err := q.StartCampaign(ctx, c.ID); err != nil {
				return err
			}
			log.Info("campaign started", "recipients", n)
		case dbq.CampaignStatusRunning:
		default:
			return nil
		}
		next, err = w.batch(ctx, q, tx, ins, c, log)
		if err != nil || next.IsZero() {
			return err
		}
		_, err = ins.InsertTx(ctx, tx, a, &river.InsertOpts{ScheduledAt: next})
		return err
	})
	return next, err
}

// batch queues the next messages of a running campaign. It returns when to run again, or zero
// when the campaign has finished.
func (w *Worker) batch(ctx context.Context, q *dbq.Queries, tx pgx.Tx, ins jobs.Inserter, c dbq.Campaign, log *slog.Logger) (time.Time, error) {
	stop := func(reason string) (time.Time, error) {
		log.Warn("campaign stopped", "reason", reason)
		if err := q.SkipPendingRecipients(ctx, dbq.SkipPendingRecipientsParams{CampaignID: c.ID, Reason: &reason}); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, q.FinishCampaign(ctx, dbq.FinishCampaignParams{ID: c.ID, Status: dbq.CampaignStatusFailed})
	}
	num, err := q.GetSendingNumber(ctx, c.PhoneNumberID)
	if err != nil {
		return time.Time{}, err
	}
	if num.PhoneNumber.Status != dbq.ConnectionStatusConnected || num.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
		return stop("number_disconnected")
	}
	t, err := q.GetTemplate(ctx, c.TemplateID)
	if db.IsNotFound(err) || (err == nil && t.Status != dbq.TemplateStatusApproved) {
		return stop("template_unavailable")
	}
	if err != nil {
		return time.Time{}, err
	}

	rate := int32(DefaultRate)
	if c.SendRatePerMin != nil {
		rate = *c.SendRatePerMin
	}
	size := min(rate, maxBatch)
	if limit := dailyLimit(num.PhoneNumber.MessagingLimitTier); limit >= 0 {
		used, err := q.BusinessInitiatedToday(ctx, c.PhoneNumberID)
		if err != nil {
			return time.Time{}, err
		}
		if room := limit - used; room < size {
			size = room
		}
		if size <= 0 {
			log.Info("campaign waiting for the messaging limit", "limit", limit)
			return w.now().Add(limitWait), nil
		}
	}

	rows, err := q.NextCampaignRecipients(ctx, dbq.NextCampaignRecipientsParams{CampaignID: c.ID, Lim: size})
	if err != nil {
		return time.Time{}, err
	}
	var vars map[string]string
	_ = json.Unmarshal(c.Variables, &vars)
	now := w.now()
	for _, row := range rows {
		contact := row.Contact
		content, ok := render(t, vars, contact)
		if !ok {
			err := q.SkipCampaignRecipient(ctx, dbq.SkipCampaignRecipientParams{CampaignID: c.ID, ContactID: contact.ID, Reason: ptr("missing_variable")})
			if err != nil {
				return time.Time{}, err
			}
			continue
		}
		body, err := json.Marshal(content)
		if err != nil {
			return time.Time{}, err
		}
		conv, err := q.UpsertConversation(ctx, dbq.UpsertConversationParams{
			ID: db.NewID(), TenantID: c.TenantID, PhoneNumberID: c.PhoneNumberID, ContactID: contact.ID,
		})
		if err != nil {
			return time.Time{}, err
		}
		msg, err := q.InsertCampaignMessage(ctx, dbq.InsertCampaignMessageParams{
			ID: db.NewID(), TenantID: c.TenantID, ConversationID: conv.ID, PhoneNumberID: c.PhoneNumberID,
			ContactID: contact.ID, Content: body, TemplateID: &t.ID, CampaignID: &c.ID,
		})
		if err != nil {
			return time.Time{}, err
		}
		if _, err := ins.InsertTx(ctx, tx, messaging.SendArgs{MessageID: msg.ID, TenantID: c.TenantID}, nil); err != nil {
			return time.Time{}, err
		}
		if err := q.QueueCampaignRecipient(ctx, dbq.QueueCampaignRecipientParams{CampaignID: c.ID, ContactID: contact.ID, MessageID: &msg.ID}); err != nil {
			return time.Time{}, err
		}
		if err := q.TouchConversationOutbound(ctx, dbq.TouchConversationOutboundParams{ID: conv.ID, At: now, Preview: "Template: " + t.Name}); err != nil {
			return time.Time{}, err
		}
	}

	more, err := q.HasPendingRecipients(ctx, c.ID)
	if err != nil {
		return time.Time{}, err
	}
	if !more {
		log.Info("campaign finished")
		return time.Time{}, q.FinishCampaign(ctx, dbq.FinishCampaignParams{ID: c.ID, Status: dbq.CampaignStatusCompleted})
	}
	// Spread batches so the campaign averages its rate: a batch of size takes size/rate minutes.
	return now.Add(time.Duration(float64(size) / float64(rate) * float64(time.Minute))), nil
}
