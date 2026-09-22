package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

/*
A deliberately small JSON Schema subset.

The point is not to be a schema engine — it is to answer one question: did the
model return the shape we asked for? A full validator would be a dependency and
a surface area; what the pipeline needs is object/array/string/number/boolean,
required fields, enums, and nesting. Anything a schema here cannot express is a
sign the prompt is asking for too much at once.

Unknown keywords are ignored rather than rejected, so a schema can carry
"description" text for the prompt without the validator caring.
*/

// SchemaType is the JSON type a value must have.
type SchemaType string

const (
	TypeObject  SchemaType = "object"
	TypeArray   SchemaType = "array"
	TypeString  SchemaType = "string"
	TypeNumber  SchemaType = "number"
	TypeInteger SchemaType = "integer"
	TypeBoolean SchemaType = "boolean"
)

// Schema describes the structured output a generation task must return.
type Schema struct {
	// Name is used in the prompt and in error messages, e.g. "opportunity".
	Name string `json:"name,omitempty"`

	Description string     `json:"description,omitempty"`
	Type        SchemaType `json:"type"`

	// Object
	Properties map[string]*Schema `json:"properties,omitempty"`
	Required   []string           `json:"required,omitempty"`

	// Array
	Items    *Schema `json:"items,omitempty"`
	MinItems int     `json:"minItems,omitempty"`
	MaxItems int     `json:"maxItems,omitempty"`

	// String / number
	Enum      []string `json:"enum,omitempty"`
	MinLength int      `json:"minLength,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`

	// Nullable permits an explicit null where evidence is genuinely absent —
	// which the epic wants said out loud rather than filled in.
	Nullable bool `json:"-"`
}

// Validate checks a decoded JSON value against the schema. The error names the
// path that failed, because that string goes back to the model on the retry.
func (s *Schema) Validate(value any) error {
	if s == nil {
		return nil
	}
	return s.validate("$", value)
}

func (s *Schema) validate(path string, value any) error {
	if value == nil {
		if s.Nullable {
			return nil
		}
		return fmt.Errorf("%s: expected %s, got null", path, s.Type)
	}

	switch s.Type {
	case TypeObject:
		obj, ok := value.(map[string]any)
		if !ok {
			return typeErr(path, "object", value)
		}
		for _, req := range s.Required {
			if _, present := obj[req]; !present {
				return fmt.Errorf("%s: required property %q is missing", path, req)
			}
		}
		// Deterministic order: the same bad output must produce the same
		// message twice, or a retry log is unreadable.
		keys := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, present := obj[k]
			if !present {
				continue
			}
			if err := s.Properties[k].validate(path+"."+k, v); err != nil {
				return err
			}
		}
		return nil

	case TypeArray:
		arr, ok := value.([]any)
		if !ok {
			return typeErr(path, "array", value)
		}
		if s.MinItems > 0 && len(arr) < s.MinItems {
			return fmt.Errorf("%s: expected at least %d items, got %d", path, s.MinItems, len(arr))
		}
		if s.MaxItems > 0 && len(arr) > s.MaxItems {
			return fmt.Errorf("%s: expected at most %d items, got %d", path, s.MaxItems, len(arr))
		}
		if s.Items == nil {
			return nil
		}
		for i, item := range arr {
			if err := s.Items.validate(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
		return nil

	case TypeString:
		str, ok := value.(string)
		if !ok {
			return typeErr(path, "string", value)
		}
		if s.MinLength > 0 && len(strings.TrimSpace(str)) < s.MinLength {
			return fmt.Errorf("%s: expected at least %d characters, got %d", path, s.MinLength, len(str))
		}
		if len(s.Enum) > 0 {
			for _, allowed := range s.Enum {
				if str == allowed {
					return nil
				}
			}
			return fmt.Errorf("%s: %q is not one of [%s]", path, str, strings.Join(s.Enum, ", "))
		}
		return nil

	case TypeNumber, TypeInteger:
		num, ok := value.(float64)
		if !ok {
			return typeErr(path, string(s.Type), value)
		}
		if s.Type == TypeInteger && num != float64(int64(num)) {
			return fmt.Errorf("%s: expected an integer, got %v", path, num)
		}
		if s.Minimum != nil && num < *s.Minimum {
			return fmt.Errorf("%s: %v is below the minimum %v", path, num, *s.Minimum)
		}
		if s.Maximum != nil && num > *s.Maximum {
			return fmt.Errorf("%s: %v is above the maximum %v", path, num, *s.Maximum)
		}
		return nil

	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return typeErr(path, "boolean", value)
		}
		return nil

	case "":
		return nil // an untyped node constrains nothing

	default:
		return fmt.Errorf("%s: schema has unknown type %q", path, s.Type)
	}
}

func typeErr(path, want string, got any) error {
	return fmt.Errorf("%s: expected %s, got %T", path, want, got)
}

/*
Prompt renders the schema as the instruction appended to the system message.

Models follow a literal JSON Schema far more reliably than an English
description of one, and every provider here can be told "return JSON" in words
even when it has no structured-output mode — which is the only thing that works
across LM Studio, vLLM, Ollama and Claude alike.
*/
func (s *Schema) Prompt() string {
	if s == nil {
		return ""
	}
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return ""
	}
	return "Respond with a single JSON object and nothing else — no prose, no " +
		"markdown fence — matching this JSON Schema:\n" + string(encoded)
}

/*
extractJSON pulls the JSON value out of a model's reply.

Models wrap JSON in ```json fences and add a sentence before it however firmly
they are told not to. Failing the whole call on that would burn a retry on
punctuation, so the fence is stripped and the outermost balanced object or
array is taken. What it will not do is guess: if there is no balanced JSON
value, that is a validation failure and the output gets dropped.
*/
func extractJSON(text string) (map[string]any, error) {
	trimmed := strings.TrimSpace(text)

	if fence := strings.Index(trimmed, "```"); fence >= 0 {
		rest := trimmed[fence+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			// Drop the language tag on the fence line ("json").
			if lang := strings.TrimSpace(rest[:nl]); lang == "" || !strings.ContainsAny(lang, "{[") {
				rest = rest[nl+1:]
			}
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		trimmed = strings.TrimSpace(rest)
	}

	start := strings.IndexAny(trimmed, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found in the reply")
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(trimmed); i++ {
		c := trimmed[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				var out map[string]any
				if err := json.Unmarshal([]byte(trimmed[start:i+1]), &out); err != nil {
					return nil, fmt.Errorf("the reply is not valid JSON: %w", err)
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("the JSON object in the reply is not closed")
}
