package numbers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

const maxLogoBytes = 5 << 20

// verticals are the business categories Meta accepts for a profile.
var verticals = map[string]bool{
	"UNDEFINED": true, "OTHER": true, "AUTO": true, "BEAUTY": true, "APPAREL": true, "EDU": true,
	"ENTERTAIN": true, "EVENT_PLAN": true, "FINANCE": true, "GROCERY": true, "GOVT": true, "HOTEL": true,
	"HEALTH": true, "NONPROFIT": true, "PROF_SERVICES": true, "RETAIL": true, "TRAVEL": true,
	"RESTAURANT": true, "NOT_A_BIZ": true,
}

// number loads a phone number the caller may see, with its decrypted account token.
func (s *Service) number(ctx context.Context, p auth.Principal, idParam string) (dbq.GetSendingNumberRow, string, error) {
	var (
		row   dbq.GetSendingNumberRow
		token string
	)
	id, err := uuid.Parse(idParam)
	if err != nil || !p.AllowsNumber(id) {
		return row, "", httpx.ErrNotFound
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if row, err = q.GetSendingNumber(ctx, id); err != nil {
			return err
		}
		if row.PhoneNumber.Status != dbq.ConnectionStatusConnected || row.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
			return httpx.NewError(http.StatusUnprocessableEntity, "number_not_connected", "This number is not connected. Reconnect it from Numbers.")
		}
		token, err = credentials.Token(ctx, q, s.keys, row.WhatsappAccount)
		return err
	})
	if db.IsNotFound(err) {
		return row, "", httpx.ErrNotFound
	}
	return row, token, err
}

func metaFailure(err error) error {
	var me *metaclient.Error
	if errors.As(err, &me) {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "meta_error", Message: me.Friendly(), MetaErrorCode: me.Code}
	}
	return err
}

func (s *Service) getProfile(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	num, token, err := s.number(r.Context(), p, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	prof, err := s.meta.GetBusinessProfile(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, num.PhoneNumber.PhoneNumberID)
	if err != nil {
		return metaFailure(err)
	}
	httpx.JSON(w, http.StatusOK, prof)
	return nil
}

type profilePatch struct {
	About       *string   `json:"about"`
	Address     *string   `json:"address"`
	Description *string   `json:"description"`
	Email       *string   `json:"email"`
	Vertical    *string   `json:"vertical"`
	Websites    *[]string `json:"websites"`
}

func tooLong(param string, s *string, max int) error {
	if s != nil && utf8.RuneCountInString(*s) > max {
		return httpx.BadRequest(param, fmt.Sprintf("Use at most %d characters.", max))
	}
	return nil
}

// fields validates the patch and returns the Graph API fields it sets.
func (pp profilePatch) fields() (map[string]any, error) {
	for _, c := range []struct {
		param string
		v     *string
		max   int
	}{{"about", pp.About, 139}, {"address", pp.Address, 256}, {"description", pp.Description, 512}, {"email", pp.Email, 128}} {
		if err := tooLong(c.param, c.v, c.max); err != nil {
			return nil, err
		}
	}
	out := map[string]any{}
	if pp.About != nil {
		if strings.TrimSpace(*pp.About) == "" {
			return nil, httpx.BadRequest("about", "The About text cannot be empty.")
		}
		out["about"] = *pp.About
	}
	if pp.Address != nil {
		out["address"] = *pp.Address
	}
	if pp.Description != nil {
		out["description"] = *pp.Description
	}
	if pp.Email != nil {
		if *pp.Email != "" {
			if a, err := mail.ParseAddress(*pp.Email); err != nil || a.Address != *pp.Email {
				return nil, httpx.BadRequest("email", "Enter a valid email address.")
			}
		}
		out["email"] = *pp.Email
	}
	if pp.Vertical != nil {
		if !verticals[*pp.Vertical] {
			return nil, httpx.BadRequest("vertical", "Choose one of the listed business categories.")
		}
		out["vertical"] = *pp.Vertical
	}
	if pp.Websites != nil {
		if len(*pp.Websites) > 2 {
			return nil, httpx.BadRequest("websites", "Add at most 2 websites.")
		}
		for _, site := range *pp.Websites {
			u, err := url.Parse(site)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(site) > 256 {
				return nil, httpx.BadRequest("websites", "Websites must start with http:// or https://.")
			}
		}
		out["websites"] = *pp.Websites
	}
	if len(out) == 0 {
		return nil, httpx.BadRequest("", "Nothing to change.")
	}
	return out, nil
}

func (s *Service) patchProfile(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req profilePatch
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	fields, err := req.fields()
	if err != nil {
		return err
	}
	num, token, err := s.number(r.Context(), p, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	ctx := metaclient.WithTenant(r.Context(), p.TenantID.String())
	if err := s.meta.UpdateBusinessProfile(ctx, token, num.PhoneNumber.PhoneNumberID, fields); err != nil {
		return metaFailure(err)
	}
	if err := s.audit(r, p, "number.profile_updated", num.PhoneNumber.ID); err != nil {
		return err
	}
	prof, err := s.meta.GetBusinessProfile(ctx, token, num.PhoneNumber.PhoneNumberID)
	if err != nil {
		return metaFailure(err)
	}
	httpx.JSON(w, http.StatusOK, prof)
	return nil
}

func (s *Service) uploadLogo(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+1<<16)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return httpx.BadRequest("file", "Send the picture as a multipart file field named file, up to 5 MB.")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("file", "Choose a picture to upload.")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLogoBytes+1))
	if err != nil || len(data) > maxLogoBytes {
		return httpx.BadRequest("file", "The picture must be 5 MB or smaller.")
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return httpx.BadRequest("file", "Use a JPEG or PNG picture.")
	}
	num, token, err := s.number(r.Context(), p, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	ctx := metaclient.WithTenant(r.Context(), p.TenantID.String())
	handle, err := s.meta.UploadProfilePicture(ctx, token, mime, hdr.Filename, data)
	if err != nil {
		return metaFailure(err)
	}
	if err := s.meta.UpdateBusinessProfile(ctx, token, num.PhoneNumber.PhoneNumberID, map[string]any{"profile_picture_handle": handle}); err != nil {
		return metaFailure(err)
	}
	if err := s.audit(r, p, "number.logo_updated", num.PhoneNumber.ID); err != nil {
		return err
	}
	prof, err := s.meta.GetBusinessProfile(ctx, token, num.PhoneNumber.PhoneNumberID)
	if err != nil {
		return metaFailure(err)
	}
	httpx.JSON(w, http.StatusOK, prof)
	return nil
}

