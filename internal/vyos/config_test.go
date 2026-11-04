package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"status", 500, `{"success":true,"data":{}}`},
		{"success false", 200, `{"success":false,"data":{}}`},
		{"missing success", 200, `{"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
			if _, err := vc.Show(context.Background(), "system"); err == nil {
				t.Fatal("failure response accepted")
			}
		})
	}
}

func TestReadRefreshesExternalDrift(t *testing.T) {
	value := "before"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"success":true,"data":{"system":{"host-name":%q}}}`, value)
	}))
	defer s.Close()
	vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	if _, err := vc.Show(context.Background(), "system host-name"); err != nil {
		t.Fatal(err)
	}
	value = "after"
	got, err := vc.Show(context.Background(), "system host-name")
	if err != nil || got != "after" {
		t.Fatalf("stale read: %v, %v", got, err)
	}
}

func TestMutationPersistenceAndSerialization(t *testing.T) {
	for _, mode := range []string{"create", "update", "delete"} {
		for _, saving := range []string{"default", "custom", "skip"} {
			t.Run(mode+"/"+saving, func(t *testing.T) {
				var calls []string
				var save map[string]any
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.URL.Path)
					r.ParseForm()
					if r.FormValue("key") != "test" {
						t.Error("missing key")
					}
					if r.URL.Path == "/retrieve" {
						if mode == "create" {
							fmt.Fprint(w, `{"success":true,"data":{}}`)
						} else {
							fmt.Fprint(w, `{"success":true,"data":{"node":{"x":"old"}}}`)
						}
						return
					}
					if r.URL.Path == "/config-file" {
						json.Unmarshal([]byte(r.FormValue("data")), &save)
					}
					if r.URL.Path == "/configure" {
						var ops []map[string]any
						if json.Unmarshal([]byte(r.FormValue("data")), &ops) != nil {
							t.Error("invalid operations")
						}
						if mode == "update" && (len(ops) != 2 || ops[0]["op"] != "delete" || ops[1]["op"] != "set") {
							t.Errorf("update must use one delete/set batch: %v", ops)
						}
					}
					fmt.Fprint(w, `{"success":true,"data":null}`)
				}))
				defer s.Close()
				file := ""
				if saving == "custom" {
					file = "/config/custom.boot"
				}
				vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, saving == "skip", file)
				var err error
				switch mode {
				case "create":
					err = vc.Set(context.Background(), "node x", "new")
				case "update":
					err = vc.Update(context.Background(), "node x", "new")
				case "delete":
					err = vc.Delete(context.Background(), "node x")
				}
				if err != nil {
					t.Fatal(err)
				}
				expected := []string{"/retrieve", "/configure"}
				if saving != "skip" {
					expected = append(expected, "/config-file")
					if save["op"] != "save" {
						t.Fatal(save)
					}
					if saving == "custom" && save["file"] != file {
						t.Fatal(save)
					}
				}
				if !reflect.DeepEqual(calls, expected) {
					t.Fatalf("calls %v; want %v", calls, expected)
				}
			})
		}
	}
}

func TestMutationFailures(t *testing.T) {
	for _, fail := range []string{"retrieve", "configure", "config-file"} {
		t.Run(fail, func(t *testing.T) {
			var calls []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.URL.Path == "/"+fail {
					fmt.Fprint(w, `{"success":false,"error":"SECRET"}`)
				} else {
					fmt.Fprint(w, `{"success":true,"data":{"node":{"x":"old"}}}`)
				}
			}))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET"}, false, "")
			err := vc.Update(context.Background(), "node x", "SECRET")
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			var applied *AppliedError
			if errors.As(err, &applied) != (fail == "config-file") {
				t.Fatalf("wrong partial-commit status: %v", err)
			}
			expected := map[string]int{"retrieve": 1, "configure": 2, "config-file": 3}[fail]
			if len(calls) != expected {
				t.Fatal(calls)
			}
		})
	}
}

func TestConcurrentResources(t *testing.T) {
	var calls []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		fmt.Fprint(w, `{"success":true,"data":{}}`)
	}))
	defer s.Close()
	vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := vc.Set(context.Background(), fmt.Sprintf("node n%d", i), "value"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < len(calls); i += 3 {
		if !reflect.DeepEqual(calls[i:i+3], []string{"/retrieve", "/configure", "/config-file"}) {
			t.Fatalf("interleaved transaction: %v", calls)
		}
	}
}

