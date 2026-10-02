package ai

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

const (
	maxSources  = 200
	maxFAQItems = 500
)

type Service struct {
	db       *db.DB
	agent    *Agent
	inserter jobs.Inserter
	log      *slog.Logger
	// allowPrivate accepts web addresses inside the network; only tests turn it on.
	allowPrivate bool
}

func NewService(d *db.DB, agent *Agent, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, agent: agent, inserter: inserter, log: log}
}

// AllowPrivateURLs accepts website sources on private addresses and any port, for tests.
func (s *Service) AllowPrivateURLs() *Service {
	s.allowPrivate = true
	return s
}

var manage = auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin)

// KnowledgeRoutes mounts /v1/knowledge.
func (s *Service) KnowledgeRoutes(r chi.Router) {
	r.Use(manage)
	r.Get("/sources", httpx.Handler(s.log, s.list))
	r.Post("/sources", httpx.Handler(s.log, s.create))
	r.Post("/sources/upload", httpx.Handler(s.log, s.upload))
	r.Delete("/sources/{id}", httpx.Handler(s.log, s.remove))
	r.Post("/sources/{id}/refresh", httpx.Handler(s.log, s.refresh))
}

// Routes mounts /v1/ai.
func (s *Service) Routes(r chi.Router) {
	r.Use(manage)
	r.Get("/usage", httpx.Handler(s.log, s.usage))
	r.Post("/test", httpx.Handler(s.log, s.test))
	r.Get("/logs", httpx.Handler(s.log, s.logs))
}

