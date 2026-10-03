// Package events streams live changes to the dashboard over Server-Sent Events.
//
// Database triggers (migration 000004) announce every message, conversation and template change
// on the Postgres channel ecogo_events when its transaction commits, whichever service made it.
// Each api pod LISTENs on one connection and forwards events to the open streams of the same
// tenant. This needs no extra infrastructure; Redis pub/sub can replace it if Postgres
// notifications ever become a bottleneck.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
)

// Channel is the Postgres notification channel the triggers publish on.
const Channel = "ecogo_events"

// Event is one change, as sent to the dashboard.
type Event struct {
	TenantID       uuid.UUID  `json:"-"`
	Type           string     `json:"type"` // message, conversation, template, notification
	ID             uuid.UUID  `json:"id"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
}

type Hub struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Event]struct{}

	// Heartbeat keeps proxies from closing idle streams.
	Heartbeat time.Duration
}

func NewHub(pool *pgxpool.Pool, log *slog.Logger) *Hub {
	return &Hub{pool: pool, log: log, subs: map[uuid.UUID]map[chan Event]struct{}{}, Heartbeat: 25 * time.Second}
}

// Run listens for notifications until ctx ends, reconnecting after errors.
func (h *Hub) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listen(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("events: listener stopped, reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (h *Hub) listen(ctx context.Context) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			// The connection may still be listening; close it rather than return it to the pool.
			_ = conn.Conn().Close(context.Background())
			return err
		}
		var e struct {
			Event
			TenantID uuid.UUID `json:"tenant_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &e); err != nil {
			continue
		}
		e.Event.TenantID = e.TenantID
		h.publish(e.Event)
	}
}

func (h *Hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[e.TenantID] {
		select {
		case ch <- e:
		default: // a slow client misses events; it refetches on the next one
		}
	}
}

// Subscribe returns a channel of the tenant's events and a function that ends the subscription.
func (h *Hub) Subscribe(tenantID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	if h.subs[tenantID] == nil {
		h.subs[tenantID] = map[chan Event]struct{}{}
	}
	h.subs[tenantID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[tenantID], ch)
		if len(h.subs[tenantID]) == 0 {
			delete(h.subs, tenantID)
		}
		h.mu.Unlock()
	}
}

// ServeHTTP streams the signed-in tenant's events as text/event-stream.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel := h.Subscribe(p.TenantID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // ingress-nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	tick := time.NewTicker(h.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case e := <-events:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
		}
		flusher.Flush()
	}
}
