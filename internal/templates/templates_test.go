package templates

import (
	"reflect"
	"testing"
)

func TestPlaceholders(t *testing.T) {
	got := Placeholders("Hi {{1}}, order {{2}} ships {{ 1 }}. Code {{otp_code}}")
	if want := []string{"1", "2", "otp_code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Placeholders = %v, want %v", got, want)
	}
}

func TestShapeOf(t *testing.T) {
	s := ShapeOf([]byte(`[{"type":"HEADER","format":"IMAGE"},{"type":"BODY","text":"{{1}} and {{2}}"},{"type":"FOOTER","text":"Bye"}]`))
	if s != (Shape{HeaderFormat: "IMAGE", BodyVars: 2}) {
		t.Fatalf("ShapeOf = %+v", s)
	}
	s = ShapeOf([]byte(`[{"type":"header","text":"Hello {{name}}"},{"type":"body","text":"No vars"}]`))
	if s != (Shape{HeaderFormat: "TEXT", HeaderVars: 1}) {
		t.Fatalf("ShapeOf text header = %+v", s)
	}
}

func TestStatusFromMeta(t *testing.T) {
	if st, ok := StatusFromMeta("approved"); !ok || st != "approved" {
		t.Fatalf("approved -> %v %v", st, ok)
	}
	if _, ok := StatusFromMeta("PENDING_DELETION"); ok {
		t.Fatal("PENDING_DELETION should mean deleted")
	}
}
