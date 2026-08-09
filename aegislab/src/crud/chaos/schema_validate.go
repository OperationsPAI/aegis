package chaos

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrSchemaValidation marks failures from per-capability JSON Schema
// checks (target / param_overrides / params). The handler maps it to 400.
var ErrSchemaValidation = errors.New("chaos: schema validation failed")

// SchemaLeaf carries one leaf-level field path and message from a failed
// validation. The handler exposes these on the HTTP response so callers
// can attribute errors without re-parsing a flattened string.
type SchemaLeaf struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SchemaValidationError is returned wrapped in ErrSchemaValidation; the
// HTTP handler unwraps to surface Leaves verbatim in the response body.
type SchemaValidationError struct {
	Leaves []SchemaLeaf
}

func (e *SchemaValidationError) Error() string {
	parts := make([]string, len(e.Leaves))
	for i, l := range e.Leaves {
		parts[i] = fmt.Sprintf("%s: %s", l.Path, l.Message)
	}
	return strings.Join(parts, "; ")
}

func (e *SchemaValidationError) Unwrap() error { return ErrSchemaValidation }

// schemaCompiler caches compiled target/param/subset schemas across
// every Point in a manifest import or every child in an injection batch,
// so the same capability isn't recompiled per row.
type schemaCompiler struct {
	target map[string]*jsonschema.Schema
	params map[string]*jsonschema.Schema
	subset map[string]*jsonschema.Schema
}

func newSchemaCompiler() *schemaCompiler {
	return &schemaCompiler{
		target: map[string]*jsonschema.Schema{},
		params: map[string]*jsonschema.Schema{},
		subset: map[string]*jsonschema.Schema{},
	}
}

func compileSchema(raw map[string]any) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:///schema.json", any(raw)); err != nil {
		return nil, err
	}
	return c.Compile("mem:///schema.json")
}

func (sc *schemaCompiler) forTarget(c *Capability) (*jsonschema.Schema, error) {
	if s, ok := sc.target[c.Name]; ok {
		return s, nil
	}
	cloned := cloneStrictObjects(map[string]any(c.TargetSchema))
	s, err := compileSchema(cloned)
	if err != nil {
		return nil, fmt.Errorf("compile target_schema for %q: %w", c.Name, err)
	}
	sc.target[c.Name] = s
	return s, nil
}

func (sc *schemaCompiler) forParams(c *Capability) (*jsonschema.Schema, error) {
	if s, ok := sc.params[c.Name]; ok {
		return s, nil
	}
	cloned := cloneStrictObjects(map[string]any(c.ParamSchema))
	s, err := compileSchema(cloned)
	if err != nil {
		return nil, fmt.Errorf("compile param_schema for %q: %w", c.Name, err)
	}
	sc.params[c.Name] = s
	return s, nil
}

// forParamsSubset returns the param_schema with `required` stripped at
// object-schema positions, so partial param_overrides still get type /
// additionalProperties checks without being rejected for missing fields.
func (sc *schemaCompiler) forParamsSubset(c *Capability) (*jsonschema.Schema, error) {
	if s, ok := sc.subset[c.Name]; ok {
		return s, nil
	}
	cloned := cloneStrictObjects(map[string]any(c.ParamSchema))
	stripRequiredAtObjects(cloned)
	s, err := compileSchema(cloned)
	if err != nil {
		return nil, fmt.Errorf("compile param_schema (subset) for %q: %w", c.Name, err)
	}
	sc.subset[c.Name] = s
	return s, nil
}

// cloneStrictObjects deep-clones a schema and forces
// additionalProperties:false on every object schema that doesn't set it.
// Seed audits are easy to miss, so the server makes "strict" structural
// rather than depending on every author remembering the keyword.
func cloneStrictObjects(v any) map[string]any {
	src, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, val := range src {
		out[k] = cloneSchemaNode(val)
	}
	if isObjectSchema(out) {
		if _, set := out["additionalProperties"]; !set {
			out["additionalProperties"] = false
		}
	}
	return out
}

// cloneSchemaNode recurses into known JSON Schema keywords whose values
// are themselves schemas (or maps of schemas / arrays of schemas). For
// any other key the value is returned as-is — that way a user-defined
// `properties.required` (an OBJECT field literally named "required")
// keeps its sub-schema instead of being mistaken for the `required`
// keyword that gates "this key must appear in the instance".
func cloneSchemaNode(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			switch k {
			case "properties", "patternProperties", "$defs", "definitions":
				out[k] = cloneSchemaMap(val)
			case "items", "additionalProperties", "contains",
				"not", "if", "then", "else",
				"propertyNames", "unevaluatedItems", "unevaluatedProperties":
				out[k] = cloneSchemaOrBool(val)
			case "allOf", "anyOf", "oneOf", "prefixItems":
				out[k] = cloneSchemaArray(val)
			default:
				out[k] = cloneRaw(val)
			}
		}
		if isObjectSchema(out) {
			if _, set := out["additionalProperties"]; !set {
				out["additionalProperties"] = false
			}
		}
		return out
	case []any:
		dup := make([]any, len(t))
		for i, e := range t {
			dup[i] = cloneRaw(e)
		}
		return dup
	default:
		return v
	}
}

