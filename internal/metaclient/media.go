package metaclient

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// UploadMedia uploads a file for a phone number to send, and returns Meta's media ID. Meta
// keeps uploaded media for 30 days.
func (c *Client) UploadMedia(ctx context.Context, token, phoneNumberID, mimeType, filename string, file io.Reader) (string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := mw.WriteField("messaging_product", "whatsapp")
		if err == nil {
			err = mw.WriteField("type", mimeType)
		}
		if err == nil {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, safeFilename(filename)))
			h.Set("Content-Type", mimeType)
			var part io.Writer
			if part, err = mw.CreatePart(h); err == nil {
				_, err = io.Copy(part, file)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	path := "/" + url.PathEscape(phoneNumberID) + "/media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+c.version+path, pr)
	if err != nil {
		pr.Close()
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out struct {
		ID string `json:"id"`
	}
	// Uploads can be up to 100 MB, longer than the default client timeout allows.
	uploader := *c
	uploader.http = &http.Client{Timeout: 5 * time.Minute}
	if err := uploader.send(ctx, req, http.MethodPost, path, token, &out); err != nil {
		pr.Close()
		return "", err
	}
	return out.ID, nil
}

func safeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "file"
	}
	return s
}

// MediaInfo is what Meta returns for a media ID; URL is a short-lived download link.
type MediaInfo struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	FileSize int64  `json:"file_size"`
}

// GetMedia looks up a media ID (for inbound media, the ID in the webhook).
func (c *Client) GetMedia(ctx context.Context, token, mediaID string) (*MediaInfo, error) {
	var out MediaInfo
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(mediaID), token, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadMedia opens the download link from GetMedia. The caller closes the body.
func (c *Client) DownloadMedia(ctx context.Context, token, link string) (io.ReadCloser, error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && !strings.HasPrefix(link, c.baseURL+"/")) {
		return nil, fmt.Errorf("meta: unexpected media download link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	downloader := &http.Client{Timeout: 5 * time.Minute}
	resp, err := downloader.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meta: media download failed")
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, &Error{HTTPStatus: resp.StatusCode, Message: "media download failed"}
	}
	return resp.Body, nil
}
