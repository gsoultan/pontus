package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Fields that legitimately have no consumer outside this package.
//
// Keep this list short and justified. Every entry is a promise that the field
// is either an alias resolved here or is consumed by something a source scan
// cannot see.
var allowedUnwired = map[string]string{
	"AuthKey": "alias for JWTSecret, resolved in resolveSecrets before anything reads it",
}

// Every configuration field must be read by something.
//
// This exists because the same defect kept shipping: a field parsed, defaulted,
// merged, offered in the dashboard, and consumed by nothing. The firewall was
// never enabled, four balancer strategies were unreachable, pooling_mode did
// nothing, and health_interval did not change the health interval. Each was
// found by accident, months apart, and each looked like a working feature until
// someone tested the behaviour rather than the setting.
//
// A source scan is crude — it matches ".FieldName" textually — but it fails
// loudly the moment a field is added with no reader, which is the only property
// that matters here.
func TestEveryConfigFieldIsConsumed(t *testing.T) {
	root := moduleRoot(t)
	sources := goSources(t, root)

	var unwired []string
	for _, field := range exportedFields(reflect.TypeOf(Options{})) {
		if reason, ok := allowedUnwired[field]; ok {
			t.Logf("skipping %s: %s", field, reason)
			continue
		}
		if !referenced(sources, field) {
			unwired = append(unwired, field)
		}
	}

	if len(unwired) > 0 {
		t.Errorf("config fields with no consumer outside pkg/config: %v\n"+
			"Either wire the field up, remove it, or add it to allowedUnwired with a reason.",
			unwired)
	}
}

// Nested config structs matter just as much: cache.max_size was parsed and
// ignored for a long time, which made the result cache unbounded.
func TestNestedConfigFieldsAreConsumed(t *testing.T) {
	root := moduleRoot(t)
	sources := goSources(t, root)

	// Derived from Options rather than listed by hand. A hardcoded list is a
	// second thing to remember to update, and it silently stopped covering the
	// failover block the moment that block was added — which is the same
	// failure mode this whole file exists to catch.
	nested := nestedStructs(reflect.TypeOf(Options{}))
	if len(nested) < 4 {
		t.Fatalf("found only %d nested config structs; the walk is not working", len(nested))
	}

	for name, typ := range nested {
		t.Run(name, func(t *testing.T) {
			var unwired []string
			for _, field := range exportedFields(typ) {
				if !referenced(sources, field) {
					unwired = append(unwired, field)
				}
			}
			if len(unwired) > 0 {
				t.Errorf("%s fields with no consumer outside pkg/config: %v", name, unwired)
			}
		})
	}
}

func exportedFields(typ reflect.Type) []string {
	var out []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.IsExported() {
			out = append(out, field.Name)
		}
	}
	return out
}

// referenced reports whether ".Field" appears in any scanned source file.
func referenced(sources []string, field string) bool {
	needle := "." + field
	for _, src := range sources {
		if strings.Contains(src, needle) {
			return true
		}
	}
	return false
}

// goSources returns every non-test Go file outside pkg/config.
//
// Tests are excluded deliberately: a field referenced only by a test that
// constructs a config is not a consumer, and counting it would hide exactly the
// defect this is looking for.
func goSources(t *testing.T, root string) []string {
	t.Helper()

	skipDirs := map[string]bool{
		".git": true, "web": true, "graphify-out": true,
		"node_modules": true, ".dev": true, "vendor": true,
	}

	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory is not this test's problem
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			// The definitions themselves do not count as consumers.
			if path == filepath.Join(root, "pkg", "config") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		out = append(out, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no Go sources found under %s; the scan would pass vacuously", root)
	}
	return out
}

// moduleRoot locates the repository root from this test's own path.
func moduleRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// pkg/config/wired_test.go -> repository root
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found at %s: %v", root, err)
	}
	return root
}

// nestedStructs collects every struct type reachable one level down from the
// given config struct, keyed by the element type's name.
//
// Slices count: Backends is []Backend, and a field on Backend that nothing
// reads is exactly as dead as a field on Cache that nothing reads.
func nestedStructs(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}

	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}

		ft := field.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() != reflect.Struct || ft.PkgPath() != typ.PkgPath() {
			continue
		}
		out[ft.Name()] = ft
	}

	return out
}
