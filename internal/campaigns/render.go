package campaigns

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
)

// A variable's value is literal text with optional contact fields in it:
//
//	"{{contact.name}}"               the contact's name, or their WhatsApp profile name
//	"Hi {{contact.first_name|there}}" a fallback after | is used when the field is empty
//	"{{contact.city}}"               any other key reads the contact's custom field
var fieldRE = regexp.MustCompile(`\{\{\s*contact\.([A-Za-z0-9_]+)\s*(?:\|([^{}]*))?\}\}`)

// slot is one value a template needs: a header or body placeholder, the header's media link,
// or a URL button's dynamic suffix.
type slot struct {
	key    string // variables key: "1", "first_name", "header.1", "header", "button.0"
	part   string // header, body, button
	name   string // placeholder name, for named templates
	media  string // image, video or document for a media header
	button int
}

// slotsOf lists the values a template needs, in the order Meta expects them.
func slotsOf(t dbq.Template) ([]slot, error) {
	var comps []struct {
		Type    string `json:"type"`
		Format  string `json:"format"`
		Text    string `json:"text"`
		Buttons []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"buttons"`
	}
	_ = json.Unmarshal(t.Components, &comps)
	var out []slot
	for _, c := range comps {
		switch strings.ToUpper(c.Type) {
		case "HEADER":
			switch f := strings.ToUpper(c.Format); f {
			case "IMAGE", "VIDEO", "DOCUMENT":
				out = append(out, slot{key: "header", part: "header", media: strings.ToLower(f)})
			case "LOCATION":
				return nil, unprocessable("template_not_supported", "template.name",
					"Templates with a location header cannot be sent as a campaign yet.")
			default:
				for _, n := range templates.Placeholders(c.Text) {
					out = append(out, slot{key: "header." + n, part: "header", name: n})
				}
			}
		case "BODY":
			for _, n := range templates.Placeholders(c.Text) {
				out = append(out, slot{key: n, part: "body", name: n})
			}
		case "BUTTONS":
			for i, b := range c.Buttons {
				if strings.ToUpper(b.Type) == "URL" && len(templates.Placeholders(b.URL)) > 0 {
					out = append(out, slot{key: "button." + strconv.Itoa(i), part: "button", button: i})
				}
			}
		}
	}
	return out, nil
}

// checkVariables makes sure vars supplies exactly what the template needs and that each value
// is well formed, so problems show up when the campaign is created rather than per recipient.
func checkVariables(t dbq.Template, vars map[string]string) error {
	slots, err := slotsOf(t)
	if err != nil {
		return err
	}
	need := map[string]slot{}
	for _, s := range slots {
		need[s.key] = s
	}
	for k := range vars {
		if _, ok := need[k]; !ok {
			return httpx.BadRequest("template.variables."+k, "The template has no variable "+k+".")
		}
	}
	var missing []string
	for _, s := range slots {
		v, ok := vars[s.key]
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, s.key)
			continue
		}
		param := "template.variables." + s.key
		if utf8.RuneCountInString(v) > 1024 {
			return httpx.BadRequest(param, "A variable must be at most 1024 characters.")
		}
		if s.media != "" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return httpx.BadRequest(param, "The header needs an https link to the "+s.media+".")
			}
			continue
		}
		rest := fieldRE.ReplaceAllString(v, "")
		if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
			return httpx.BadRequest(param, "Use {{contact.<field>}} to insert a contact field, for example {{contact.name}}.")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return unprocessable("template_param_mismatch", "template.variables",
			"The template needs a value for: "+strings.Join(missing, ", ")+".")
	}
	return nil
}

// render builds the template message for one contact. ok is false when a value comes out empty
// (a contact field is blank and has no fallback); that recipient is skipped.
func render(t dbq.Template, vars map[string]string, c dbq.Contact) (map[string]any, bool) {
	slots, err := slotsOf(t)
	if err != nil {
		return nil, false
	}
	named := t.ParameterFormat == "named"
	var header, body []map[string]any
	var buttons []map[string]any
	for _, s := range slots {
		raw := vars[s.key]
		if s.media != "" {
			header = append(header, map[string]any{"type": s.media, s.media: map[string]string{"link": raw}})
			continue
		}
		v, ok := fill(raw, c)
		if !ok {
			return nil, false
		}
		p := map[string]any{"type": "text", "text": v}
		if named && s.part != "button" {
			p["parameter_name"] = s.name
		}
		switch s.part {
		case "header":
			header = append(header, p)
		case "body":
			body = append(body, p)
		case "button":
			buttons = append(buttons, map[string]any{
				"type": "button", "sub_type": "url", "index": strconv.Itoa(s.button),
				"parameters": []map[string]any{p},
			})
		}
	}
	var comps []map[string]any
	if header != nil {
		comps = append(comps, map[string]any{"type": "header", "parameters": header})
	}
	if body != nil {
		comps = append(comps, map[string]any{"type": "body", "parameters": body})
	}
	comps = append(comps, buttons...)
	obj := map[string]any{"name": t.Name, "language": map[string]string{"code": t.Language}}
	if comps != nil {
		obj["components"] = comps
	}
	return map[string]any{"type": "template", "template": obj}, true
}

// fill replaces contact fields in a value. Meta refuses parameters with line breaks or tabs, so
// they become spaces.
func fill(raw string, c dbq.Contact) (string, bool) {
	empty := false
	out := fieldRE.ReplaceAllStringFunc(raw, func(m string) string {
		sub := fieldRE.FindStringSubmatch(m)
		v := strings.TrimSpace(field(c, sub[1]))
		if v == "" {
			v = strings.TrimSpace(sub[2])
		}
		if v == "" {
			empty = true
		}
		return v
	})
	out = strings.Join(strings.Fields(out), " ")
	if empty || out == "" {
		return "", false
	}
	return out, true
}

func field(c dbq.Contact, key string) string {
	name := ""
	if c.Name != nil {
		name = *c.Name
	} else if c.ProfileName != nil {
		name = *c.ProfileName
	}
	switch key {
	case "name":
		return name
	case "first_name":
		if f := strings.Fields(name); len(f) > 0 {
			return f[0]
		}
		return ""
	case "wa_id", "phone":
		return c.WaID
	case "language":
		if c.Language != nil {
			return *c.Language
		}
		return ""
	}
	var fields map[string]any
	_ = json.Unmarshal(c.CustomFields, &fields)
	switch v := fields[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
