package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// ValidateArguments validates model-produced arguments against the subset of
// JSON Schema emitted by this package's property builders.
func (t *Tool) ValidateArguments(args map[string]interface{}) string {
	if t == nil {
		return "Tool definition is missing.\nSuggestion: do not retry this tool; report the internal configuration error."
	}
	issues := validateSchemaValue("arguments", args, t.JsonSchema())
	if len(issues) == 0 {
		return ""
	}
	return "Invalid arguments:\n- " + strings.Join(issues, "\n- ") +
		"\nSuggestion: correct all issues above and retry the tool call."
}

func validateSchemaValue(path string, value interface{}, schema map[string]interface{}) []string {
	var issues []string
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s must be an object; replace the %s value with a JSON object", path, jsonTypeName(value))}
		}
		properties, _ := schema["properties"].(map[string]interface{})
		for _, name := range schemaStrings(schema["required"]) {
			if _, exists := object[name]; !exists {
				expected := "value"
				if property, ok := properties[name].(map[string]interface{}); ok {
					expected = expectedValueDescription(property)
				}
				issues = append(issues, fmt.Sprintf("%s.%s is required; add it with %s", path, name, expected))
			}
		}
		keys := make([]string, 0, len(object))
		for name := range object {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			item := object[name]
			property, exists := properties[name]
			if !exists {
				switch additional := schema["additionalProperties"].(type) {
				case bool:
					if !additional {
						allowed := sortedSchemaKeys(properties)
						message := fmt.Sprintf("%s.%s is not supported; remove it", path, name)
						if len(allowed) > 0 {
							message += "; allowed fields are " + strings.Join(allowed, ", ")
						}
						issues = append(issues, message)
					}
				case map[string]interface{}:
					issues = append(issues, validateSchemaValue(path+"."+name, item, additional)...)
				}
				continue
			}
			propertySchema, ok := property.(map[string]interface{})
			if ok {
				issues = append(issues, validateSchemaValue(path+"."+name, item, propertySchema)...)
			}
		}
		if min, ok := schemaNumber(schema["minProperties"]); ok && float64(len(object)) < min {
			issues = append(issues, fmt.Sprintf("%s must contain at least %d properties; add the missing properties", path, int(min)))
		}
		if max, ok := schemaNumber(schema["maxProperties"]); ok && float64(len(object)) > max {
			issues = append(issues, fmt.Sprintf("%s must contain at most %d properties; remove unnecessary properties", path, int(max)))
		}
		if propertyNames, ok := schema["propertyNames"].(map[string]interface{}); ok {
			for _, name := range keys {
				namePath := fmt.Sprintf("%s property name %q", path, name)
				issues = append(issues, validateSchemaValue(namePath, name, propertyNames)...)
			}
		}
	case "array":
		array, ok := value.([]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s must be an array; replace the %s value with a JSON array", path, jsonTypeName(value))}
		}
		if min, ok := schemaNumber(schema["minItems"]); ok && float64(len(array)) < min {
			issues = append(issues, fmt.Sprintf("%s must contain at least %d items; add more items", path, int(min)))
		}
		if max, ok := schemaNumber(schema["maxItems"]); ok && float64(len(array)) > max {
			issues = append(issues, fmt.Sprintf("%s must contain at most %d items; remove extra items", path, int(max)))
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := map[string]struct{}{}
			for i, item := range array {
				raw, _ := json.Marshal(item)
				key := string(raw)
				if _, exists := seen[key]; exists {
					issues = append(issues, fmt.Sprintf("%s[%d] duplicates an earlier item; remove or replace the duplicate", path, i))
				}
				seen[key] = struct{}{}
			}
		}
		if itemSchema, ok := schema["items"].(map[string]interface{}); ok {
			for i, item := range array {
				issues = append(issues, validateSchemaValue(fmt.Sprintf("%s[%d]", path, i), item, itemSchema)...)
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return []string{fmt.Sprintf("%s must be a string; replace the %s value with a JSON string", path, jsonTypeName(value))}
		}
		if min, ok := schemaNumber(schema["minLength"]); ok && float64(len([]rune(text))) < min {
			issues = append(issues, fmt.Sprintf("%s must contain at least %d characters; provide a longer string", path, int(min)))
		}
		if max, ok := schemaNumber(schema["maxLength"]); ok && float64(len([]rune(text))) > max {
			issues = append(issues, fmt.Sprintf("%s must contain at most %d characters; shorten the string", path, int(max)))
		}
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil || !matched {
				issues = append(issues, fmt.Sprintf("%s must match pattern %q; provide a value in that format", path, pattern))
			}
		}
	case "number", "integer":
		number, ok := schemaNumber(value)
		if !ok || (typeName == "integer" && math.Trunc(number) != number) {
			return []string{fmt.Sprintf("%s must be a %s; replace the %s value with a JSON %s", path, typeName, jsonTypeName(value), typeName)}
		}
		if min, ok := schemaNumber(schema["minimum"]); ok && number < min {
			issues = append(issues, fmt.Sprintf("%s must be at least %v; increase the value", path, min))
		}
		if max, ok := schemaNumber(schema["maximum"]); ok && number > max {
			issues = append(issues, fmt.Sprintf("%s must be at most %v; decrease the value", path, max))
		}
		if multiple, ok := schemaNumber(schema["multipleOf"]); ok && multiple > 0 && !isMultipleOf(number, multiple) {
			issues = append(issues, fmt.Sprintf("%s must be a multiple of %v; choose a divisible value", path, multiple))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s must be a boolean; replace the %s value with true or false", path, jsonTypeName(value))}
		}
	}

	if enum, ok := schema["enum"]; ok && !enumContains(enum, value) {
		issues = append(issues, fmt.Sprintf("%s must be one of %s; replace it with an allowed value", path, formatJSONValue(enum)))
	}
	return issues
}

func sortedSchemaKeys(properties map[string]interface{}) []string {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func expectedValueDescription(schema map[string]interface{}) string {
	if enum, ok := schema["enum"]; ok {
		return "one of " + formatJSONValue(enum)
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		return "a JSON object"
	case "array":
		return "a JSON array"
	case "integer":
		return "an integer value"
	case "number":
		return "a numeric value"
	case "boolean":
		return "true or false"
	case "string":
		return "a string value"
	default:
		return "a valid value"
	}
}

func jsonTypeName(value interface{}) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		if _, ok := schemaNumber(value); ok {
			return "number"
		}
		return fmt.Sprintf("%T", value)
	}
}

func formatJSONValue(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func isMultipleOf(value, multiple float64) bool {
	quotient := value / multiple
	return math.Abs(quotient-math.Round(quotient)) < 1e-9
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
