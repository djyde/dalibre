package plugin

import (
	"slices"
	"strings"
	"testing"
)

func TestFormPrefill(t *testing.T) {
	f := Form{Remember: true, Fields: []FormField{
		{Key: "name", Type: FieldText, Value: "x"},
		{Key: "by", Type: FieldSelect, Options: []Option{{Value: "title"}, {Value: "isbn"}}},
		{Key: "gone", Type: FieldSelect, Options: []Option{{Value: "a"}}, Value: "a"},
		{Key: "on", Type: FieldToggle},
		{Key: "fix", Type: FieldMultiselect, Options: []Option{{Value: "a"}, {Value: "b"}}},
	}}
	f.Prefill(map[string]any{"name": "Dune", "by": "isbn", "gone": "z", "on": true, "fix": []any{"b", "old"}})
	if f.Fields[0].Value != "Dune" || f.Fields[1].Value != "isbn" || f.Fields[2].Value != "a" || f.Fields[3].Value != true ||
		!slices.Equal(f.Fields[4].Value.([]string), []string{"b"}) {
		t.Fatalf("%+v", f.Fields)
	}
}

func TestSettingTypes(t *testing.T) {
	base := `{"id":"a.b","name":"A","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.example.com"]},"settings":[%s]}`
	for _, c := range []struct{ setting, err string }{
		{`{"key":"on","title":"On","type":"toggle","default":"true"}`, ""},
		{`{"key":"n","title":"N","type":"number","default":"5"}`, ""},
		{`{"key":"c","title":"C","type":"select","default":"us","options":[{"value":"us"},{"value":"tw","title":"Taiwan"}]}`, ""},
		{`{"key":"c","title":"C","type":"select"}`, "1 to 64 options"},
		{`{"key":"c","title":"C","type":"select","default":"cn","options":[{"value":"us"}]}`, "isn’t one of its options"},
		{`{"key":"on","title":"On","type":"toggle","default":"yes"}`, "toggle's default"},
		{`{"key":"n","title":"N","type":"number","default":"five"}`, "isn’t a number"},
		{`{"key":"t","title":"T","options":[{"value":"x"}]}`, "only a select"},
	} {
		_, err := ParseManifest([]byte(strings.Replace(base, "%s", c.setting, 1)))
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: %v", c.setting, err)
		}
	}
}

func TestNeedsDalibre(t *testing.T) {
	m := `{"id":"a.b","name":"A","version":"1.0.0","api":1,"dalibre":"%s","capabilities":["source"],"permissions":{"hosts":["a.example.com"]}}`
	defer func(v string) { DalibreVersion = v }(DalibreVersion)
	for _, c := range []struct{ running, want, err string }{
		{"", ">=9.0.0", ""},      // a development build runs everything
		{"0.4.0", ">=0.4.0", ""}, // just new enough
		{"0.3.0", ">=0.4.0", "needs Dalibre 0.4.0 or newer; this is 0.3.0"},
		{"0.4.0", "0.4.0", "must be a version to start from"},
	} {
		DalibreVersion = c.running
		_, err := ParseManifest([]byte(strings.Replace(m, "%s", c.want, 1)))
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s on %q: %v", c.want, c.running, err)
		}
	}
}