// disconnect releases the number from the Cloud API and frees its plan slot. Reconnecting goes
// through Connect WhatsApp again, which registers the number anew.
func (s *Service) disconnect(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var (
		num   dbq.GetSendingNumberRow
		token string
	)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if num, err = q.GetSendingNumber(r.Context(), id); err != nil {
			return err
		}
		if num.PhoneNumber.Status == dbq.ConnectionStatusDisconnected {
			return httpx.NewError(http.StatusConflict, "already_disconnected", "This number is already disconnected.")
		}
		if num.WhatsappAccount.Status == dbq.ConnectionStatusConnected {
			token, err = credentials.Token(r.Context(), q, s.keys, num.WhatsappAccount)
		}
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	// Meta may already have released the number; our side is disconnected either way.
	if token != "" && num.PhoneNumber.RegisteredAt != nil {
		if err := s.meta.DeregisterPhoneNumber(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, num.PhoneNumber.PhoneNumberID); err != nil {
			s.log.Warn("numbers: deregister failed", "number_id", id, "err", err)
		}
	}
	var out PhoneNumber
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if err := q.DisconnectPhoneNumber(r.Context(), id); err != nil {
			return err
		}
		if err := s.auditTx(r, q, p, "number.disconnected", id); err != nil {
			return err
		}
		row, err := q.GetPhoneNumber(r.Context(), id)
		out = View(row.PhoneNumber, row.WabaID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) audit(r *http.Request, p auth.Principal, action string, id uuid.UUID) error {
	return s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return s.auditTx(r, q, p, action, id)
	})
}

func (s *Service) auditTx(r *http.Request, q *dbq.Queries, p auth.Principal, action string, id uuid.UUID) error {
	tt, tid := "phone_number", id.String()
	params := dbq.InsertAuditLogParams{
		TenantID: &p.TenantID, ActorType: dbq.ActorTypeUser, Action: action,
		TargetType: &tt, TargetID: &tid, Metadata: json.RawMessage("{}"),
	}
	if p.UserID != uuid.Nil {
		params.ActorID = &p.UserID
	} else {
		params.ActorType, params.ActorID = dbq.ActorTypeApiKey, &p.APIKeyID
	}
	params.Ip = auth.ClientIP(r)
	return q.InsertAuditLog(r.Context(), params)
}
