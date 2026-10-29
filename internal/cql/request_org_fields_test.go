//go:build test

package cql

import (
	"strings"
	"testing"
)

func TestRequestTypeFieldsGenerateSubqueries(t *testing.T) {
	t.Parallel()

	sql, _ := generateSLA(t, `requestType = "Bug report"`)
	if !strings.Contains(sql, "FROM request_types rt WHERE rt.id = i.request_type_id") {
		t.Fatalf("requestType SQL = %q", sql)
	}

	idSQL, _ := generateSLA(t, "requestTypeId = 5")
	if !strings.Contains(idSQL, "i.request_type_id") {
		t.Fatalf("requestTypeId SQL = %q", idSQL)
	}
}

func TestCustomerOrganisationFieldsGenerateSubqueries(t *testing.T) {
	t.Parallel()

	sql, _ := generateSLA(t, `customerOrganisation = "Acme"`)
	if !strings.Contains(sql, "customer_organisations") || !strings.Contains(sql, "co.name") {
		t.Fatalf("customerOrganisation SQL = %q", sql)
	}

	idSQL, _ := generateSLA(t, "customerOrganisationId = 7")
	if !strings.Contains(idSQL, "pc.customer_organisation_id") {
		t.Fatalf("customerOrganisationId SQL = %q", idSQL)
	}
}
