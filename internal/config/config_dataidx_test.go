package config

import (
	"reflect"
	"sort"
	"testing"
)

func TestParseDataIndexFields(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string][]string
	}{
		{name: "leer", in: "", want: nil},
		{name: "nur Whitespace", in: "  ", want: nil},
		{
			name: "ein Feld",
			in:   "identity.employee.new.v2:department",
			want: map[string][]string{"identity.employee.new.v2": {"department"}},
		},
		{
			name: "mehrere Felder gleicher Typ",
			in:   "emp:department, emp:lastName",
			want: map[string][]string{"emp": {"department", "lastName"}},
		},
		{
			name: "verschiedene Typen",
			in:   "a:x,b:y",
			want: map[string][]string{"a": {"x"}, "b": {"y"}},
		},
		{
			name: "Duplikat zusammengefasst",
			in:   "a:x,a:x",
			want: map[string][]string{"a": {"x"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDataIndexFields(tc.in)
			if err != nil {
				t.Fatalf("unerwarteter fehler: %v", err)
			}
			for k := range got {
				sort.Strings(got[k])
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseDataIndexFields(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDataIndexFieldsInvalid(t *testing.T) {
	for _, in := range []string{"keinDoppelpunkt", ":feld", "typ:", "a:b,kaputt"} {
		if _, err := parseDataIndexFields(in); err == nil {
			t.Errorf("parseDataIndexFields(%q) sollte fehlschlagen", in)
		}
	}
}

func TestFromEnvDataIndexFields(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envDataIdx, "emp:department")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	want := map[string][]string{"emp": {"department"}}
	if !reflect.DeepEqual(cfg.DataIndexFields, want) {
		t.Fatalf("DataIndexFields = %v, want %v", cfg.DataIndexFields, want)
	}
}
