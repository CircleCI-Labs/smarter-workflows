package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// schema is the part of libs/proto/schema.json the tests check against.
type schema struct {
	Required   []string `json:"required"`
	Properties map[string]struct {
		Type string `json:"type"`
	} `json:"properties"`
}

func loadSchema(t *testing.T) schema {
	t.Helper()
	data, err := os.ReadFile("../../libs/proto/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestGreetingFollowsTheSchema(t *testing.T) {
	s := loadSchema(t)
	rec := get(t, "/greeting?name=Ada")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range s.Required {
		if _, ok := body[key]; !ok {
			t.Errorf("the response lacks the required property %q: %v", key, body)
		}
	}
	for key, prop := range s.Properties {
		if v, ok := body[key]; ok && prop.Type == "string" {
			if _, isString := v.(string); !isString {
				t.Errorf("property %q = %v, want a string", key, v)
			}
		}
	}
	if body["message"] != "Hello, Ada!" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestGreetingDefaultsToTheWorld(t *testing.T) {
	var g Greeting
	if err := json.Unmarshal(get(t, "/greeting").Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if g.Name != "world" || g.Message != "Hello, world!" {
		t.Errorf("greeting = %+v", g)
	}
}

func TestHealthz(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	if code := get(t, "/nope").Code; code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
}