// Source matches the KnowledgeSource schema in api/openapi.yaml.
type Source struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	URL        *string   `json:"url"`
	Status     string    `json:"status"`
	Error      *string   `json:"error"`
	ChunkCount int32     `json:"chunk_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func sourceView(x dbq.KbSource) Source {
	return Source{ID: x.ID, Kind: x.Kind, Title: x.Title, URL: x.Url, Status: x.Status, Error: x.Error,
		ChunkCount: x.ChunkCount, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Source{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListKBSources(r.Context())
		for _, x := range rows {
			out = append(out, sourceView(x))
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

type FAQItem struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type createRequest struct {
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Items []FAQItem `json:"items"`
	Text  string    `json:"text"`
	URL   string    `json:"url"`
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req createRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Title = strings.TrimSpace(req.Title)
	var (
		chunks []string
		link   *string
		status = "ready"
	)
	switch req.Kind {
	case "faq":
		if len(req.Items) == 0 || len(req.Items) > maxFAQItems {
			return httpx.BadRequest("items", fmt.Sprintf("Add 1 to %d questions with answers.", maxFAQItems))
		}
		for i, it := range req.Items {
			q, a := strings.TrimSpace(it.Question), strings.TrimSpace(it.Answer)
			if q == "" || a == "" || utf8.RuneCountInString(q) > 300 || utf8.RuneCountInString(a) > 1500 {
				return httpx.BadRequest("items", fmt.Sprintf("Question %d needs a question of up to 300 characters and an answer of up to 1,500.", i+1))
			}
			chunks = append(chunks, "Q: "+q+"\nA: "+a)
		}
		if req.Title == "" {
			req.Title = "FAQ"
		}
	case "text":
		if utf8.RuneCountInString(req.Text) > maxSourceText {
			return httpx.BadRequest("text", "The text is too long. Split it into several sources.")
		}
		chunks = Chunk(req.Text)
		if len(chunks) == 0 {
			return httpx.BadRequest("text", "Enter some text.")
		}
		if req.Title == "" {
			return httpx.BadRequest("title", "Give the source a title.")
		}
	case "website":
		u, err := checkURL(req.URL, s.allowPrivate)
		if err != nil {
			return httpx.BadRequest("url", capital(err.Error())+".")
		}
		link, status = ptr(u.String()), "pending"
		if req.Title == "" {
			req.Title = u.Hostname()
		}
	default:
		return httpx.BadRequest("kind", "kind must be faq, text or website. Upload documents to /v1/knowledge/sources/upload.")
	}
	if n := utf8.RuneCountInString(req.Title); n < 1 || n > 200 {
		return httpx.BadRequest("title", "title must have 1 to 200 characters.")
	}
	var out Source
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		src, err := s.add(r.Context(), q, p, req.Kind, req.Title, link, status, chunks)
		if err != nil {
			return err
		}
		if req.Kind == "website" {
			if err := s.enqueue(r.Context(), tx, p.TenantID, src.ID); err != nil {
				return err
			}
		}
		out = sourceView(src)
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// upload adds a text, Markdown, CSV or HTML file.
func (s *Service) upload(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxSourceText+(64<<10))
	if err := r.ParseMultipartForm(maxSourceText); err != nil {
		return httpx.BadRequest("file", "Upload a file of up to 1 MB.")
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("file", "Choose a file to upload.")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSourceText+1))
	if err != nil || len(raw) > maxSourceText {
		return httpx.BadRequest("file", "Upload a file of up to 1 MB.")
	}
	if !utf8.Valid(raw) {
		return httpx.BadRequest("file", "The file must be UTF-8 text.")
	}
	var text string
	switch strings.ToLower(path.Ext(hdr.Filename)) {
	case ".txt", ".md", ".markdown", ".csv":
		text = string(raw)
	case ".html", ".htm":
		_, text = HTMLToText(string(raw))
	default:
		return httpx.BadRequest("file", "Upload a .txt, .md, .csv or .html file, or paste the text. PDF files are not supported yet.")
	}
	chunks := Chunk(text)
	if len(chunks) == 0 {
		return httpx.BadRequest("file", "The file has no text.")
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = hdr.Filename
	}
	if utf8.RuneCountInString(title) > 200 {
		return httpx.BadRequest("title", "title must have at most 200 characters.")
	}
	var out Source
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		src, err := s.add(r.Context(), q, p, "document", title, nil, "ready", chunks)
		out = sourceView(src)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// add stores a source and its passages, within the workspace's limits.
func (s *Service) add(ctx context.Context, q *dbq.Queries, p auth.Principal, kind, title string, link *string, status string, chunks []string) (dbq.KbSource, error) {
	sources, err := q.ListKBSources(ctx)
	if err != nil {
		return dbq.KbSource{}, err
	}
	if len(sources) >= maxSources {
		return dbq.KbSource{}, httpx.NewError(http.StatusConflict, "plan_limit", fmt.Sprintf("A workspace can have %d knowledge sources. Delete some first.", maxSources))
	}
	total, err := q.CountKBChunks(ctx)
	if err != nil {
		return dbq.KbSource{}, err
	}
	if int(total)+len(chunks) > maxChunksTotal {
		return dbq.KbSource{}, httpx.NewError(http.StatusConflict, "plan_limit", "The knowledge base is full. Delete a source before adding more.")
	}
	src, err := q.InsertKBSource(ctx, dbq.InsertKBSourceParams{
		ID: db.NewID(), TenantID: p.TenantID, Kind: kind, Title: title, Url: link, Status: status, CreatedBy: p.User(),
	})
	if err != nil {
		return src, err
	}
	if status != "ready" {
		return src, nil
	}
	return saveChunks(ctx, q, p.TenantID, src.ID, chunks)
}

func saveChunks(ctx context.Context, q *dbq.Queries, tenantID, sourceID uuid.UUID, chunks []string) (dbq.KbSource, error) {
	if err := q.DeleteKBChunks(ctx, sourceID); err != nil {
		return dbq.KbSource{}, err
	}
	for i, c := range chunks {
		if err := q.InsertKBChunk(ctx, dbq.InsertKBChunkParams{ID: db.NewID(), TenantID: tenantID, SourceID: sourceID, Ord: int32(i), Content: c}); err != nil {
			return dbq.KbSource{}, err
		}
	}
	return q.FinishKBSource(ctx, dbq.FinishKBSourceParams{ID: sourceID, Status: "ready", ChunkCount: int32(len(chunks))})
}

func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, tenantID, sourceID uuid.UUID) error {
	ins, err := jobs.From(ctx, s.inserter)
	if err != nil {
		return err
	}
	_, err = ins.InsertTx(ctx, tx, IngestArgs{TenantID: tenantID, SourceID: sourceID, Nonce: db.NewID()}, nil)
	return err
}

func sourceID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := sourceID(r)
	if err != nil {
		return err
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.DeleteKBSource(r.Context(), id)
		if err == nil && n == 0 {
			return httpx.ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// refresh downloads a website source again.
func (s *Service) refresh(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := sourceID(r)
	if err != nil {
		return err
	}
	var out Source
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		src, err := q.GetKBSource(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if src.Kind != "website" {
			return httpx.NewError(http.StatusConflict, "conflict", "Only web pages can be refreshed.")
		}
		if err := q.MarkKBSourcePending(r.Context(), id); err != nil {
			return err
		}
		if err := s.enqueue(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		src.Status, src.Error = "pending", nil
		out = sourceView(src)
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusAccepted, out)
	return nil
}

// Usage matches the AIUsage schema in api/openapi.yaml.
type Usage struct {
	Configured  bool      `json:"configured"`
	Limit       int32     `json:"limit"`
	Used        int64     `json:"used"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

