package metaclient

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// BusinessProfile is the public profile customers see for a number.
type BusinessProfile struct {
	About             string   `json:"about"`
	Address           string   `json:"address"`
	Description       string   `json:"description"`
	Email             string   `json:"email"`
	Vertical          string   `json:"vertical"`
	Websites          []string `json:"websites"`
	ProfilePictureURL string   `json:"profile_picture_url"`
}

const profileFields = "about,address,description,email,profile_picture_url,websites,vertical"

func (c *Client) GetBusinessProfile(ctx context.Context, token, phoneNumberID string) (*BusinessProfile, error) {
	var out struct {
		Data []BusinessProfile `json:"data"`
	}
	q := url.Values{"fields": {profileFields}}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(phoneNumberID)+"/whatsapp_business_profile", token, q, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return &BusinessProfile{Websites: []string{}}, nil
	}
	p := out.Data[0]
	if p.Websites == nil {
		p.Websites = []string{}
	}
	return &p, nil
}

// UpdateBusinessProfile changes the named profile fields (about, address, description, email,
// vertical, websites, profile_picture_handle); fields not in the map keep their value.
func (c *Client) UpdateBusinessProfile(ctx context.Context, token, phoneNumberID string, fields map[string]any) error {
	body := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		body[k] = v
	}
	body["messaging_product"] = "whatsapp"
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/whatsapp_business_profile", token, nil, body, nil)
}

// UploadProfilePicture sends a picture through Meta's resumable upload API and returns the
// handle that UpdateBusinessProfile takes as profile_picture_handle.
func (c *Client) UploadProfilePicture(ctx context.Context, token, mimeType, filename string, data []byte) (string, error) {
	return c.ResumableUpload(ctx, token, mimeType, filename, data)
}

// ResumableUpload sends a file through Meta's resumable upload API and returns its handle: a
// profile picture, or the sample a template's image, video or document header needs.
func (c *Client) ResumableUpload(ctx context.Context, token, mimeType, filename string, data []byte) (string, error) {
	q := url.Values{"file_length": {strconv.Itoa(len(data))}, "file_type": {mimeType}, "file_name": {safeFilename(filename)}}
	var session struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(c.appID)+"/uploads", token, q, nil, &session); err != nil {
		return "", err
	}
	if session.ID == "" {
		return "", fmt.Errorf("meta: upload session has no id")
	}
	path := "/" + session.ID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+c.version+path, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("file_offset", "0")
	req.Header.Set("Content-Type", mimeType)
	var out struct {
		Handle string `json:"h"`
	}
	// The upload step authenticates with the OAuth scheme rather than Bearer.
	if err := c.sendWithAuth(ctx, req, http.MethodPost, path, "OAuth "+token, &out); err != nil {
		return "", err
	}
	if out.Handle == "" {
		return "", fmt.Errorf("meta: upload returned no handle")
	}
	return out.Handle, nil
}

// DeregisterPhoneNumber releases a number from the Cloud API so it can be used elsewhere.
func (c *Client) DeregisterPhoneNumber(ctx context.Context, token, phoneNumberID string) error {
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/deregister", token, nil, map[string]string{}, nil)
}
