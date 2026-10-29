package cql

import "testing"

func TestSubstituteFunctions(t *testing.T) {
	uid := 42
	cid := 7
	oid := 99

	cases := []struct {
		name  string
		query string
		ctx   FunctionContext
		want  string
	}{
		{"empty", "", FunctionContext{UserID: &uid}, ""},
		{"noop without ctx values", `assignee = currentUser()`, FunctionContext{}, `assignee = currentUser()`},
		{"basic user", `assignee = currentUser()`, FunctionContext{UserID: &uid}, `assignee = 42`},
		{"case-insensitive function name", `assignee = CURRENTUSER()`, FunctionContext{UserID: &uid}, `assignee = 42`},
		{"whitespace inside parens", `assignee = currentUser(  )`, FunctionContext{UserID: &uid}, `assignee = 42`},
		{"multiple occurrences", `assignee = currentUser() OR creator = currentUser()`, FunctionContext{UserID: &uid}, `assignee = 42 OR creator = 42`},
		{"customer", `cf_owner = currentCustomer()`, FunctionContext{CustomerID: &cid}, `cf_owner = 7`},
		{"organisation", `cf_org = currentOrganisation()`, FunctionContext{OrganisationID: &oid}, `cf_org = 99`},
		{"unknown function untouched", `assignee = somethingElse()`, FunctionContext{UserID: &uid}, `assignee = somethingElse()`},
		{"literal currentUser in double quotes", `title = "currentUser()"`, FunctionContext{UserID: &uid}, `title = "currentUser()"`},
		{"literal currentUser in single quotes", `title = 'currentUser()'`, FunctionContext{UserID: &uid}, `title = 'currentUser()'`},
		{"literal currentUser in backtick identifier", "`currentUser()` = 1", FunctionContext{UserID: &uid}, "`currentUser()` = 1"},
		{"literal with whitespace variant", `title = "currentUser ( )"`, FunctionContext{UserID: &uid}, `title = "currentUser ( )"`},
		{"mixed literal and real call", `title = "currentUser()" AND assignee = currentUser()`, FunctionContext{UserID: &uid}, `title = "currentUser()" AND assignee = 42`},
		{"escaped quote inside literal", `title = "say \"currentUser()\""`, FunctionContext{UserID: &uid}, `title = "say \"currentUser()\""`},
		{"escaped backslash inside literal", `title = "back\\currentUser()"`, FunctionContext{UserID: &uid}, `title = "back\\currentUser()"`},
		{"unresolved context left untouched", `customer = currentCustomer() AND assignee = currentUser()`, FunctionContext{UserID: &uid}, `customer = currentCustomer() AND assignee = 42`},
		{"literal customer not substituted", `title = "currentCustomer()"`, FunctionContext{CustomerID: &cid}, `title = "currentCustomer()"`},
		{"literal organisation not substituted", `title = "currentOrganisation()"`, FunctionContext{OrganisationID: &oid}, `title = "currentOrganisation()"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SubstituteFunctions(tc.query, tc.ctx)
			if got != tc.want {
				t.Fatalf("SubstituteFunctions(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
