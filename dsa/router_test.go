package dsa

import (
	"reflect"
	"testing"
)

type testHandler struct {
	name string
}

func (h testHandler) handle() {}

func TestRouter_StaticRoute(t *testing.T) {
	r := NewRouter()

	h := testHandler{"static"}
	r.add(GET, "/users/profile", h)

	handler, params := r.findHandler(GET, "/users/profile")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}

	if len(params) != 0 {
		t.Fatalf("expected empty params, got %v", params)
	}
}

func TestRouter_ParamRoute(t *testing.T) {
	r := NewRouter()

	h := testHandler{"param"}
	r.add(GET, "/users/:id", h)

	handler, params := r.findHandler(GET, "/users/123")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}

	expected := map[string]string{
		":id": "123",
	}

	if !reflect.DeepEqual(params, expected) {
		t.Fatalf("expected params %v, got %v", expected, params)
	}
}

func TestRouter_WildcardRoute(t *testing.T) {
	r := NewRouter()

	h := testHandler{"wildcard"}
	r.add(GET, "/files/*path", h)

	handler, params := r.findHandler(GET, "/files/a/b/c")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}

	if params != nil && len(params) != 0 {
		t.Fatalf("expected nil or empty params, got %v", params)
	}
}

func TestRouter_Priority_StaticOverParam(t *testing.T) {
	r := NewRouter()

	staticHandler := testHandler{"static"}
	paramHandler := testHandler{"param"}

	r.add(GET, "/users/me", staticHandler)
	r.add(GET, "/users/:id", paramHandler)

	handler, _ := r.findHandler(GET, "/users/me")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}
}

func TestRouter_Priority_ParamOverWildcard(t *testing.T) {
	r := NewRouter()

	paramHandler := testHandler{"param"}
	wildHandler := testHandler{"wildcard"}

	r.add(GET, "/users/:id", paramHandler)
	r.add(GET, "/users/*rest", wildHandler)

	handler, _ := r.findHandler(GET, "/users/123")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}
}

func TestRouter_MultipleMethods(t *testing.T) {
	r := NewRouter()

	getHandler := testHandler{"get"}
	postHandler := testHandler{"post"}

	r.add(GET, "/users", getHandler)
	r.add(POST, "/users", postHandler)

	handlerGet, _ := r.findHandler(GET, "/users")
	handlerPost, _ := r.findHandler(POST, "/users")

	if handlerGet == nil || handlerPost == nil {
		t.Fatalf("expected both handlers, got nil")
	}
}

func TestRouter_NotFound(t *testing.T) {
	r := NewRouter()

	r.add(GET, "/users", testHandler{"users"})

	handler, _ := r.findHandler(GET, "/unknown")

	if handler != nil {
		t.Fatalf("expected nil handler, got %v", handler)
	}
}

func TestRouter_DeepPath(t *testing.T) {
	r := NewRouter()

	h := testHandler{"deep"}
	r.add(GET, "/a/b/c/d", h)

	handler, _ := r.findHandler(GET, "/a/b/c/d")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}
}

func TestRouter_ParamMultipleSegments(t *testing.T) {
	r := NewRouter()

	h := testHandler{"multi-param"}
	r.add(GET, "/users/:id/orders/:orderId", h)

	handler, params := r.findHandler(GET, "/users/123/orders/456")

	if handler == nil {
		t.Fatalf("expected handler, got nil")
	}

	expected := map[string]string{
		":id":      "123",
		":orderId": "456",
	}

	if !reflect.DeepEqual(params, expected) {
		t.Fatalf("expected params %v, got %v", expected, params)
	}
}
