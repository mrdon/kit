package runtime

import (
	"reflect"
	"testing"
)

func TestTranslateFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filter     map[string]any
		startIdx   int
		wantSQL    string
		wantParams []any
		wantErr    bool
	}{
		{
			name:       "nil filter",
			filter:     nil,
			startIdx:   4,
			wantSQL:    "",
			wantParams: []any{},
		},
		{
			name:       "empty filter",
			filter:     map[string]any{},
			startIdx:   4,
			wantSQL:    "",
			wantParams: []any{},
		},
		{
			name:       "simple equality",
			filter:     map[string]any{"name": "Jane"},
			startIdx:   4,
			wantSQL:    "data->>'name' = $4",
			wantParams: []any{"Jane"},
		},
		{
			name:       "explicit $eq",
			filter:     map[string]any{"name": map[string]any{"$eq": "Jane"}},
			startIdx:   4,
			wantSQL:    "data->>'name' = $4",
			wantParams: []any{"Jane"},
		},
		{
			name:       "$ne",
			filter:     map[string]any{"status": map[string]any{"$ne": "closed"}},
			startIdx:   4,
			wantSQL:    "data->>'status' != $4",
			wantParams: []any{"closed"},
		},
		{
			name:       "$gt numeric",
			filter:     map[string]any{"age": map[string]any{"$gt": 25}},
			startIdx:   4,
			wantSQL:    "(data->>'age')::numeric > $4",
			wantParams: []any{25},
		},
		{
			name:       "$gte numeric",
			filter:     map[string]any{"age": map[string]any{"$gte": 18}},
			startIdx:   1,
			wantSQL:    "(data->>'age')::numeric >= $1",
			wantParams: []any{18},
		},
		{
			name:       "$lt numeric",
			filter:     map[string]any{"count": map[string]any{"$lt": 100}},
			startIdx:   4,
			wantSQL:    "(data->>'count')::numeric < $4",
			wantParams: []any{100},
		},
		{
			name:       "$lte numeric float",
			filter:     map[string]any{"score": map[string]any{"$lte": 9.5}},
			startIdx:   4,
			wantSQL:    "(data->>'score')::numeric <= $4",
			wantParams: []any{9.5},
		},
		{
			name:       "$in non-empty list",
			filter:     map[string]any{"status": map[string]any{"$in": []any{"open", "pending"}}},
			startIdx:   4,
			wantSQL:    "data->>'status' = ANY($4::text[])",
			wantParams: []any{[]string{"open", "pending"}},
		},
		{
			name:       "$in with []string",
			filter:     map[string]any{"status": map[string]any{"$in": []string{"a", "b"}}},
			startIdx:   4,
			wantSQL:    "data->>'status' = ANY($4::text[])",
			wantParams: []any{[]string{"a", "b"}},
		},
		{
			name:       "$in empty list is contradiction",
			filter:     map[string]any{"status": map[string]any{"$in": []any{}}},
			startIdx:   4,
			wantSQL:    "FALSE",
			wantParams: []any{},
		},
		{
			name:       "_id maps to id column",
			filter:     map[string]any{"_id": "abc-123"},
			startIdx:   4,
			wantSQL:    "id = $4",
			wantParams: []any{"abc-123"},
		},
		{
			name:       "_id with $ne",
			filter:     map[string]any{"_id": map[string]any{"$ne": "abc-123"}},
			startIdx:   4,
			wantSQL:    "id != $4",
			wantParams: []any{"abc-123"},
		},
		{
			name:       "_id with $in",
			filter:     map[string]any{"_id": map[string]any{"$in": []any{"a", "b"}}},
			startIdx:   4,
			wantSQL:    "id::text = ANY($4::text[])",
			wantParams: []any{[]string{"a", "b"}},
		},
		{
			name: "multiple keys implicit AND (alphabetical)",
			filter: map[string]any{
				"age":  map[string]any{"$gt": 25},
				"name": "Jane",
			},
			startIdx:   4,
			wantSQL:    "(data->>'age')::numeric > $4 AND data->>'name' = $5",
			wantParams: []any{25, "Jane"},
		},
		{
			name: "nested range ops on same field",
			filter: map[string]any{
				"x": map[string]any{"$gte": 1, "$lt": 10},
			},
			startIdx:   4,
			wantSQL:    "((data->>'x')::numeric >= $4 AND (data->>'x')::numeric < $5)",
			wantParams: []any{1, 10},
		},
		{
			name:       "boolean equality",
			filter:     map[string]any{"active": true},
			startIdx:   4,
			wantSQL:    "(data->>'active')::boolean = $4",
			wantParams: []any{true},
		},
		{
			name:       "boolean $ne",
			filter:     map[string]any{"active": map[string]any{"$ne": false}},
			startIdx:   4,
			wantSQL:    "(data->>'active')::boolean != $4",
			wantParams: []any{false},
		},
		{
			name:    "unknown operator errors",
			filter:  map[string]any{"x": map[string]any{"$foo": 1}},
			wantErr: true,
		},
		{
			name:    "$gt non-numeric errors",
			filter:  map[string]any{"x": map[string]any{"$gt": "cat"}},
			wantErr: true,
		},
		{
			name:    "$in non-list errors",
			filter:  map[string]any{"x": map[string]any{"$in": "not-a-list"}},
			wantErr: true,
		},
		{
			name:       "combined big filter example",
			filter:     map[string]any{"name": "Jane", "age": map[string]any{"$gt": 25}, "tags": map[string]any{"$in": []any{"vip", "gold"}}},
			startIdx:   4,
			wantSQL:    "(data->>'age')::numeric > $4 AND data->>'name' = $5 AND data->>'tags' = ANY($6::text[])",
			wantParams: []any{25, "Jane", []string{"vip", "gold"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, params, err := TranslateFilter(tc.filter, tc.startIdx)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (sql=%q)", sql)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != tc.wantSQL {
				t.Errorf("sql mismatch\n got: %q\nwant: %q", sql, tc.wantSQL)
			}
			if !reflect.DeepEqual(params, tc.wantParams) {
				t.Errorf("params mismatch\n got: %#v\nwant: %#v", params, tc.wantParams)
			}
		})
	}
}

