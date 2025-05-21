package functions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewFunctionTool(name, description string, fn any) *Tool {
	fnType := reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		panic("function tool must be a function")
	}

	schema := generateSchemaFromFunction(fnType)

	return &Tool{
		name:        name,
		description: description,
		function:    fn,
		schema:      schema,
	}
}

func (tool *Tool) Name() string {
	return tool.name
}

func (tool *Tool) Description() string {
	return tool.description
}

func (tool *Tool) SetFunction(fn Function) *Tool {
	tool.function = fn
	return tool
}

// func (tool *Tool) Call(ctx context.Context, input string) (any, error) {
// 	errInput := func(err error) error {
// 		return fmt.Errorf("failed parse input: %w", err)
// 	}
// 	outputSchema := func() (string, error) {
// 		if tool.schema != nil {
// 			buf, err := json.Marshal(tool.schema)
// 			if err != nil {
// 				return "", errInput(err)
// 			}
// 			return fmt.Sprintf("The %s schema is required to execute %s.", buf, tool.name), nil
// 		}
// 		return "", errors.New("function call failed.")
// 	}
// 	args := make(map[string]any)
// 	if input != "" && input != "None" {
// 		if err := json.Unmarshal([]byte(input), &args); err != nil {
// 			return outputSchema()
// 		}
// 	}
// 	output, err := tool.fn(ctx, args)
// 	if err != nil {
// 		if errors.Is(err, ErrRequired) {
// 			return outputSchema()
// 		}
// 		return "", err
// 	}
// 	return output, nil
// }

func (t *Tool) Execute(ctx context.Context, params map[string]any) (any, error) {
	fnType := reflect.TypeOf(t.function)
	fnValue := reflect.ValueOf(t.function)

	// Check if the function accepts a context as the first parameter
	hasContext := fnType.NumIn() > 0 && fnType.In(0).Implements(reflect.TypeOf((*context.Context)(nil)).Elem())

	// Prepare arguments
	args := make([]reflect.Value, fnType.NumIn())

	// Set context if the function accepts it
	argIndex := 0
	if hasContext {
		args[0] = reflect.ValueOf(ctx)
		argIndex = 1
	}

	// Set parameters based on function signature
	for i := argIndex; i < fnType.NumIn(); i++ {
		paramType := fnType.In(i)

		// If the function expects a map[string]any directly
		if i == argIndex && paramType.Kind() == reflect.Map &&
			paramType.Key().Kind() == reflect.String &&
			paramType.Elem().Kind() == reflect.Interface {
			args[i] = reflect.ValueOf(params)
			continue
		}

		// Handle struct parameter - map params to struct fields
		if paramType.Kind() == reflect.Struct {
			structValue := reflect.New(paramType).Elem()

			// For each field in the struct, check if we have a corresponding parameter
			for j := 0; j < paramType.NumField(); j++ {
				field := paramType.Field(j)

				// Get the JSON tag if available
				jsonTag := field.Tag.Get("json")
				if jsonTag == "" {
					jsonTag = field.Name
				} else {
					// Handle json tag options like `json:"name,omitempty"`
					parts := strings.Split(jsonTag, ",")
					jsonTag = parts[0]
				}

				// Check if we have a parameter with this name
				if paramValue, ok := params[jsonTag]; ok {
					// Try to set the field
					fieldValue := structValue.Field(j)
					if fieldValue.CanSet() {
						// Convert the parameter value to the field type
						convertedValue, err := convertToType(paramValue, field.Type)
						if err != nil {
							return nil, fmt.Errorf("failed to convert parameter %s: %w", jsonTag, err)
						}

						fieldValue.Set(reflect.ValueOf(convertedValue))
					}
				}
			}

			args[i] = structValue
			continue
		}

		// For a single parameter function with a primitive type, try to use the first parameter or a parameter with the same name
		paramName := ""
		// Only try to access struct fields if the parameter type is a struct
		if paramType.Kind() == reflect.Struct {
			for j := 0; j < paramType.NumField(); j++ {
				field := paramType.Field(j)
				jsonTag := field.Tag.Get("json")
				if jsonTag != "" {
					parts := strings.Split(jsonTag, ",")
					jsonTag = parts[0]
					if _, ok := params[jsonTag]; ok {
						paramName = jsonTag
						break
					}
				}
			}
		}

		if paramName == "" && len(params) > 0 {
			// Just use the first parameter
			for name := range params {
				paramName = name
				break
			}
		}

		if paramName != "" {
			if paramValue, ok := params[paramName]; ok {
				// Try to convert the parameter value to the expected type
				convertedValue, err := convertToType(paramValue, paramType)
				if err != nil {
					return nil, fmt.Errorf("failed to convert parameter %s: %w", paramName, err)
				}

				args[i] = reflect.ValueOf(convertedValue)
				continue
			}
		}

		// If we couldn't find a parameter, use the zero value for the type
		args[i] = reflect.Zero(paramType)
	}

	// Call the function
	results := fnValue.Call(args)

	// Handle return values
	if len(results) == 0 {
		return nil, nil
	} else if len(results) == 1 {
		return results[0].Interface(), nil
	} else {
		// Assume the last result is an error
		errVal := results[len(results)-1]
		if errVal.IsNil() {
			return results[0].Interface(), nil
		}
		return results[0].Interface(), errVal.Interface().(error)
	}
}