func TestInvalidValuesCannotMutate(t *testing.T) {
	for _, raw := range []string{``, `{`, `null`, `42`, `true`, `[]`, `[{}]`, `{"x":[]}`, `{"x":null}`, `{"":{}}`, `"x" garbage`} {
		if _, err := DecodeValue(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, path := range []string{"", " ", " node", "node ", "node  x", "node\nx"} {
		if _, err := ParsePath(path); err == nil {
			t.Errorf("accepted path %q", path)
		}
	}
	value, err := DecodeValue(`{"null":null,"flag":{},"scalar":"space separated secret","list":["a","b"],"tag with space":{"x":"y"}}`)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := Operations([]string{"node"}, value)
	if err != nil || len(ops) != 5 {
		t.Fatalf("%v %v", ops, err)
	}
	if !Equivalent(`{ "x": ["one"], "omitted":null }`, map[string]any{"x": "one"}) {
		t.Fatal("equivalent scalar/singleton list and null members differ")
	}
	if Equivalent(`{"x":"one"}`, map[string]any{"x": "two"}) {
		t.Fatal("drift hidden")
	}
}

func TestMalformedAndRootErrors(t *testing.T) {
	for _, body := range []string{`<html>SECRET</html>`, `null`, `{"success":true,"data":[]}`, `{"success":true,"data":null}`, `{"success":false,"error":"could not fetch config SECRET"}`, `{"success":true,"data":{"node":null}}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
		_, err := vc.Show(context.Background(), "node")
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", body, err)
		}
		s.Close()
	}
}

func TestOwnershipAndScalarTraversal(t *testing.T) {
	var mutations int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/configure" {
			mutations++
		}
		fmt.Fprint(w, `{"success":true,"data":{"node":{"existing":{},"scalar":"value"}}}`)
	}))
	defer s.Close()
	vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	if err := vc.Set(context.Background(), "node existing", map[string]any{}); err == nil {
		t.Fatal("adopted existing flag")
	}
	if _, err := vc.Show(context.Background(), "node scalar child"); err == nil {
		t.Fatal("scalar traversal must fail")
	}
	vc = New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	if _, err := vc.Show(context.Background(), "node"); err != nil {
		t.Fatal(err)
	}
	if err := vc.Set(context.Background(), "node child", "value"); err == nil {
		t.Fatal("overlapping child ownership accepted")
	}
	vc = New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	if _, err := vc.Show(context.Background(), "node existing"); err != nil {
		t.Fatal(err)
	}
	if err := vc.Update(context.Background(), "node", map[string]any{}); err == nil {
		t.Fatal("overlapping parent ownership accepted")
	}
	if mutations != 0 {
		t.Fatal("unsafe mutation sent")
	}
}

func TestNestedNullOmission(t *testing.T) {
	if !Equivalent(`{"parent":{"omitted":null},"kept":"value"}`, map[string]any{"kept": "value"}) {
		t.Fatal("empty null-only subtree was not omitted")
	}
	if Equivalent(`{"flag":{},"kept":"value"}`, map[string]any{"kept": "value"}) {
		t.Fatal("real empty flag was omitted")
	}
}

func TestConcurrentCreatesCannotAdoptSamePath(t *testing.T) {
	exists := false
	mutations := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/retrieve" {
			if exists {
				fmt.Fprint(w, `{"success":true,"data":{"node":{"x":"created"}}}`)
			} else {
				fmt.Fprint(w, `{"success":true,"data":{}}`)
			}
			return
		}
		if r.URL.Path == "/configure" {
			exists = true
			mutations++
		}
		fmt.Fprint(w, `{"success":true,"data":null}`)
	}))
	defer s.Close()
	vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() { results <- vc.Set(context.Background(), "node x", "created") }()
	}
	success := 0
	for i := 0; i < 10; i++ {
		if <-results == nil {
			success++
		}
	}
	if success != 1 || mutations != 1 {
		t.Fatalf("%d successful creates and %d mutations", success, mutations)
	}
}

func TestDeletedPathsDoNotBlockReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, oldPath, newPath string
		present                bool
		refreshOnly            bool
	}{
		{"parent to child", "node", "node child", true, false},
		{"child to parent", "node child", "node", true, false},
		{"already absent", "node", "node child", false, false},
		{"absent on refresh", "node", "node child", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present := tc.present
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/retrieve" {
					if present {
						fmt.Fprint(w, `{"success":true,"data":{"node":{"child":{}}}}`)
					} else {
						fmt.Fprint(w, `{"success":true,"data":{}}`)
					}
					return
				}
				if r.URL.Path == "/configure" {
					present = false
				}
				fmt.Fprint(w, `{"success":true,"data":null}`)
			}))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
			ctx := context.Background()
			if tc.refreshOnly {
				if _, err := vc.Show(ctx, tc.oldPath); err != nil {
					t.Fatal(err)
				}
			} else if err := vc.Delete(ctx, tc.oldPath); err != nil {
				t.Fatal(err)
			}
			if err := vc.Set(ctx, tc.newPath, map[string]any{}); err != nil {
				t.Fatalf("replacement rejected after old path disappeared: %v", err)
			}
		})
	}
}

func TestFailedDeleteKeepsOwnership(t *testing.T) {
	for _, failure := range []string{"retrieve", "configure", "config-file"} {
		t.Run(failure, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+failure {
					fmt.Fprint(w, `{"success":false}`)
				} else {
					fmt.Fprint(w, `{"success":true,"data":{"node":{"child":{}}}}`)
				}
			}))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
			if err := vc.Delete(context.Background(), "node"); err == nil {
				t.Fatal("failed delete accepted")
			}
			if err := vc.Set(context.Background(), "node child", map[string]any{}); err == nil || !strings.Contains(err.Error(), "overlapping") {
				t.Fatalf("failed delete released ownership: %v", err)
			}
		})
	}
}
