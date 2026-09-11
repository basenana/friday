package cards

import "testing"

func TestValidateFormRecursesIntoNestedFields(t *testing.T) {
	err := Default.ValidateForm(FormSchema{Fields: []Field{{
		Name: "sample", Type: FieldObject, Fields: []Field{{Name: "choice", Type: FieldSelect}},
	}}})
	if err == nil {
		t.Fatal("nested select without options was accepted")
	}
}

func TestValidateFormRejectsDuplicateTopLevelNames(t *testing.T) {
	err := Default.ValidateForm(FormSchema{Fields: []Field{
		{Name: "scope", Type: FieldText},
		{Name: "scope", Type: FieldText},
	}})
	if err == nil {
		t.Fatal("duplicate top-level field names were accepted")
	}
}
