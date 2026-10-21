package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	kversion "github.com/zypher-systems/kairos/internal/version"
)

type stubLeader struct {
	id    string
	lead  bool
	until time.Time
}

func (s stubLeader) InstanceID() string    { return s.id }
func (s stubLeader) IsLeader() bool        { return s.lead }
func (s stubLeader) LeaseUntil() time.Time { return s.until }

func TestInfoReportsLeader(t *testing.T) {
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, nil, nil, nil)
	until := time.Date(2026, 9, 9, 19, 0, 0, 0, time.UTC)
	api.SetLeader(stubLeader{id: "replica-a", lead: true, until: until})
	h := api.Router()
	req := httptest.NewRequest(http.MethodGet, "http://kairos.local/api/v1/info", nil)
	req.Host = "kairos.local"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "kairos" || body["version"] != kversion.Version {
		t.Fatalf("identity %+v", body)
	}
	if body["instance_id"] != "replica-a" {
		t.Fatalf("instance_id %+v", body)
	}
	if body["leader"] != true {
		t.Fatalf("leader %+v", body)
	}
	if body["lease_until"] != until.Format(time.RFC3339) {
		t.Fatalf("lease_until %+v", body)
	}
}