func (s *Service) usage(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := Usage{Configured: s.agent.Configured()}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		allow, used, err := s.agent.Usage(r.Context(), q)
		out.Limit, out.Used, out.PeriodStart, out.PeriodEnd = allow.Limit, used, allow.PeriodStart, allow.PeriodEnd
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// test asks the agent a question without sending anything. It uses one AI reply when it answers.
func (s *Service) test(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Question     string  `json:"question"`
		Instructions string  `json:"instructions"`
		Threshold    float64 `json:"threshold"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Question) == "" {
		return httpx.BadRequest("question", "Enter a question.")
	}
	if req.Threshold < 0 || req.Threshold > 1 {
		return httpx.BadRequest("threshold", "threshold must be between 0 and 1.")
	}
	var res Result
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err := q.GetTenant(r.Context(), p.TenantID)
		if err != nil {
			return err
		}
		res, err = s.agent.Answer(r.Context(), q, p.TenantID, t.Name, req.Question, Options{Instructions: req.Instructions, Threshold: req.Threshold, Test: true})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// Log matches the AILog schema in api/openapi.yaml.
type Log struct {
	ID             uuid.UUID  `json:"id"`
	BotID          *uuid.UUID `json:"bot_id"`
	ConversationID *uuid.UUID `json:"conversation_id"`
	Question       string     `json:"question"`
	Answer         *string    `json:"answer"`
	Confidence     *float32   `json:"confidence"`
	Outcome        string     `json:"outcome"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *Service) logs(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListAILogsParams{Lim: lim + 1}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "Invalid cursor.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	out := []Log{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListAILogs(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1]
			c := httpx.EncodeCursor(last.CreatedAt, last.ID)
			next = &c
		}
		for _, x := range rows {
			out = append(out, Log{ID: x.ID, BotID: x.BotID, ConversationID: x.ConversationID, Question: x.Question,
				Answer: x.Answer, Confidence: x.Confidence, Outcome: x.Outcome, CreatedAt: x.CreatedAt})
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// IngestArgs is the River job that downloads a web page into the knowledge base.
type IngestArgs struct {
	TenantID uuid.UUID `json:"tenant_id"`
	SourceID uuid.UUID `json:"source_id"`
	Nonce    uuid.UUID `json:"nonce"` // makes each refresh its own job
}

func (IngestArgs) Kind() string { return "kb_ingest" }

func (IngestArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 1}
}

// IngestWorker downloads web pages. It never retries: a page that fails is marked failed with the
// reason, and the user refreshes it once fixed.
type IngestWorker struct {
	river.WorkerDefaults[IngestArgs]
	db      *db.DB
	fetcher Fetcher
	log     *slog.Logger
}

func NewIngestWorker(d *db.DB, f Fetcher, log *slog.Logger) *IngestWorker {
	return &IngestWorker{db: d, fetcher: f, log: log}
}

func (w *IngestWorker) Timeout(*river.Job[IngestArgs]) time.Duration { return time.Minute }

func (w *IngestWorker) Work(ctx context.Context, job *river.Job[IngestArgs]) error {
	a := job.Args
	var src dbq.KbSource
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		src, err = q.GetKBSource(ctx, a.SourceID)
		return err
	})
	if db.IsNotFound(err) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	if src.Url == nil {
		return nil
	}
	_, text, ferr := w.fetcher.Page(ctx, *src.Url)
	chunks := Chunk(text)
	return w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := q.GetKBSourceForUpdate(ctx, a.SourceID); db.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		if ferr == nil {
			total, err := q.CountKBChunks(ctx)
			if err != nil {
				return err
			}
			if int(total)+len(chunks) > maxChunksTotal {
				ferr = fmt.Errorf("the knowledge base is full")
			}
		}
		if ferr != nil {
			msg := capital(ferr.Error()) + "."
			_, err := q.FinishKBSource(ctx, dbq.FinishKBSourceParams{ID: a.SourceID, Status: "failed", Error: &msg})
			return err
		}
		_, err := saveChunks(ctx, q, a.TenantID, a.SourceID, chunks)
		return err
	})
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func ptr[T any](v T) *T { return &v }
