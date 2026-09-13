package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
)

// ValidateArguments validates model-produced arguments against the subset of
// JSON Schema emitted by this package's property builders.
func (t *Tool) ValidateArguments(args map[string]interface{}) string {
	if t == nil {
		return "tool definition is nil"
	}
	if err := validateSchemaValue("arguments", args, t.JsonSchema()); err != nil {
		return err.Error() + ". Correct the arguments and retry the tool call."
	}
	return ""
}

func validateSchemaValue(path string, value interface{}, schema map[string]interface{}) error {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := schema["properties"].(map[string]interface{})
		for _, name := range schemaStrings(schema["required"]) {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		for name, item := range object {
			property, exists := properties[name]
			if !exists {
				if allowed, ok := schema["additionalProperties"].(bool); ok && !allowed {
					return fmt.Errorf("%s.%s is not supported", path, name)
				}
				continue
			}
			propertySchema, ok := property.(map[string]interface{})
			if ok {
				if err := validateSchemaValue(path+"."+name, item, propertySchema); err != nil {
					return err
				}
			}
		}
		if min, ok := schemaNumber(schema["minProperties"]); ok && float64(len(object)) < min {
			return fmt.Errorf("%s must contain at least %d properties", path, int(min))
		}
		if max, ok := schemaNumber(schema["maxProperties"]); ok && float64(len(object)) > max {
			return fmt.Errorf("%s must contain at most %d properties", path, int(max))
		}
	case "array":
		array, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if min, ok := schemaNumber(schema["minItems"]); ok && float64(len(array)) < min {
			return fmt.Errorf("%s must contain at least %d items", path, int(min))
		}
		if max, ok := schemaNumber(schema["maxItems"]); ok && float64(len(array)) > max {
			return fmt.Errorf("%s must contain at most %d items", path, int(max))
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := map[string]struct{}{}
			for i, item := range array {
				raw, _ := json.Marshal(item)
				key := string(raw)
				if _, exists := seen[key]; exists {
					return fmt.Errorf("%s[%d] duplicates an earlier item", path, i)
				}
				seen[key] = struct{}{}
			}
		}
		if itemSchema, ok := schema["items"].(map[string]interface{}); ok {
			for i, item := range array {
				if err := validateSchemaValue(fmt.Sprintf("%s[%d]", path, i), item, itemSchema); err != nil {
					return err
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", path)
		}
		if min, ok := schemaNumber(schema["minLength"]); ok && float64(len([]rune(text))) < min {
			return fmt.Errorf("%s is shorter than %d characters", path, int(min))
		}
		if max, ok := schemaNumber(schema["maxLength"]); ok && float64(len([]rune(text))) > max {
			return fmt.Errorf("%s exceeds %d characters", path, int(max))
		}
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil || !matched {
				return fmt.Errorf("%s does not match %q", path, pattern)
			}
		}
	case "number", "integer":
		number, ok := schemaNumber(value)
		if !ok || (typeName == "integer" && math.Trunc(number) != number) {
			return fmt.Errorf("%s must be a %s", path, typeName)
		}
		if min, ok := schemaNumber(schema["minimum"]); ok && number < min {
			return fmt.Errorf("%s must be at least %v", path, min)
		}
		if max, ok := schemaNumber(schema["maximum"]); ok && number > max {
			return fmt.Errorf("%s must be at most %v", path, max)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	}

	if enum, ok := schema["enum"]; ok && !enumContains(enum, value) {
		return fmt.Errorf("%s must be one of %v", path, enum)
	}
	return nil
}

func schemaStrings(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func schemaNumber(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func enumContains(enum interface{}, value interface{}) bool {
	items := reflect.ValueOf(enum)
	if !items.IsValid() || items.Kind() != reflect.Slice {
		return true
	}
	for i := 0; i < items.Len(); i++ {
		if reflect.DeepEqual(items.Index(i).Interface(), value) {
			return true
		}
	}
	return false
}

// ValidateDefinition reports model-facing contract problems in built-in tools.
func (t *Tool) ValidateDefinition(maxDepth int) []error {
	var errors []error
	if t == nil || strings.TrimSpace(t.Name) == "" {
		return []error{fmt.Errorf("tool name is required")}
	}
	if strings.TrimSpace(t.Description) == "" {
		errors = append(errors, fmt.Errorf("tool %s requires a description", t.Name))
	}
	for name, raw := range t.InputSchema.Properties {
		schema, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		description, _ := schema["description"].(string)
		if strings.TrimSpace(description) == "" {
			errors = append(errors, fmt.Errorf("tool %s parameter %s requires a description", t.Name, name))
		}
		if depth := schemaContainerDepth(schema); maxDepth > 0 && depth > maxDepth {
			errors = append(errors, fmt.Errorf("tool %s parameter %s has container depth %d; maximum is %d", t.Name, name, depth, maxDepth))
		}
		typeName, _ := schema["type"].(string)
		if (typeName == "array" || typeName == "object") && len(t.Examples) == 0 {
			errors = append(errors, fmt.Errorf("tool %s complex parameter %s requires a complete example", t.Name, name))
		}
	}
	for index, example := range t.Examples {
		if message := t.ValidateArguments(example); message != "" {
			errors = append(errors, fmt.Errorf("tool %s example %d is invalid: %s", t.Name, index+1, message))
		}
	}
	return errors
}

func schemaContainerDepth(schema map[string]interface{}) int {
	typeName, _ := schema["type"].(string)
	depth := 0
	if typeName == "array" || typeName == "object" {
		depth = 1
	}
	childMax := 0
	if item, ok := schema["items"].(map[string]interface{}); ok {
		childMax = schemaContainerDepth(item)
	}
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		for _, raw := range properties {
			if child, ok := raw.(map[string]interface{}); ok {
				if candidate := schemaContainerDepth(child); candidate > childMax {
					childMax = candidate
				}
			}
		}
	}
	return depth + childMax
}
