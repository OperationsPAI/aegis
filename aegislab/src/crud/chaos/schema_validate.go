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

// schemaCompiler builds a per-request cache so the same capability's
// target/param schema is compiled at most twice (once full, once subset)
// even when a manifest references it on many points.
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
	s, err := compileSchema(map[string]any(c.TargetSchema))
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
	s, err := compileSchema(map[string]any(c.ParamSchema))
	if err != nil {
		return nil, fmt.Errorf("compile param_schema for %q: %w", c.Name, err)
	}
	sc.params[c.Name] = s
	return s, nil
}

// forParamsSubset returns the param_schema with `required` stripped at
// every level, so partial param_overrides (which intentionally specify
// only a subset of params) still get type / additionalProperties checks
// without being rejected for missing fields.
func (sc *schemaCompiler) forParamsSubset(c *Capability) (*jsonschema.Schema, error) {
	if s, ok := sc.subset[c.Name]; ok {
		return s, nil
	}
	cloned := deepCloneStripRequired(map[string]any(c.ParamSchema))
	s, err := compileSchema(cloned)
	if err != nil {
		return nil, fmt.Errorf("compile param_schema (subset) for %q: %w", c.Name, err)
	}
	sc.subset[c.Name] = s
	return s, nil
}

func deepCloneStripRequired(v any) map[string]any {
	src, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, val := range src {
		if k == "required" {
			continue
		}
		out[k] = cloneStrip(val)
	}
	return out
}

func cloneStrip(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCloneStripRequired(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneStrip(e)
		}
		return out
	default:
		return v
	}
}

func validateInstance(schema *jsonschema.Schema, instance map[string]any, prefix string) error {
	// jsonschema/v6 requires the instance to be plain interface{} (the
	// shape json.Unmarshal would produce). map[string]any qualifies.
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
		return fmt.Errorf("%w: %s: %v", ErrSchemaValidation, prefix, err)
	}
	leaves := flattenSchemaLeaves(verr, prefix)
	return fmt.Errorf("%w: %s", ErrSchemaValidation, strings.Join(leaves, "; "))
}

func flattenSchemaLeaves(ve *jsonschema.ValidationError, prefix string) []string {
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			path := prefix
			if len(e.InstanceLocation) > 0 {
				path = prefix + "." + strings.Join(e.InstanceLocation, ".")
			}
			out = append(out, fmt.Sprintf("%s: %s", path, e.Error()))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("%s: %s", prefix, ve.Error()))
	}
	return out
}

// mergeParams returns overrides ⨁ params: caller-supplied params win
// over baked-in point.param_overrides. The merge is shallow — the same
// semantics every aegis caller already assumes for params payloads.
func mergeParams(overrides, params map[string]any) map[string]any {
	out := make(map[string]any, len(overrides)+len(params))
	for k, v := range overrides {
		out[k] = v
	}
	for k, v := range params {
		out[k] = v
	}
	return out
}
