package executor

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"gruzilla/internal/scenario"
)

func TestSplitExtractPath(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		parts []string
	}{
		{"simple", "a.b.c", []string{"a", "b", "c"}},
		{"single", "root", []string{"root"}},
		{"dot_in_brackets", "data.values.[id=payment.orderId].value", []string{"data", "values", "[id=payment.orderId]", "value"}},
		{"quoted_in_brackets", `x.[k="a.b"].y`, []string{"x", `[k="a.b"]`, "y"}},
		{"trim_segments", " a . b ", []string{"a", "b"}},
		{"nested_brackets_depth", "a.[x=y].b", []string{"a", "[x=y]", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitExtractPath(tt.path)
			if !reflect.DeepEqual(got, tt.parts) {
				t.Fatalf("splitExtractPath(%q) = %#v, want %#v", tt.path, got, tt.parts)
			}
		})
	}
}

func TestExtractJSONPathValue_success(t *testing.T) {
	tests := []struct {
		name string
		json string
		path string
		want any
	}{
		{
			name: "dot_single_level",
			json: `{"x": 1}`,
			path: "x",
			want: float64(1),
		},
		{
			name: "dot_nested",
			json: `{"a":{"b":{"c": "ok"}}}`,
			path: "a.b.c",
			want: "ok",
		},
		{
			name: "dot_deep_numeric",
			json: `{"a":{"b":7}}`,
			path: "a.b",
			want: float64(7),
		},
		{
			name: "array_index_zero",
			json: `{"items":["first","second"]}`,
			path: "items.0",
			want: "first",
		},
		{
			name: "array_index_then_key",
			json: `{"items":[{"id":1},{"id":2}]}`,
			path: "items.1.id",
			want: float64(2),
		},
		{
			name: "map_string_key_zero_not_array",
			json: `{"rows":{"0":"cell"}}`,
			path: "rows.0",
			want: "cell",
		},
		{
			name: "array_match_string_id",
			json: `{"values":[{"id":"b","v":2},{"id":"a","v":1}]}`,
			path: "values.[id=a].v",
			want: float64(1),
		},
		{
			name: "array_match_id_with_dots",
			json: `{"values":[{"id":"x.y","v":"hit"}]}`,
			path: "values.[id=x.y].v",
			want: "hit",
		},
		{
			name: "array_match_quoted_equals_in_value",
			json: `{"arr":[{"k":"p=q","n":9}]}`,
			path: `arr.[k="p=q"].n`,
			want: float64(9),
		},
		{
			name: "array_match_number_field",
			json: `{"rows":[{"n":42,"tag":"yes"}]}`,
			path: "rows.[n=42].tag",
			want: "yes",
		},
		{
			name: "array_match_bool_true",
			json: `{"flags":[{"active":true,"name":"on"}]}`,
			path: "flags.[active=true].name",
			want: "on",
		},
		{
			name: "array_match_bool_false",
			json: `{"flags":[{"active":false,"name":"off"}]}`,
			path: "flags.[active=false].name",
			want: "off",
		},
		{
			name: "array_match_first_wins",
			json: `{"x":[{"id":"1","v":"a"},{"id":"1","v":"b"}]}`,
			path: "x.[id=1].v",
			want: "a",
		},
		{
			name: "array_match_skips_non_objects",
			json: `{"mix":[null,"skip",{"id":"k","v":3}]}`,
			path: "mix.[id=k].v",
			want: float64(3),
		},
		{
			name: "null_value_at_path",
			json: `{"a":null}`,
			path: "a",
			want: nil,
		},
		{
			name: "empty_string_value",
			json: `{"s":""}`,
			path: "s",
			want: "",
		},
		{
			name: "float_json_number",
			json: `{"pi":3.14}`,
			path: "pi",
			want: 3.14,
		},
		{
			name: "full_chain_operation_values",
			json: `{"data":{"operation":{"values":[
				{"id":"other","value":"z"},
				{"id":"payment.orderId","value":"uuid-here"}
			]}}}`,
			path: "data.operation.values.[id=payment.orderId].value",
			want: "uuid-here",
		},
		{
			name: "array_match_bracket_spaces",
			json: `{"x":[{"id":"a","n":1}]}`,
			path: "x.[ id = a ].n",
			want: float64(1),
		},
		{
			name: "array_match_json_null_equals_null",
			json: `{"x":[{"id":null,"v":"nil"}]}`,
			path: "x.[id=null].v",
			want: "nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload any
			if err := json.Unmarshal([]byte(tt.json), &payload); err != nil {
				t.Fatalf("json: %v", err)
			}
			got, err := extractJSONPathValue(payload, tt.path)
			if err != nil {
				t.Fatalf("extractJSONPathValue: %v", err)
			}
			if !extractValuesEqual(got, tt.want) {
				t.Fatalf("got %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func extractValuesEqual(got, want any) bool {
	if got == nil && want == nil {
		return true
	}
	if got == nil || want == nil {
		return false
	}
	gf, gok := got.(float64)
	wf, wok := want.(float64)
	if gok && wok {
		return math.Abs(gf-wf) < 1e-9
	}
	return reflect.DeepEqual(got, want)
}

func TestExtractJSONPathValue_errors(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		path    string
		errSubs string // substring
	}{
		{"empty_path", `{}`, "", "empty path"},
		{"key_missing", `{"a":1}`, "a.b", "not an object"},
		{"not_object_mid", `{"a":1}`, "a.x", "not an object"},
		{"array_index_oob", `{"a":[1]}`, "a.9", "out of range"},
		{"array_index_on_primitive", `{"a":1}`, "a.0", "not a JSON array"},
		{"bracket_on_object", `{"a":{"b":1}}`, "a.[x=y]", "requires current value to be a JSON array"},
		{"array_match_no_row", `{"a":[{"id":"x"}]}`, "a.[id=missing].z", `no array element with "id"="missing"`},
		{"invalid_match_segment", `{"a":[]}`, "a.[noeq]", "invalid array match"},
		{"empty_match_field", `{"a":[]}`, "a.[ =v]", "empty field in array match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload any
			if err := json.Unmarshal([]byte(tt.json), &payload); err != nil {
				t.Fatalf("json: %v", err)
			}
			_, err := extractJSONPathValue(payload, tt.path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.errSubs) {
				t.Fatalf("error %q should contain %q", err.Error(), tt.errSubs)
			}
		})
	}
}

func TestApplyAllJSONExtracts_interpolationAndMap(t *testing.T) {
	step := scenario.Step{
		Extract: map[string]string{
			"orderId": "data.values.[id=payment.orderId].value",
			"fixed":   "meta.version",
		},
	}
	vars := map[string]string{}
	payload := jsonPayload(t, `{
		"data":{"values":[
			{"id":"payment.orderId","value":"ord-99"},
			{"id":"other","value":"x"}
		]},
		"meta":{"version":"1"}
	}`)
	if err := applyAllJSONExtracts(step, vars, payload); err != nil {
		t.Fatal(err)
	}
	if vars["orderId"] != "ord-99" {
		t.Fatalf("orderId=%q", vars["orderId"])
	}
	if vars["fixed"] != "1" {
		t.Fatalf("fixed=%q", vars["fixed"])
	}
}

func TestApplyAllJSONExtracts_extractVarPath(t *testing.T) {
	step := scenario.Step{
		ExtractVar:  "out",
		ExtractPath: "items.[name=beta].id",
	}
	vars := map[string]string{}
	payload := jsonPayload(t, `{"items":[
		{"name":"alpha","id":"1"},
		{"name":"beta","id":"2"}
	]}`)
	if err := applyAllJSONExtracts(step, vars, payload); err != nil {
		t.Fatal(err)
	}
	if vars["out"] != "2" {
		t.Fatalf("out=%q", vars["out"])
	}
}

func TestApplyAllJSONExtracts_pathInterpolation(t *testing.T) {
	step := scenario.Step{
		Extract: map[string]string{
			"v": "data.rows.[id={{wantId}}].value",
		},
	}
	vars := map[string]string{"wantId": "payment.orderId"}
	payload := jsonPayload(t, `{"data":{"rows":[
		{"id":"other","value":"no"},
		{"id":"payment.orderId","value":"yes"}
	]}}`)
	if err := applyAllJSONExtracts(step, vars, payload); err != nil {
		t.Fatal(err)
	}
	if vars["v"] != "yes" {
		t.Fatalf("v=%q", vars["v"])
	}
}

func jsonPayload(t *testing.T, raw string) any {
	t.Helper()
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	return payload
}
