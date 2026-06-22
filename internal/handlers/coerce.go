package handlers

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// The PM web client submits form values as strings (e.g.
// {"amount":"5000","target":"60","delta":"5"}), but the request structs declare
// those fields as float64/int. Plain ShouldBindJSON would then 400 with
// `json: cannot unmarshal string into Go struct field`. These helpers re-encode
// string-encoded scalars to the destination field's kind before the final
// decode. Nested struct and slice-of-struct fields (e.g. createTasksBulk's
// tasks[].sprintId) are recursed into; a slice of bare scalars
// (deleteTasksBatch's ids[]) is not. String-typed fields are never touched
// because coercion only fires when the target kind is numeric/bool.

// bindJSONCoerced decodes a single JSON object body into dst, first coercing
// string-encoded numeric/bool fields to dst's scalar field kinds.
func bindJSONCoerced(c *gin.Context, dst any) error {
	raw, err := c.GetRawData()
	if err != nil {
		return err
	}
	return json.Unmarshal(coerceJSONScalars(raw, dst), dst)
}

func coerceScalarStrings(t reflect.Type, m map[string]any) {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		val, present := m[name]
		if !present {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if s, ok := val.(string); ok && s == "" {
			switch ft.Kind() {
			case reflect.Float32, reflect.Float64,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Bool:
				// Blank numeric/bool form value clears the field: JSON null decodes
				// to zero (value) or nil (pointer); "" would fail the unmarshal.
				m[name] = nil
				continue
			}
		}
		switch ft.Kind() {
		case reflect.Struct:
			if nested, ok := val.(map[string]any); ok {
				coerceScalarStrings(ft, nested)
			}
		case reflect.Slice, reflect.Array:
			et := ft.Elem()
			for et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				if arr, ok := val.([]any); ok {
					for _, el := range arr {
						if nested, ok := el.(map[string]any); ok {
							coerceScalarStrings(et, nested)
						}
					}
				}
			}
		case reflect.Float32, reflect.Float64:
			if s, ok := val.(string); ok && s != "" {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					m[name] = v
				}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if s, ok := val.(string); ok && s != "" {
				if v, err := strconv.ParseInt(s, 10, 64); err == nil {
					m[name] = v
				}
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if s, ok := val.(string); ok && s != "" {
				if v, err := strconv.ParseUint(s, 10, 64); err == nil {
					m[name] = v
				}
			}
		case reflect.Bool:
			if s, ok := val.(string); ok && s != "" {
				if v, err := strconv.ParseBool(s); err == nil {
					m[name] = v
				}
			}
		}
	}
}

func coerceJSONScalars(raw []byte, dst any) []byte {
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil {
		return raw
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		var rows []map[string]any
		if json.Unmarshal(raw, &rows) != nil {
			return raw
		}
		et := t.Elem()
		for _, m := range rows {
			coerceScalarStrings(et, m)
		}
		if out, err := json.Marshal(rows); err == nil {
			return out
		}
	case reflect.Struct:
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return raw
		}
		coerceScalarStrings(t, m)
		if out, err := json.Marshal(m); err == nil {
			return out
		}
	}
	return raw
}
