package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestCloudClientListObjectSchemasAcceptsCurrentAssetsPagination(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/jsm/assets/workspace/workspace-1/v1/objectschema/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"values": [{
				"id": "7",
				"name": "IT assets",
				"objectSchemaKey": "IT",
				"description": "Managed devices",
				"objectCount": 3,
				"objectTypeCount": 2
			}],
			"startAt": 0,
			"maxResults": 25,
			"total": 1,
			"isLast": true
		}`))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		assetsURL:  server.URL + "/jsm/assets/workspace/workspace-1/v1",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	schemas, err := client.ListObjectSchemas(context.Background())
	if err != nil {
		t.Fatalf("ListObjectSchemas() error = %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("len(schemas) = %d, want 1", len(schemas))
	}
	if schemas[0].ID != "7" || schemas[0].Name != "IT assets" ||
		schemas[0].ObjectCount != 3 || schemas[0].ObjectTypeCount != 2 {
		t.Fatalf("schema = %#v", schemas[0])
	}
}

func TestDiscoverAssetsWorkspaceBaseUsesReadOnlyServiceDeskEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/rest/servicedeskapi/assets/workspace" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Fatalf("limit = %q, want 100", got)
		}
		if got := r.Header.Get("Authorization"); got != "Basic test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"size": 1,
			"isLastPage": true,
			"values": [{"workspaceId": "workspace-1"}]
		}`))
	}))
	t.Cleanup(server.Close)

	base, err := discoverAssetsWorkspaceBase(
		server.URL,
		"Basic test-token",
		server.Client(),
	)
	if err != nil {
		t.Fatalf("discoverAssetsWorkspaceBase() error = %v", err)
	}
	want := server.URL + "/jsm/assets/workspace/workspace-1/v1"
	if base != want {
		t.Fatalf("base = %q, want %q", base, want)
	}
}

// TestCloudRoutingProbeResolvesSupportedAssetsWorkspace is the regression for
// WI-1705: Cloud routing must resolve the supported workspace Assets API for
// both site (legacy token) and gateway (scoped token) routing, and report
// Assets unavailable (empty base) instead of falling back to the deprecated
// /rest/assets/1.0 navlist endpoint when discovery fails.
func TestCloudRoutingProbeResolvesSupportedAssetsWorkspace(t *testing.T) {
	t.Parallel()

	const (
		cloudID     = "cloud-1"
		workspaceID = "ws-1"
	)

	tests := []struct {
		name                string
		tenantInfoStatus    int
		gatewayMyselfStatus int
		siteAssetsStatus    int
		gatewayAssetsStatus int
		wantViaGateway      bool
		wantAssetsPath      string
	}{
		{
			name:             "legacy site token resolves the site workspace API",
			tenantInfoStatus: http.StatusNotFound,
			siteAssetsStatus: http.StatusOK,
			wantViaGateway:   false,
			wantAssetsPath:   "/jsm/assets/workspace/" + workspaceID + "/v1",
		},
		{
			name:                "scoped token resolves the gateway workspace API",
			tenantInfoStatus:    http.StatusOK,
			gatewayMyselfStatus: http.StatusOK,
			gatewayAssetsStatus: http.StatusOK,
			wantViaGateway:      true,
			wantAssetsPath:      "/gateway/" + cloudID + "/jsm/assets/workspace/" + workspaceID + "/v1",
		},
		{
			name:             "site workspace discovery failure reports unavailable",
			tenantInfoStatus: http.StatusNotFound,
			siteAssetsStatus: http.StatusNotFound,
			wantViaGateway:   false,
			wantAssetsPath:   "",
		},
		{
			name:                "declined gateway probe falls back to the site workspace API",
			tenantInfoStatus:    http.StatusOK,
			gatewayMyselfStatus: http.StatusUnauthorized,
			siteAssetsStatus:    http.StatusOK,
			wantViaGateway:      false,
			wantAssetsPath:      "/jsm/assets/workspace/" + workspaceID + "/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/_edge/tenant_info":
					if tt.tenantInfoStatus == http.StatusOK {
						_, _ = fmt.Fprintf(w, `{"cloudId":%q}`, cloudID)
						return
					}
					http.Error(w, "tenant info unavailable", tt.tenantInfoStatus)
				case "/gateway/" + cloudID + "/rest/api/3/myself":
					writeStatus(w, tt.gatewayMyselfStatus)
				case "/gateway/" + cloudID + "/rest/servicedeskapi/assets/workspace":
					writeAssetsWorkspace(w, tt.gatewayAssetsStatus, workspaceID)
				case "/rest/servicedeskapi/assets/workspace":
					writeAssetsWorkspace(w, tt.siteAssetsStatus, workspaceID)
				default:
					http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)

			routing := cloudRoutingProbeWithGateway(
				server.URL,
				"Basic test-token",
				server.Client(),
				server.URL+"/gateway/",
			)

			if routing.viaGateway != tt.wantViaGateway {
				t.Fatalf("viaGateway = %v, want %v", routing.viaGateway, tt.wantViaGateway)
			}
			wantAssets := ""
			if tt.wantAssetsPath != "" {
				wantAssets = server.URL + tt.wantAssetsPath
			}
			if routing.assetsBase != wantAssets {
				t.Fatalf("assetsBase = %q, want %q", routing.assetsBase, wantAssets)
			}
		})
	}
}