func (tool *Tool) WithSchema(schema *Schema) *Tool {
	tool.schema = schema
	return tool
}

func (tool *Tool) Schema() *Schema {
	return tool.schema
}

func (tool *Tool) ServerTool() server.ServerTool {
	t := mcp.Tool{
		Name:        tool.name,
		Description: tool.description,
		InputSchema: mcp.ToolInputSchema{},
	}
	if tool.schema != nil {
		t.InputSchema = tool.schema.MCPTool()
	}
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params := map[string]any{}
			if req.Params.Arguments != nil {
				buf, err := json.Marshal(&req.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := json.Unmarshal(buf, &params); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
			}
			res, err := tool.Execute(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			buf, err := json.Marshal(res)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(buf)), nil
		},
	}
}

func generateSchemaFromFunction(fnType reflect.Type) *Schema {
	// Initialize schema
	schema := &Schema{
		Type:       "object",
		Properties: map[string]any{},
		Required:   []string{},
	}

	// Check if the function accepts a context as the first parameter
	hasContext := fnType.NumIn() > 0 && fnType.In(0).Implements(reflect.TypeOf((*context.Context)(nil)).Elem())

	// Start from the first non-context parameter
	startIndex := 0
	if hasContext {
		startIndex = 1
	}

	// If the function has no parameters beyond context, return empty schema
	if fnType.NumIn() <= startIndex {
		return schema
	}

	// Get the first parameter type after context (if any)
	paramType := fnType.In(startIndex)

	// If the parameter is a map[string]any, we can't infer the schema
	if paramType.Kind() == reflect.Map &&
		paramType.Key().Kind() == reflect.String &&
		paramType.Elem().Kind() == reflect.Interface {
		// Generic map, can't infer schema
		return schema
	}

	// If the parameter is a struct, create a schema from its fields
	if paramType.Kind() == reflect.Struct {
		for i := range paramType.NumField() {
			field := paramType.Field(i)

			// Skip unexported fields
			if field.PkgPath != "" {
				continue
			}

			// Get the field name from JSON tag or fallback to field name
			fieldName := field.Name
			jsonTag := field.Tag.Get("json")
			if jsonTag != "" {
				// Handle json tag options like `json:"name,omitempty"`
				parts := strings.Split(jsonTag, ",")
				fieldName = parts[0]

				// Skip if the field is explicitly omitted with "-"
				if fieldName == "-" {
					continue
				}

				// Check if the field is required (not marked as omitempty)
				isRequired := true
				if slices.Contains(parts[1:], "omitempty") {
					isRequired = false
				}

				if isRequired {
					schema.Required = append(schema.Required, fieldName)
				}
			} else {
				// If no JSON tag, assume it's required
				schema.Required = append(schema.Required, fieldName)
			}

			// Get the field schema
			fieldSchema := getTypeSchema(field.Type)

			// Add description from doc tag if available
			if docTag := field.Tag.Get("mcpdescription"); docTag != "" {
				fieldSchema["description"] = docTag
			}

			// Add the field to properties
			schema.Properties[fieldName] = fieldSchema
		}
	} else {
		// For other parameter types, create a single property schema
		propName := "value"
		propSchema := getTypeSchema(paramType)
		schema.Properties[propName] = propSchema
		schema.Required = append(schema.Required, propName)
	}

	return schema
}