func TestTranslateSort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []any
		want    string
		wantErr bool
	}{
		{
			name:  "nil sort",
			input: nil,
			want:  "",
		},
		{
			name:  "empty sort",
			input: []any{},
			want:  "",
		},
		{
			name:  "single field asc",
			input: []any{[]any{"name", 1}},
			want:  "(data->>'name') ASC",
		},
		{
			name:  "single field desc",
			input: []any{[]any{"created_at", -1}},
			want:  "(data->>'created_at') DESC",
		},
		{
			name: "multiple fields",
			input: []any{
				[]any{"status", 1},
				[]any{"created_at", -1},
			},
			want: "(data->>'status') ASC, (data->>'created_at') DESC",
		},
		{
			name:  "_id field",
			input: []any{[]any{"_id", -1}},
			want:  "id DESC",
		},
		{
			name:  "float direction",
			input: []any{[]any{"name", 1.0}},
			want:  "(data->>'name') ASC",
		},
		{
			name:    "invalid direction value",
			input:   []any{[]any{"name", 2}},
			wantErr: true,
		},
		{
			name:    "non-string field",
			input:   []any{[]any{1, 1}},
			wantErr: true,
		},
		{
			name:    "not a tuple",
			input:   []any{"just a string"},
			wantErr: true,
		},
		{
			name:    "tuple wrong length",
			input:   []any{[]any{"name"}},
			wantErr: true,
		},
		{
			name:    "non-numeric direction",
			input:   []any{[]any{"name", "asc"}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := TranslateSort(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("sort mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
