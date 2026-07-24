// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cosmosquery

import (
	"testing"
)

func TestValidateReadOnlyQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
		keyword string
	}{
		{
			name:    "valid SELECT query",
			query:   "SELECT * FROM c",
			wantErr: false,
		},
		{
			name:    "valid SELECT with WHERE",
			query:   "SELECT * FROM c WHERE c.resourceType = 'Microsoft.RedHatOpenshift/hcpOpenShiftClusters'",
			wantErr: false,
		},
		{
			name:    "valid SELECT with functions",
			query:   "SELECT c.id, c.resourceID FROM c WHERE STARTSWITH(c.resourceID, '/subscriptions/', true)",
			wantErr: false,
		},
		{
			name:    "valid SELECT with COUNT",
			query:   "SELECT VALUE COUNT(1) FROM c",
			wantErr: false,
		},
		{
			name:    "DELETE keyword rejected",
			query:   "DELETE FROM c WHERE c.id = '123'",
			wantErr: true,
			keyword: "DELETE",
		},
		{
			name:    "UPDATE keyword rejected",
			query:   "UPDATE c SET c.name = 'test'",
			wantErr: true,
			keyword: "UPDATE",
		},
		{
			name:    "INSERT keyword rejected",
			query:   "INSERT INTO c VALUES ('test')",
			wantErr: true,
			keyword: "INSERT",
		},
		{
			name:    "CREATE keyword rejected",
			query:   "CREATE TABLE test (id string)",
			wantErr: true,
			keyword: "CREATE",
		},
		{
			name:    "DROP keyword rejected",
			query:   "DROP TABLE c",
			wantErr: true,
			keyword: "DROP",
		},
		{
			name:    "TRUNCATE keyword rejected",
			query:   "TRUNCATE TABLE c",
			wantErr: true,
			keyword: "TRUNCATE",
		},
		{
			name:    "ALTER keyword rejected",
			query:   "ALTER TABLE c ADD column1 string",
			wantErr: true,
			keyword: "ALTER",
		},
		{
			name:    "MERGE keyword rejected",
			query:   "MERGE INTO c USING source",
			wantErr: true,
			keyword: "MERGE",
		},
		{
			name:    "UPSERT keyword rejected",
			query:   "UPSERT INTO c VALUES ('test')",
			wantErr: true,
			keyword: "UPSERT",
		},
		{
			name:    "REPLACE keyword rejected",
			query:   "REPLACE INTO c VALUES ('test')",
			wantErr: true,
			keyword: "REPLACE",
		},
		{
			name:    "EXEC keyword rejected",
			query:   "EXEC sprocName",
			wantErr: true,
			keyword: "EXEC",
		},
		{
			name:    "EXECUTE keyword rejected",
			query:   "EXECUTE sprocName",
			wantErr: true,
			keyword: "EXECUTE",
		},
		{
			name:    "SET keyword rejected",
			query:   "SET c.name = 'test'",
			wantErr: true,
			keyword: "SET",
		},
		{
			name:    "case insensitive - lowercase delete",
			query:   "delete from c",
			wantErr: true,
			keyword: "DELETE",
		},
		{
			name:    "case insensitive - mixed case Delete",
			query:   "Delete from c",
			wantErr: true,
			keyword: "DELETE",
		},
		{
			name:    "keyword in string literal is allowed",
			query:   "SELECT * FROM c WHERE c.status = 'PENDING_DELETE'",
			wantErr: false,
		},
		{
			name:    "keyword in string literal with spaces is allowed",
			query:   "SELECT * FROM c WHERE c.description = 'please delete this'",
			wantErr: false,
		},
		{
			name:    "field name containing keyword substring is allowed - lastUpdate",
			query:   "SELECT c.lastUpdate FROM c",
			wantErr: false,
		},
		{
			name:    "field name containing keyword substring is allowed - createdBy",
			query:   "SELECT c.createdBy FROM c",
			wantErr: false,
		},
		{
			name:    "field name containing keyword substring is allowed - deleteFlag",
			query:   "SELECT * FROM c WHERE c.deleteFlag = true",
			wantErr: false,
		},
		{
			name:    "field name containing keyword substring is allowed - isDeleted",
			query:   "SELECT * FROM c WHERE c.isDeleted = false",
			wantErr: false,
		},
		{
			name:    "OFFSET and LIMIT are allowed",
			query:   "SELECT * FROM c OFFSET 10 LIMIT 5",
			wantErr: false,
		},
		{
			name:    "multiple string literals with keywords inside",
			query:   "SELECT * FROM c WHERE c.a = 'delete' AND c.b = 'update' AND c.c = 'insert'",
			wantErr: false,
		},
		{
			name:    "escaped single quote in string literal",
			query:   "SELECT * FROM c WHERE c.name = 'it''s a delete test'",
			wantErr: false,
		},
		{
			name:    "keyword after string literal is still caught",
			query:   "SELECT * FROM c WHERE c.status = 'ok'; DELETE FROM c",
			wantErr: true,
			keyword: "DELETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReadOnlyQuery(tt.query)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for keyword %s, got nil", tt.keyword)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %s", err)
				}
			}
		})
	}
}

func TestStripStringLiterals(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no strings",
			input: "SELECT * FROM c",
			want:  "SELECT * FROM c",
		},
		{
			name:  "single string",
			input: "SELECT * FROM c WHERE c.id = 'hello'",
			want:  "SELECT * FROM c WHERE c.id = ",
		},
		{
			name:  "multiple strings",
			input: "SELECT * FROM c WHERE c.a = 'foo' AND c.b = 'bar'",
			want:  "SELECT * FROM c WHERE c.a =  AND c.b = ",
		},
		{
			name:  "escaped quotes",
			input: "SELECT * FROM c WHERE c.name = 'it''s'",
			want:  "SELECT * FROM c WHERE c.name = ",
		},
		{
			name:  "empty string",
			input: "SELECT * FROM c WHERE c.id = ''",
			want:  "SELECT * FROM c WHERE c.id = ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripStringLiterals(tt.input)
			if got != tt.want {
				t.Errorf("stripStringLiterals(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
