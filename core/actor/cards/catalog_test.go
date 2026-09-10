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
