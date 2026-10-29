package templates

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

func carouselComps(cards ...string) []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"type":"BODY","text":"Deals for {{1}}","example":{"body_text":[["you"]]}}`),
		json.RawMessage(`{"type":"CAROUSEL","cards":[` + strings.Join(cards, ",") + `]}`),
	}
}

const imageCard = `{"components":[{"type":"HEADER","format":"IMAGE","example":{"header_handle":["h"]}},` +
	`{"type":"BODY","text":"Card"},{"type":"BUTTONS","buttons":[{"type":"QUICK_REPLY","text":"Book"},{"type":"URL","text":"See","url":"https://x.example/{{1}}","example":["a"]}]}]}`

func TestCheckComponentsCarousel(t *testing.T) {
	if _, err := checkComponents(carouselComps(imageCard, imageCard), dbq.TemplateCategoryMarketing); err != nil {
		t.Fatalf("valid carousel rejected: %v", err)
	}
	if _, err := checkComponents(carouselComps(imageCard, imageCard), dbq.TemplateCategoryUtility); err == nil {
		t.Fatal("utility carousel accepted")
	}
	if _, err := checkComponents(carouselComps(imageCard), dbq.TemplateCategoryMarketing); err == nil {
		t.Fatal("one-card carousel accepted")
	}
	noBody := `{"components":[{"type":"HEADER","format":"IMAGE"},{"type":"BUTTONS","buttons":[{"type":"QUICK_REPLY","text":"Book"},{"type":"URL","text":"See","url":"https://x.example"}]}]}`
	if _, err := checkComponents(carouselComps(imageCard, noBody), dbq.TemplateCategoryMarketing); err == nil {
		t.Fatal("cards with different layouts accepted")
	}
	textHeader := strings.Replace(imageCard, `"format":"IMAGE"`, `"format":"TEXT"`, 1)
	if _, err := checkComponents(carouselComps(textHeader, textHeader), dbq.TemplateCategoryMarketing); err == nil {
		t.Fatal("text card header accepted")
	}
	withFooter := append(carouselComps(imageCard, imageCard), json.RawMessage(`{"type":"FOOTER","text":"x"}`))
	if _, err := checkComponents(withFooter, dbq.TemplateCategoryMarketing); err == nil {
		t.Fatal("carousel with footer accepted")
	}
}

func TestShapeOfCarousel(t *testing.T) {
	b, _ := json.Marshal(carouselComps(imageCard, imageCard, imageCard))
	if s := ShapeOf(b); s.Cards != 3 || s.BodyVars != 1 || s.HeaderFormat != "" {
		t.Fatalf("ShapeOf carousel = %+v", s)
	}
}
