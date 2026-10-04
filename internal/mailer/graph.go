package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/config"
)

// Graph sends email from a Microsoft 365 mailbox through Microsoft Graph's sendMail, signed in as
// an Entra ID app with the Mail.Send application permission (client credentials). The From
// address is the sending mailbox; Exchange can limit the app to that mailbox alone.
type Graph struct {
	cfg  config.Mail
	http *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewGraph(cfg config.Mail) *Graph {
	return &Graph{cfg: cfg, http: &http.Client{Timeout: 20 * time.Second}}
}

type graphAddress struct {
	EmailAddress struct {
		Address string `json:"address"`
		Name    string `json:"name,omitempty"`
	} `json:"emailAddress"`
}

func (g *Graph) Send(ctx context.Context, m Message) error {
	from, err := checkMessage(g.cfg.From, m)
	if err != nil {
		return err
	}
	var to, sender graphAddress
	to.EmailAddress.Address = m.To
	sender.EmailAddress.Address, sender.EmailAddress.Name = from.Address, from.Name
	body := map[string]any{
		"message": map[string]any{
			"subject":      m.Subject,
			"body":         map[string]string{"contentType": "Text", "content": m.Text},
			"from":         sender,
			"toRecipients": []graphAddress{to},
		},
		"saveToSentItems": false,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	token, err := g.accessToken(ctx)
	if err != nil {
		return err
	}
	endpoint := g.cfg.M365.GraphBaseURL + "/v1.0/users/" + url.PathEscape(from.Address) + "/sendMail"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("mailer: microsoft 365: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		if resp.StatusCode == http.StatusUnauthorized {
			g.mu.Lock()
			g.token = "" // revoked or rotated secret: sign in again next time
			g.mu.Unlock()
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("mailer: microsoft 365 sendMail: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// accessToken returns a cached app token, signing in again a minute before it expires.
func (g *Graph) accessToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.expires) {
		return g.token, nil
	}
	c := g.cfg.M365
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
	}
	endpoint := c.LoginBaseURL + "/" + url.PathEscape(c.TenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("mailer: microsoft 365 sign-in: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("mailer: microsoft 365 sign-in: %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", fmt.Errorf("mailer: microsoft 365 sign-in: %s %s: %s", resp.Status, out.Error, out.Description)
	}
	g.token = out.AccessToken
	g.expires = time.Now().Add(time.Duration(out.ExpiresIn)*time.Second - time.Minute)
	return g.token, nil
}