func getTypeSchema(t reflect.Type) map[string]any {
	schema := make(map[string]any)

	// Handle pointers
	if t.Kind() == reflect.Ptr {
		elemSchema := getTypeSchema(t.Elem())

		// For pointers, the field is nullable
		if enum, ok := elemSchema["enum"]; ok {
			// If the schema has enum values, add null to the enum
			enumValues := enum.([]any)
			enumValues = append(enumValues, nil)
			elemSchema["enum"] = enumValues
		}

		return elemSchema
	}

	// Handle different types
	switch t.Kind() {
	case reflect.Bool:
		schema["type"] = "boolean"

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema["type"] = "integer"

	case reflect.Float32, reflect.Float64:
		schema["type"] = "number"

	case reflect.String:
		schema["type"] = "string"

	case reflect.Slice, reflect.Array:
		schema["type"] = "array"
		schema["items"] = getTypeSchema(t.Elem())

	case reflect.Map:
		schema["type"] = "object"
		if t.Key().Kind() == reflect.String {
			schema["additionalProperties"] = getTypeSchema(t.Elem())
		} else {
			// Non-string keyed maps are not well represented in JSON Schema
			schema["additionalProperties"] = true
		}

	case reflect.Struct:
		schema["type"] = "object"
		schema["properties"] = make(map[string]any)
		schema["required"] = []string{}

		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)

			// Skip unexported fields
			if field.PkgPath != "" {
				continue
			}

			// Get the field name from JSON tag or fallback to field name
			fieldName := field.Name
			jsonTag := field.Tag.Get("json")
			if jsonTag != "" {
				// Handle json tag options like `json:"name,omitempty"`
				parts := strings.Split(jsonTag, ",")
				fieldName = parts[0]

				// Skip if the field is explicitly omitted with "-"
				if fieldName == "-" {
					continue
				}

				// Check if the field is required (not marked as omitempty)
				isRequired := true
				if slices.Contains(parts[1:], "omitempty") {
					isRequired = false
				}

				if isRequired {
					schema["required"] = append(schema["required"].([]string), fieldName)
				}
			} else {
				// If no JSON tag, assume it's required
				schema["required"] = append(schema["required"].([]string), fieldName)
			}

			// Get the field schema
			fieldSchema := getTypeSchema(field.Type)

			// Add description from doc tag if available
			if docTag := field.Tag.Get("doc"); docTag != "" {
				fieldSchema["description"] = docTag
			}

			// Add the field to properties
			schema["properties"].(map[string]any)[fieldName] = fieldSchema
		}

	default:
		// For unknown types, fallback to string
		schema["type"] = "string"
	}

	return schema
}

func convertToType(value any, targetType reflect.Type) (any, error) {
	// Handle nil special case
	if value == nil {
		return reflect.Zero(targetType).Interface(), nil
	}

	// Get the value's type
	valueType := reflect.TypeOf(value)

	// If the value is already assignable to the target type, return it
	if valueType.AssignableTo(targetType) {
		return value, nil
	}

	// Handle some common conversions
	switch targetType.Kind() {
	case reflect.String:
		// Convert to string
		return fmt.Sprintf("%v", value), nil

	case reflect.Bool:
		// Try to convert to bool
		switch v := value.(type) {
		case bool:
			return v, nil
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return reflect.ValueOf(v).Int() != 0, nil
		case string:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return false, fmt.Errorf("cannot convert %v to bool: %w", value, err)
			}
			return b, nil
		default:
			return false, fmt.Errorf("cannot convert %v to bool", value)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Try to convert to int
		switch v := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			intVal := reflect.ValueOf(v).Int()
			return reflect.ValueOf(intVal).Convert(targetType).Interface(), nil
		case float32, float64:
			floatVal := reflect.ValueOf(v).Float()
			return reflect.ValueOf(int64(floatVal)).Convert(targetType).Interface(), nil
		case string:
			i, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("cannot convert %v to int: %w", value, err)
			}
			return reflect.ValueOf(i).Convert(targetType).Interface(), nil
		default:
			return 0, fmt.Errorf("cannot convert %v to int", value)
		}

	case reflect.Float32, reflect.Float64:
		// Try to convert to float
		switch v := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			intVal := reflect.ValueOf(v).Int()
			return reflect.ValueOf(float64(intVal)).Convert(targetType).Interface(), nil
		case float32, float64:
			floatVal := reflect.ValueOf(v).Float()
			return reflect.ValueOf(floatVal).Convert(targetType).Interface(), nil
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0.0, fmt.Errorf("cannot convert %v to float: %w", value, err)
			}
			return reflect.ValueOf(f).Convert(targetType).Interface(), nil
		default:
			return 0.0, fmt.Errorf("cannot convert %v to float", value)
		}

	case reflect.Slice:
		// Try to convert to slice
		switch v := value.(type) {
		case []any:
			elemType := targetType.Elem()
			sliceValue := reflect.MakeSlice(targetType, len(v), len(v))

			for i, elem := range v {
				convertedElem, err := convertToType(elem, elemType)
				if err != nil {
					return nil, fmt.Errorf("cannot convert slice element %d: %w", i, err)
				}
				sliceValue.Index(i).Set(reflect.ValueOf(convertedElem))
			}

			return sliceValue.Interface(), nil
		default:
			return nil, fmt.Errorf("cannot convert %v to slice", value)
		}

	case reflect.Map:
		// Try to convert to map
		if targetType.Key().Kind() == reflect.String {
			switch v := value.(type) {
			case map[string]any:
				elemType := targetType.Elem()
				mapValue := reflect.MakeMap(targetType)

				for key, elem := range v {
					convertedElem, err := convertToType(elem, elemType)
					if err != nil {
						return nil, fmt.Errorf("cannot convert map element %s: %w", key, err)
					}
					mapValue.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(convertedElem))
				}

				return mapValue.Interface(), nil
			default:
				return nil, fmt.Errorf("cannot convert %v to map", value)
			}
		}
	}

	// If we couldn't convert, return an error
	return nil, fmt.Errorf("cannot convert %v (type %T) to %v", value, value, targetType)
}