func cloneSchemaMap(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return cloneRaw(v)
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = cloneSchemaOrBool(val)
	}
	return out
}

func cloneSchemaArray(v any) any {
	a, ok := v.([]any)
	if !ok {
		return cloneRaw(v)
	}
	out := make([]any, len(a))
	for i, e := range a {
		out[i] = cloneSchemaOrBool(e)
	}
	return out
}

func cloneSchemaOrBool(v any) any {
	switch t := v.(type) {
	case map[string]any:
		// Recurse via cloneSchemaNode so additionalProperties:false is
		// injected at nested object schemas.
		return cloneSchemaNode(t)
	default:
		return cloneRaw(t)
	}
}

func cloneRaw(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = cloneRaw(val)
		}
		return out
	case []any:
		dup := make([]any, len(t))
		for i, e := range t {
			dup[i] = cloneRaw(e)
		}
		return dup
	default:
		return v
	}
}

// isObjectSchema returns true when the map looks like an object-typed
// schema position: explicit `type:"object"`, or carrying object-shaped
// keywords (properties / additionalProperties / required …) without a
// conflicting non-object `type`.
func isObjectSchema(m map[string]any) bool {
	switch t := m["type"].(type) {
	case string:
		return t == "object"
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s == "object" {
				return true
			}
		}
		return false
	}
	if _, ok := m["properties"]; ok {
		return true
	}
	if _, ok := m["patternProperties"]; ok {
		return true
	}
	if _, ok := m["required"]; ok {
		return true
	}
	return false
}

// stripRequiredAtObjects removes the `required` keyword at any
// object-schema position reachable via JSON Schema keywords. A user
// property literally named "required" survives because we never recurse
// into `properties` values as if they were keyword maps.
func stripRequiredAtObjects(m map[string]any) {
	if isObjectSchema(m) {
		delete(m, "required")
	}
	for k, v := range m {
		switch k {
		case "properties", "patternProperties", "$defs", "definitions":
			if sub, ok := v.(map[string]any); ok {
				for _, child := range sub {
					if cm, ok := child.(map[string]any); ok {
						stripRequiredAtObjects(cm)
					}
				}
			}
		case "items", "additionalProperties", "contains",
			"not", "if", "then", "else",
			"propertyNames", "unevaluatedItems", "unevaluatedProperties":
			if cm, ok := v.(map[string]any); ok {
				stripRequiredAtObjects(cm)
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if arr, ok := v.([]any); ok {
				for _, e := range arr {
					if cm, ok := e.(map[string]any); ok {
						stripRequiredAtObjects(cm)
					}
				}
			}
		}
	}
}

func validateInstance(schema *jsonschema.Schema, instance map[string]any, prefix string) error {
	var doc any = instance
	if instance == nil {
		doc = map[string]any{}
	}
	err := schema.Validate(doc)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return &SchemaValidationError{Leaves: []SchemaLeaf{{Path: prefix, Message: err.Error()}}}
	}
	return &SchemaValidationError{Leaves: flattenSchemaLeaves(verr, prefix)}
}

func flattenSchemaLeaves(ve *jsonschema.ValidationError, prefix string) []SchemaLeaf {
	var out []SchemaLeaf
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			path := prefix
			if len(e.InstanceLocation) > 0 {
				path = prefix + "." + strings.Join(e.InstanceLocation, ".")
			}
			out = append(out, SchemaLeaf{Path: path, Message: e.Error()})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(out) == 0 {
		out = append(out, SchemaLeaf{Path: prefix, Message: ve.Error()})
	}
	return out
}

// mergeParams deep-merges caller-supplied params with the point's
// param_overrides; **overrides win** at every leaf. param_overrides are
// the manifest author's lockdown of permitted runtime values for a
// published Point — a caller that sends `duration_s:9999` for a Point
// that pinned `duration_s:30` gets 30. Nested objects are merged
// recursively; arrays and scalars are replaced wholesale by the
// override side. Callers can still fill in keys the override didn't
// pin.
func mergeParams(callerParams, overrides map[string]any) map[string]any {
	out := make(map[string]any, len(callerParams)+len(overrides))
	for k, v := range callerParams {
		out[k] = cloneRaw(v)
	}
	for k, ov := range overrides {
		if existing, ok := out[k]; ok {
			if em, eok := existing.(map[string]any); eok {
				if om, ook := ov.(map[string]any); ook {
					out[k] = mergeParams(em, om)
					continue
				}
			}
		}
		out[k] = cloneRaw(ov)
	}
	return out
}
