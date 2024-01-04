package hypnohubquery

import (
	"slices"
	"strconv"
	"strings"

	"libdb.so/hypnoview/lib/hypnohub"
)

// Query represents a search query for the Hypnohub API.
// It is a slice of strings, where each string is a tag.
type Query []string

// NewQuery creates a new Query with the given tags.
func NewQuery(tags ...string) Query {
	return Query(tags)
}

// Tag returns a Query with the given tag.
func Tag(s string) Query {
	return Query{s}
}

// And joins the given queries together. The default behavior of each query item
// is to be joined with an AND operator, so this function also doubles as a
// way to join multiple tags together.
func And(qs ...Query) Query {
	var qn Query
	for _, q1 := range qs {
		qn = append(qn, q1...)
	}
	return qn
}

// Not negates the given query.
func Not(qs ...Query) Query {
	q := And(qs...)
	for i, s := range q {
		q[i] = "-" + s
	}
	return q
}

// Or combines the given queries with an OR operator.
// The returned query will be of length 1, where the first element is the
// combined query.
func Or(qs ...Query) Query {
	q1 := strings.Join([]string(And(qs...)), " ~ ")
	return Query{"{" + q1 + "}"}
}

// Fuzzy applies the fuzzy search operator to the given query.
// Queries with this operator will return results that are similar to the
// query, but not necessarily matching it, based on the Levenshtein distance.
func Fuzzy(q Query) Query {
	q = slices.Clone(q)
	for i, s := range q {
		q[i] = s + "~"
	}
	return q
}

// HasPrefix applies the prefix search operator to the given query.
func HasSuffix(q Query) Query {
	q = slices.Clone(q)
	for i, s := range q {
		q[i] = "*" + s
	}
	return q
}

// User adds a user filter to the given query.
func User(u string) Query {
	return Query{"user:" + u}
}

// MD5 adds an MD5 filter to the given query.
func MD5(md5 string) Query {
	return Query{"md5:" + md5}
}

// Rating adds a rating filter to the given query.
func Rating(rating hypnohub.Rating) Query {
	return Query{"rating:" + string(rating)}
}

// Pool adds a pool filter to the given query.
func Pool(id int) Query {
	return Query{"pool:" + strconv.Itoa(id)}
}

// ComparisonOperator is a comparison operator.
type ComparisonOperator string

const (
	Equal        ComparisonOperator = "="
	GreaterThan  ComparisonOperator = ">"
	LessThan     ComparisonOperator = "<"
	GreaterEqual ComparisonOperator = ">="
	LessEqual    ComparisonOperator = "<="
)

// Width adds a width filter to the given query.
func Width(op ComparisonOperator, width int) Query {
	return Query{"width:" + string(op) + strconv.Itoa(width)}
}

// Height adds a height filter to the given query.
func Height(op ComparisonOperator, height int) Query {
	return Query{"height:" + string(op) + strconv.Itoa(height)}
}

// Score adds a score filter to the given query.
func Score(op ComparisonOperator, score int) Query {
	return Query{"score:" + string(op) + strconv.Itoa(score)}
}

// ID adds an ID filter to the given query.
func ID(op ComparisonOperator, id hypnohub.PostID) Query {
	return Query{"id:" + string(op) + strconv.Itoa(int(id))}
}

// SortRandom adds a random sort to the given query.
func SortRandom() Query {
	return Query{"sort:random"}
}

// SortRandomWithSeed adds a random sort with a seed to the given query.
func SortRandomWithSeed(seed int) Query {
	return Query{"sort:random:" + strconv.Itoa(seed)}
}

// SortOption is a sort option.
type SortOption string

const (
	SortID      SortOption = "id"
	SortScore   SortOption = "score"
	SortRating  SortOption = "rating"
	SortUser    SortOption = "user"
	SortWidth   SortOption = "width"
	SortHeight  SortOption = "height"
	SortSource  SortOption = "source"
	SortUpdated SortOption = "updated"
)

// SortOrder is a sort order.
type SortOrder string

const (
	SortAscending  SortOrder = "asc"
	SortDescending SortOrder = "desc"
)

// Sort adds a sort to the given query.
func Sort(opt SortOption, order SortOrder) Query {
	return Query{"sort:" + string(opt) + ":" + string(order)}
}

// String builds the query into a string.
func (q Query) String() string {
	return strings.Join([]string(q), " ")
}