func writeStatus(w http.ResponseWriter, status int) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
}

func writeAssetsWorkspace(w http.ResponseWriter, status int, workspaceID string) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	_, _ = fmt.Fprintf(w, `{"size":1,"isLastPage":true,"values":[{"workspaceId":%q}]}`, workspaceID)
}

// TestCloudClientAssetsUnavailableWithoutWorkspaceBase asserts that an
// unresolved Assets workspace reports ErrAssetsNotAvailable without any HTTP
// dispatch, so the deprecated legacy endpoints are unreachable.
func TestCloudClientAssetsUnavailableWithoutWorkspaceBase(t *testing.T) {
	t.Parallel()

	transport := &dispatchCountingTransport{}
	client := &cloudClient{
		assetsURL:  "",
		httpClient: &http.Client{Transport: transport},
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	ctx := context.Background()

	if _, err := client.ListObjectSchemas(ctx); !errors.Is(err, ErrAssetsNotAvailable) {
		t.Errorf("ListObjectSchemas() error = %v, want ErrAssetsNotAvailable", err)
	}
	if _, err := client.GetObjectSchema(ctx, "7"); !errors.Is(err, ErrAssetsNotAvailable) {
		t.Errorf("GetObjectSchema() error = %v, want ErrAssetsNotAvailable", err)
	}
	if _, err := client.ListObjectTypes(ctx, "7"); !errors.Is(err, ErrAssetsNotAvailable) {
		t.Errorf("ListObjectTypes() error = %v, want ErrAssetsNotAvailable", err)
	}
	if _, err := client.GetObjectTypeAttributes(ctx, "9"); !errors.Is(err, ErrAssetsNotAvailable) {
		t.Errorf("GetObjectTypeAttributes() error = %v, want ErrAssetsNotAvailable", err)
	}
	if _, err := client.SearchObjects(ctx, ObjectSearchOptions{ObjectSchemaID: "7"}); !errors.Is(err, ErrAssetsNotAvailable) {
		t.Errorf("SearchObjects() error = %v, want ErrAssetsNotAvailable", err)
	}
	if transport.count != 0 {
		t.Errorf("dispatched %d requests, want 0", transport.count)
	}
}

func TestCloudClientSearchObjectsUsesReadOnlyCurrentAQLQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/jsm/assets/workspace/workspace-1/v1/object/aql" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("startAt"); got != "100" {
			t.Fatalf("startAt = %q, want 100", got)
		}
		if got := r.URL.Query().Get("maxResults"); got != "100" {
			t.Fatalf("maxResults = %q, want 100", got)
		}
		if got := r.URL.Query().Get("includeAttributes"); got != "true" {
			t.Fatalf("includeAttributes = %q, want true", got)
		}
		var body struct {
			QLQuery string `json:"qlQuery"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.QLQuery != "objectSchemaId = 7 AND objectTypeId = 9" {
			t.Fatalf("qlQuery = %q", body.QLQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"values": [{
				"id": "42",
				"label": "Laptop 42",
				"objectKey": "IT-42",
				"attributes": [{
					"id": "84",
					"objectTypeAttributeId": "12",
					"objectAttributeValues": [{
						"value": "Serial 42",
						"displayValue": "Serial 42",
						"referencedType": false
					}]
				}]
			}],
			"objectTypeAttributes": [],
			"maxResults": 100,
			"startAt": 100,
			"total": 101,
			"isLast": true,
			"hasMoreResults": false
		}`))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		assetsURL:  server.URL + "/jsm/assets/workspace/workspace-1/v1",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	result, err := client.SearchObjects(context.Background(), ObjectSearchOptions{
		ObjectSchemaID:    "7",
		ObjectTypeID:      "9",
		Page:              2,
		PageSize:          100,
		IncludeAttributes: true,
	})
	if err != nil {
		t.Fatalf("SearchObjects() error = %v", err)
	}
	if len(result.ObjectEntries) != 1 || result.ObjectEntries[0].ObjectKey != "IT-42" {
		t.Fatalf("ObjectEntries = %#v", result.ObjectEntries)
	}
	if !result.IsLast || result.PageNumber != 2 || result.TotalFilterCount != 101 {
		t.Fatalf("pagination = %#v", result)
	}
}

