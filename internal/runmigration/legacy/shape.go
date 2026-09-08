package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Source control objects were emitted by typed plan serialization even though
// extra Run fields were appended to a map. Check their original presence before
// conversion can manufacture Go zero values. User parameter objects stay open.
func checkSourceShape(raw json.RawMessage, typ reflect.Type, path string) error {
	if typ.Kind() == reflect.Interface {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		return checkSourceShape(raw, typ.Elem(), path)
	}
	if bytes.Equal(raw, []byte("null")) {
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			return nil
		}
		return fmt.Errorf("%s must not be null", path)
	}
	if typ.Implements(reflect.TypeFor[json.Marshaler]()) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var values map[string]json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		type field struct {
			typ      reflect.Type
			required bool
		}
		fields := map[string]field{}
		var collect func(reflect.Type, bool)
		collect = func(t reflect.Type, required bool) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				if f.Anonymous {
					collect(f.Type, true)
					continue
				}
				tags := strings.Split(f.Tag.Get("json"), ",")
				name := tags[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = f.Name
				}
				optional := len(tags) > 1 && (tags[1] == "omitempty" || tags[1] == "omitzero")
				fields[name] = field{f.Type, required && !optional}
			}
		}
		collect(typ, typ != reflect.TypeFor[Snapshot]())
		for name, v := range values {
			f, ok := fields[name]
			if !ok {
				return fmt.Errorf("%s.%s is unknown", path, name)
			}
			if e := checkSourceShape(v, f.typ, path+"."+name); e != nil {
				return e
			}
		}
		for name, f := range fields {
			if _, ok := values[name]; !ok && f.required {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return e
		}
		for i, v := range values {
			if e := checkSourceShape(v, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); e != nil {
				return e
			}
		}
	case reflect.Map:
		if typ.Elem().Kind() == reflect.Interface {
			return nil
		}
		var values map[string]json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return e
		}
		for k, v := range values {
			if e := checkSourceShape(v, typ.Elem(), path+"."+k); e != nil {
				return e
			}
		}
	}
	return nil
}
