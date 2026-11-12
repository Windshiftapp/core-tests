package jira

import (
	"context"
	"errors"
	"testing"
)

// TestDataCenterListObjectSchemasReportsUnavailable is the WI-2078 regression:
// Data Center Assets object reads are unimplemented, so the whole capability
// must be reported unavailable instead of advertising discoverable schemas that
// become empty native sets.
func TestDataCenterListObjectSchemasReportsUnavailable(t *testing.T) {
	t.Parallel()

	client := &dataCenterClient{}
	schemas, err := client.ListObjectSchemas(context.Background())
	if !errors.Is(err, ErrAssetsNotAvailable) {
		t.Fatalf("ListObjectSchemas() error = %v, want ErrAssetsNotAvailable", err)
	}
	if len(schemas) != 0 {
		t.Fatalf("schemas = %#v, want none", schemas)
	}
}
