package flows

import (
	"reflect"
	"testing"
)

func TestValidFlowJSON(t *testing.T) {
	if err := ValidFlowJSON([]byte(StarterJSON)); err != nil {
		t.Fatalf("starter flow: %v", err)
	}
	for name, raw := range map[string]string{
		"not json":   `nope`,
		"no version": `{"screens":[{}]}`,
		"no screens": `{"version":"6.3","screens":[]}`,
		"array":      `[]`,
	} {
		if ValidFlowJSON([]byte(raw)) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestParseResponseAndFlatten(t *testing.T) {
	token, answers, err := ParseResponse(`{"flow_token":"abc","name":"Ravi","interests":["rice","oil"],"qty":3,"vip":true,"note":null}`)
	if err != nil || token != "abc" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
	if _, ok := answers["flow_token"]; ok {
		t.Fatal("flow_token left in answers")
	}
	want := map[string]string{"name": "Ravi", "interests": "rice, oil", "qty": "3", "vip": "true", "note": ""}
	if got := Flatten(answers); !reflect.DeepEqual(got, want) {
		t.Fatalf("flatten = %v, want %v", got, want)
	}
	if _, _, err := ParseResponse("{"); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestMetaStatus(t *testing.T) {
	if s, ok := MetaStatus("PUBLISHED"); !ok || s != "published" {
		t.Fatalf("PUBLISHED = %q %v", s, ok)
	}
	if _, ok := MetaStatus("WEIRD"); ok {
		t.Fatal("unknown status accepted")
	}
}
