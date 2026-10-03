package campaigns

import (
	"encoding/json"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

func tpl(format, components string) dbq.Template {
	return dbq.Template{Name: "t", Language: "hi", ParameterFormat: format, Components: []byte(components)}
}

func TestRenderNamedWithMediaHeader(t *testing.T) {
	tp := tpl("named", `[{"type":"HEADER","format":"IMAGE"},{"type":"BODY","text":"Namaste {{first_name}}, your {{city}} store is open"}]`)
	vars := map[string]string{"header": "https://cdn.example/diwali.jpg", "first_name": "{{contact.first_name}}", "city": "{{ contact.city | your }}"}
	if err := checkVariables(tp, vars); err != nil {
		t.Fatal(err)
	}
	name := "Ravi Kumar"
	c := dbq.Contact{WaID: "919800000001", Name: &name, CustomFields: []byte(`{"city":"Kochi\nCentral"}`)}
	msg, ok := render(tp, vars, c)
	if !ok {
		t.Fatal("render failed")
	}
	got, _ := json.Marshal(msg["template"])
	want := `{"components":[{"parameters":[{"image":{"link":"https://cdn.example/diwali.jpg"},"type":"image"}],"type":"header"},` +
		`{"parameters":[{"parameter_name":"first_name","text":"Ravi","type":"text"},{"parameter_name":"city","text":"Kochi Central","type":"text"}],"type":"body"}],` +
		`"language":{"code":"hi"},"name":"t"}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	// The fallback fills an empty field; a field without one skips the contact.
	c.CustomFields = []byte(`{}`)
	if _, ok := render(tp, vars, c); !ok {
		t.Fatal("fallback not used")
	}
	c.Name = nil
	if _, ok := render(tp, vars, c); ok {
		t.Fatal("rendered without a first name")
	}
}

func TestCheckVariables(t *testing.T) {
	tp := tpl("positional", `[{"type":"HEADER","format":"TEXT","text":"Order {{1}}"},{"type":"BODY","text":"Hi {{1}}"}]`)
	for _, tc := range []struct {
		vars map[string]string
		ok   bool
	}{
		{map[string]string{"header.1": "ORD-1", "1": "Hi {{contact.name|friend}}!"}, true},
		{map[string]string{"1": "x"}, false},                            // header missing
		{map[string]string{"header.1": "a", "1": "b", "2": "c"}, false}, // unknown variable
		{map[string]string{"header.1": "a", "1": "{{name}}"}, false},    // not a contact field
		{map[string]string{"header.1": "a", "1": "   "}, false},         // blank
	} {
		if err := checkVariables(tp, tc.vars); (err == nil) != tc.ok {
			t.Errorf("%v: err = %v", tc.vars, err)
		}
	}
	media := tpl("positional", `[{"type":"HEADER","format":"DOCUMENT"},{"type":"BODY","text":"Invoice"}]`)
	if checkVariables(media, map[string]string{"header": "http://insecure.example/a.pdf"}) == nil {
		t.Error("http media link accepted")
	}
	loc := tpl("positional", `[{"type":"HEADER","format":"LOCATION"},{"type":"BODY","text":"Visit"}]`)
	if checkVariables(loc, map[string]string{}) == nil {
		t.Error("location header accepted")
	}
}