// TestCloudClientSearchObjectsUsesOptionalHasMoreResults is the regression for
// WI-1154: an absent hasMoreResults must not be negated into a forced IsLast.
// The legacy response shape omits the field, so the old expression
// ("!current.HasMoreResults") stopped asset imports after the first page.
func TestCloudClientSearchObjectsUsesOptionalHasMoreResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fields   map[string]any
		wantLast bool
	}{
		{
			name:     "omitted hasMoreResults keeps pagination open",
			fields:   map[string]any{},
			wantLast: false,
		},
		{
			name:     "explicit hasMoreResults true keeps pagination open",
			fields:   map[string]any{"hasMoreResults": true},
			wantLast: false,
		},
		{
			name:     "explicit hasMoreResults false marks last",
			fields:   map[string]any{"hasMoreResults": false},
			wantLast: true,
		},
		{
			name:     "isLast true marks last",
			fields:   map[string]any{"isLast": true},
			wantLast: true,
		},
		{
			name:     "total exhaustion marks last",
			fields:   map[string]any{"total": 100},
			wantLast: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := assetAQLPage(t, tt.fields)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			client := &cloudClient{
				assetsURL:  server.URL + "/jsm/assets/workspace/workspace-1/v1",
				httpClient: server.Client(),
				limiter:    rate.NewLimiter(rate.Inf, 1),
			}

			result, err := client.SearchObjects(context.Background(), ObjectSearchOptions{
				ObjectSchemaID: "7",
				Page:           1,
				PageSize:       100,
			})
			if err != nil {
				t.Fatalf("SearchObjects() error = %v", err)
			}
			if len(result.ObjectEntries) != 100 {
				t.Fatalf("len(ObjectEntries) = %d, want 100", len(result.ObjectEntries))
			}
			if result.IsLast != tt.wantLast {
				t.Fatalf("IsLast = %v, want %v", result.IsLast, tt.wantLast)
			}
		})
	}
}

