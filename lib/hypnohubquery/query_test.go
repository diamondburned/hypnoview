package hypnohubquery

import (
	"testing"

	"libdb.so/hypnoview/lib/hypnohub"
)

func TestQuery(t *testing.T) {
	tests := []struct {
		query  Query
		expect string
	}{
		{
			And(Tag("skirt"), Not(Rating(hypnohub.RatingExplicit))),
			"skirt -rating:explicit",
		},
		{
			Or(Tag("skirt"), Tag("dress")),
			"{skirt ~ dress}",
		},
		{
			ID(GreaterEqual, 3000),
			"id:>=3000",
		},
	}

	for _, test := range tests {
		if test.query.String() != test.expect {
			t.Errorf("expected %q, got %q", test.expect, test.query.String())
		}
	}
}
