package mentions

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse("Hey @DM and @SK — cc @DM")
	want := []string{"DM", "SK"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %v, want %v", got, want)
	}
}
