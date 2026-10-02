package contacts

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

const (
	maxImportBytes = 10 << 20
	maxImportRows  = 50_000
	importBatch    = 500
	maxRowErrors   = 50
)

var nonDigits = regexp.MustCompile(`[^0-9]`)

// NormalizeNumber turns a phone number as people write it ("+91 98765-43210", "098765 43210",
// "9876543210") into WhatsApp's digits-only international form, adding countryCode to
// national numbers.
func NormalizeNumber(s, countryCode string) (string, bool) {
	s = strings.TrimSpace(s)
	intl := strings.HasPrefix(s, "+") || strings.HasPrefix(s, "00")
	d := nonDigits.ReplaceAllString(s, "")
	if strings.HasPrefix(s, "00") {
		d = strings.TrimPrefix(d, "00")
	}
	if !intl {
		switch {
		case len(d) == 11 && strings.HasPrefix(d, "0"):
			d = countryCode + d[1:]
		case len(d) == 10:
			d = countryCode + d
		}
	}
	return d, len(d) >= 8 && len(d) <= 15
}

// RowError explains why one line of an import was skipped.
type RowError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ImportResult summarises a CSV import.
type ImportResult struct {
	Rows     int        `json:"rows"`
	Created  int        `json:"created"`
	Updated  int        `json:"updated"`
	Skipped  int        `json:"skipped"`
	OptedIn  int        `json:"opted_in"`
	OptedOut int        `json:"opted_out"`
	Errors   []RowError `json:"errors"`
}

type importRow struct {
	line    int
	waID    string
	name    *string
	lang    *string
	tags    []string
	consent dbq.OptInStatus // empty for no change
	fields  []byte
}

func headerKind(h string) string {
	switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))) {
	case "phone", "phone number", "number", "mobile", "whatsapp", "wa_id", "whatsapp number":
		return "phone"
	case "name", "full name":
		return "name"
	case "language", "lang":
		return "language"
	case "tags", "tag":
		return "tags"
	case "opt_in", "opt-in", "opt in", "opted_in", "consent":
		return "opt_in"
	}
	return ""
}

func consentValue(v string) (dbq.OptInStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", true
	case "yes", "y", "true", "1", "opted_in", "opt_in", "in":
		return dbq.OptInStatusOptedIn, true
	case "no", "n", "false", "0", "opted_out", "opt_out", "out":
		return dbq.OptInStatusOptedOut, true
	}
	return "", false
}