// assetAQLPage builds a full first page of an Assets AQL search (100 of 200
// objects) and lets the caller override or add pagination flags.
func assetAQLPage(t *testing.T, fields map[string]any) string {
	t.Helper()

	values := make([]map[string]any, 100)
	for i := range values {
		values[i] = map[string]any{
			"id":        fmt.Sprintf("%d", i+1),
			"objectKey": fmt.Sprintf("IT-%d", i+1),
		}
	}
	payload := map[string]any{
		"values":               values,
		"objectTypeAttributes": []any{},
		"maxResults":           100,
		"startAt":              0,
		"total":                200,
	}
	for key, value := range fields {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return string(body)
}

// TestCloudClientAssetPayloadsDecodeNumericIDsAndEpochMillis pins the Assets
// decode contract: the Insight/Assets REST API serializes IDs as JSON numbers
// and timestamps as epoch-millisecond numbers, which previously failed every
// Assets call with "cannot unmarshal number into Go struct field". Data
// Center-style string IDs and ISO timestamps must keep decoding too.
func TestCloudClientAssetPayloadsDecodeNumericIDsAndEpochMillis(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/jsm/assets/workspace/workspace-1/v1/objectschema/7/objecttypes/flat":
			// Cloud: numeric IDs, epoch-millis timestamps, numeric parent ref.
			_, _ = w.Write([]byte(`[{
				"id": 91,
				"name": "Laptop",
				"position": 0,
				"created": 1726300800000,
				"updated": 1726387200123,
				"objectSchemaId": 7,
				"inherited": false,
				"abstractObjectType": false,
				"parentObjectTypeId": 90
			}]`))
		case r.URL.Path == "/jsm/assets/workspace/workspace-1/v1/objecttype/91/attributes":
			_, _ = w.Write([]byte(`[{
				"id": 501,
				"name": "Serial",
				"label": true,
				"type": 0,
				"typeValue": null,
				"defaultTypeId": 0,
				"editable": true,
				"hidden": false,
				"includeChildObjectTypes": false,
				"uniqueAttribute": false,
				"minimumCardinality": 1,
				"maximumCardinality": 1,
				"removable": true,
				"position": 0
			}]`))
		case r.URL.Path == "/jsm/assets/workspace/workspace-1/v1/object/aql":
			_, _ = w.Write([]byte(`{
				"values": [{
					"id": 42,
					"label": "Laptop 42",
					"objectKey": "IT-42",
					"created": 1726300800000,
					"updated": 1726387200123,
					"timestamp": 1726387200123,
					"attributes": [{
						"id": 9001,
						"objectTypeAttributeId": 501,
						"objectAttributeValues": [{
							"value": "Serial 42",
							"displayValue": "Serial 42",
							"searchValue": "SERIAL42"
						}]
					}]
				}],
				"objectTypeAttributes": [],
				"maxResults": 100,
				"startAt": 0,
				"total": 1,
				"isLast": true,
				"hasMoreResults": false
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		assetsURL:  server.URL + "/jsm/assets/workspace/workspace-1/v1",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	ctx := context.Background()

	types, err := client.ListObjectTypes(ctx, "7")
	if err != nil {
		t.Fatalf("ListObjectTypes() error = %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("len(types) = %d, want 1", len(types))
	}
	typ := types[0]
	if typ.ID != "91" || typ.ObjectSchemaID != "7" || typ.ParentObjectTypeID != "90" {
		t.Errorf("type IDs = %q / %q / %q, want 91 / 7 / 90", typ.ID, typ.ObjectSchemaID, typ.ParentObjectTypeID)
	}
	if got := time.Time(typ.Created).UTC(); got.UnixMilli() != 1726300800000 {
		t.Errorf("Created = %v, want epoch millis 1726300800000", got)
	}
	if got := time.Time(typ.Updated).UTC(); got.UnixMilli() != 1726387200123 {
		t.Errorf("Updated = %v, want epoch millis 1726387200123", got)
	}

	attrs, err := client.GetObjectTypeAttributes(ctx, "91")
	if err != nil {
		t.Fatalf("GetObjectTypeAttributes() error = %v", err)
	}
	if len(attrs) != 1 || attrs[0].ID != "501" || attrs[0].Name != "Serial" {
		t.Fatalf("attrs = %#v, want one attribute 501 Serial", attrs)
	}

	result, err := client.SearchObjects(ctx, ObjectSearchOptions{ObjectSchemaID: "7"})
	if err != nil {
		t.Fatalf("SearchObjects() error = %v", err)
	}
	if len(result.ObjectEntries) != 1 {
		t.Fatalf("len(objectEntries) = %d, want 1", len(result.ObjectEntries))
	}
	object := result.ObjectEntries[0]
	if object.ID != "42" || object.ObjectKey != "IT-42" || object.Label != "Laptop 42" {
		t.Errorf("object = %#v", object)
	}
	if len(object.Attributes) != 1 || object.Attributes[0].ObjectTypeAttributeID != "501" {
		t.Errorf("object attributes = %#v", object.Attributes)
	}
	if got := time.Time(object.Created).UTC(); got.UnixMilli() != 1726300800000 {
		t.Errorf("object.Created = %v, want epoch millis 1726300800000", got)
	}
}

// TestAssetTimeDecodesDataCenterISOStrings keeps the Data Center wire shape
// working: string IDs and ISO date-time strings with Java-style offsets.
func TestAssetTimeDecodesDataCenterISOStrings(t *testing.T) {
	t.Parallel()

	var payload struct {
		ID      string    `json:"id"`
		Created AssetTime `json:"created"`
	}
	raw := []byte(`{"id": "77", "created": "2026-01-15T10:30:00.000+0000"}`)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal Data Center payload: %v", err)
	}
	got := time.Time(payload.Created).UTC()
	want := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("created = %v, want %v", got, want)
	}
	if payload.ID != "77" {
		t.Errorf("id = %q, want 77", payload.ID)
	}
}
