package messaging

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func carousel(cards ...string) *SendRequest {
	return &SendRequest{
		PhoneNumberID: uuid.New(), To: "919876543210", Type: "interactive",
		Interactive: json.RawMessage(`{"type":"carousel","body":{"text":"Pick a flight"},"action":{"cards":[` + strings.Join(cards, ",") + `]}}`),
	}
}

func urlCard(i string) string {
	return `{"card_index":` + i + `,"type":"cta_url","header":{"type":"image","image":{"link":"https://x.example/a.jpg"}},` +
		`"body":{"text":"COK to DXB"},"action":{"name":"cta_url","parameters":{"display_text":"Book","url":"https://x.example/b"}}}`
}

func replyCard(i string, n int) string {
	var bs []string
	for j := 0; j < n; j++ {
		bs = append(bs, `{"type":"quick_reply","quick_reply":{"id":"id`+i+`","title":"Select"}}`)
	}
	return `{"card_index":` + i + `,"type":"cta_url","header":{"type":"video","video":{"id":"123"}},"action":{"buttons":[` + strings.Join(bs, ",") + `]}}`
}

func TestCarouselValid(t *testing.T) {
	for name, req := range map[string]*SendRequest{
		"url":   carousel(urlCard("0"), urlCard("1")),
		"reply": carousel(replyCard("0", 2), replyCard("1", 2), replyCard("2", 2)),
	} {
		if _, err := buildContent(req); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCarouselInvalid(t *testing.T) {
	ourMedia := strings.Replace(urlCard("1"), `"link":"https://x.example/a.jpg"`, `"media_id":"`+uuid.NewString()+`"`, 1)
	docHeader := strings.Replace(urlCard("1"), `"type":"image","image"`, `"type":"document","document"`, 1)
	longBody := strings.Replace(urlCard("1"), "COK to DXB", strings.Repeat("x", 161), 1)
	cards := make([]string, 11)
	for i := range cards {
		cards[i] = urlCard(strconv.Itoa(i))
	}
	withFooter := carousel(urlCard("0"), urlCard("1"))
	withFooter.Interactive = json.RawMessage(strings.Replace(string(withFooter.Interactive), `"body"`, `"footer":{"text":"f"},"body"`, 1))
	for name, req := range map[string]*SendRequest{
		"one card":        carousel(urlCard("0")),
		"mixed buttons":   carousel(urlCard("0"), replyCard("1", 1)),
		"button count":    carousel(replyCard("0", 1), replyCard("1", 2)),
		"bad index":       carousel(urlCard("0"), urlCard("5")),
		"our media_id":    carousel(urlCard("0"), ourMedia),
		"document header": carousel(urlCard("0"), docHeader),
		"long card body":  carousel(urlCard("0"), longBody),
		"main footer":     withFooter,
		"too many cards":  carousel(cards...),
	} {
		if _, err := buildContent(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
