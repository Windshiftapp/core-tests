package handlers

import "testing"

func stringPointer(value string) *string { return &value }

func TestValidatePublicFormFieldSchema(t *testing.T) {
	available := []AvailableField{
		{Identifier: "title", Type: "default"},
		{Identifier: "description", Type: "default"},
		{Identifier: "42", Type: "custom"},
	}
	tests := []struct {
		name    string
		fields  []publicFormFieldSchema
		wantErr bool
	}{
		{
			name: "valid mixed schema",
			fields: []publicFormFieldSchema{
				{Identifier: "title", FieldType: "default"},
				{Identifier: "42", FieldType: "custom"},
				{Identifier: "vf_choice", FieldType: "virtual", VirtualFieldType: stringPointer("select"), VirtualFieldOptions: stringPointer(`[{"value":"one","label":"One"}]`)},
			},
		},
		{name: "custom field outside create screen", fields: []publicFormFieldSchema{{Identifier: "99", FieldType: "custom"}}, wantErr: true},
		{name: "unknown default field", fields: []publicFormFieldSchema{{Identifier: "status", FieldType: "default"}}, wantErr: true},
		{name: "duplicate identifier", fields: []publicFormFieldSchema{{Identifier: "title", FieldType: "default"}, {Identifier: "title", FieldType: "default"}}, wantErr: true},
		{name: "unknown field type", fields: []publicFormFieldSchema{{Identifier: "title", FieldType: "mystery"}}, wantErr: true},
		{name: "unsafe identifier", fields: []publicFormFieldSchema{{Identifier: "vf.bad", FieldType: "virtual", VirtualFieldType: stringPointer("text")}}, wantErr: true},
		{name: "invalid virtual type", fields: []publicFormFieldSchema{{Identifier: "vf_bad", FieldType: "virtual", VirtualFieldType: stringPointer("html")}}, wantErr: true},
		{name: "virtual number type rejected", fields: []publicFormFieldSchema{{Identifier: "vf_num", FieldType: "virtual", VirtualFieldType: stringPointer("number")}}, wantErr: true},
		{name: "select without options", fields: []publicFormFieldSchema{{Identifier: "vf_select", FieldType: "virtual", VirtualFieldType: stringPointer("select")}}, wantErr: true},
		{name: "select with duplicate values", fields: []publicFormFieldSchema{{Identifier: "vf_select", FieldType: "virtual", VirtualFieldType: stringPointer("select"), VirtualFieldOptions: stringPointer(`[{"value":"x","label":"X"},{"value":"x","label":"Again"}]`)}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePublicFormFieldSchema(tt.fields, available)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePublicFormFieldSchema() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
