//go:build test

package v2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
	"windshift/internal/services"
)

type timeEmailPermissions struct {
	list, admin bool
	err         error
}

func (p timeEmailPermissions) HasGlobalPermission(userID int, permission string) (bool, error) {
	if userID != 42 || permission != models.PermissionUserList {
		panic("unexpected permission check")
	}
	return p.list, p.err
}
func (p timeEmailPermissions) IsSystemAdmin(userID int) (bool, error) {
	if userID != 42 {
		panic("unexpected admin check")
	}
	return p.admin, p.err
}

type timeEmailProjects struct {
	fakeTimeProjects
	denied bool
}

func (p timeEmailProjects) ListManagers(userID, projectID int) ([]models.TimeProjectManager, error) {
	if userID != 42 || projectID != 7 {
		panic("unexpected project access")
	}
	if p.denied {
		return nil, services.ErrTimeProjectForbidden
	}
	return []models.TimeProjectManager{{ID: 1, ProjectID: 7, ManagerType: "user", ManagerID: 8, ManagerName: "Member", ManagerEmail: "private@example.test", GrantedAt: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)}}, nil
}
func (p timeEmailProjects) ListMembers(userID, projectID int) ([]models.TimeProjectMember, error) {
	rows, err := p.ListManagers(userID, projectID)
	if err != nil {
		return nil, err
	}
	row := rows[0]
	return []models.TimeProjectMember{{ID: row.ID, ProjectID: row.ProjectID, MemberType: row.ManagerType, MemberID: row.ManagerID, MemberName: row.ManagerName, MemberEmail: row.ManagerEmail, GrantedAt: row.GrantedAt}}, nil
}
func (p timeEmailProjects) AddManager(actor services.AuditActor, projectID int, input models.TimeProjectManagerRequest) (*models.TimeProjectManager, error) {
	rows, err := p.ListManagers(actor.UserID, projectID)
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}
func (p timeEmailProjects) AddMember(actor services.AuditActor, projectID int, input models.TimeProjectMemberRequest) (*models.TimeProjectMember, error) {
	rows, err := p.ListMembers(actor.UserID, projectID)
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func TestTimeProjectEmailVisibility(t *testing.T) {
	for _, tc := range []struct {
		name            string
		permissions     timeEmailPermissions
		visible, denied bool
	}{
		{name: "project visibility alone"},
		{name: "user directory reader", permissions: timeEmailPermissions{list: true}, visible: true},
		{name: "system administrator", permissions: timeEmailPermissions{admin: true}, visible: true},
		{name: "permission lookup fails closed", permissions: timeEmailPermissions{err: errors.New("permission lookup failed")}},
		{name: "project access denied", permissions: timeEmailPermissions{list: true}, denied: true},
	} {
		for _, resource := range []string{"members", "managers"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for _, prefix := range []string{restPrefix, sessionPrefix} {
					t.Run(tc.name+"/"+resource+"/"+method+prefix, func(t *testing.T) {
						deps := Deps{TimeProjects: timeEmailProjects{denied: tc.denied}, GlobalPermission: tc.permissions, SystemAdmins: tc.permissions,
							Tokens:      fakeTokens{user: &models.User{ID: 42}, token: &models.APIToken{ID: 7}, allow: true},
							Concurrency: fakeConcurrency{allow: true}, CORS: NewCORS(nil, false, true)}
						deps.SessionMiddleware = func(next http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								ctx := context.WithValue(r.Context(), contextkeys.User, &models.User{ID: 42})
								next.ServeHTTP(w, r.WithContext(ctx))
							})
						}
						builder := &routeBuilder{}
						registerTimeRoutes(builder, deps)
						mux := http.NewServeMux()
						registerMount(mux, prefix, builder.routes, deps, prefix == sessionPrefix)
						request := httptest.NewRequest(method, prefix+"/time/projects/7/"+resource, strings.NewReader(`{"principal_type":"user","principal_id":8}`))
						if prefix == restPrefix {
							request.Header.Set("Authorization", "Bearer crw_valid")
						}
						request.Header.Set("Content-Type", "application/json")
						response := httptest.NewRecorder()
						mux.ServeHTTP(response, request)
						if tc.denied {
							if response.Code != http.StatusForbidden {
								t.Fatalf("status=%d body=%s", response.Code, response.Body)
							}
							var body struct {
								Error struct{ Code, Message string }
							}
							if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
								t.Fatal(err)
							}
							if body.Error.Code != "forbidden" || body.Error.Message != "Time project operation is not permitted" {
								t.Fatalf("denial=%s", response.Body)
							}
							if strings.Contains(response.Body.String(), "private@example.test") {
								t.Fatal("denial leaked email")
							}
							return
						}
						wantStatus := http.StatusOK
						if method == http.MethodPost {
							wantStatus = http.StatusCreated
						}
						if response.Code != wantStatus {
							t.Fatalf("status=%d body=%s", response.Code, response.Body)
						}
						var body struct {
							Data json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
							t.Fatal(err)
						}
						var row map[string]any
						if method == http.MethodGet {
							var rows []map[string]any
							if err := json.Unmarshal(body.Data, &rows); err != nil {
								t.Fatal(err)
							}
							if len(rows) != 1 {
								t.Fatalf("rows=%v", rows)
							}
							row = rows[0]
						} else if err := json.Unmarshal(body.Data, &row); err != nil {
							t.Fatal(err)
						}
						want := map[string]any{"id": float64(1), "project_id": float64(7), "principal_type": "user", "principal_id": float64(8), "granted_by": nil, "granted_at": "2026-09-09T00:00:00Z", "name": "Member"}
						if tc.visible {
							want["email"] = "private@example.test"
						}
						if !reflect.DeepEqual(row, want) {
							t.Fatalf("response=%v; want=%v", row, want)
						}
					})
				}
			}
		}
	}
}
