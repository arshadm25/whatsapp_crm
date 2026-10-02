package bots

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustFlow(t *testing.T, s string) Flow {
	t.Helper()
	f, err := ParseFlow([]byte(s))
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	return f
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]string{
		"no nodes":         `{"start":"a","nodes":{}}`,
		"missing start":    `{"start":"x","nodes":{"a":{"type":"end"}}}`,
		"dangling next":    `{"start":"a","nodes":{"a":{"type":"message","text":"hi","next":"b"}}}`,
		"unknown type":     `{"start":"a","nodes":{"a":{"type":"sing"}}}`,
		"four buttons":     `{"start":"a","nodes":{"a":{"type":"buttons","text":"x","buttons":[{"id":"1","title":"a"},{"id":"2","title":"b"},{"id":"3","title":"c"},{"id":"4","title":"d"}]}}}`,
		"long title":       `{"start":"a","nodes":{"a":{"type":"buttons","text":"x","buttons":[{"id":"1","title":"` + strings.Repeat("a", 21) + `"}]}}}`,
		"duplicate button": `{"start":"a","nodes":{"a":{"type":"buttons","text":"x","buttons":[{"id":"1","title":"a"},{"id":"1","title":"b"}]}}}`,
		"empty keyword":    `{"start":"a","triggers":[{"type":"keyword","keywords":[" "]}],"nodes":{"a":{"type":"end"}}}`,
		"bad var":          `{"start":"a","nodes":{"a":{"type":"question","text":"x","var":"a b"}}}`,
		"loop":             `{"start":"a","nodes":{"a":{"type":"message","text":"x","next":"b"},"b":{"type":"message","text":"y","next":"a"}}}`,
		"unknown field":    `{"start":"a","nodes":{"a":{"type":"end","colour":"red"}}}`,
	}
	for name, raw := range cases {
		if _, err := ParseFlow([]byte(raw)); err == nil {
			t.Errorf("%s: flow accepted", name)
		}
	}
	// A loop that waits for the customer is fine.
	mustFlow(t, `{"start":"a","nodes":{"a":{"type":"question","text":"again?","var":"x","next":"a"}}}`)
}

func TestMatches(t *testing.T) {
	f := mustFlow(t, `{"start":"a","nodes":{"a":{"type":"end"}},"triggers":[
		{"type":"keyword","keywords":["Menu"]},
		{"type":"keyword","keywords":["price"],"match":"contains"},
		{"type":"button_reply","button_id":"order"}]}`)
	yes := []struct {
		in    Input
		first bool
	}{{Input{Text: " menu "}, false}, {Input{Text: "what is the PRICE of rice"}, false}, {Input{ButtonID: "order"}, false}}
	for _, c := range yes {
		if !f.Matches(c.in, c.first) {
			t.Errorf("%+v should match", c.in)
		}
	}
	for _, in := range []Input{{Text: "menus"}, {Text: ""}, {ButtonID: "other"}} {
		if f.Matches(in, false) {
			t.Errorf("%+v should not match", in)
		}
	}
	first := mustFlow(t, `{"start":"a","nodes":{"a":{"type":"end"}},"triggers":[{"type":"first_message"}]}`)
	if !first.Matches(Input{Text: "hello"}, true) || first.Matches(Input{Text: "hello"}, false) {
		t.Error("first_message trigger")
	}
}

func TestRunBranchesAndRetries(t *testing.T) {
	f := mustFlow(t, `{"start":"age","nodes":{
		"age":{"type":"question","text":"Age?","var":"age","kind":"number","next":"check"},
		"check":{"type":"condition","var":"age","op":"gt","value":"17","then":"adult","else":"minor"},
		"adult":{"type":"set","var":"group","value":"{{contact.name}}-adult","next":"bye"},
		"minor":{"type":"message","text":"Sorry"},
		"bye":{"type":"end","text":"Bye {{group}}"}}}`)
	c := Contact{Name: "Ravi", Phone: "91"}
	out := Start(f, c)
	if out.Status != StatusActive || out.State.NodeID != "age" || len(out.Actions) != 1 {
		t.Fatalf("start = %+v", out)
	}
	// Two unusable answers re-ask, the third hands over.
	st := out.State
	for i := 0; i < maxRetries; i++ {
		out = Resume(f, st, Input{Text: "old"}, c)
		if out.Status != StatusActive || out.State.NodeID != "age" {
			t.Fatalf("retry %d = %+v", i, out)
		}
		st = out.State
	}
	out = Resume(f, st, Input{Text: "old"}, c)
	if out.Status != StatusHandedOff || out.Reason != "no_understood_reply" {
		t.Fatalf("handoff = %+v", out)
	}
	adult := Resume(f, st, Input{Text: "30"}, c)
	if adult.Status != StatusCompleted || adult.State.Vars["group"] != "Ravi-adult" {
		t.Fatalf("adult = %+v", adult)
	}
	last := adult.Actions[len(adult.Actions)-1]
	body, _ := json.Marshal(last.Content)
	if !strings.Contains(string(body), "Bye Ravi-adult") {
		t.Fatalf("last message = %s", body)
	}
	minor := Resume(f, State{NodeID: "age", Vars: map[string]string{}}, Input{Text: "9"}, c)
	if minor.Status != StatusCompleted || len(minor.Actions) != 1 {
		t.Fatalf("minor = %+v", minor)
	}
}

func TestButtonsMatchByIDOrTitle(t *testing.T) {
	f := mustFlow(t, `{"start":"b","nodes":{
		"b":{"type":"buttons","text":"Pick","buttons":[{"id":"y","title":"Yes","next":"ok"},{"id":"n","title":"No"}]},
		"ok":{"type":"message","text":"Great"}}}`)
	for _, in := range []Input{{ButtonID: "y", Text: "Yes"}, {Text: "yes"}} {
		out := Resume(f, State{NodeID: "b"}, in, Contact{})
		if out.Status != StatusCompleted || len(out.Actions) != 1 {
			t.Fatalf("%+v = %+v", in, out)
		}
	}
	// A button with no next ends the flow without sending anything.
	out := Resume(f, State{NodeID: "b"}, Input{ButtonID: "n"}, Contact{})
	if out.Status != StatusCompleted || len(out.Actions) != 0 {
		t.Fatalf("no next = %+v", out)
	}
}