// importCSV reads an uploaded CSV with a header row. A phone column is required; name,
// language, tags (separated by ; or |) and opt_in are recognised; any other column becomes a
// custom field. Opt-in values are recorded only when the uploader confirms the contacts agreed.
func (s *Service) importCSV(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return httpx.NewError(http.StatusRequestEntityTooLarge, "invalid_request", "The file is larger than 10 MB. Split it into smaller files.")
		}
		return httpx.BadRequest("file", "Upload a CSV file in the field file.")
	}
	defer file.Close()
	cc := strings.TrimPrefix(strings.TrimSpace(r.FormValue("default_country_code")), "+")
	if cc == "" {
		cc = "91"
	}
	if len(cc) > 3 || nonDigits.MatchString(cc) {
		return httpx.BadRequest("default_country_code", "default_country_code must be 1 to 3 digits, for example 91.")
	}
	attested := r.FormValue("consent_confirmed") == "true"
	evidence := "CSV import: " + filepath.Base(hdr.Filename)

	rd := csv.NewReader(file)
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	head, err := rd.Read()
	if err != nil {
		return httpx.BadRequest("file", "The file is empty or is not a CSV.")
	}
	cols := make([]string, len(head))
	phoneCol := -1
	for i, h := range head {
		cols[i] = headerKind(h)
		if cols[i] == "phone" && phoneCol < 0 {
			phoneCol = i
		}
		if cols[i] == "" {
			cols[i] = "field:" + strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		}
	}
	if phoneCol < 0 {
		return httpx.BadRequest("file", "The CSV needs a phone column (phone, mobile, number or wa_id).")
	}

	res := ImportResult{Errors: []RowError{}}
	skip := func(line int, msg string) {
		res.Skipped++
		if len(res.Errors) < maxRowErrors {
			res.Errors = append(res.Errors, RowError{Line: line, Message: msg})
		}
	}
	var batch []importRow
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			return s.importBatch(r.Context(), q, p, batch, attested, evidence, &res)
		})
		batch = batch[:0]
		return err
	}
	line := 1
	for {
		rec, err := rd.Read()
		line++
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				skip(line, "This line could not be read as CSV.")
				continue
			}
			return err
		}
		res.Rows++
		if res.Rows > maxImportRows {
			return httpx.BadRequest("file", "A file can have at most "+strconv.Itoa(maxImportRows)+" contacts. Split it into smaller files.")
		}
		row := importRow{line: line}
		fields := map[string]string{}
		bad := ""
		for i, v := range rec {
			if i >= len(cols) {
				break
			}
			v = strings.TrimSpace(v)
			switch c := cols[i]; {
			case c == "phone" && i == phoneCol:
				n, ok := NormalizeNumber(v, cc)
				if !ok {
					bad = "The phone number " + strconv.Quote(v) + " is not valid."
				}
				row.waID = n
			case c == "name" && v != "":
				if utf8.RuneCountInString(v) > 200 {
					v = string([]rune(v)[:200])
				}
				row.name = &v
			case c == "language" && v != "":
				if !languageRE.MatchString(v) {
					bad = "The language " + strconv.Quote(v) + " is not a code such as en or hi."
				}
				row.lang = &v
			case c == "tags":
				tags, err := cleanTags("tags", strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == '|' }))
				if err != nil {
					bad = "Too many or too long tags."
				}
				row.tags = tags
			case c == "opt_in":
				st, ok := consentValue(v)
				if !ok {
					bad = "The opt_in value " + strconv.Quote(v) + " should be yes or no."
				}
				row.consent = st
			case strings.HasPrefix(c, "field:") && v != "":
				if k := strings.TrimPrefix(c, "field:"); k != "" && utf8.RuneCountInString(k) <= 64 && len(fields) < 50 {
					fields[k] = v
				}
			}
		}
		if bad != "" {
			skip(line, bad)
			continue
		}
		if len(fields) > 0 {
			row.fields, _ = json.Marshal(fields)
		}
		batch = append(batch, row)
		if len(batch) >= importBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Service) importBatch(ctx context.Context, q *dbq.Queries, p auth.Principal, rows []importRow, attested bool, evidence string, res *ImportResult) error {
	now := time.Now().UTC()
	for _, row := range rows {
		c, err := q.UpsertContact(ctx, dbq.UpsertContactParams{
			ID: db.NewID(), TenantID: p.TenantID, WaID: row.waID, Name: row.name, Language: row.lang, CustomFields: row.fields,
		})
		if err != nil {
			return err
		}
		if c.Inserted {
			res.Created++
		} else {
			res.Updated++
		}
		if err := addTags(ctx, q, p.TenantID, c.ID, row.tags); err != nil {
			return err
		}
		// Opting out is always honoured; opting in needs the uploader's confirmation.
		if row.consent == dbq.OptInStatusOptedOut || (row.consent == dbq.OptInStatusOptedIn && attested) {
			if row.consent == c.OptInStatus {
				continue
			}
			if _, err := RecordConsent(ctx, q, p.TenantID, c.ID, row.consent, dbq.ConsentSourceCsvImport, evidence, p.User(), now); err != nil {
				return err
			}
			if row.consent == dbq.OptInStatusOptedIn {
				res.OptedIn++
			} else {
				res.OptedOut++
			}
		}
	}
	return nil
}
