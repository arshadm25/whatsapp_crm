package analytics

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Agent is one team member's inbox work in a period.
type Agent struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	// Chats counts conversations they replied in; Replies the customer turns they answered first.
	Chats   int32 `json:"chats"`
	Replies int32 `json:"replies"`
	// MedianFirstResponseSeconds is null when they answered no customer first.
	MedianFirstResponseSeconds *float64 `json:"median_first_response_seconds"`
	Assigned                   int32    `json:"assigned"`
	Resolved                   int32    `json:"resolved"`
	// ResolvedPct is resolved / assigned, null with nothing assigned.
	ResolvedPct *float64 `json:"resolved_pct"`
}

// Team is the team performance report: how fast customers get a first reply, overall and per
// team member.
type Team struct {
	From     string `json:"from"`
	To       string `json:"to"`
	TimeZone string `json:"time_zone"`
	// Replies counts answered customer turns, including those a bot or the API answered.
	Replies                    int32    `json:"replies"`
	MedianFirstResponseSeconds *float64 `json:"median_first_response_seconds"`
	Agents                     []Agent  `json:"agents"`
}

func median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	sort.Float64s(xs)
	m := xs[len(xs)/2]
	if len(xs)%2 == 0 {
		m = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
	}
	return &m
}

// period turns inclusive local dates into the instants that bound them.
func period(from, to time.Time, loc *time.Location) (time.Time, time.Time) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	return start, end
}

func (s *Service) team(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	qs := r.URL.Query()
	var out Team
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err := q.GetTenant(ctx, p.TenantID)
		if err != nil {
			return err
		}
		loc := Location(t.Timezone)
		from, to, err := dateRange(qs.Get("from"), qs.Get("to"), s.now().In(loc))
		if err != nil {
			return err
		}
		start, end := period(from, to, loc)
		out = Team{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), TimeZone: loc.String(), Agents: []Agent{}}

		responses, err := q.FirstResponses(ctx, dbq.FirstResponsesParams{Start: start, EndAt: end})
		if err != nil {
			return err
		}
		chats, err := q.AgentChats(ctx, dbq.AgentChatsParams{Start: start, EndAt: end})
		if err != nil {
			return err
		}
		assigned, err := q.AgentAssignments(ctx, dbq.AgentAssignmentsParams{Start: start, EndAt: end})
		if err != nil {
			return err
		}
		members, err := q.ListMembers(ctx)
		if err != nil {
			return err
		}

		all := make([]float64, 0, len(responses))
		byAgent := map[uuid.UUID][]float64{}
		for _, x := range responses {
			all = append(all, x.Seconds)
			if x.SentByUserID != nil {
				byAgent[*x.SentByUserID] = append(byAgent[*x.SentByUserID], x.Seconds)
			}
		}
		out.Replies = int32(len(all))
		out.MedianFirstResponseSeconds = median(all)

		agents := map[uuid.UUID]*Agent{}
		for _, m := range members {
			agents[m.ID] = &Agent{UserID: m.ID, Name: m.Name}
		}
		get := func(id uuid.UUID) *Agent {
			if agents[id] == nil { // a former member
				agents[id] = &Agent{UserID: id, Name: "Former member"}
			}
			return agents[id]
		}
		for id, xs := range byAgent {
			a := get(id)
			a.Replies = int32(len(xs))
			a.MedianFirstResponseSeconds = median(xs)
		}
		for _, c := range chats {
			get(c.UserID).Chats = c.Chats
		}
		for _, c := range assigned {
			a := get(c.UserID)
			a.Assigned, a.Resolved = c.Assigned, c.Resolved
			if c.Assigned > 0 {
				pct := float64(c.Resolved) * 100 / float64(c.Assigned)
				a.ResolvedPct = &pct
			}
		}
		for _, a := range agents {
			out.Agents = append(out.Agents, *a)
		}
		sort.Slice(out.Agents, func(i, j int) bool {
			if out.Agents[i].Chats != out.Agents[j].Chats {
				return out.Agents[i].Chats > out.Agents[j].Chats
			}
			return out.Agents[i].Name < out.Agents[j].Name
		})
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
